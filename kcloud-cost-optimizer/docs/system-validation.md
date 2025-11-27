# Policy Engine System Validation Guide

이 문서는 Policy Engine 시스템의 전체 검증 및 테스트 방법을 설명합니다.

## 개요

Policy Engine은 다음과 같은 포괄적인 검증 과정을 거칩니다:

1. **단위 테스트** - 개별 컴포넌트 테스트
2. **통합 테스트** - 컴포넌트 간 상호작용 테스트
3. **CLI 통합 테스트** - 명령줄 인터페이스 테스트
4. **시스템 검증** - 전체 시스템 동작 확인

## 테스트 구조

```
tests/
├── integration_test.go          # API 및 서비스 통합 테스트
├── cli_integration_test.go      # CLI 통합 테스트
└── system-validation.sh         # 전체 시스템 검증 스크립트
```

## 테스트 실행 방법

### 1. 단위 테스트

```bash
# 모든 단위 테스트 실행
make test

# 특정 패키지 테스트
go test ./internal/validator/...

# 커버리지 포함 테스트
make test-coverage
```

### 2. 통합 테스트

```bash
# 통합 테스트 실행
make test-integration

# 또는 직접 실행
go test -v -timeout 30m ./tests/integration_test.go
```

### 3. CLI 통합 테스트

```bash
# CLI 통합 테스트 실행
make test-cli-integration

# 또는 직접 실행
go test -v -timeout 30m ./tests/cli_integration_test.go
```

### 4. 전체 시스템 검증

```bash
# 포괄적인 시스템 검증 실행
make validate-system

# 또는 직접 스크립트 실행
./scripts/system-validation.sh
```

## 통합 테스트 상세

### API 엔드포인트 테스트

- **헬스체크**: `/health` 엔드포인트 응답 확인
- **메트릭**: `/metrics` 엔드포인트에서 Prometheus 메트릭 확인
- **정책 API**: CRUD 작업 테스트
- **워크로드 API**: CRUD 작업 테스트
- **평가 API**: 정책 평가 기능 테스트
- **자동화 API**: 자동화 규칙 관리 테스트

### 데이터 영속성 테스트

- 정책 생성 후 조회 가능성 확인
- 워크로드 생성 후 조회 가능성 확인
- 자동화 규칙 생성 후 조회 가능성 확인

### 오류 처리 테스트

- 잘못된 JSON 데이터 처리
- 존재하지 않는 리소스 조회
- 잘못된 평가 요청 처리

### 성능 테스트

- 동시 요청 처리 능력
- 응답 시간 측정
- 메모리 사용량 확인

## CLI 통합 테스트 상세

### CLI 명령어 테스트

- **기본 명령어**: `--help`, `status`, `ping`, `metrics`
- **정책 관리**: `policy create/list/get/update/delete`
- **워크로드 관리**: `workload create/list/get/update/delete`
- **평가 기능**: `evaluate workload/policy/batch`
- **자동화 관리**: `automation create/list/get/update/delete/enable/disable`

### CLI 설정 테스트

- 환경 변수 설정
- 설정 파일 사용
- 서버 연결 테스트

### CLI 오류 처리 테스트

- 잘못된 서버 연결
- 존재하지 않는 파일
- 잘못된 명령어 인수

## 시스템 검증 스크립트

`system-validation.sh` 스크립트는 다음을 수행합니다:

### 1. 사전 조건 확인

- Go 설치 및 버전 확인
- 필요한 도구 설치 확인 (make, docker, kubectl, jq)

### 2. 애플리케이션 빌드

- 메인 애플리케이션 빌드
- CLI 바이너리 빌드
- 빌드 성공 확인

### 3. 단위 테스트 실행

- 모든 Go 단위 테스트 실행
- 테스트 결과 확인

### 4. Makefile 타겟 테스트

- `make help` - 도움말 표시
- `make version` - 버전 정보 표시
- `make clean` - 정리 작업
- `make build` - 빌드 작업
- `make build-cli` - CLI 빌드

### 5. 예제 파일 검증

- 예제 정책 파일 존재 확인
- 예제 워크로드 파일 존재 확인

### 6. Docker 빌드 테스트

- Docker 이미지 빌드
- Docker 컨테이너 실행 테스트

### 7. API 엔드포인트 테스트

- 헬스체크 엔드포인트
- 메트릭 엔드포인트
- 정책 API 엔드포인트
- 워크로드 API 엔드포인트
- 자동화 API 엔드포인트

### 8. CRUD 작업 테스트

- 정책 생성/조회/업데이트/삭제
- 워크로드 생성/조회/업데이트/삭제
- 평가 작업 테스트

### 9. CLI 작업 테스트

- CLI 도움말 확인
- CLI 상태 확인
- CLI 핑 테스트
- CLI 메트릭 조회

### 10. 통합 테스트 실행

- Go 통합 테스트 실행
- CLI 통합 테스트 실행

## 테스트 결과 해석

### 성공적인 테스트 결과

```
==========================================
         SYSTEM VALIDATION REPORT        
==========================================

Total Tests: 25
Passed: 25
Failed: 0

✅ All tests passed! System validation successful.
```

### 실패한 테스트 결과

```
==========================================
         SYSTEM VALIDATION REPORT        
==========================================

Total Tests: 25
Passed: 20
Failed: 5

❌ 5 tests failed. System validation unsuccessful.
```

## 문제 해결

### 일반적인 문제

1. **빌드 실패**
   ```bash
   # 의존성 확인
   go mod tidy
   go mod download
   
   # 깨끗한 빌드
   make clean
   make build
   ```

2. **테스트 실패**
   ```bash
   # 상세 로그로 테스트 실행
   go test -v ./tests/...
   
   # 특정 테스트만 실행
   go test -v -run TestIntegrationBasicFlow ./tests/
   ```

3. **CLI 테스트 실패**
   ```bash
   # CLI 바이너리 확인
   ls -la build/policy-cli
   
   # CLI 도움말 확인
   ./build/policy-cli --help
   ```

4. **서버 연결 실패**
   ```bash
   # 서버 상태 확인
   curl http://localhost:8080/health
   
   # 포트 사용 확인
   netstat -tlnp | grep 8080
   ```

### 로그 분석

테스트 실패 시 다음 로그를 확인하세요:

- **애플리케이션 로그**: 서버 시작 및 실행 로그
- **테스트 로그**: Go 테스트 실행 로그
- **CLI 로그**: CLI 명령어 실행 로그

## 지속적 통합 (CI)

### GitHub Actions 예제

```yaml
name: System Validation

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v2
    
    - name: Set up Go
      uses: actions/setup-go@v2
      with:
        go-version: 1.21
    
    - name: Run system validation
      run: |
        make validate-system
```

### 로컬 개발 워크플로우

1. **코드 변경 후**
   ```bash
   make test
   ```

2. **주요 기능 완성 후**
   ```bash
   make test-integration
   ```

3. **릴리스 전**
   ```bash
   make validate-system
   ```

## 성능 기준

### 응답 시간 기준

- **헬스체크**: < 100ms
- **정책 조회**: < 200ms
- **워크로드 평가**: < 500ms
- **CLI 명령어**: < 1s

### 동시성 기준

- **동시 요청**: 최소 10개 요청 동시 처리
- **메모리 사용량**: < 512MB
- **CPU 사용량**: < 80%

## 보안 테스트

### 입력 검증

- 잘못된 JSON 데이터 처리
- SQL 인젝션 시도
- XSS 공격 시도

### 인증 및 인가

- API 토큰 검증
- 역할 기반 접근 제어
- 세션 관리

## 모니터링 및 메트릭

### 테스트 중 메트릭 수집

- HTTP 요청/응답 시간
- 메모리 사용량
- CPU 사용량
- 에러율

### 알림 설정

- 테스트 실패 시 알림
- 성능 기준 미달 시 알림
- 리소스 사용량 임계값 초과 시 알림

## 결론

이 시스템 검증 가이드를 통해 Policy Engine의 안정성과 신뢰성을 보장할 수 있습니다. 정기적인 테스트 실행과 지속적인 모니터링을 통해 시스템의 품질을 유지하세요.


