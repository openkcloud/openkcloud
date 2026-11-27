# 테스트배드 서비스 클러스터 + Kubeflow 적용 과정 (M2)

> ETRI 관리망 테스트배드에서 **NPU 서비스 클러스터**를 확보하고 **Kubeflow(Notebook/Profile Controller)** 를 올려
> AI 노트북 생성 기반을 마련한 과정. [[테스트배드_프론트엔드_적용과정]](M1)에 이어짐.
>
> - 작성일: 2026-09-28
> - 결과: **M2 코어 완료** — 기존 정상 클러스터(`k8s-furiosa-rngd`)에 Kubeflow 설치 + Profile `okc` 네임스페이스 생성
> - 서비스 클러스터: `k8s-furiosa-rngd` (Magnum/CAPI, k8s v1.36.2, Furiosa RNGD NPU 노드 포함)

---

## 1. 개요

### 목표
포털 백엔드(cmp-backend)가 노트북을 생성할 **서비스 클러스터**를 준비하고, 그 위에 **Kubeflow**를 설치한다.
(단일 클러스터 방침 — 포털/백엔드는 프로세스로, 노트북 실체는 이 서비스 클러스터에서 실행)

### 결론 요약
- 직접 만든 `k8s-mnt-nb`는 **calico 이미지 egress 리셋 → 노드 NotReady → CAPI가 control-plane 재생성 반복 → 사망**.
- 원인은 우리 앱이 아니라 **서비스 클러스터 노드의 이미지 pull 불안정**(일시적 리셋).
- **이미 정상 기동된 `k8s-furiosa-rngd`** 를 서비스 클러스터로 재사용 → Kubeflow 설치 성공.
- NPU 하드웨어는 worker에 **물리적으로 존재**(PCI 벤더 `1ed2` = FuriosaAI)하나, **디바이스 플러그인 미설치**로 아직 k8s 리소스로 노출 안 됨 → 별도 트랙.

---

## 2. 서비스 클러스터 조사 (Horizon)

- **컨테이너 인프라 → 클러스터 / 클러스터 템플릿**에서 확인.
- 템플릿 6종 (전부 kubernetes + calico). 명명 규칙:
  - `rngd` = **FuriosaAI RNGD NPU** 지원 (이 플랫폼이 겨냥하는 국산 AI반도체)
  - `nogpu` = NVIDIA GPU 없음 (rngd와 모순 아님 — NVIDIA GPU 대신 Furiosa NPU)
  - `capi-driver-*` = Magnum이 **CAPI 드라이버**로 클러스터를 프로비저닝
- 사용 템플릿: `ct-k8s-1.36.2-rngd-nodash-nogpu` (id `9cc35044-...`), 키페어 `k8s-key`.

> 키페어(`k8s-key`) 개인키가 없어도 무방 — 클러스터 조작은 전부 kubeconfig(API)로 하며, 노드 SSH는 불필요.

---

## 3. kubeconfig 확보 & 도달성

- Horizon 클러스터 상세에서 **kubeconfig 다운로드** → controller로 반입 (`~/mnt_notebook/kubeconfig-<cluster>/config`), `chmod 600`.
- kubeconfig의 API 주소: `https://192.168.90.x:6443` (LB/provider 망).
- **controller가 이 망에 직접 물려 있음**: `ping` 소스가 `192.168.90.154` 로 확인됨 → controller = LB망 `.154`.
  → **M3에서 백엔드가 이 서비스 클러스터 API로 붙는 것도 문제없음**(도달 가능).
- 접속 확인:
  ```bash
  export KUBECONFIG=~/mnt_notebook/kubeconfig-k8s-furiosa-rngd/config
  kubectl get nodes -o wide      # control-plane + worker 모두 Ready
  ```

> controller에 kubectl 없으면 root 없이 설치:
> ```bash
> curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
> chmod +x kubectl && mv kubectl ~/.local/bin/
> ```

---

## 4. 클러스터 상태 점검

```bash
kubectl get nodes -o wide          # control-plane + worker Ready
kubectl get storageclass           # rbd1 (default) — cinder.csi.openstack.org  → 노트북 PVC OK
kubectl get pods -A                 # calico/coredns/metrics/cinder-csi 전부 Running
```

### NPU 확인
```bash
# allocatable에 NPU 리소스 있는지 → (현재) 없음
kubectl get nodes -o json | python3 -c 'import sys,json;[print(n["metadata"]["name"],{k:v for k,v in n["status"]["allocatable"].items() if any(t in k.lower() for t in("furiosa","rngd","npu"))}) for n in json.load(sys.stdin)["items"]]'

# NFD가 찍은 PCI 라벨 → Furiosa 벤더(1ed2) 존재 확인
kubectl get node <worker> -o jsonpath='{.metadata.labels}' | tr ',' '\n' | grep -iE 'pci-1ed2|furiosa'
# → "feature.node.kubernetes.io/pci-1ed2.present":"true"  ← NPU 하드웨어 있음
```
**해석**: NPU 하드웨어는 있으나(1ed2), device plugin이 없어 리소스 미노출. → 아래 6장(NPU 트랙).

---

## 5. Kubeflow 최소 설치 (Notebook + Profile Controller)

`KUBECONFIG`가 서비스 클러스터를 가리키는 상태에서 (controller는 인터넷 되어 kustomize fetch 가능):

```bash
# Profile controller 의존 CRD (Istio)
kubectl apply -f https://raw.githubusercontent.com/istio/istio/release-1.24/manifests/charts/base/files/crd-all.gen.yaml
# Notebook Controller v1.10
kubectl apply -k "github.com/kubeflow/kubeflow/components/notebook-controller/config/overlays/standalone?ref=v1.10.0"
# Profile Controller v1.10
kubectl create ns kubeflow
kubectl apply -k "github.com/kubeflow/kubeflow/components/profile-controller/config/overlays/kubeflow?ref=v1.10.0"
```
확인:
```bash
kubectl get pods -n kubeflow                    # notebook-controller, profile-controller Running
kubectl get crd | grep -E 'notebook|profile'    # notebooks.kubeflow.org, profiles.kubeflow.org
```

> 파이프라인/Katib/KServe/cert-manager는 **의도적으로 생략**(노트북 코어 우선, 이미지 pull 리스크 축소). 필요 시 나중에 추가.

### 워크스페이스(Profile) 생성
Profile 하나 = 네임스페이스 + RBAC(+쿼터) 자동 생성:
```bash
kubectl apply -f - <<'EOF'
apiVersion: kubeflow.org/v1
kind: Profile
metadata:
  name: okc
spec:
  owner:
    kind: User
    name: okc@openkcloud.local
EOF

kubectl get ns okc                                  # Active
kubectl -n okc get serviceaccount                   # default-editor, default-viewer 자동 생성
```
→ 이 `okc` 네임스페이스가 **cmp-backend가 노트북을 생성할 기본 워크스페이스**(config.env의 `DEFAULT_NAMESPACE=okc`).

---

## 6. NPU 활성화 (별도 트랙 — 미완)

worker에 Furiosa NPU(PCI 1ed2)가 있으나 k8s 리소스로 쓰려면 3층이 필요:
1. **게스트 드라이버** — VM 내부 furiosa 커널 드라이버 → `/dev/...` 장치 노출
2. **Furiosa device plugin** — DaemonSet, `furiosa.ai/rngd` 리소스 광고 (**벤더 제공물** — ETRI의 rngd 템플릿 구축 자료 확인 필요)
3. **런타임/컨테이너 툴킷** — 파드의 NPU 접근

드라이버 존재 확인(privileged 프로브):
```bash
kubectl run npu-probe --rm -it --restart=Never --image=busybox \
  --overrides='{"apiVersion":"v1","spec":{"nodeName":"<worker>","containers":[{"name":"p","image":"busybox","stdin":true,"tty":true,"command":["sh","-c","ls /dev | grep -iE \"furiosa|rngd\" || echo none; ls /sys/module | grep -iE \"furiosa|rngd\" || echo none"],"securityContext":{"privileged":true},"volumeMounts":[{"name":"d","mountPath":"/dev"}]}],"volumes":[{"name":"d","hostPath":{"path":"/dev"}}]}}'
```

> ⚠️ **cmp-backend는 NVIDIA 전제**(`GPU_RUNTIME_CLASS=nvidia`, `nvidia.com/gpu`, CUDA 이미지). Furiosa NPU 노트북은 **리소스명·런타임·이미지 적응이 필요한 백엔드 코드 작업**. → 전략: **CPU 노트북으로 M2/M3 파이프라인 먼저 검증**, NPU는 이후 트랙.

---

## 7. 트러블슈팅 기록

| 증상 | 원인 | 해결/판단 |
|---|---|---|
| `k8s-mnt-nb` 생성 중 calico-node `Init:ErrImagePull` | `docker.io/calico/...` blob을 CloudFront(`production.cloudfront.docker.com`)에서 받다 `connection reset by peer` | **일시적 CDN 리셋** — 재시도로 뚫림(정상 클러스터의 calico-node는 RESTARTS 3회 후 Running) |
| `k8s-mnt-nb` 사망 (API `no route to host`) | 노드 NotReady 지속 → CAPI MachineHealthCheck가 control-plane 재생성 루프 → API VIP 소멸 | 붙잡지 말고 **기존 정상 클러스터 재사용**으로 전환 |
| `kubectl get profile okc` → "Invalid name" | Profile get 경로 webhook 이름검증 쿼크 | **무해** — ns+SA 정상 생성이 동작 증거. `kubectl get profiles`(목록)로 확인 |
| NPU allocatable 비어있음 | device plugin 미설치 (하드웨어는 PCI 1ed2로 존재) | 6장 NPU 트랙 |

### 핵심 교훈
- 서비스 클러스터 노드의 **이미지 egress가 불안정**(간헐 리셋)하면 CAPI 부트스트랩이 실패 루프에 빠질 수 있음.
  안정적 운영에는 **레지스트리 미러**(Magnum 템플릿 라벨) 또는 egress(방화벽/MTU) 정비가 필요 — ETRI 인프라 사안.
- 당장은 **이미 고비를 넘긴 기존 클러스터를 재사용**하는 것이 최단 경로였음(단, 공유 자원이라 소유자가 재생성하면 우리 설치물도 소멸).

---

## 8. 다음 단계 (M3)

- controller에서 `cmp-backend` `config.env` 작성(`DEFAULT_NAMESPACE=okc`, `KUBECONFIG`=서비스 클러스터) → `uvicorn :8000` 실행.
- 백엔드를 M1과 동일 패턴(Service/Endpoints/VirtualService)으로 `notebook-api.kcloud.cluster.local:25443` 노출.
- 프론트 `NEXT_PUBLIC_KF_API`를 백엔드 주소로 재빌드 → real 모드 → 포털에서 **CPU 노트북 생성/IDE 연결** 검증.
- 이후: NPU 활성화(6장) + 백엔드 NPU 적응.
