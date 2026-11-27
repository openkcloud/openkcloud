# kcloud-operator-images

[kcloud-operator](https://github.com/openkcloud/kcloud-operator) 가 노드에 배포하는 부속 컨테이너 이미지의 빌드 컨텍스트를 모아 둔 저장소입니다. 디렉터리 하나가 이미지 하나에 대응하고, 발행된 이미지는 `ghcr.io/openkcloud/<이미지명>:<태그>` 로 받습니다. operator 의 Helm 차트는 `global.registry` 기본값이 `ghcr.io/openkcloud` 라서 별도 설정 없이 이 저장소가 발행한 이미지를 사용합니다.

## 이미지 목록

| 이미지 | 디렉터리 | 역할 | 태그 형태 | 현재 태그 |
|---|---|---|---|---|
| `kcloud-host-exec` | `kcloud-host-exec/` | operator 가 호스트에서 명령을 대신 실행할 때 쓰는 hostPID Job 이미지입니다. `alpine` 에 `nsenter` 만 더했습니다. MIG 관측과 적용, 노드 재부팅이 이 이미지를 공유합니다. | `vX.Y.Z` | `v0.1.0` |
| `kcloud-node-manager` | `node-device-detector/` | 전 노드에서 상시 실행되는 비특권 감지 에이전트입니다. PCI 장치, 드라이버 버전, MIG 상태를 스캔해 `NodeDeviceReport` 리소스에 기록합니다. | `X.Y.Z` | `0.6.21` |
| `nvidia-driver-ds` | `driver-installer/nvidia/` | NVIDIA 드라이버 설치기입니다. 빌드 파일은 `Dockerfile.v17` 입니다. 이미지에 NVIDIA 패키지를 담지 않습니다(설치는 노드 apt). | `vN` | `v9` |
| `furiosa-driver-ds` | `driver-installer/furiosa/` | Furiosa Warboy 와 RNGD 공용 드라이버 설치기입니다. | `vN` | `v4` |
| `tenstorrent-driver-ds` | `driver-installer/tenstorrent/` | Tenstorrent tt-kmd 드라이버 설치기입니다. 태그의 앞부분이 내장한 tt-kmd 소스의 버전입니다. | `<kmd 버전>-vN` | `2.8.0-v1` |
| `kcloud-tt-device-plugin` | `tenstorrent-device-plugin/` | Tenstorrent Blackhole 용 Kubernetes device plugin 입니다. `tenstorrent.com/blackhole` 리소스를 광고합니다. | `vX.Y.Z` | `v0.1.0` |
| `furiosa-exporter` | `furiosa-exporter/` | Furiosa NPU 지표를 Prometheus 형식으로 내보내는 exporter 입니다. 차트 기본값은 비활성입니다. | `X.Y.Z` | `0.4.0` |

태그 형태는 operator 차트의 `values.yaml` 이 참조하는 값을 따릅니다. 이미지마다 형태가 달라 저장소 전체를 아우르는 버전 번호는 없습니다.

## 벤더 산출물 처리

이 저장소는 벤더가 배포하는 드라이버와 라이브러리를 이미지에 담지 않습니다.

- 드라이버 설치기 3종은 드라이버 바이너리를 포함하지 않습니다. NVIDIA 설치기는 실행 시점에 Ubuntu 패키지 저장소에서, Furiosa 설치기는 벤더 apt 저장소에서 드라이버 패키지를 받아 호스트에 설치합니다.
- Tenstorrent 설치기는 빌드 시점에 upstream 저장소의 태그에서 tt-kmd 소스를 받아 담고, 실행 시점에 DKMS 로 호스트 커널에 맞춰 빌드합니다.
- `furiosa-exporter` 는 벤더 라이브러리 `libfuriosa_smi.so` 를 빌드 단계에서만 링크하고 이미지에는 담지 않습니다. 실행 시점에는 드라이버 설치기가 호스트에 설치한 같은 파일을 hostPath 로 읽습니다.

device plugin 처럼 벤더가 공개 이미지를 제공하는 구성 요소는 이 저장소에서 빌드하지 않고 operator 차트가 벤더 이미지를 직접 참조합니다.

## 발행 절차

발행 대상과 빌드 방법은 저장소 루트의 [images.yaml](images.yaml) 이 정합니다. 각 항목의 키가 이미지 이름이고, `dir` 이 빌드 컨텍스트, `dockerfile` 이 빌드 파일 이름(기본값 `Dockerfile`), `build_args` 가 고정 build-arg 입니다. 워크플로 [publish-image.yml](.github/workflows/publish-image.yml) 이 이 파일을 읽어 한 번에 이미지 하나를 빌드하고 GHCR 로 push 합니다.

### git 태그로 발행

`<이미지명>/<태그>` 형태의 git 태그를 push 하면 슬래시 앞의 이미지가 슬래시 뒤의 태그로 발행됩니다.

```bash
git tag kcloud-host-exec/v0.1.1
git push origin kcloud-host-exec/v0.1.1
```

### 수동 실행으로 발행

GitHub Actions 에서 `Publish image` 워크플로를 골라 실행합니다. 입력은 세 가지입니다.

- `image`: images.yaml 에 등록된 이미지 이름을 목록에서 고릅니다.
- `tag`: 위 표의 형태를 따르는 이미지 태그를 적습니다.
- `build_args`: 선택 입력입니다. 한 줄에 `KEY=VALUE` 하나씩 적으면 images.yaml 의 기본값보다 우선합니다.

tt-kmd 버전을 올릴 때가 `build_args` 의 대표 용례입니다. `tenstorrent-driver-ds` 를 고르고 태그를 `2.9.0-v1` 로 준 뒤 다음 두 줄을 넣습니다.

```
TT_KMD_VERSION=2.9.0
TT_KMD_GIT_TAG=ttkmd-2.9.0
```

`furiosa-driver-ds` 의 `BUILD_VARIANT` 는 images.yaml 에 `${TAG}` 로 적혀 있어 발행 태그로 자동 치환됩니다.

### 기존 태그는 덮어쓰지 않음

워크플로는 push 전에 `docker manifest inspect` 로 같은 태그가 GHCR 에 있는지 확인하고, 있으면 실패합니다. 같은 태그의 내용이 바뀌면 이미 그 태그를 받은 노드와 새로 받는 노드가 서로 다른 이미지를 실행하게 되기 때문입니다. 내용을 바꾸려면 태그를 올립니다.

### 새 이미지 추가

두 곳을 함께 고칩니다. 한 곳만 고치면 워크플로가 그 이름을 입력으로 받지 못하거나 매니페스트에서 항목을 찾지 못해 실패합니다.

1. `images.yaml` 에 항목을 추가합니다. `dir` 은 필수이고, 빌드 파일 이름이 `Dockerfile` 이 아니면 `dockerfile` 을, 고정 build-arg 가 있으면 `build_args` 를 함께 적습니다.
2. `publish-image.yml` 의 `workflow_dispatch` 입력 `image` 선택지에 같은 이름을 넣습니다.

빌드 컨텍스트는 항상 `dir` 이 가리키는 디렉터리입니다. 그 바깥의 파일을 참조하는 Dockerfile 은 이 워크플로로 빌드할 수 없습니다.

### GHCR 패키지 공개 설정

워크플로는 각 이미지에 `org.opencontainers.image.source` 라벨을 붙이고, GHCR 은 이 라벨로 패키지를 저장소에 연결합니다. 연결이 곧 공개는 아닙니다. 조직 설정에 따라 처음 발행된 패키지가 비공개일 수 있으므로, 익명 pull 이 거부되면 GHCR 패키지 설정에서 공개로 바꿉니다. openkcloud 조직은 공개로 발행됩니다.

## 로컬 빌드

워크플로와 같은 방식으로 각 디렉터리를 컨텍스트로 빌드합니다.

```bash
docker build -t kcloud-host-exec:dev kcloud-host-exec
docker build -t kcloud-node-manager:dev node-device-detector
docker build -t nvidia-driver-ds:dev -f driver-installer/nvidia/Dockerfile.v17 driver-installer/nvidia
docker build -t furiosa-driver-ds:dev --build-arg BUILD_VARIANT=dev driver-installer/furiosa
docker build -t tenstorrent-driver-ds:dev driver-installer/tenstorrent
docker build -t kcloud-tt-device-plugin:dev tenstorrent-device-plugin
docker build -t furiosa-exporter:dev furiosa-exporter
```

`furiosa-driver-ds` 는 이미지가 자기 태그를 알아야 설치 마커를 판정하므로 `BUILD_VARIANT` 를 발행 태그와 같은 값으로 줍니다. `furiosa-exporter` 는 빌드 단계에서 벤더 apt 저장소에 접근하므로 빌드 환경에 인터넷이 필요합니다.

## 관련 저장소

- [kcloud-operator](https://github.com/openkcloud/kcloud-operator): 이 이미지들을 배포하는 operator 와 Helm 차트입니다. 차트의 `values.yaml` 이 각 이미지의 이름과 태그를 정합니다.
