// ============================================================
// observe.go: operator-driven MIG 관측 — privileged nsenter Job + fail-closed parse
// 상세: detector 가 host nvidia-smi 실행 불가라, operator 가 kcloud-host-exec Job(nsenter)으로
// MIG mode/geometry/lgip 를 per-PCI 관측해 Backend.WithObservations 로 in-memory 주입한다.
// 생성일: 2026-07-27 | 수정일: 2026-10-11
// ============================================================
package nvidia

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MIG mode/geometry 문자열 상수(fail-closed 판정에 반복 등장 — goconst).
const (
	modeEnabled  = "Enabled"
	modeDisabled = "Disabled"
	modeNA       = "NA"
	modeUnknown  = "Unknown"
	geomDisabled = "disabled"
)

// Observation 은 단일 GPU(PCI)의 operator-관측 MIG 상태다(fail-closed, ).
type Observation struct {
	PCI, ModeCurrent, ModePending, Geometry, LgipOutput, Err string
}

// Observer 는 노드의 per-PCI MIG 상태를 관측하는 seam 이다(reconciler 가 fresh 관측을 Backend 에
// 주입할 때 사용, ). 실 구현은 *MigObserver(Job), 테스트는 fake 로 대체한다.
type Observer interface {
	Observe(ctx context.Context, nodeName string, pcis []string) ([]Observation, error)
}

var _ Observer = (*MigObserver)(nil)

// migGiLineRe 는 `mig -lgi` 출력의 GI 행에서 "1g.6gb" 프로파일 토큰을 추출한다("+me" 변형 포함).
var migGiLineRe = regexp.MustCompile(`MIG\s+(\d+g\.\d+gb)`)

// normalizeMigMode 는 nvidia-smi mig.mode.current/pending CSV 필드값을 정규화한다.
// 인식 못하는 값은 전부 "Unknown"(fail-closed) — 조용히 "Disabled" 로 오판하지 않는다.
func normalizeMigMode(s string) string {
	switch strings.TrimSpace(s) {
	case modeEnabled:
		return modeEnabled
	case modeDisabled:
		return modeDisabled
	case "N/A", "[N/A]":
		return modeNA
	default:
		return modeUnknown
	}
}

// parseObservation 은 fail-closed 로 관측을 판정한다: mode csv 파싱 실패, current/pending
// Unknown, 또는 Enabled 인데 -lgi 를 파싱 못하면 전부 Unknown+Err 를 반환하고 절대 조용히
// "disabled" 로 보고하지 않는다(detector parseMigObservation 포팅).
func parseObservation(pci, modeCsv, lgi, lgip string) Observation {
	f := strings.Split(strings.TrimSpace(modeCsv), ",")
	if len(f) != 2 || strings.TrimSpace(f[0]) == "" {
		return Observation{PCI: pci, ModeCurrent: modeUnknown, ModePending: modeUnknown, Err: "unparseable mode csv"}
	}
	cur, pend := normalizeMigMode(f[0]), normalizeMigMode(f[1])
	if cur == modeUnknown {
		return Observation{PCI: pci, ModeCurrent: modeUnknown, ModePending: pend, LgipOutput: lgip, Err: "unrecognized mig.mode.current"}
	}
	if pend == modeUnknown {
		return Observation{PCI: pci, ModeCurrent: cur, ModePending: modeUnknown, LgipOutput: lgip, Err: "unrecognized mig.mode.pending"}
	}
	obs := Observation{PCI: pci, ModeCurrent: cur, ModePending: pend, LgipOutput: lgip}
	switch cur {
	case modeDisabled:
		obs.Geometry = geomDisabled
	case modeNA:
		// MIG 미지원 GPU — 분할될 여지가 없으므로 trivially "disabled".
		obs.Geometry = geomDisabled
	case modeEnabled:
		g, err := summarizeMigGeometryStrict(lgi)
		if err != nil {
			return Observation{PCI: pci, ModeCurrent: modeUnknown, ModePending: pend, LgipOutput: lgip, Err: "enabled but lgi unparseable: " + err.Error()}
		}
		obs.Geometry = g
	}
	return obs
}

// summarizeMigGeometryStrict 는 `mig -lgi` 원문을 요약한다. current=Enabled 인데 파싱 불가면
// error 를 반환한다(fail-closed) — Enabled 상태에서 조용히 "disabled" 로 보고하지 않는다.
// Enabled + "No GPU instances found"(GI 없음) → "" (enabled-no-GI, 안전 baseline).
func summarizeMigGeometryStrict(lgi string) (string, error) {
	if strings.Contains(lgi, "No MIG-enabled devices") {
		return geomDisabled, nil
	}
	if strings.Contains(lgi, "No GPU instances found") {
		return "", nil // MIG enabled 이나 GI 없음 — 안전 baseline.
	}
	if strings.TrimSpace(lgi) == "" {
		return "", fmt.Errorf("empty lgi")
	}
	counts := map[string]int{}
	for _, line := range strings.Split(lgi, "\n") {
		if m := migGiLineRe.FindStringSubmatch(line); m != nil {
			counts[m[1]]++
		}
	}
	if len(counts) == 0 {
		return "", fmt.Errorf("no parseable GI in non-empty lgi")
	}
	parts := make([]string, 0, len(counts))
	for p, n := range counts {
		parts = append(parts, fmt.Sprintf("%s x%d", p, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", "), nil
}

const (
	observePollInterval = 2 * time.Second
	observeTimeout      = 2 * time.Minute
	observeCreateWait   = 30 * time.Second
)

// MigObserver 는 privileged nsenter Job 으로 대상 노드의 per-PCI MIG 상태를 관측한다.
type MigObserver struct {
	c         client.Client
	namespace string
	image     string // kcloud-host-exec 이미지(HOST_EXEC_IMAGE) — nsenter 포함
	stream    string // 관측 스트림 식별자 — Job 이름을 호출자별로 가른다(observeJobName 참조)
}

// NewMigObserver 는 stream 으로 이름공간이 갈린 관측기를 만든다. stream 은 이 관측기를 돌리는
// 제어 루프를 가리킨다(정책 적용 경로 / 상시 관측 경로). 노드 이름만으로 Job 을 식별하면 같은
// 노드를 보는 두 루프가 **서로의 Job 을 지운다** — Observe 는 stale 재사용을 막으려 시작할 때
// 기존 Job 을 지우기 때문이다. 진 쪽은 fail-closed 관측(Err 채움)을 받고, 그 관측은 보고서에
// 기록되지 않아 NodeDeviceReport 의 geometry 가 영영 갱신되지 않는다.
func NewMigObserver(c client.Client, namespace, image, stream string) *MigObserver {
	return &MigObserver{c: c, namespace: namespace, image: image, stream: stream}
}

// 관측 스트림 식별자. 값은 Job 이름에 들어가므로 DNS-1123 label 로 안전한 짧은 토큰이어야 한다.
const (
	// StreamApply 는 ACPP 적용 상태머신이 돌리는 관측이다.
	StreamApply = "apply"
	// StreamStanding 은 정책과 무관하게 주기적으로 도는 관측이다.
	StreamStanding = "standing"
)

// observeJobName 은 (스트림, 노드) 당 하나인 결정론적 Job 이름이다. 스트림이 비면 기존 이름을
// 유지한다 — 이름이 바뀌면 이전 버전이 남긴 Job 을 못 지운다.
func observeJobName(stream, nodeName string) string {
	if stream == "" {
		return "acpp-mig-observe-" + shortHash(nodeName)
	}
	return "acpp-mig-observe-" + stream + "-" + shortHash(nodeName)
}

// observePCIsPerJob 은 관측 Job 하나가 담는 PCI 수다. 1 인 이유는 전송 채널이다 — 관측 출력은
// `/dev/termination-log` 로 나가고 kubelet 이 이를 4096 바이트에서 자른다. MIG 를 켠 A30 한
// 장의 표가 약 2.5KB 라 두 장만 담아도 한 섹션이 통째로 잘린다(실측 합계 5018 바이트). 잘린
// 장치는 fail-closed 되어 target 집합이 줄고, ApplyRecord 와 개수가 어긋나 ACPP 가 자기 배치를
// 남의 것으로 오판해 terminal Failed 로 굳는다. 장치당 한 Job 으로 고정해 막는다.
const observePCIsPerJob = 1

// Observe 는 nodeName 의 각 pci 에 대해 mode/-lgi/-lgip 를 privileged Job 으로 조회하고
// fail-closed 로 parse 한 Observation 을 pci 당 하나씩 반환한다. Job 실패·pod 소실·빈
// 메시지·section 누락은 조용히 드롭하지 않고 Err 를 채운 Observation 으로 표면화한다(apply 차단용).
//
// PCI 는 observePCIsPerJob 단위로 나눠 Job 을 돌린다. 반환 순서는 입력 pcis 순서와 같다.
func (o *MigObserver) Observe(ctx context.Context, nodeName string, pcis []string) ([]Observation, error) {
	if len(pcis) == 0 {
		return nil, nil
	}
	out := make([]Observation, 0, len(pcis))
	var firstErr error
	for chunkIdx, chunk := range chunkPCIs(pcis) {
		obs, err := o.observeChunk(ctx, nodeName, chunk, chunkIdx)
		out = append(out, obs...)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return out, firstErr
}

// chunkPCIs 는 pcis 를 Job 하나가 담을 수 있는 크기로 나눈다. 순서와 내용은 보존된다.
func chunkPCIs(pcis []string) [][]string {
	out := make([][]string, 0, (len(pcis)+observePCIsPerJob-1)/observePCIsPerJob)
	for i := 0; i < len(pcis); i += observePCIsPerJob {
		out = append(out, pcis[i:min(i+observePCIsPerJob, len(pcis))])
	}
	return out
}

// observeChunk 는 PCI 한 묶음을 Job 하나로 관측한다. chunkIdx 는 Job 이름을 가르는 접미다 —
// 같은 (스트림, 노드)의 묶음들이 서로의 Job 을 지우지 않게 한다.
func (o *MigObserver) observeChunk(ctx context.Context, nodeName string, pcis []string, chunkIdx int) ([]Observation, error) {
	name := fmt.Sprintf("%s-%d", observeJobName(o.stream, nodeName), chunkIdx)
	// 이전 Job(TTL 잔존/실패)을 먼저 정리해 stale 성공을 재사용하지 않는다.
	o.deleteJob(ctx, name)
	if err := o.createTolerant(ctx, renderObserveJob(name, nodeName, pcis, o.image, o.namespace)); err != nil {
		return failClosed(pcis, "observe job create: "+err.Error()), err
	}
	if err := o.waitDone(ctx, name); err != nil {
		return failClosed(pcis, "observe job: "+err.Error()), err
	}
	msg, err := o.terminationMessage(ctx, name)
	if err != nil {
		return failClosed(pcis, "observe message: "+err.Error()), err
	}
	obs := parseDelimitedMessage(msg, pcis)
	o.deleteJob(ctx, name) // best-effort 즉시 정리(실패해도 TTL 이 회수).
	return obs, nil
}

// deleteJob 은 Job(+pods, background propagation)을 best-effort 삭제한다(NotFound 무시).
func (o *MigObserver) deleteJob(ctx context.Context, name string) {
	bg := metav1.DeletePropagationBackground
	_ = o.c.Delete(ctx, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: o.namespace}},
		&client.DeleteOptions{PropagationPolicy: &bg})
}

// createTolerant 는 Job 을 생성하되, 직전 삭제의 background 정리가 아직 안 끝나 AlreadyExists 면
// ctx-aware 로 잠시 재시도한다.
func (o *MigObserver) createTolerant(ctx context.Context, job *batchv1.Job) error {
	deadline := time.NewTimer(observeCreateWait)
	defer deadline.Stop()
	ticker := time.NewTicker(observePollInterval)
	defer ticker.Stop()
	for {
		err := o.c.Create(ctx, job)
		if err == nil || !apierrors.IsAlreadyExists(err) {
			return err
		}
		o.deleteJob(ctx, job.Name)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("observe job %q still exists after %s", job.Name, observeCreateWait)
		case <-ticker.C:
		}
	}
}

// waitDone 은 Job 완료(Succeeded/Failed)를 ctx-aware 폴링으로 대기한다(migjob.go wait 패턴 재사용).
func (o *MigObserver) waitDone(ctx context.Context, name string) error {
	ticker := time.NewTicker(observePollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(observeTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("observe job %q timed out after %s", name, observeTimeout)
		case <-ticker.C:
			var job batchv1.Job
			if err := o.c.Get(ctx, types.NamespacedName{Name: name, Namespace: o.namespace}, &job); err != nil {
				return err
			}
			if job.Status.Succeeded > 0 {
				return nil
			}
			if job.Status.Failed > 0 {
				return fmt.Errorf("observe job %q failed", name)
			}
		}
	}
}

// terminationMessage 는 Job 이 만든 종료 pod 의 첫 컨테이너 terminated.Message(/dev/termination-log)를 읽는다.
func (o *MigObserver) terminationMessage(ctx context.Context, name string) (string, error) {
	var pods corev1.PodList
	if err := o.c.List(ctx, &pods, client.InNamespace(o.namespace), client.MatchingLabels{"job-name": name}); err != nil {
		return "", err
	}
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				return cs.State.Terminated.Message, nil
			}
		}
	}
	return "", fmt.Errorf("no terminated pod message for job %q", name)
}

// failClosed 는 각 pci 에 대해 Err 를 채운 Unknown Observation 을 만든다(관측 실패시 apply 차단 신호).
func failClosed(pcis []string, reason string) []Observation {
	out := make([]Observation, 0, len(pcis))
	for _, pci := range pcis {
		out = append(out, Observation{PCI: pci, ModeCurrent: modeUnknown, ModePending: modeUnknown, Err: reason})
	}
	return out
}

// parseDelimitedMessage 는 Job 이 termination-log 에 쓴 구분자 포맷을 pci 별 Observation 으로 파싱한다.
// pci 마다 하나씩 반환하며, section 이 없으면 Err 를 채운다(조용히 드롭 금지, fail-closed).
func parseDelimitedMessage(msg string, pcis []string) []Observation {
	sections := map[string]string{}
	var curPCI string
	buf := make([]string, 0, 16)
	flush := func() {
		if curPCI != "" {
			sections[curPCI] = strings.Join(buf, "\n")
		}
		buf = buf[:0]
	}
	for _, line := range strings.Split(msg, "\n") {
		if p, ok := parsePCIHeader(line); ok {
			flush()
			curPCI = p
			continue
		}
		buf = append(buf, line)
	}
	flush()

	out := make([]Observation, 0, len(pcis))
	for _, pci := range pcis {
		block, ok := sections[pci]
		if !ok {
			out = append(out, Observation{PCI: pci, ModeCurrent: modeUnknown, ModePending: modeUnknown, Err: "missing PCI section in observe output"})
			continue
		}
		mode := sectionBetween(block, "<<<MODE", ">>>MODE")
		lgi := sectionBetween(block, "<<<LGI", ">>>LGI")
		lgip := sectionBetween(block, "<<<LGIP", ">>>LGIP")
		out = append(out, parseObservation(pci, mode, lgi, lgip))
	}
	return out
}

// parsePCIHeader 는 "===PCI <pci>===" 행에서 pci 를 추출한다.
func parsePCIHeader(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "===PCI ") || !strings.HasSuffix(s, "===") {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(s, "===PCI "), "===")
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return "", false
	}
	return inner, true
}

// sectionBetween 은 start 마커 이후 ~ end 마커 이전의 내용을 trim 해 반환한다(없으면 "").
// LGI/LGIP 는 접두 충돌(<<<LGI ⊂ <<<LGIP)이 있으나, LGI 가 항상 LGIP 보다 먼저 나와 첫 매칭이 정답이다.
func sectionBetween(block, start, end string) string {
	i := strings.Index(block, start)
	if i < 0 {
		return ""
	}
	rest := block[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

// renderObserveJob 은 각 pci 에 nsenter nvidia-smi 질의를 실행하고 구분자 포맷을 termination-log 에
// 쓰는 privileged/hostPID 1회성 Job 을 빌드한다. set -e 미사용 — nvidia-smi 실패(2>&1)도 텍스트로
// 캡처해 parse 가 fail-closed 판정하게 한다.
func renderObserveJob(name, nodeName string, pcis []string, image, namespace string) *batchv1.Job {
	ttl := migJobTTLSeconds
	backoff := migJobBackoffLimit
	labels := map[string]string{
		"app.kubernetes.io/name":      "kcloud-acpp-mig",
		"app.kubernetes.io/component": "mig-observe",
		"npu.ai/node":                 nodeName,
	}
	podSpec := corev1.PodSpec{
		NodeName:      nodeName,
		HostPID:       true,
		RestartPolicy: corev1.RestartPolicyNever,
		Tolerations:   []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
		Containers: []corev1.Container{{
			Name:                     "mig-observe",
			Image:                    image,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			Command:                  []string{"/bin/sh", "-c", observeScript(pcis)},
			SecurityContext:          &corev1.SecurityContext{Privileged: boolPtr(true)},
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		}},
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

// observeScript 는 각 pci 에 대해 MODE/LGI/LGIP 섹션을 구분자로 감싸 termination-log 에 쓴다.
// multi-line -lgi/-lgip 를 위해 <<<SEC ... >>>SEC 마커로 감싸고, echo 로 마커가 항상 자기 행에
// 오도록 보장한다. 전체 루프 출력을 termination-log 로 리다이렉트(4096B 한도 내, 보통 1 device).
func observeScript(pcis []string) string {
	var b strings.Builder
	b.WriteString("for pci in")
	for _, p := range pcis {
		b.WriteString(" '" + p + "'")
	}
	b.WriteString("; do\n")
	b.WriteString("printf '===PCI %s===\\n' \"$pci\"\n")
	b.WriteString("printf '<<<MODE\\n'\n")
	b.WriteString("nsenter --target 1 --mount --pid -- nvidia-smi -i \"$pci\" --query-gpu=mig.mode.current,mig.mode.pending --format=csv,noheader 2>&1\n")
	b.WriteString("echo; printf '>>>MODE\\n'\n")
	b.WriteString("printf '<<<LGI\\n'\n")
	b.WriteString("nsenter --target 1 --mount --pid -- nvidia-smi mig -i \"$pci\" -lgi 2>&1\n")
	b.WriteString("echo; printf '>>>LGI\\n'\n")
	b.WriteString("printf '<<<LGIP\\n'\n")
	b.WriteString("nsenter --target 1 --mount --pid -- nvidia-smi mig -i \"$pci\" -lgip 2>&1\n")
	b.WriteString("echo; printf '>>>LGIP\\n'\n")
	b.WriteString("done > /dev/termination-log\n")
	return b.String()
}
