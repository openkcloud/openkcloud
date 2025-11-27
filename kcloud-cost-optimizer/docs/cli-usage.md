# Policy Engine CLI Usage Guide

Policy Engine CLI는 Policy Engine 서비스와 상호작용하기 위한 명령줄 인터페이스입니다.

## 설치 및 빌드

### 바이너리 빌드

```bash
# CLI 바이너리 빌드
make build-cli

# 또는 직접 빌드
go build -o bin/policy-cli cmd/cli/main.go
```

### Docker 이미지에서 사용

```bash
# Docker 컨테이너에서 CLI 실행
docker run --rm -it kcloud-policy-engine:latest policy-cli --help
```

## 기본 사용법

### 전역 옵션

```bash
policy-cli [global-options] <command> [command-options] [arguments]
```

**전역 옵션:**
- `--config`: 설정 파일 경로 (기본값: $HOME/.policy-cli.yaml)
- `--verbose, -v`: 상세 출력 활성화
- `--config-path`: 서버 설정 파일 경로
- `--log-level`: 로그 레벨 (debug, info, warn, error)
- `--server-host`: 서버 호스트 (기본값: localhost)
- `--server-port`: 서버 포트 (기본값: 8080)

### 도움말

```bash
# 전체 도움말
policy-cli --help

# 특정 명령어 도움말
policy-cli policy --help
policy-cli policy create --help
```

## 정책 관리

### 정책 생성

```bash
# YAML 파일에서 정책 생성
policy-cli policy create examples/policies/cost-optimization-policy.yaml

# 상세 출력과 함께
policy-cli --verbose policy create examples/policies/cost-optimization-policy.yaml
```

### 정책 목록 조회

```bash
# 모든 정책 목록
policy-cli policy list

# JSON 형태로 출력
policy-cli policy list | jq .
```

### 정책 조회

```bash
# 특정 정책 조회
policy-cli policy get policy-123
```

### 정책 업데이트

```bash
# 정책 업데이트
policy-cli policy update policy-123 updated-policy.yaml
```

### 정책 삭제

```bash
# 정책 삭제
policy-cli policy delete policy-123
```

## 워크로드 관리

### 워크로드 생성

```bash
# 워크로드 생성
policy-cli workload create examples/workloads/sample-workload.yaml
```

### 워크로드 목록 조회

```bash
# 모든 워크로드 목록
policy-cli workload list
```

### 워크로드 조회

```bash
# 특정 워크로드 조회
policy-cli workload get workload-123
```

### 워크로드 업데이트

```bash
# 워크로드 업데이트
policy-cli workload update workload-123 updated-workload.yaml
```

### 워크로드 삭제

```bash
# 워크로드 삭제
policy-cli workload delete workload-123
```

## 정책 평가

### 워크로드 평가

```bash
# 특정 워크로드에 대한 모든 정책 평가
policy-cli evaluate workload workload-123
```

### 특정 정책으로 워크로드 평가

```bash
# 특정 정책으로 워크로드 평가
policy-cli evaluate policy policy-123 workload-456
```

### 배치 평가

```bash
# 여러 워크로드 배치 평가
policy-cli evaluate batch batch-workloads.yaml
```

### 평가 기록 조회

```bash
# 평가 기록 조회
policy-cli evaluate history

# 평가 통계 조회
policy-cli evaluate stats
```

## 자동화 규칙 관리

### 자동화 규칙 생성

```bash
# 자동화 규칙 생성
policy-cli automation create examples/policies/automation-rule.yaml
```

### 자동화 규칙 목록 조회

```bash
# 모든 자동화 규칙 목록
policy-cli automation list
```

### 자동화 규칙 조회

```bash
# 특정 자동화 규칙 조회
policy-cli automation get rule-123
```

### 자동화 규칙 업데이트

```bash
# 자동화 규칙 업데이트
policy-cli automation update rule-123 updated-rule.yaml
```

### 자동화 규칙 삭제

```bash
# 자동화 규칙 삭제
policy-cli automation delete rule-123
```

### 자동화 규칙 활성화/비활성화

```bash
# 자동화 규칙 활성화
policy-cli automation enable rule-123

# 자동화 규칙 비활성화
policy-cli automation disable rule-123
```

### 자동화 엔진 상태 조회

```bash
# 자동화 엔진 상태 조회
policy-cli automation status
```

## 시스템 상태 확인

### 서비스 상태 확인

```bash
# Policy Engine 서비스 상태 확인
policy-cli status

# 연결 테스트
policy-cli ping

# 상세 정보 조회
policy-cli info
```

### 메트릭 조회

```bash
# Prometheus 메트릭 조회
policy-cli metrics

# 특정 메트릭만 필터링
policy-cli metrics | grep "policy_evaluations_total"
```

## 설정 관리

### 설정 파일 생성

```bash
# 기본 설정 파일 생성
cat > ~/.policy-cli.yaml << EOF
server:
  host: localhost
  port: 8080
logging:
  level: info
EOF
```

### 환경 변수 사용

```bash
# 환경 변수로 서버 설정
export POLICY_SERVER_HOST=policy-engine.example.com
export POLICY_SERVER_PORT=8080
policy-cli policy list
```

## 스크립트 자동화

### 정책 배포 스크립트

```bash
#!/bin/bash
# deploy-policies.sh

set -e

SERVER_HOST=${SERVER_HOST:-localhost}
SERVER_PORT=${SERVER_PORT:-8080}

echo "Deploying policies to $SERVER_HOST:$SERVER_PORT"

# 정책 배포
for policy in examples/policies/*.yaml; do
    echo "Deploying $policy..."
    policy-cli --server-host=$SERVER_HOST --server-port=$SERVER_PORT policy create "$policy"
done

echo "All policies deployed successfully"
```

### 워크로드 평가 스크립트

```bash
#!/bin/bash
# evaluate-workloads.sh

set -e

SERVER_HOST=${SERVER_HOST:-localhost}
SERVER_PORT=${SERVER_PORT:-8080}

echo "Evaluating workloads against policies..."

# 모든 워크로드 평가
workloads=$(policy-cli --server-host=$SERVER_HOST --server-port=$SERVER_PORT workload list | jq -r '.workloads[].id')

for workload in $workloads; do
    echo "Evaluating workload: $workload"
    policy-cli --server-host=$SERVER_HOST --server-port=$SERVER_PORT evaluate workload "$workload"
done

echo "All workloads evaluated successfully"
```

## 오류 처리

### 일반적인 오류

1. **연결 오류**
   ```bash
   # 서버가 실행 중인지 확인
   policy-cli ping
   
   # 서버 호스트/포트 확인
   policy-cli --server-host=localhost --server-port=8080 status
   ```

2. **인증 오류**
   ```bash
   # API 토큰 설정 (필요한 경우)
   export POLICY_API_TOKEN="your-token-here"
   ```

3. **파일 오류**
   ```bash
   # 파일 존재 확인
   ls -la examples/policies/cost-optimization-policy.yaml
   
   # YAML 문법 검증
   policy-cli --verbose policy create examples/policies/cost-optimization-policy.yaml
   ```

### 디버깅

```bash
# 상세 로그와 함께 실행
policy-cli --verbose --log-level=debug policy list

# HTTP 요청/응답 확인
policy-cli --verbose policy create policy.yaml 2>&1 | tee debug.log
```

## 성능 최적화

### 배치 작업

```bash
# 여러 정책을 한 번에 생성
for policy in policies/*.yaml; do
    policy-cli policy create "$policy" &
done
wait
```

### 병렬 평가

```bash
# 여러 워크로드를 병렬로 평가
workloads=(workload-1 workload-2 workload-3)
for workload in "${workloads[@]}"; do
    policy-cli evaluate workload "$workload" &
done
wait
```

## 보안 고려사항

### API 토큰 관리

```bash
# 환경 변수로 토큰 설정
export POLICY_API_TOKEN="your-secure-token"

# 설정 파일에 토큰 저장 (권장하지 않음)
echo "api_token: your-secure-token" >> ~/.policy-cli.yaml
```

### 네트워크 보안

```bash
# HTTPS 사용 (프로덕션 환경)
policy-cli --server-host=https://policy-engine.example.com policy list

# 로컬 네트워크에서만 접근
policy-cli --server-host=192.168.1.100 policy list
```

## 예제 워크플로우

### 1. 정책 배포 및 테스트

```bash
# 1. 정책 생성
policy-cli policy create examples/policies/cost-optimization-policy.yaml

# 2. 워크로드 생성
policy-cli workload create examples/workloads/sample-workload.yaml

# 3. 평가 실행
policy-cli evaluate workload sample-workload

# 4. 결과 확인
policy-cli evaluate history
```

### 2. 자동화 규칙 설정

```bash
# 1. 자동화 규칙 생성
policy-cli automation create examples/policies/automation-rule.yaml

# 2. 규칙 활성화
policy-cli automation enable auto-rule-1

# 3. 상태 확인
policy-cli automation status
```

### 3. 모니터링 설정

```bash
# 1. 서비스 상태 확인
policy-cli status

# 2. 메트릭 조회
policy-cli metrics

# 3. 정기적인 헬스체크
while true; do
    policy-cli ping && echo "Service is healthy" || echo "Service is down"
    sleep 60
done
```


