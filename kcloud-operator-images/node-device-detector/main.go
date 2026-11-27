// ============================================================
// main.go: node-agent 부트스트랩 + 재조정 루프
// 상세: env/k8s client 초기화 후 30초 주기로 scan → validate → report 능력을 구동한다. 기존
// detector 의 상시·비특권·전노드 프로필은 유지하며 Validation 능력만 additive 로 얹는다.
// 생성일: 2026-03-25 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const scanInterval = 30 * time.Second

// excludedNodeLabel 은 operator(npuclusterpolicy_controller.go 의 nodeExclusion)가 발행하는
// 배제 판정 라벨이다. detector 는 이 값을 읽기만 한다 — 배제 규칙을 다시 계산하지 않는다.
const excludedNodeLabel = "kcloud.ai/excluded"

// excludedReasonNodeLabel 은 배제 사유 라벨이다. 값은 operator 의 고정 어휘
// (node_exclusion.go: control-plane | policy)이며 detector 는 그대로 옮겨 적기만 한다.
// 사유를 추측해 문구에 박으면 excludeNodeSelector 로 배제된 워커에 "control-plane" 이라는
// 거짓이 적힌다 — 판정을 복제하지 않는다는 원칙은 사유에도 적용된다.
const excludedReasonNodeLabel = "kcloud.ai/excluded-reason"

func main() {
	// 사이드카 모드: operator 가 이 컨테이너를 Command: ["/podresources-writer"] 로 띄우므로
	// 실행 파일 이름으로 들어온다. kubelet PodResources 소켓을 10초마다 읽어 공유 파일에 쓰기만
	// 하고 끝낸다(scan·report·metrics 는 돌지 않는다). --podresources-writer 인자는 중복 방어.
	if isPodResourcesWriterMode(os.Args) {
		runPodResourcesWriter(context.Background(), "/var/lib/kubelet/pod-resources/kubelet.sock", podResourcesAllocationsPath)
		return
	}

	node := os.Getenv("NODE_NAME")
	if node == "" {
		fmt.Println("NODE_NAME empty")
		os.Exit(1)
	}

	// 능력 토글. scan/report 는 항상 on(하위호환).
	//  - VALIDATION/METRICS: 기본 on(비특권, 신규 RBAC 불요).
	//  - LABELS: 기본 off — node patch RBAC 필요, helm 에 RBAC 확대가 반영되면 opt-in 으로 켠다.
	enableValidation := os.Getenv("NODEAGENT_ENABLE_VALIDATION") != "false"
	enableMetrics := os.Getenv("NODEAGENT_ENABLE_METRICS") != "false"
	enableLabels := os.Getenv("NODEAGENT_ENABLE_LABELS") == "true"

	cfg, err := rest.InClusterConfig()
	if err != nil {
		fmt.Println("InClusterConfig err:", err)
		os.Exit(1)
	}
	c, err := client.New(cfg, client.Options{})
	if err != nil {
		fmt.Println("client.New err:", err)
		os.Exit(1)
	}

	var collector *metricsCollector
	if enableMetrics {
		collector = newMetricsCollector()
		startMetricsServer(collector)
	}

	for {
		ctx := context.Background()
		snap := Scan(node)

		// allocatable 은 validation/metrics 가 공유하므로 필요 시 1회만 조회.
		var alloc map[string]int64
		var excluded bool
		var excludedReason string
		if enableValidation || enableMetrics {
			alloc, excluded, excludedReason = fetchAllocatable(ctx, c, node)
		}
		if enableValidation {
			snap.Validation = Validate(snap, alloc, excluded, excludedReason)
		}
		if enableMetrics {
			// allocated(스케줄 관점 사용률)는 metrics 전용이라 여기서만 조회한다.
			collector.Update(snap, alloc, fetchAllocated(ctx, c, node))
		}
		if err := Report(ctx, c, snap); err != nil {
			fmt.Println("report err:", err)
		}
		if enableLabels {
			if err := Label(ctx, c, snap); err != nil {
				fmt.Println("label err:", err)
			}
		}
		time.Sleep(scanInterval)
	}
}

// fetchAllocatable 은 노드의 status.allocatable 을 resource→수량(int64) 맵으로 읽고,
// 같은 Node 조회에 실려 온 배제 라벨(excludedNodeLabel)과 그 사유(excludedReasonNodeLabel)도
// 함께 돌려준다 — 이미 쥐고 있는 Node 객체에서 꺼내는 것이라 새 API 호출을 만들지 않는다.
// device-plugin 이 등록한 가속기 리소스(nvidia.com/gpu 등)를 Validation ③에서 사용한다.
// 조회 실패 시 nil, false, "" — Validation 은 allocatable=0 으로 degrade(오탐 없이 FAIL 표기).
func fetchAllocatable(ctx context.Context, c client.Client, node string) (map[string]int64, bool, string) {
	n := &corev1.Node{}
	if err := c.Get(ctx, client.ObjectKey{Name: node}, n); err != nil {
		fmt.Println("get node allocatable err:", err)
		return nil, false, ""
	}
	m := make(map[string]int64, len(n.Status.Allocatable))
	for name, q := range n.Status.Allocatable {
		m[string(name)] = q.Value()
	}
	return m, n.Labels[excludedNodeLabel] == "true", n.Labels[excludedReasonNodeLabel]
}

// fetchAllocated 는 이 노드에 스케줄된 Pod 들의 가속기 requests 합을 리소스별로 집계한다.
// "장치 busy%" 대신 전 벤더에 공통 정의가 가능한 스케줄 관점 사용률이다.
// 확장 리소스만 대상으로 하려고 "<domain>/<name>" 형태(슬래시 포함) 키만 남기며,
// 벤더 판정은 metrics 가 allocatable 과 동일한 매핑으로 수행한다.
// 조회 실패 시 nil — 해당 시리즈만 빠지고 다른 metric 은 영향 없다(fail-open).
func fetchAllocated(ctx context.Context, c client.Client, node string) map[string]int64 {
	var pods corev1.PodList
	if err := c.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node}); err != nil {
		// field index 가 없는 클라이언트를 위한 폴백: 전체 목록 후 노드로 거른다.
		if err := c.List(ctx, &pods); err != nil {
			fmt.Println("list pods err:", err)
			return nil
		}
	}
	out := map[string]int64{}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName != node {
			continue
		}
		// 종료된 Pod 는 자원을 점유하지 않는다.
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		for ci := range p.Spec.Containers {
			for name, q := range p.Spec.Containers[ci].Resources.Requests {
				n := string(name)
				if !strings.Contains(n, "/") {
					continue // cpu/memory 등 core 리소스 제외
				}
				out[n] += q.Value()
			}
		}
	}
	return out
}
