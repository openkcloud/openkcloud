## kcloud-mlperf — 쿠버네티스 LLM 벤치마크 (Llama 3.1 8B)

**마스터/워커 IP만 설정하면 바로 실행되는** bare-metal K8s 벤치마크 모음입니다.

| 벤치마크 | 설명 | 구현 |
|---|---|---|
| **MLPerf Inference** | CNN/DailyMail 요약 → ROUGE | **MLCommons 공식 LoadGen** |
| **MMLU-Pro** | 5-shot CoT 평가 → 정확도 | TIGER-Lab 공식 |
| **LLM Inference** | vLLM 처리량 테스트 | vLLM 백엔드 |

> **권장:** 항상 `--smoke`(10샘플) 먼저 통과 → 풀 데이터 실행

---

## ✅ 처음 사용자 가이드 (Bare Metal 신규 서버 설정)

### 0) 준비물 및 시스템 요구사항

**하드웨어:**
- Ubuntu 20.04/22.04 머신 2대 이상 (마스터 1 + GPU 워커 1+)
- 워커 노드: NVIDIA GPU (최소 16GB VRAM 권장)
- 마스터 노드: 최소 2GB RAM, 2 CPU 코어
- 네트워크: 마스터와 워커 간 SSH 접근 가능

**소프트웨어:**
- 워커 노드에 NVIDIA 드라이버 설치 (버전 525 이상 권장)
- HuggingFace 토큰 (Llama 3.1 라이선스 승인 필요)
  - 토큰 발급: https://huggingface.co/settings/tokens
- sudo 권한이 있는 사용자 계정

**NVIDIA 드라이버 설치 (워커 노드):**
```bash
# Ubuntu에서 NVIDIA 드라이버 설치
sudo apt update
sudo apt install -y nvidia-driver-550  # 또는 최신 버전
sudo reboot

# 설치 확인
nvidia-smi
```

### 1) 레포 받기 및 설정 파일 작성

**모든 노드에서 실행:**
```bash
git clone --recursive https://github.com/openkcloud/kcloud-mlperf.git
cd kcloud-mlperf
```

**마스터 노드에서 설정 파일 작성:**
```bash
cp config/cluster.env config/cluster.env.local
nano config/cluster.env.local
```

`config/cluster.env.local` 예시:
```bash
# Master Node Configuration
MASTER_IP="192.168.1.100"          # 마스터 노드 IP (실제 IP로 변경)
MASTER_USER="ubuntu"               # 마스터 노드 사용자명 (실제 사용자명으로 변경)

# Worker Node Configuration
WORKER_IP="192.168.1.101"          # 워커 노드 IP (실제 IP로 변경)
WORKER_USER="ubuntu"               # 워커 노드 사용자명 (실제 사용자명으로 변경)
WORKER_SSH_PORT="22"               # SSH 포트 (기본값: 22)

# HuggingFace Token (필수)
HF_TOKEN="hf_xxxxxxxxxxxxxxxxxxxx"  # HuggingFace 토큰 (실제 토큰으로 변경)

# Kubernetes Configuration (선택사항)
K8S_VERSION="1.28"                 # Kubernetes 버전
POD_NETWORK_CIDR="10.244.0.0/16"  # Pod 네트워크 CIDR
```

**워커 노드에서 설정 파일 작성 (최소한 MASTER_IP 필요):**
```bash
# 워커 노드에서도 레포를 받은 후
cp config/cluster.env config/cluster.env.local
nano config/cluster.env.local
```

워커 노드 최소 설정:
```bash
MASTER_IP="192.168.1.100"          # 마스터 노드 IP (실제 IP로 변경, 필수)
MASTER_USER="ubuntu"               # 마스터 노드 사용자명 (실제 사용자명으로 변경, 필수)
HF_TOKEN="hf_xxxxxxxxxxxxxxxxxxxx"  # HuggingFace 토큰 (실제 토큰으로 변경)
```

### 2) 클러스터 설치

#### 2-1) 마스터 노드 설정

**마스터 노드에서 실행:**
```bash
cd ~/kcloud-mlperf
./scripts/setup_master.sh
```

이 스크립트는 자동으로:
- 시스템 준비 (swap 비활성화, 커널 모듈 로드)
- containerd 설치 및 설정
- Kubernetes (kubeadm, kubelet, kubectl) 설치
- 클러스터 초기화 (`kubeadm init`)
- CNI 플러그인 (Flannel) 설치
- NVIDIA RuntimeClass 생성
- NVIDIA Device Plugin 설치
- 워커 조인 명령어 생성 (`config/join-command.sh`)

**설치 완료 후 확인:**
```bash
kubectl get nodes
kubectl get pods -n kube-system
```

#### 2-2) 워커 노드 설정

**워커 노드에서 실행:**
```bash
cd ~/kcloud-mlperf
git pull  # 최신 코드 받기

# 자동으로 모든 작업 수행 (GPU 해제, Calico 정리, 클러스터 조인)
./scripts/setup_worker.sh
```

이 스크립트는 자동으로:
- 시스템 준비 (swap 비활성화, 커널 모듈 로드)
- containerd 설치 및 설정
- NVIDIA Container Toolkit 설치 및 설정
- Kubernetes (kubeadm, kubelet, kubectl) 설치
- SSH 키 자동 생성 및 마스터에 복사 (비밀번호 1회 입력)
- 마스터에서 조인 명령어 자동 가져오기
- 클러스터 자동 조인 (`kubeadm join`)
- 불완전한 조인 상태 자동 정리


**워커 조인 확인 (마스터 노드에서):**
```bash
kubectl get nodes
# 워커 노드가 Ready 상태가 될 때까지 대기 (보통 1-2분)
```

#### 2-3) 워커 노드 라벨링 (GPU 스케줄링용)

**마스터 노드에서 실행:**
```bash
# 워커 노드 이름 확인
kubectl get nodes

# GPU 워커 노드에 라벨 추가
kubectl label node <워커노드이름> nvidia.com/gpu.present=true

# 라벨 확인
kubectl get nodes --show-labels
```

### 3) 클러스터 검증 및 벤치마크 실행

**준비 상태 점검:**
```bash
# 마스터 노드에서 실행
./scripts/preflight.sh
```

이 스크립트는 다음을 확인합니다:
- 클러스터 연결 상태
- GPU 할당 가능 여부 (NVIDIA Device Plugin)
- 노드 라벨 설정
- HuggingFace 토큰 설정

**스모크 테스트 (10샘플, ~15분):**
```bash
./scripts/run_benchmarks.sh --smoke
```

**전체 벤치마크 실행 (8~10시간):**
```bash
./scripts/run_benchmarks.sh
```

**특정 벤치마크만 실행:**
```bash
./scripts/run_benchmarks.sh --smoke --mlperf    # MLPerf Inference만
./scripts/run_benchmarks.sh --smoke --mmlu       # MMLU-Pro만
./scripts/run_benchmarks.sh --smoke --inference  # LLM Inference만
```

---

## 📁 주요 파일 구조

```
kcloud-mlperf/
├── config/
│   ├── cluster.env              # 설정 템플릿
│   └── cluster.env.local        # 실제 설정 (gitignored)
├── scripts/
│   ├── setup_master.sh          # 마스터 노드 자동 설정
│   ├── setup_worker.sh          # 워커 노드 자동 설정 (--auto-join 지원)
│   ├── preflight.sh             # 클러스터 상태 점검/자동수정
│   └── run_benchmarks.sh        # 벤치마크 실행
├── k8s/
│   └── jobs/
│       ├── mlperf-job.yaml      # MLPerf Inference Job
│       ├── mmlu-job.yaml        # MMLU-Pro Job
│       └── inference-job.yaml   # LLM Inference Job
└── results/                     # 벤치마크 결과 저장
```

## 🚀 자동화 기능

### setup_master.sh
- ✅ 불완전한 kubeadm 상태 자동 감지 및 정리
- ✅ Flannel CNI 자동 설치
- ✅ NVIDIA Device Plugin 자동 설치
- ✅ 워커 조인 명령어 자동 생성

### setup_worker.sh
- ✅ 불완전한 조인 상태 자동 감지 및 정리
- ✅ SSH 키 자동 생성 및 마스터에 복사 (비밀번호 1회 입력)
- ✅ 마스터에서 조인 명령어 자동 가져오기
- ✅ GPU 사용 프로세스 자동 감지 및 해제
- ✅ Calico CNI 충돌 자동 정리
- ✅ containerd 자동 재시작 (필요 시)
- ✅ 완전 자동화 - 플래그 없이 모든 작업 자동 수행

---

## 자주 쓰는 명령어

```bash
# 특정 벤치마크만 실행
./scripts/run_benchmarks.sh --smoke --mlperf
./scripts/run_benchmarks.sh --smoke --mmlu
./scripts/run_benchmarks.sh --smoke --inference

# 자동수정(마스터 IP 변경, 라벨 누락 등)
./scripts/preflight.sh --fix
./scripts/run_benchmarks.sh --smoke --fix
```

---

## 결과 위치

```
results/<RUN_ID>/
├── summary.txt
├── mlperf-bench.log
├── mlperf-bench-metrics.txt
├── mmlu-bench.log
└── inference-bench.log
```

---

## 🔧 트러블슈팅

### 1) 클러스터 연결 안 됨

**증상:** `kubectl get nodes` 실행 시 연결 오류

**해결:**
```bash
# 자동 수정 시도
./scripts/preflight.sh --fix

# 수동 확인
sudo systemctl status kubelet
sudo systemctl status containerd
sudo crictl ps | grep kube-apiserver

# kubeconfig 확인
ls -la ~/.kube/config
```

### 2) 워커 노드가 조인되지 않음

**증상:** `kubectl get nodes`에서 워커 노드가 보이지 않음

**해결:**
```bash
# 워커 노드에서
./scripts/setup_worker.sh --auto-join

# 또는 수동으로 정리 후 재조인
sudo kubeadm reset --force
sudo rm -rf /etc/kubernetes/pki
sudo rm -rf /etc/cni/net.d/*
sudo systemctl restart containerd
./scripts/setup_worker.sh --auto-join
```

### 3) GPU Pending 또는 Insufficient nvidia.com/gpu

**증상:** Pod가 Pending 상태이고 `Insufficient nvidia.com/gpu` 오류

**해결:**
```bash
# GPU 할당 가능 여부 확인
kubectl get nodes -o jsonpath='{.items[*].status.allocatable.nvidia\.com/gpu}'

# NVIDIA Device Plugin 상태 확인
kubectl get pods -n kube-system -l name=nvidia-device-plugin-ds
kubectl logs -n kube-system -l name=nvidia-device-plugin-ds --tail=50

# Device Plugin 재시작
kubectl delete pod -n kube-system -l name=nvidia-device-plugin-ds

# 노드 라벨 확인
kubectl get nodes --show-labels | grep nvidia.com/gpu.present
```

### 4) SSH 연결 실패 (워커 → 마스터)

**증상:** 워커 노드에서 마스터로 SSH 연결 실패

**해결:**
```bash
# 워커 노드에서
# 1. SSH 키 확인
ls -la ~/.ssh/id_ed25519_kcloud*

# 2. 수동으로 SSH 키 복사
ssh-copy-id -i ~/.ssh/id_ed25519_kcloud.pub <MASTER_USER>@<MASTER_IP>

# 3. 또는 setup_worker.sh가 자동으로 처리 (비밀번호 1회 입력)
./scripts/setup_worker.sh --auto-join
```

### 4b) GPU 메모리 부족 또는 워커 노드 문제

**증상:** GPU가 사용 중이거나 워커 노드가 NotReady 상태

**해결:**
```bash
# 워커 노드에서 - setup_worker.sh가 자동으로 처리합니다
./scripts/setup_worker.sh

# setup_worker.sh는 다음을 자동으로 처리합니다:
# - GPU 사용 프로세스 자동 감지 및 종료
# - Calico CNI 충돌 자동 정리
# - kubelet 재시작 (필요 시)
# - 클러스터 자동 조인
```

### 5) containerd 오류: "container runtime is not running"

**증상:** `kubeadm join` 실행 시 containerd 연결 오류

**해결:**
```bash
# containerd 상태 확인
sudo systemctl status containerd

# containerd 재시작
sudo systemctl restart containerd
sudo systemctl enable containerd

# 확인
sudo crictl ps
```

### 6) 불완전한 kubeadm 상태 오류

**증상:** `kubeadm init` 또는 `kubeadm join` 실행 시 "file already exists" 오류

**해결:**
```bash
# 마스터 노드에서
sudo kubeadm reset --force
sudo rm -rf /etc/kubernetes/pki
sudo rm -rf /etc/kubernetes/manifests
sudo rm -f /etc/kubernetes/*.conf
sudo rm -rf /etc/cni/net.d/*
sudo systemctl restart containerd
./scripts/setup_master.sh

# 워커 노드에서
sudo kubeadm reset --force
sudo rm -rf /etc/kubernetes/pki
sudo rm -rf /etc/cni/net.d/*
sudo systemctl restart containerd
./scripts/setup_worker.sh --auto-join
```

### 7) Calico CNI 충돌

**증상:** Flannel과 Calico가 동시에 설치되어 충돌

**해결:**
```bash
# 마스터 노드에서
sudo rm -f /etc/cni/net.d/10-calico.conflist
sudo rm -f /etc/cni/net.d/calico-kubeconfig
sudo systemctl restart kubelet
# setup_master.sh가 자동으로 처리합니다
```

---

## 참고
- `mlcommons_inference/` : MLCommons 공식 구현(서브모듈)
- `mmlu_pro/` : TIGER-Lab 공식 구현(서브모듈)


---

# kcloud-tool (installer & tooling) — v0.1.5


Pilot benchmark installer and tooling for the kcloud MLPerf evaluation suite.
Targets Kubernetes clusters with GPU and NPU accelerators (NVIDIA, FuriosaAI RNGD, Rebellions Atom+).

## Pilot Kubernetes Install

Deploys MLPerf CNN/DM + MMLU-Pro into a fresh namespace (`kcloud-mlperf`) with a single command.
Only required input: the node IP list.

```bash
./scripts/install_pilot_k8s.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"
```

Device mode, storage class, HF token, and registry are all auto-detected.
Dry-run and validate-only modes are available before any cluster mutation:

```bash
# Read-only preflight — verify cluster is ready
./scripts/install_pilot_k8s.sh --node-ips "..." --validate-only

# Render + dry-run — print plan, no apply
./scripts/install_pilot_k8s.sh --node-ips "..." --dry-run
```

See **[docs/pilot_k8s_installation.md](docs/pilot_k8s_installation.md)** for the full reference:
prerequisites, auto-detection behavior, HF token handling, GPU/NPU/CPU fallback, all flags, cleanup, and troubleshooting.

## Full-Stack Install

Deploys the **entire ETRI LLM evaluation platform** — storage, device operators, observability,
web application (frontend + backend), and benchmark layer — from a single command.
Only required input: the cluster node IP list.

```bash
./scripts/install_kcloud_stack.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"
```

Stages run in order: `storage → operators → observability → webapp → benchmarks → verify`.
Everything is auto-detected (device mode, NFS server, access IP, HF token); supply override
flags only for non-default topology.

Safe-by-default modes (no cluster mutation):

```bash
# Read-only preflight — verify cluster is ready for full-stack install
./scripts/install_kcloud_stack.sh --node-ips "..." --validate-only

# Render + dry-run — print plan, no apply
./scripts/install_kcloud_stack.sh --node-ips "..." --dry-run
```

Access URLs after install:

| Service | URL |
|---|---|
| Frontend | `http://<access-ip>:30001` |
| Backend API | `http://<access-ip>:30980/api` |

For kind-based confidence-loop testing (no real hardware required):

```bash
test/run_confidence_loop.sh 3    # 3 iterations: kind_up → install → kind_down
```

See **[docs/full_stack_installation.md](docs/full_stack_installation.md)** for the complete reference:
stage breakdown, auto-detect vs. override table, bare-node provisioning, storage/NFS selection,
HF token and imagePullSecret handling, GPU/NPU/CPU fallback, verification, kind testing workflow,
cleanup, and troubleshooting.

## Repository Layout

```
scripts/                  Installer entrypoint + lib (install_pilot_k8s.sh, lib/)
deploy/templates/         Kubernetes manifest templates (envsubst-rendered at install time)
benchmarks/               Benchmark Python scripts and profiles
jobs/                     Reference Job manifests (existing workloads)
infra/                    Infrastructure scripts
docs/                     End-user documentation
```

## Supported Accelerators

| Mode | Resource | Notes |
|---|---|---|
| `gpu` | `nvidia.com/gpu` | NVIDIA L40 / A40 via GPU Operator |
| `npu-rngd` | `furiosa.ai/rngd` | FuriosaAI RNGD; thin-client benchmark mode |
| `npu-atom` | `rebellions.ai/ATOM` | Rebellions Atom+; device plugin currently parked |
| `cpu` | — | Fallback; no accelerator required |

Default selection priority: `gpu > npu-rngd > npu-atom > cpu`.
