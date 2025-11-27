# Policy Engine Kubernetes Deployment

이 디렉토리는 Policy Engine을 Kubernetes 클러스터에 배포하기 위한 모든 매니페스트 파일들을 포함합니다.

## 파일 구조

- `namespace.yaml` - Policy Engine 전용 네임스페이스
- `configmap.yaml` - 애플리케이션 설정
- `secret.yaml` - 민감한 정보 (DB 패스워드, API 토큰 등)
- `rbac.yaml` - 역할 기반 액세스 제어
- `deployment.yaml` - 메인 애플리케이션 배포
- `service.yaml` - 서비스 및 헤드리스 서비스
- `ingress.yaml` - 외부 접근을 위한 인그레스
- `hpa.yaml` - 수평 파드 자동 스케일링
- `pdb.yaml` - 파드 중단 예산
- `monitoring.yaml` - Prometheus 모니터링 설정
- `kustomization.yaml` - Kustomize 설정

## 배포 방법

### 1. 자동 배포 (권장)

```bash
# 전체 배포
./scripts/deploy-k8s.sh

# 또는 특정 명령어
./scripts/deploy-k8s.sh deploy
```

### 2. 수동 배포

```bash
# 네임스페이스 생성
kubectl apply -f k8s/namespace.yaml

# 설정 및 시크릿 배포
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/secret.yaml

# RBAC 배포
kubectl apply -f k8s/rbac.yaml

# 애플리케이션 배포
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml
kubectl apply -f k8s/ingress.yaml
kubectl apply -f k8s/hpa.yaml
kubectl apply -f k8s/pdb.yaml

# 모니터링 배포
kubectl apply -f k8s/monitoring.yaml
```

### 3. Kustomize를 사용한 배포

```bash
# Kustomize 빌드 후 배포
kustomize build k8s/ | kubectl apply -f -

# 또는 kubectl의 -k 옵션 사용
kubectl apply -k k8s/
```

## 설정

### 환경 변수

- `IMAGE_TAG`: Docker 이미지 태그 (기본값: latest)
- `DOMAIN`: 인그레스 도메인 (기본값: policy-engine.local)

### 시크릿 수정

`secret.yaml` 파일에서 다음 값들을 실제 환경에 맞게 수정하세요:

```yaml
stringData:
  db_password: "your-actual-db-password"
  redis_password: "your-actual-redis-password"
  api_token: "your-actual-api-token"
  webhook_url: "https://your-actual-webhook-url.com"
  webhook_token: "your-actual-webhook-token"
```

### 인그레스 도메인 설정

`ingress.yaml` 파일에서 `policy-engine.yourdomain.com`을 실제 도메인으로 변경하세요.

## 모니터링

### 메트릭 엔드포인트

- HTTP 메트릭: `http://service:8080/metrics`
- Prometheus 메트릭: `http://service:9090/metrics`

### 알림 규칙

다음 상황에서 알림이 발생합니다:

- Policy Engine 서비스 다운 (1분 이상)
- 메모리 사용량 85% 초과
- CPU 사용량 80% 초과
- HTTP 에러율 5% 초과

## 스케일링

### 수평 스케일링

HPA(Horizontal Pod Autoscaler)가 자동으로 파드 수를 조정합니다:

- 최소 파드 수: 3
- 최대 파드 수: 10
- CPU 임계값: 70%
- 메모리 임계값: 80%

### 수직 스케일링

`deployment.yaml`에서 리소스 요청 및 제한을 조정할 수 있습니다:

```yaml
resources:
  requests:
    memory: "256Mi"
    cpu: "250m"
  limits:
    memory: "512Mi"
    cpu: "500m"
```

## 보안

### 보안 컨텍스트

모든 파드는 다음 보안 설정으로 실행됩니다:

- 비루트 사용자로 실행
- 권한 에스컬레이션 비활성화
- 읽기 전용 루트 파일시스템
- 모든 Linux 기능 제거

### RBAC

애플리케이션은 최소 권한 원칙에 따라 필요한 리소스에만 접근할 수 있습니다.

## 트러블슈팅

### 일반적인 문제

1. **파드가 시작되지 않음**
   ```bash
   kubectl describe pod -n policy-engine
   kubectl logs -n policy-engine deployment/policy-engine
   ```

2. **서비스에 연결할 수 없음**
   ```bash
   kubectl get svc -n policy-engine
   kubectl get endpoints -n policy-engine
   ```

3. **인그레스가 작동하지 않음**
   ```bash
   kubectl describe ingress -n policy-engine
   kubectl get ingress -n policy-engine
   ```

### 로그 확인

```bash
# 모든 파드 로그
kubectl logs -n policy-engine -l app=policy-engine

# 특정 파드 로그
kubectl logs -n policy-engine deployment/policy-engine

# 실시간 로그 스트리밍
kubectl logs -n policy-engine deployment/policy-engine -f
```

### 상태 확인

```bash
# 배포 상태
kubectl get deployment -n policy-engine

# 파드 상태
kubectl get pods -n policy-engine

# 서비스 상태
kubectl get svc -n policy-engine

# 전체 상태 확인 스크립트
./scripts/deploy-k8s.sh status
```

## 정리

배포를 정리하려면:

```bash
# 자동 정리
./scripts/deploy-k8s.sh cleanup

# 또는 수동 정리
kubectl delete -k k8s/
```

## 추가 리소스

- [Kubernetes 공식 문서](https://kubernetes.io/docs/)
- [Prometheus 모니터링](https://prometheus.io/docs/)
- [Grafana 대시보드](https://grafana.com/docs/)


