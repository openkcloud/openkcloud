// ============================================================
// kube.go: kubelet 할당 기록으로 장치를 쓰는 컨테이너를 찾는다(--kube-resources-label)
// 상세: kubelet PodResources API(List)에서 furiosa.ai 자원으로 할당된 장치 ID 를 읽는다. 공개
// Furiosa device plugin 은 장치 ID 로 NPU UUID 를 쓰므로 지표의 uuid 라벨 값으로 찾는다. 공식
// furiosa-metrics-exporter 와 같은 방식이다. 장치 단위 할당만 다룬다 — DRA·파티션 할당은 이
// 목록에 UUID 로 나오지 않는다.
// 생성일: 2026-10-15
// ============================================================
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

// podResourcesSocket 은 kubelet 이 할당 기록을 내주는 소켓이다. DaemonSet 이 그 디렉터리를
// hostPath 로 마운트해야 한다. 디렉터리가 root 전용이라 컨테이너는 uid 0 이어야 한다.
const podResourcesSocket = "/var/lib/kubelet/pod-resources/kubelet.sock"

// furiosaResourcePrefix 는 Furiosa device plugin 이 광고하는 자원 이름의 접두다(furiosa.ai/rngd 등).
const furiosaResourcePrefix = "furiosa.ai/"

// podRef 는 장치를 할당받은 컨테이너다. 빈 값은 할당되지 않은 장치다.
type podRef struct{ Namespace, Pod, Container string }

// deviceOwners 는 List 결과에서 Furiosa 장치 ID → 컨테이너 표를 만든다. 같은 장치가 여러
// 컨테이너에 나오면 응답에서 먼저 나온 것을 쓴다. device plugin 할당에서는 한 장치가 한 컨테이너에만 나온다.
func deviceOwners(resp *podresourcesv1.ListPodResourcesResponse) map[string]podRef {
	out := map[string]podRef{}
	for _, p := range resp.GetPodResources() {
		for _, c := range p.GetContainers() {
			for _, d := range c.GetDevices() {
				if !strings.HasPrefix(d.GetResourceName(), furiosaResourcePrefix) {
					continue
				}
				for _, id := range d.GetDeviceIds() {
					if _, seen := out[id]; !seen {
						out[id] = podRef{Namespace: p.GetNamespace(), Pod: p.GetName(), Container: c.GetName()}
					}
				}
			}
		}
	}
	return out
}

// listPodResources 는 kubelet 소켓에 List 를 한 번 부른다(5초 제한).
func listPodResources(ctx context.Context, socket string) (*podresourcesv1.ListPodResourcesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("podresources 연결: %w", err)
	}
	defer conn.Close()
	resp, err := podresourcesv1.NewPodResourcesListerClient(conn).List(ctx, &podresourcesv1.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("podresources list: %w", err)
	}
	return resp, nil
}

// ownerTable 은 수집 주기마다 다시 읽는 장치(UUID) → 컨테이너 표다. 읽지 못한 회차는 표를
// 비운다 — 끝난 파드의 이름을 계속 붙이는 쪽이 라벨이 잠깐 빠지는 쪽보다 나쁘다.
type ownerTable struct {
	list func(context.Context) (*podresourcesv1.ListPodResourcesResponse, error)

	mu   sync.RWMutex
	byID map[string]podRef

	failing bool // 수집 goroutine 만 쓴다. 실패가 이어지는 동안 로그는 처음 한 번만 남긴다.
	warned  bool // 수집 goroutine 만 쓴다. 장치 ID 불일치 경고는 프로세스마다 한 번이다.
}

// refresh 는 kubelet 에서 할당 기록을 다시 읽어 표를 바꾼다.
func (o *ownerTable) refresh(ctx context.Context) {
	resp, err := o.list(ctx)
	switch {
	case err != nil && !o.failing:
		log.Printf("kubelet 할당 기록을 읽지 못해 namespace·pod·container 라벨을 비운다: %v", err)
	case err == nil && o.failing:
		log.Printf("kubelet 할당 기록을 다시 읽었다")
	}
	o.failing = err != nil
	var next map[string]podRef
	if err == nil {
		next = deviceOwners(resp)
	}
	o.mu.Lock()
	o.byID = next
	o.mu.Unlock()
}

// owner 는 장치(UUID)를 할당받은 컨테이너다. 할당되지 않았으면 빈 값이다.
func (o *ownerTable) owner(uuid string) podRef {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.byID[uuid]
}

// warnUnmatched 는 furiosa.ai 장치 할당이 있는데 수집한 장치 UUID 와 하나도 맞지 않으면 로그를 한 번
// 남긴다. device plugin 이 UUID 가 아닌 장치 ID 를 쓰면 List 는 성공하고 세 라벨만 소리 없이 빈다.
func (o *ownerTable) warnUnmatched(samples []DeviceSample) {
	if o.warned || len(samples) == 0 {
		return
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if len(o.byID) == 0 {
		return
	}
	for _, s := range samples {
		if _, ok := o.byID[s.UUID]; ok {
			return
		}
	}
	var example string
	for id := range o.byID {
		example = id
		break
	}
	log.Printf("kubelet 의 furiosa.ai 장치 ID %d개(예: %q)가 이 노드 장치 UUID 와 하나도 맞지 않아 "+
		"namespace·pod·container 라벨이 비어 나간다 — device plugin 의 장치 ID 형식을 확인할 것", len(o.byID), example)
	o.warned = true
}
