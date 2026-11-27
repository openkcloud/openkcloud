// ============================================================
// migjob.go: NVIDIA MIG apply 용 JobExecutor — command-hash 기반 1회성 Job
// 상세: CommandStep 시퀀스를 nsenter 스크립트로 렌더해 privileged/hostPID 1회성 Job 으로
// 실행한다. Job 이름은 (uid,generation,node,action,cmdHash) 로 결정론적이라 동일 의도는 재사용된다.
// 생성일: 2026-07-24 | 수정일: 2026-10-11
// ============================================================
package nvidia

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/internal/naming"
)

// CmdHashAnnotation 은 렌더된 Job 에 실행 명령 정체성을 새기는 annotation 키다.
// 동일 이름 Job 재조우 시 이 값으로 "같은 의도 재실행(재사용)" vs "충돌(하드 에러)" 을 가른다.
const CmdHashAnnotation = "acpp.npu.ai/cmd-hash"

// NodeBootIDAnnotation 은 Job 을 만든 순간 대상 노드가 돌고 있던 부팅 세대(BootID)다.
// RebootRestartsAnnotation 은 재부팅에 치여 다시 만든 횟수이며, 재생성 때 이어서 센다.
// mode 복원 재부팅이 GI rollback Job 의 파드를 실행 전에 덮치면, 파드는 NodeName 고정 +
// RestartPolicy=Never 라 재부팅 후에도 Pending 에서 회복하지 못한다. Job 은 끝나지 않는 채로
// 남고, 이름이 결정론적이라 이후 모든 pass 가 같은 죽은 Job 을 다시 찾아 기다린다 — 자력
// 탈출 경로가 없어 ACPP 삭제가 finalizer 에 영구히 걸린다.
const (
	NodeBootIDAnnotation     = "acpp.npu.ai/node-boot-id"
	RebootRestartsAnnotation = "acpp.npu.ai/reboot-restarts"
)

// Namespace 는 MIG apply Job 이 생성되는 네임스페이스(operator 관리, driverjob 과 동일 규칙).
var Namespace = naming.OperatorNamespace()

const (
	migJobTTLSeconds    int32         = 600 // 완료 후 로그 보존 window
	migJobBackoffLimit  int32         = 0   // 재시도는 상태기계 재조정(requeue) 몫 — Job 자체 재시도 없음
	defaultPollInterval time.Duration = 2 * time.Second
	defaultWaitTimeout  time.Duration = 10 * time.Minute
)

// maxJobRebootRestarts 는 "재부팅에 치인 Job 을 다시 만든다" 의 상한이다. 상한이 없으면 노드가
// 반복 재부팅하는 동안(외부 재부팅 루프·하드웨어 이상) 같은 Job 을 영원히 다시 만들며, 그 사이
// 삭제/적용은 끝나지도 실패하지도 않는다 — 결함 3 이 만든 것과 같은 종류의 무한 대기다.
// 값은 mode 전환 재부팅 예산(maxMigRebootAttempts=2)과 같다: 한 방향 전환이 정당하게 쓸 수 있는
// 재부팅 수만큼만 봐준다. 넘으면 하드 에러로 올려 운영자를 부른다(terminal).
const maxJobRebootRestarts = 2

// Executor 는 CommandStep 시퀀스 실행 seam 이다(하드웨어 의존 격리).
// *JobExecutor 가 실 구현이며, 단위 테스트는 fake 를 주입한다(Backend.WithExecutor).
type Executor interface {
	Run(ctx context.Context, opID, cmdHash, nodeName string, steps []CommandStep) error
}

// JobExecutor 는 CommandStep 시퀀스를 대상 노드에서 1회성 Job 으로 실행한다.
type JobExecutor struct {
	Client       client.Client
	Image        string // nsenter 를 포함한 실행 이미지
	PollInterval time.Duration
	Timeout      time.Duration
}

// NewJobExecutor 는 기본 poll/timeout 값을 채운 JobExecutor 를 만든다.
func NewJobExecutor(c client.Client, image string) *JobExecutor {
	return &JobExecutor{Client: c, Image: image}
}

// 컴파일 타임 계약 확인 — *JobExecutor 는 Executor 를 만족한다.
var _ Executor = (*JobExecutor)(nil)

// Run 은 opID(=OperationID 가 계산한 결정론적 Job 이름) 로 Job 을 조회하고,
// 없으면 생성, 있으면 cmdHash 로 재사용/충돌을 판정한 뒤 완료를 대기한다.
func (e *JobExecutor) Run(ctx context.Context, opID, cmdHash, nodeName string, steps []CommandStep) error {
	var existing batchv1.Job
	err := e.Client.Get(ctx, types.NamespacedName{Name: opID, Namespace: Namespace}, &existing)
	switch {
	case err == nil:
		if existing.Annotations[CmdHashAnnotation] != cmdHash {
			return fmt.Errorf("nvidia: job %q exists with different cmd-hash (name collision, different intent)", opID)
		}
		restarts := rebootRestartsOf(&existing)
		// 아직 실행 중이면 그대로 완료 대기(중복 실행 방지). 이미 완료(Succeeded/Failed)된 Job 은
		// 재실행을 위해 삭제 후 재생성한다 — rollback 으로 하드웨어가 되돌려진 뒤 과거 성공을 재사용하면
		// GI 를 다시 만들지 않아 무한 apply→verify실패→rollback 루프가 된다.
		if existing.Status.Succeeded == 0 && existing.Status.Failed == 0 {
			// "실행 중" 과 "재부팅에 치여 영영 안 끝남" 을 여기서 가른다.
			// 노드가 그대로면 아무리 느린 Job 도 건드리지 않는다 — 판정 근거는 파드 phase 가 아니라
			// 부팅 세대이고, 부팅 세대는 실제로 부팅해야만 바뀐다.
			if !e.strandedByReboot(ctx, &existing) {
				return e.wait(ctx, opID)
			}
			restarts++
			if restarts > maxJobRebootRestarts {
				return fmt.Errorf("nvidia: job %q stranded by node reboot %d times; manual intervention required", opID, restarts-1)
			}
		}
		bg := metav1.DeletePropagationBackground
		if delErr := e.Client.Delete(ctx, &existing, &client.DeleteOptions{PropagationPolicy: &bg}); delErr != nil && !apierrors.IsNotFound(delErr) {
			return delErr
		}
		if err := e.createWhenGone(ctx, opID, cmdHash, nodeName, steps, restarts); err != nil {
			return err
		}
	case apierrors.IsNotFound(err):
		job := e.renderJob(ctx, opID, cmdHash, nodeName, steps, 0)
		if createErr := e.Client.Create(ctx, job); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
	default:
		return err
	}
	return e.wait(ctx, opID)
}

// strandedByReboot 는 이 Job 이 얹혀 있던 노드가 Job 이 끝나기 전에 재부팅됐는지다.
// BootID 로 판정하는 이유: operator 는 자기가 재부팅을 요청한 것만 알 뿐 그 시점이 이 Job 의
// 파드보다 앞인지 뒤인지 모르고, 재부팅 주체도 우리만이 아니다(드라이버 업그레이드·운영자·정전).
// BootID 는 그 모든 원인을 하나로 덮는 관측 사실이다. 재부팅이 없으면 영원히 그대로이고,
// 있었으면 파드는 NodeName 고정 + RestartPolicy=Never 라 재스케줄되지 않아 그 Job 은 절대
// 성공할 수 없다. 모르면(스탬프 없음·조회 실패·빈 값) false 로 남는다(fail-safe).
func (e *JobExecutor) strandedByReboot(ctx context.Context, job *batchv1.Job) bool {
	placedOn := job.Annotations[NodeBootIDAnnotation]
	node := job.Spec.Template.Spec.NodeName
	if placedOn == "" || node == "" {
		return false
	}
	var n corev1.Node
	if err := e.Client.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return false
	}
	cur := n.Status.NodeInfo.BootID
	return cur != "" && cur != placedOn
}

// renderJob 은 renderMigJob 에 재부팅 판정용 스탬프(부팅 세대 + 재생성 횟수)를 새겨 돌려준다.
// BootID 조회 실패는 무시한다 — 스탬프가 없으면 strandedByReboot 가 판정을 포기할 뿐이고,
// 그 상태는 이 수정 이전과 정확히 같다. 관측 실패로 Job 생성 자체를 막을 이유는 없다.
func (e *JobExecutor) renderJob(ctx context.Context, opID, cmdHash, nodeName string, steps []CommandStep, restarts int) *batchv1.Job {
	job := renderMigJob(opID, cmdHash, nodeName, steps, e.Image)
	var n corev1.Node
	if err := e.Client.Get(ctx, types.NamespacedName{Name: nodeName}, &n); err == nil {
		job.Annotations[NodeBootIDAnnotation] = n.Status.NodeInfo.BootID
	}
	if restarts > 0 {
		job.Annotations[RebootRestartsAnnotation] = strconv.Itoa(restarts)
	}
	return job
}

// rebootRestartsOf 는 Job 에 누적된 재생성 횟수를 읽는다(없거나 깨졌으면 0).
func rebootRestartsOf(job *batchv1.Job) int {
	n, err := strconv.Atoi(job.Annotations[RebootRestartsAnnotation])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// wait 는 Job 완료(Succeeded>0/Failed>0)를 ctx-aware 폴링으로 대기한다(time.Sleep 미사용).
// createWhenGone 은 삭제 진행 중인 동명 Job 이 사라질 때까지 폴링한 뒤 새 Job 을 만든다.
func (e *JobExecutor) createWhenGone(ctx context.Context, opID, cmdHash, nodeName string, steps []CommandStep, restarts int) error {
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	for {
		job := e.renderJob(ctx, opID, cmdHash, nodeName, steps, restarts)
		err := e.Client.Create(ctx, job)
		if err == nil {
			return nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("nvidia: prior mig job %q not deleted in time", opID)
		case <-time.After(2 * time.Second):
		}
	}
}

func (e *JobExecutor) wait(ctx context.Context, name string) error {
	interval := e.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("nvidia: job %q timed out after %s", name, timeout)
		case <-ticker.C:
			var job batchv1.Job
			if err := e.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: Namespace}, &job); err != nil {
				return err
			}
			if job.Status.Succeeded > 0 {
				return nil
			}
			if job.Status.Failed > 0 {
				return fmt.Errorf("nvidia: job %q failed", name)
			}
			// 대기 도중 노드가 재부팅되면 이 Job 은 끝나지 않는다 — 남은 timeout 을 다 태울 이유가
			// 없다. 여기서 바로 빠져나오면 다음 pass 의 Run 이 재생성으로 복구한다. 이 조기 탈출이
			// 없으면 최초 재부팅 pass 가 10분을 통째로 낭비한다.
			if e.strandedByReboot(ctx, &job) {
				return fmt.Errorf("nvidia: job %q stranded by node reboot; recreating on the next pass", name)
			}
		}
	}
}

// OperationID 는 (uid,generation,node,action,steps) 로 결정론적 Job 이름 + cmdHash 를 만든다.
// name = "acpp-mig-<uid8>-<gen>-<nodeHash8>-<action>-<cmdHash8>". generation 변경이나
// steps(명령) 변경은 서로 다른 name/hash 를 낳는다 — 동일 의도만 같은 이름으로 수렴(재사용).
// 63자 초과 시 앞부분을 잘라내되 hash suffix 는 항상 보존한다(DNS-1123 label 제약).
func OperationID(uid string, gen int64, node, action string, steps []CommandStep) (name string, cmdHash string) {
	cmdHash = hashSteps(steps)
	suffix := "-" + cmdHash[:8]
	name = fmt.Sprintf("acpp-mig-%s-%d-%s-%s%s", trunc8(uid), gen, shortHash(node), action, suffix)
	if len(name) > 63 {
		name = name[:63-len(suffix)] + suffix
	}
	return name, cmdHash
}

// hashSteps 는 steps(argv + 기대값)를 결정론적으로 직렬화해 sha256 hex 로 반환한다.
func hashSteps(steps []CommandStep) string {
	h := sha256.New()
	for _, s := range steps {
		for _, a := range s.Argv {
			h.Write([]byte(a))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
		if s.ExpectEmpty {
			h.Write([]byte("empty"))
		}
		for _, want := range s.ExpectOneOf {
			h.Write([]byte(want))
			h.Write([]byte{0})
		}
		h.Write([]byte{2})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func trunc8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// renderMigJob 은 steps 를 nsenter 스크립트로 감싼 privileged/hostPID 1회성 Job 을 빌드한다.
// RestartPolicy=Never(재시도는 상태기계 몫), cmdHash 는 annotation 에 새겨 재조우 시 재사용/충돌을 가른다.
func renderMigJob(name, cmdHash, nodeName string, steps []CommandStep, image string) *batchv1.Job {
	ttl := migJobTTLSeconds
	backoff := migJobBackoffLimit
	labels := map[string]string{
		"app.kubernetes.io/name":      "kcloud-acpp-mig",
		"app.kubernetes.io/component": "mig-apply",
		"npu.ai/node":                 nodeName,
	}
	podSpec := corev1.PodSpec{
		NodeName:      nodeName, // 대상 노드 고정(스케줄러 우회)
		HostPID:       true,
		RestartPolicy: corev1.RestartPolicyNever,
		Tolerations:   []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
		Containers: []corev1.Container{{
			Name:            "mig-apply",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/bin/sh", "-c", scriptFor(steps)},
			SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
		}},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   Namespace,
			Labels:      labels,
			Annotations: map[string]string{CmdHashAnnotation: cmdHash},
		},
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

// scriptFor 는 steps 를 순서대로 nsenter 실행 + 기대값 assert 로 렌더한다.
// ExpectEmpty → 출력이 비어야 함(있으면 exit 1). ExpectOneOf → 공백 제거 후 후보값 중 하나와 일치해야 함.
func scriptFor(steps []CommandStep) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, s := range steps {
		ns := "nsenter --target 1 --mount --pid -- " + strings.Join(s.Argv, " ")
		switch {
		case s.Optional:
			// best-effort: 대상 없음 등 non-zero 종료를 관용(-dci/-dgi).
			b.WriteString(ns + " || true\n")
		case s.ExpectEmpty:
			b.WriteString("if " + ns + " | grep -q .; then echo 'expected empty output' >&2; exit 1; fi\n")
		case len(s.ExpectOneOf) > 0:
			cond := make([]string, 0, len(s.ExpectOneOf))
			for _, want := range s.ExpectOneOf {
				cond = append(cond, "[ \"$V\" = \""+want+"\" ]")
			}
			b.WriteString("V=$(" + ns + " | tr -d ' '); if ! ( " + strings.Join(cond, " || ") + " ); then echo \"unexpected: $V\" >&2; exit 1; fi\n")
		default:
			b.WriteString(ns + "\n")
		}
	}
	return b.String()
}

func boolPtr(b bool) *bool { return &b }
