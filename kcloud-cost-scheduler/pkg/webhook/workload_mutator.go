// Package webhook은 Kubernetes 리소스 및 Kueue Workload 객체의 Admission 제어를 담당합니다.
//
// Author: 미정 <support@kcloud.io>
// Created: 2024-06-15
// Related: JIRA-OPT-201 (Kueue Workload 기반 7종 최적화 힌트 통합)

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ── Annotation 키 상수 ─────────────────────────────────────────────────────────
const (
	annotationIntent          = "sched.ai/intent"           // 워크로드 목적: training | serving | inference | batch
	annotationPreference      = "sched.ai/preference"       // 스케줄링 방향: throughput | latency | cost | power
	annotationAccType         = "sched.ai/acc-type"         // 가속기 종류: gpu | npu | cpu
	annotationAccProfile      = "sched.ai/acc-profile"      // 가속기 프로파일: mig-preferred | small | medium | large
	annotationAccScale        = "sched.ai/acc-scale"        // 전체 가속기 수 (PodSet count × acc/pod)
	annotationRescheduling    = "sched.ai/rescheduling"     // 재배치 허용 여부: free | forbidden
	annotationDataSensitivity = "sched.ai/data-sensitivity" // 데이터 이동 민감도: low | very-high
	annotationOptimized       = "kcloud.io/optimized"       // 최적화 완료 플래그
)

// WorkloadMutator는 Kueue Workload 객체를 분석하여 정밀한 스케줄링 힌트를 주입합니다.
type WorkloadMutator struct {
	Client  client.Client
	decoder admission.Decoder
}

// NewWorkloadMutator는 새로운 WorkloadMutator 인스턴스를 생성합니다.
func NewWorkloadMutator(client client.Client) *WorkloadMutator {
	return &WorkloadMutator{
		Client: client,
	}
}

// Handle은 Workload Admission 요청을 처리하여 최적화 Annotation을 패치합니다.
//
// Parameters:
//   - ctx: 요청 컨텍스트
//   - req: Admission 요청 데이터
//
// Returns:
//   - admission.Response: JSON Patch가 포함된 응답
func (m *WorkloadMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := log.FromContext(ctx)

	wl := &unstructured.Unstructured{}
	if err := m.decoder.Decode(req, wl); err != nil {
		logger.Error(err, "Workload 객체 디코딩 실패")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// WARNING: 중복 최적화 방지 - 이미 처리된 객체는 스킵하여 연산 자원 낭비 최소화
	if wl.GetAnnotations() != nil && wl.GetAnnotations()[annotationOptimized] == "true" {
		return admission.Allowed("이미 최적화 처리됨")
	}

	if m.mutateWorkload(wl) {
		marshaled, err := json.Marshal(wl)
		if err != nil {
			logger.Error(err, "변조된 객체 마샬링 실패")
			return admission.Errored(http.StatusInternalServerError, err)
		}
		// WHY: Kueue 스케줄러가 대기 중인 Workload를 검사하기 전에 힌트를 etcd에 확정 저장함
		return admission.PatchResponseFromRaw(req.Object.Raw, marshaled)
	}

	return admission.Allowed("수정 사항 없음")
}

// mutateWorkload는 Workload 스펙을 정밀 분석하여 7종의 최적화 힌트를 생성합니다.
func (m *WorkloadMutator) mutateWorkload(wl *unstructured.Unstructured) bool {
	annotations := wl.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// 1. 가속기 정보 추출 (GPU/NPU 수 및 전체 스케일 계산)
	accType, accPerPod, totalScale := m.analyzeAccelerators(wl)

	// 2. 워크로드 인텐트 및 선호도 결정
	intent, preference := m.determineIntent(wl, accType > "cpu")

	// 3. 데이터 및 재배치 민감도 분석 (PVC 유무 기반)
	rescheduling, sensitivity := m.determineDataPolicy(wl)

	// 7종 Annotation 주입
	annotations[annotationIntent] = intent
	annotations[annotationPreference] = preference

	if accType != "cpu" {
		annotations[annotationAccType] = accType
		annotations[annotationAccProfile] = m.getAccProfile(accPerPod, accType)
		annotations[annotationAccScale] = fmt.Sprintf("%d", totalScale)
	}

	annotations[annotationRescheduling] = rescheduling
	annotations[annotationDataSensitivity] = sensitivity
	annotations[annotationOptimized] = "true"
	wl.SetAnnotations(annotations)

	return true
}

// analyzeAccelerators는 Workload 내의 모든 PodSet을 검사하여 가속기 통계를 반환합니다.
func (m *WorkloadMutator) analyzeAccelerators(wl *unstructured.Unstructured) (string, int64, int64) {
	var accType = "cpu"
	var maxAccPerPod int64 = 0
	var totalScale int64 = 0

	for _, ps := range getWorkloadPodSets(wl) {
		count := getInt64(ps, "count")
		if count == 0 {
			count = 1
		}
		containers := getPodSetContainers(ps)
		perPod := m.getAccCountPerPod(containers)

		if perPod > 0 {
			accType = m.inferAccType(containers)
			if perPod > maxAccPerPod {
				maxAccPerPod = perPod
			}
			totalScale += count * perPod
		}
	}
	return accType, maxAccPerPod, totalScale
}

// determineIntent는 워크로드 명칭과 가속기 사용 여부로 목적을 추론합니다.
func (m *WorkloadMutator) determineIntent(wl *unstructured.Unstructured, hasAcc bool) (string, string) {
	name := strings.ToLower(wl.GetName())

	// HACK(미정, 2024-06-15): 키워드 기반 추론은 향후 WorkloadOptimizer CRD 연동으로 대체 예정
	if strings.Contains(name, "train") {
		return "training", "throughput"
	}
	if strings.Contains(name, "serve") || strings.Contains(name, "infer") {
		return "serving", "latency"
	}

	if hasAcc {
		return "training", "throughput"
	}
	return "batch", "cost"
}

// getAccProfile은 가속기 수에 따른 최적 운용 프로파일을 결정합니다.
func (m *WorkloadMutator) getAccProfile(count int64, accType string) string {
	if accType == "gpu" {
		switch {
		case count <= 1:
			return "mig-preferred"
		case count <= 2:
			return "small"
		case count <= 5:
			return "medium"
		default:
			return "large"
		}
	}
	if accType == "npu" {
		if count <= 1 {
			return "npu-single"
		}
		return "npu-multi"
	}
	return "none"
}

// determineDataPolicy는 볼륨 구성을 확인하여 재배치 가능 여부를 판단합니다.
func (m *WorkloadMutator) determineDataPolicy(wl *unstructured.Unstructured) (string, string) {
	for _, ps := range getWorkloadPodSets(wl) {
		for _, vol := range getPodSetVolumes(ps) {
			if vol.PersistentVolumeClaim != nil {
				// WHY: PVC가 연결된 워크로드는 데이터 지역성 문제로 재배치를 금지함
				return "forbidden", "very-high"
			}
		}
	}
	return "free", "low"
}

// inferAccType은 컨테이너 리소스 제한에서 가속기 제조사를 식별합니다.
func (m *WorkloadMutator) inferAccType(containers []corev1.Container) string {
	for _, c := range containers {
		for resName := range c.Resources.Limits {
			name := string(resName)
			if strings.Contains(name, "nvidia.com") {
				return "gpu"
			}
			if strings.Contains(name, "npu.com") || strings.Contains(name, "furiosa.ai") {
				return "npu"
			}
		}
	}
	return "cpu"
}

// getAccCountPerPod은 단일 파드에 할당된 가속기 수량을 합산합니다.
func (m *WorkloadMutator) getAccCountPerPod(containers []corev1.Container) int64 {
	var total int64 = 0
	for _, c := range containers {
		for resName, qty := range c.Resources.Limits {
			if strings.Contains(string(resName), "gpu") || strings.Contains(string(resName), "npu") {
				total += qty.Value()
			}
		}
	}
	return total
}

func getWorkloadPodSets(wl *unstructured.Unstructured) []map[string]interface{} {
	podSets, found, _ := unstructured.NestedSlice(wl.Object, "spec", "podSets")
	if !found {
		return nil
	}

	result := make([]map[string]interface{}, 0, len(podSets))
	for _, item := range podSets {
		if podSet, ok := item.(map[string]interface{}); ok {
			result = append(result, podSet)
		}
	}
	return result
}

func getPodSetContainers(podSet map[string]interface{}) []corev1.Container {
	items, found, _ := unstructured.NestedSlice(podSet, "template", "spec", "containers")
	if !found {
		items, found, _ = unstructured.NestedSlice(podSet, "spec", "containers")
	}
	if !found {
		return nil
	}

	containers := make([]corev1.Container, 0, len(items))
	for _, item := range items {
		containerMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		container := corev1.Container{
			Name: getString(containerMap, "name"),
		}
		container.Resources.Limits = parseResourceList(containerMap, "resources", "limits")
		container.Resources.Requests = parseResourceList(containerMap, "resources", "requests")
		containers = append(containers, container)
	}
	return containers
}

func getPodSetVolumes(podSet map[string]interface{}) []corev1.Volume {
	items, found, _ := unstructured.NestedSlice(podSet, "template", "spec", "volumes")
	if !found {
		items, found, _ = unstructured.NestedSlice(podSet, "spec", "volumes")
	}
	if !found {
		return nil
	}

	volumes := make([]corev1.Volume, 0, len(items))
	for _, item := range items {
		volumeMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		volume := corev1.Volume{Name: getString(volumeMap, "name")}
		if pvc, ok := volumeMap["persistentVolumeClaim"].(map[string]interface{}); ok {
			volume.PersistentVolumeClaim = &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: getString(pvc, "claimName"),
			}
		}
		volumes = append(volumes, volume)
	}
	return volumes
}

func parseResourceList(container map[string]interface{}, fields ...string) corev1.ResourceList {
	values, found, _ := unstructured.NestedStringMap(container, fields...)
	if !found {
		return nil
	}

	resources := corev1.ResourceList{}
	for name, value := range values {
		if qty, err := resource.ParseQuantity(value); err == nil {
			resources[corev1.ResourceName(name)] = qty
		}
	}
	return resources
}

func getString(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	return value
}

func getInt64(values map[string]interface{}, key string) int64 {
	switch value := values[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

// InjectDecoder는 Admission 컨트롤러의 디코더를 주입합니다.
func (m *WorkloadMutator) InjectDecoder(d admission.Decoder) error {
	m.decoder = d
	return nil
}
