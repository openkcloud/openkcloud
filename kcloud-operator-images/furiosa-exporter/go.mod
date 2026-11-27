// go.mod: Furiosa RNGD 텔레메트리 exporter 모듈
// 상세: furiosa-smi-go(cgo)로 코어 사용률·메모리·온도·전력을 읽어
//       Prometheus 포맷으로 :9410 에 노출한다. kubelet PodResources 클라이언트는
//       --kube-resources-label 의 namespace·pod·container 라벨에 쓴다.
// 생성일: 2026-08-07 | 수정일: 2026-10-15
module furiosa-exporter

go 1.25.0

require (
	github.com/furiosa-ai/furiosa-smi-go v0.6.1-0.20260423053636-b0ca0981e92f
	github.com/prometheus/client_golang v1.24.1
	google.golang.org/grpc v1.72.1
	k8s.io/kubelet v0.34.3
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bradfitz/iter v0.0.0-20191230175014-e8f45d346db8 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250303144028-a0af3efb3deb // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
