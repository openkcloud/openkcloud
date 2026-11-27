<!-- INSTALL.md: kcloud-operator Helm 설치와 확인 절차 | 생성일: 2026-09-14 | 수정일: 2026-10-28 -->

# kcloud-operator 설치 · 확인 절차

공개 GHCR 에서 차트를 받아 Service Kubernetes 에 설치하고, 노드가 GPU 와 NPU 를 자원으로 광고하는지까지 확인하는 절차입니다. Helm 상태가 `deployed` 로 나와도 노드의 allocatable 이 비어 있으면 워크로드는 스케줄되지 않으므로, 마지막 확인 단계까지 진행해야 설치가 끝난 것입니다.

순서는 사전 확인, 프로파일 선택, 사이트 값 작성, 설치, 검증 다섯 단계입니다.

## 1. Helm 과 Kubernetes 버전 확인

여기서 확인한 Kubernetes 마이너 버전이 다음 단계의 프로파일을 결정합니다.

```bash
helm version --short
kubectl version -o yaml | grep -A3 serverVersion
kubectl get nodes -o wide
```

```
v3.16.2+g13654a5
serverVersion:
  major: "1"
  minor: "34"
```

## 2. 릴리스 프로파일 선택

마이너 버전에 맞는 차트와 프리셋을 고릅니다. 설치에 쓰는 values 주소는 커밋으로 고정돼 있어 같은 주소가 늘 같은 내용을 돌려줍니다. 정본은 `deploy/integration/release.yaml` 입니다.

| Service K8s | 차트 | 프리셋 | 설치용 values 주소 |
|---|---|---|---|
| 1.31~1.34 | 0.8.4 | `values-k8s1.34.yaml` | `release.yaml` 의 `1.31-1.34` 프로필 `valuesURL` |
| 1.26~1.30 | 0.6.3 | `values-k8s1.28.yaml` | `release.yaml` 의 `1.26-1.30` 프로필 `valuesURL` |

브랜치 주소(`release.yaml` 의 `valuesBrowseURL`)는 최신 내용을 사람이 확인할 때만 씁니다. 브랜치는 새 커밋이 들어오면 같은 주소가 다른 내용을 돌려주므로 설치에는 쓰지 않습니다.

values 주소는 이 문서에 적지 않고 `release.yaml` 에서 그대로 옮깁니다. 주소를 문서에 다시 적으면 프리셋을 고칠 때 문서만 옛 커밋에 남습니다.

```bash
export CHART_VERSION=0.8.4
export VALUES_URL='<release.yaml 1.31-1.34 프로필의 valuesURL>'
```

1.26~1.30 클러스터라면 `CHART_VERSION=0.6.3` 과 `1.26-1.30` 프로필의 `valuesURL` 을 쓰고, 프리셋 파일 이름은 `values-k8s1.28.yaml` 입니다. 그 프리셋은 차트 `0.6.3` 을 담은 `release/k8s-1.28` 브랜치에 있고 이 라인(1.31~1.34) 차트의 소스 트리에는 없습니다 — 위 표의 values 주소는 그 브랜치 커밋을 가리키므로 설치 절차는 같습니다. 이 라인의 차트를 1.26~1.30 클러스터에 설치하려 하면 `kubeVersion` 선언 때문에 helm 이 거절합니다.

## 3. site-values.yaml 작성

저장소에 들어 있는 파일이 아니라, 설치 직전에 만들어 값을 덮어쓰는 로컬 파일입니다. 공개망이라 덮어쓸 값이 없어도 만들어야 합니다. helm 은 `-f` 로 지정한 파일이 없으면 실패합니다.

```bash
cat > site-values.yaml <<'EOF'
global:
  registry: ghcr.io/openkcloud
  vendorRegistry: ""
EOF
```

사설 미러를 쓰면 두 값을 미러 주소로 바꿉니다. `nvidia.devicePluginImage` 처럼 전체 경로를 그대로 받는 항목은 조립되지 않으므로 이 파일에 직접 적습니다. 본보기는 `../integration/examples/site-values.example.yaml` 에 있습니다.

Warboy 노드가 있으면 설치 전에 Furiosa 사설 APT 저장소 자격 증명을 릴리스 네임스페이스의 Secret `furiosa-apt-auth` 로 만듭니다. 키 `furiosa.conf` 는 apt `auth.conf` 형식이고, `furiosa.list`(저장소 줄)와 `furiosa-apt-key.gpg`(서명 키)는 필요할 때만 넣습니다. RNGD 는 공개 저장소를 쓰므로 이 Secret 이 필요 없습니다.

```bash
kubectl create namespace kcloud --dry-run=client -o yaml | kubectl apply -f -
kubectl -n kcloud create secret generic furiosa-apt-auth --from-file=furiosa.conf=./furiosa.conf
```

0.8.1 은 이 Secret 이 없으면 Warboy 설치 파드가 볼륨 마운트에서 멈춘 채 최대 30분(드라이버 버전 변경이면 최대 약 2시간) 노드를 cordon 하고 드라이버 업그레이드 슬롯을 잡습니다. 슬롯은 벤더를 구분하지 않으므로 그동안 같은 노드의 NVIDIA 를 포함한 다른 드라이버 설치도 기다립니다. 멈춘 뒤라도 Secret 을 만들면 kubelet 이 마운트를 다시 시도해 설치가 이어집니다. 이 시한이 지나 Warboy 의 DriverUpgradeState 가 `Failed` 가 되었으면 Secret 을 만든 뒤 DriverUpgradeState 를 지워 다시 시작합니다. 0.8.3 이하는 실패한 설치 Job 이 10분 동안 남아 새 사이클이 그 Job 을 다시 써서 곧바로 실패하므로, 남은 설치 Job 도 함께 지웁니다.

```bash
kubectl -n kcloud delete job -l npu.ai/vendor=furiosa,npu.ai/node=<노드>
kubectl delete driverupgradestate <노드>-furiosa
```

0.8.3 부터는 Secret 이 없으면 Warboy 설치 사이클을 시작하지 않고 기다립니다. DriverUpgradeState 는 `Idle` 에 머물고 사유와 이벤트 `InstallSecretMissing` 을 남기며, 노드 cordon 과 슬롯을 잡지 않습니다. Secret 을 만들면 1분 안팎에 설치가 시작되고, 다른 드라이버 설치(같은 노드의 다른 벤더 포함)가 슬롯을 쓰는 중이면 그 설치가 끝난 뒤에 시작됩니다. 차트가 만드는 Secret 확인용 Role 이 없는 배포(`rbac.create=false` 등)는 확인을 건너뛰고 0.8.1 처럼 동작합니다.

Rebellions ATOM+ 노드는 공식 device plugin 두 판을 모두 쓸 수 있습니다. 기본은 `v0.3.6` 이고, 신판을 쓰려면 `rebellions.devicePluginTag` 를 `v0.4.3` 으로 바꿉니다. 판은 태그로 판별합니다. digest 로 고정해 태그가 없으면 `rebellions.devicePluginGeneration` 에 `v0.3` 또는 `v0.4` 를 적습니다. 사설 미러를 쓰면 `rebellions/k8s-device-plugin:v0.4.3` 을 미리 미러에 올립니다.

플러그인 파드의 init container `rbln-cdi-prep` 는 기동 전에 두 가지를 합니다. 먼저 호스트 `/usr/bin` 이나 `/usr/local/bin` 에 `rbln-smi` 가 없으면 실패해 그 노드에서 플러그인을 띄우지 않습니다. 파드마다 전용 NPU 묶음(RSD 그룹)을 만들 수 없는 노드에서 노드 전체 묶음이 파드에 들어가는 일을 막기 위해서입니다. 다음으로 `/var/run/cdi/kcloud-rebellions.yaml` 에 플러그인이 요청하는 CDI 이름을 정의합니다. containerd 2.x 처럼 CDI 가 켜진 노드에서는 이 파일이 있어야 ATOM 파드가 만들어지고, CDI 가 꺼진 노드에서는 containerd 가 이 파일을 읽지 않습니다. v0.4 형식으로 쓴 노드에는 이 파일이 Rebellions 를 끄거나 operator 를 지운 뒤에도 남으므로, 그 노드에 벤더 container toolkit 을 설치하기 전에 파일을 지우거나 노드를 재부팅합니다(두 파일이 같은 이름 `runtime` 을 정의하면 CDI 가 둘 다 무시해 그 노드의 ATOM 파드가 만들어지지 않습니다).

준비 상태는 NPUClusterPolicy 조건 `RebellionsDevicePluginReady` 로 확인합니다. 사유가 `RblnSmiMissing` 이면 노드에 Rebellions SDK 도구를 설치하고, `NoRblnDevice` 이면 드라이버를 확인합니다. `PluginCrashLoop` 이면 플러그인 판이 이미지와 맞는지, `PluginNotStarted` 이면 플러그인 이미지를 받을 수 있는지(사설 미러에 올렸는지) 확인합니다. `PrepFailed` 이면 init container 이미지(operator 의 `HOST_EXEC_IMAGE` 환경변수가 가리키는 host-exec 이미지)를 노드가 받을 수 있는지와 플러그인 파드의 init container 종료 메시지(`kubectl describe pod`)를 확인하고, `CDIWriteFailed` 이면 노드의 `/var/run/cdi` 에 쓸 수 있는지 확인합니다. `HostExecImageUnset` 은 차트 밖에서 operator 를 실행해 `HOST_EXEC_IMAGE` 가 비었을 때 나오며, 이때는 init container 없이 플러그인만 배포되므로 환경변수를 채우고 operator 를 다시 시작합니다.

```bash
kubectl get npuclusterpolicy -A -o jsonpath='{.items[*].status.conditions[?(@.type=="RebellionsDevicePluginReady")]}'
```

`kubectl npu status` 의 NPUClusterPolicy 절에서도 같은 조건이 `rebellions  device-plugin  True  Prepared  판: …` 줄로 보입니다. 같은 절의 `operator` 줄은 외부 벤더 operator 감지 결과라 kcloud 가 직접 배포하는 환경에서는 `NotDetected` 가 정상입니다.

## 4. 설치 전 렌더링 점검

리소스를 만들지 않고 렌더링 결과만 먼저 확인합니다.

```bash
helm template kcloud-operator oci://ghcr.io/openkcloud/charts/kcloud-operator \
    --version "$CHART_VERSION" -n kcloud \
    -f "$VALUES_URL" -f site-values.yaml \
  | grep -E "image:|kind:" | sort -u
```

operator 이미지가 `ghcr.io/openkcloud/kcloud-operator:v0.8.4` 로 나오는지 봅니다. 목록에 DRA 드라이버 이미지 두 개가 함께 보이는데, 기본값이 꺼져 있어 실제로 배포되지는 않습니다. `kcloud-host-exec` 과 Tenstorrent device-plugin 은 차트가 아니라 operator 가 실행 중에 만들기 때문에 이 목록에 나오지 않습니다.

## 5. 설치

릴리스가 없으면 설치하고, 이미 있으면 업그레이드로 동작합니다.

```bash
helm upgrade --install kcloud-operator \
    oci://ghcr.io/openkcloud/charts/kcloud-operator \
    --version "$CHART_VERSION" \
    -n kcloud --create-namespace \
    --reset-values \
    -f "$VALUES_URL" -f site-values.yaml \
    --wait --wait-for-jobs --timeout 15m
```

`--reset-values` 는 이번 프리셋과 사이트 값을 기준으로 값을 다시 계산합니다. `--wait` 는 일반 리소스가 Ready 가 될 때까지, `--wait-for-jobs` 는 hook Job 이 끝날 때까지 기다립니다. hook 을 생략하면 CRD 가 갱신되지 않으므로 `--no-hooks` 는 쓰지 않습니다.

설치가 끝나면 `STATUS: deployed` 와 안내 문구가 나옵니다. 여기서 끝내지 않고 아래 확인 단계를 이어서 진행합니다.

## 6. 확인 1 — 릴리스와 Operator Pod

```bash
helm list -n kcloud
kubectl -n kcloud get pods
```

릴리스가 `deployed` 이고 operator Pod 가 `1/1 Running` 이어야 합니다. `CrashLoopBackOff` 이면 `kubectl -n kcloud logs deploy/kcloud-operator --tail=50` 으로 원인을 봅니다.

## 7. 확인 2 — CRD 와 정책 리소스

```bash
kubectl get crd | grep npu.ai | wc -l
kubectl -n kcloud get npuclusterpolicy npuclusterpolicy-sample \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
kubectl get dip -o custom-columns='NAME:.metadata.name,VENDOR:.spec.vendor,MODE:.spec.driver.mode'
```

CRD 는 13종입니다. 정책 CR 이름은 `npuclusterpolicy-sample` 이고 네임스페이스는 `kcloud` 이며, `get` 출력에는 READY 열이 없으므로 조건 값을 직접 읽습니다. DriverInstallPolicy 는 클러스터 범위이고 기본 이름은 `nvidia-gpu-ds`, `furiosa-warboy-ds`, `furiosa-rngd-ds`, `tenstorrent-blackhole-ds`, `rebellions-atom-ds` 입니다. `rebellions-atom-ds` 는 설치 경로가 없는 추적 전용(`trackOnly`) 정책입니다. Rebellions 드라이버 패키지는 벤더 포털 인증이 필요해 호스트 관리자가 설치하고, operator 는 노드별 버전만 DriverUpgradeState 로 기록합니다. 1.31~1.34 프리셋(`values-k8s1.34.yaml`)은 Rebellions 를 공개 범위 밖으로 두어 이 정책도 끕니다. 켜려면 사이트 값으로 `driverInstallPolicies.rebellions.enabled=true` 를 줍니다.

## 8. 확인 3 — 노드 라벨과 DaemonSet

`kcloud-node-manager` 가 PCI 장치를 읽어 라벨을 붙이고, device-plugin 과 드라이버 설치 Job 은 그 라벨이 있는 노드에만 배치됩니다.

```bash
kubectl get nodes -L kcloud.ai/nvidia.present,kcloud.ai/rngd.present,kcloud.ai/tenstorrent.present
kubectl get ds -A | grep -E "kcloud-|device-plugin"
```

구성 요소마다 네임스페이스가 다릅니다. `kcloud-node-manager`, `kcloud-furiosa-exporter`, Tenstorrent device-plugin 은 릴리스 네임스페이스인 `kcloud` 에 있고, 벤더 device-plugin 과 dcgm-exporter, Container Toolkit 은 `kube-system` 에 있습니다. 한쪽만 보면 빠지는 것이 생기므로 `-A` 로 봅니다.

## 9. 확인 4 — 드라이버 상태

```bash
kubectl get driverupgradestate
kubectl -n kcloud get jobs
```

이름은 `<노드>-<벤더>` 형식이고 열은 NODE, VENDOR, STATE, CURRENT, DESIRED 입니다. 설치 Job 이 끝나면 상태가 `Idle` 로 돌아옵니다. 드라이버 설치 Job 도 `kcloud` 네임스페이스에 생깁니다. `Installing` 이 오래 유지되면 그 Job 의 로그를 확인합니다. Warboy 설치 파드가 `ContainerCreating` 에 머물고 `kubectl -n kcloud describe pod <파드>` 이벤트에 `secret "furiosa-apt-auth" not found` 가 보이거나(0.8.1), Warboy 가 `Idle` 인데 드라이버가 없고 `kubectl describe driverupgradestate <노드>-furiosa` 의 사유가 `설치 Secret … 없음` 이면(0.8.1 다음 버전) 3단계의 Secret 을 만듭니다.

`kubectl npu driver status` 는 `Failed` 인 행의 사유를 표 아래 `Failed 상세` 에 한 줄씩 보여 줍니다. 사유가 hold 된 패키지이면 11절의 "NVIDIA 드라이버 설치가 hold 된 패키지에 막힐 때" 를 봅니다.

## 10. 확인 5 — 노드 자원 광고

```bash
kubectl get nodes -o custom-columns='NODE:.metadata.name,GPU:.status.allocatable.nvidia\.com/gpu,RNGD:.status.allocatable.furiosa\.ai/rngd'
kubectl get ndr
kubectl npu status
```

노드가 자원을 광고하면 워크로드를 스케줄할 수 있습니다. `NodeDeviceReport` 는 클러스터 범위라 이름과 나이만 나옵니다. 노드별 장치와 드라이버 버전을 한눈에 보려면 `kubectl npu status` 를 씁니다.

## 11. 설치가 실패할 때

이벤트를 먼저 보고, 정상이 아닌 Pod 를 찾아 해당 구성 요소의 로그를 읽습니다.

```bash
kubectl -n kcloud get events --sort-by=.lastTimestamp | tail -20
kubectl get pods -A | grep -vE "Running|Completed"
```

`ImagePullBackOff` 이면 레지스트리부터 봅니다. 공개망인지 사설망인지, pull secret 이 필요한지, `global.registry` 와 전체 경로를 직접 지정한 항목이 맞는지 순서로 확인합니다.

### NVIDIA 드라이버 설치가 hold 된 패키지에 막힐 때

노드 이미지가 `apt-mark hold` 로 묶어 둔 패키지를 드라이버 설치가 올려야 하면 apt 가 설치를 거절합니다. 차트 0.8.4 이상의 operator 는 설치기 `nvidia-driver-ds:v9` 이상을 쓰는 NVIDIA 정책(`driver.mode: job`, `driver.installer: apt`)에서, 노드를 cordon 하기 전에 설치 전 점검 Job 을 만들어 이 충돌을 먼저 찾습니다. 차트 0.8.4 의 설치기 기본 태그는 `v9` 입니다. 점검은 드라이버 패키지와, DKMS 빌드에 필요한 커널 헤더·빌드 도구(`build-essential`·`dkms`·`gcc`·`make`)를 함께 모의 설치합니다. 점검은 apt 목록만 갱신하고, 패키지를 설치·제거하거나 hold 를 바꾸지 않습니다. 충돌이 있으면 DriverUpgradeState 가 재시도 없이 `Failed` 로 끝나고, 사유에 hold 된 패키지 이름과 apt 의 거절 줄이 남습니다.

```bash
kubectl npu driver status
kubectl describe driverupgradestate <노드>-nvidia
```

hold 를 풀지는 노드 이미지 관리자가 정합니다. 풀기로 했다면 노드에서 해제한 뒤 DriverUpgradeState 를 지워 다시 시작합니다. `Failed` 는 정책을 다시 읽지 않으므로 지워야 다음 사이클이 시작됩니다.

```bash
# 노드에서
apt-mark showhold
sudo apt-mark unhold <패키지>
# 클러스터에서
kubectl delete driverupgradestate <노드>-nvidia
```

hold 를 유지해야 하는 노드라면 드라이버가 이미 설치된 노드 이미지를 쓰고 `driverInstallPolicies.nvidia.driver.versionSource: Host` 로 그 버전을 따르게 합니다. 첫 설치가 다른 이유로 실패해도 되돌릴 이전 버전이 없으므로 롤백을 거치지 않고, 설치 컨테이너가 남긴 사유와 함께 `Failed` 가 됩니다. DriverUpgradeState 를 지우면 새 사이클이 남아 있는 실패한 설치 Job 을 지우고 다시 만듭니다.

`upgradePolicy.autoUpgrade=false` 정책에서는 드라이버가 로드된 노드의 DriverUpgradeState 를 지워도 사이클이 열리지 않습니다. 새 DriverUpgradeState 가 로드된 버전을 채택하고 `UpgradeAvailable` 경고만 남깁니다. 다시 설치하려면 `kubectl npu driver upgrade`(트리거) 또는 `upgradePolicy.forceUpgrade` 를 씁니다. 직전 사이클이 cordon 한 채 `Failed` 로 끝났다면 operator 가 `Idle` 로 돌아올 때 자기 cordon 을 풉니다. 다른 주체가 건 cordon 은 그대로 두므로 직접 `kubectl uncordon <노드>` 로 풉니다.

설치기 `v8` 이하는 점검 스크립트가 없어 점검 Job 이 `StartError` 로 끝나고, operator 는 경고 이벤트(`DriverPreflightInconclusive`)만 남긴 채 예전처럼 설치를 시도합니다. apt 목록 갱신 실패처럼 점검이 판정하지 못할 때도 같습니다.

## 12. 되돌리기와 삭제

```bash
helm history kcloud-operator -n kcloud
helm rollback kcloud-operator 1 -n kcloud
helm uninstall kcloud-operator -n kcloud --wait
```

가속기를 쓰는 Pod 가 남아 있으면 pre-delete hook 이 삭제를 중단시키고 uninstall 이 실패합니다. 사유는 `kubectl -n kcloud logs job/kcloud-operator-uninstall-gate` 에 남습니다. 삭제 정책은 `README.md` 의 Uninstall 절에 있습니다.

Rebellions 를 v0.4 형식으로 쓴 노드에는 `/var/run/cdi/kcloud-rebellions.yaml` 이 남습니다. 이 파일은 노드를 재부팅하면 사라지고, 그 전에 벤더 container toolkit 을 설치하려면 `sudo rm -f /var/run/cdi/kcloud-rebellions.yaml` 로 지웁니다. 같은 디렉터리의 두 파일이 같은 이름 `runtime` 을 정의하면 CDI 가 둘 다 무시해 그 노드의 ATOM 파드가 만들어지지 않습니다.

## 13. 완료 판정

여섯 항목이 모두 통과하면 설치가 끝난 것입니다.

| 항목 | 기준 |
|---|---|
| Helm 릴리스 | STATUS 가 deployed |
| Operator Pod | 1/1 Running |
| CRD 와 정책 | CRD 13종, Ready 조건이 True |
| 노드와 DaemonSet | 벤더 라벨이 붙고 두 네임스페이스의 DaemonSet 이 Ready |
| 드라이버 | DriverUpgradeState 가 Idle |
| 자원 광고 | 노드 allocatable 에 GPU 나 NPU 가 보임 |

Magnum CAPI Add-on 은 이 순서를 그대로 자동화합니다. 활성화 여부 확인, Kubernetes 버전 판별, 프로파일 선택, 사이트 값 생성, Helm 실행 순서이며 상수는 `deploy/integration/release.yaml` 에 있습니다.
