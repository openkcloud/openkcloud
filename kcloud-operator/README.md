# kcloud operator

## 📖 Overview  

`kcloud operator`는 Kubernetes 환경에서 **NPU/GPU 가속기 장치의 드라이버 및 디바이스 플러그인 배포를 자동화**하기 위한 Kubernetes Operator입니다.  
<br>
기존에는 각 벤더(Furiosa, NVIDIA 등)의 디바이스 플러그인을 개별적으로 설치해야 했지만, NPU Operator는 단일 CRD(`NPUClusterPolicy`)를 통해 통합 관리가 가능합니다.

✅ 노드 라벨 기반 자동 감지  
✅ 벤더별 DaemonSet 자동 생성/관리  
✅ ConfigMap 자동 배포(Furiosa)  
✅ Helm Chart 기반 설치 지원  

---

## 🚀 Getting Started

### Prerequisites
- Go 1.24+
- Docker 17.03+
- Kubectl 1.28+
- Kubernetes v1.28+ cluster
- Operator SDK v1.41.1+
- Helm 3.x
- (노드 환경 준비)  
  - NVIDIA: NVIDIA Driver, NVIDIA Container Toolkit  
  - Furiosa: Furiosa Driver, Toolkit  

---

## 🚀 Build & Deploy

### 이미지 빌드 및 푸시
```bash
make docker-build docker-push IMG=<registry>/npu-operator:<tag>
```

### CRDs 설치
```bash
make install
```

### Operator 배포
```bash
make deploy IMG=<registry>/npu-operator:<tag>
```

### Custom Resource 생성
```yaml
apiVersion: npu.ai/v1alpha1
kind: NPUClusterPolicy
metadata:
  name: my-npu-cluster-policy
spec:
  nvidia:
    enabled: true
    devicePluginImage: "nvcr.io/nvidia/k8s-device-plugin:v0.17.1"
  furiosa:
    enabled: true
    devicePluginImage: "ghcr.io/furiosa-ai/k8s-device-plugin:0.10.1"
    configMapName: "npu-device-plugin"
```

```bash
kubectl apply -f my-npu-cluster-policy.yaml
```

---

## 🗑 Uninstall
```bash
kubectl delete -f my-npu-cluster-policy.yaml
make undeploy
make uninstall
```

---

## 📦 Project Distribution

### Option 1: Install via bundled YAML
```bash
make build-installer IMG=<registry>/npu-operator:<tag>
kubectl apply -f dist/install.yaml
```

### Option 2: Install via Helm Chart
```bash
helm install npu-operator ./helm/npu-operator -n npu-operator-system --create-namespace
```

---

## 🛠 Development Notes
- CRD: `NPUClusterPolicy (npu.ai/v1alpha1)`
- Controller: `NPUClusterPolicyReconciler`
- 관리 대상:  
  - DaemonSet (Furiosa, NVIDIA Device Plugin)  
  - ConfigMap (Furiosa 설정)  
- RBAC: DaemonSet, ConfigMap 생성 권한 필요  

---

## 🤝 Contributing
1. Fork the repo  
2. Create feature branch  
3. Submit PR with 테스트 결과  

---

## 📚 References
- [Kubebuilder Book](https://book.kubebuilder.io)  
- [Operator SDK](https://sdk.operatorframework.io)  
- [NVIDIA k8s-device-plugin](https://github.com/NVIDIA/k8s-device-plugin)  
- [FuriosaAI Device Plugin](https://github.com/furiosa-ai/furiosa-device-plugin)  

---

## 📄 License
Apache License 2.0