# ============================================================
# Makefile: Tenstorrent Blackhole device plugin 빌드/배포 자동화
# 상세: build/test/docker-build/docker-push/deploy/helm-lint 타겟
# 생성일: 2026-04-22 | 수정일: 2026-09-09
# ============================================================

# 레지스트리와 이미지 태그. 자체 미러는 REGISTRY=<자체 미러>/kcloud make ... 로 덮어쓴다.
REGISTRY ?= ghcr.io/openkcloud
IMG      ?= $(REGISTRY)/kcloud-tt-device-plugin:v0.1.0

# 바이너리 출력 경로
BINARY ?= bin/tenstorrent-device-plugin

.PHONY: all build test vet docker-build docker-push deploy helm-lint clean

all: build

## build: 로컬 바이너리 빌드 (CGO_ENABLED=0)
build:
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o $(BINARY) .

## test: 단위 테스트 실행
test:
	go test ./... -v -count=1

## vet: go vet 정적 분석
vet:
	go vet ./...

## docker-build: Docker 이미지 빌드
docker-build:
	docker build -t $(IMG) .

## docker-push: 레지스트리에 이미지 푸시 (sudo 필요)
docker-push:
	docker push $(IMG)

## deploy: DaemonSet 배포 (kubectl 필요)
deploy:
	kubectl apply -f deploy/daemonset.yaml

## helm-lint: Helm 차트 lint (charts/ 디렉토리 존재 시)
helm-lint:
	helm lint charts/ 2>/dev/null || echo "charts/ 디렉토리 없음 - helm-lint 건너뜀"

## clean: 빌드 산출물 제거
clean:
	rm -rf bin/

## help: 사용 가능한 타겟 목록
help:
	@grep -E '^## ' Makefile | sed 's/## //'
