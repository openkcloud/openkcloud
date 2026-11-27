<!-- README.md: Tenstorrent Blackhole K8s Device Plugin | 생성일: 2026-04-22 | 수정일: 2026-09-09 -->

# Tenstorrent Blackhole Kubernetes Device Plugin

Kubernetes device plugin for Tenstorrent Blackhole NPU (p150, p300).  
Exposes `tenstorrent.com/blackhole` resources to the kubelet so pods can request Blackhole devices.

## Attribution

This plugin was written from scratch with reference to **OctopusET/k8s-device-plugin**  
(https://github.com/OctopusET/k8s-device-plugin), licensed under Apache 2.0.  
All source code in this repository is original and independently developed.

## Supported Devices

| Card Type | PCI ID | Resource |
|-----------|--------|----------|
| Blackhole p150 | TBD | `tenstorrent.com/blackhole` |
| Blackhole p300 | TBD | `tenstorrent.com/blackhole` |

## Architecture

```
/dev/tenstorrent/*          ← 디바이스 파일 (char device)
/sys/class/tenstorrent/<id> ← sysfs 메타 (card_type, numa_node)
```

| 패키지 | 역할 |
|--------|------|
| `pkg/discovery` | `/dev/tenstorrent/*` glob + sysfs 메타 조회 + fsnotify 감시 |
| `pkg/plugin` | kubelet device plugin gRPC 서버 (ListAndWatch / Allocate) |
| `pkg/registry` | kubelet.sock 에 RegisterRequest 전송 |
| `main.go` | graceful shutdown + kubelet.sock 재연결 처리 |

## Quick Start

### 이미지 빌드 & 푸시

```bash
sudo -E make docker-build docker-push IMG=<your-registry>/kcloud-tt-device-plugin:v0.1.0
```

### DaemonSet 배포

```bash
kubectl apply -f deploy/daemonset.yaml
```

### 노드에서 할당 확인

```bash
kubectl get node <node> -o jsonpath='{.status.allocatable}' | jq '."tenstorrent.com/blackhole"'
```

## 로컬 개발

```bash
# 빌드
make build

# 테스트
make test

# go vet
make vet
```

## 이미지 정보

- **Registry**: `ghcr.io/openkcloud` (기본값. `REGISTRY` 로 자체 미러를 지정한다)
- **Image**: `ghcr.io/openkcloud/kcloud-tt-device-plugin:v0.1.0`
- **Base**: `scratch` (multi-stage build)
- **Size**: < 20 MB

## 환경 요구사항

- Kubernetes 1.26+
- 노드에 Tenstorrent Blackhole 카드 설치
- `/dev/tenstorrent/*` 존재 (tt-kmd 드라이버 로드)
- kubelet device plugin API v1beta1

## License

Apache License 2.0 — 자세한 내용은 [LICENSE](LICENSE) 참조.
