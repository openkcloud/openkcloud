# ============================================================
# Makefile: Furiosa NPU(Warboy·RNGD 공용) 드라이버 컨테이너 빌드/배포 자동화
# 상세: furiosa-driver-ds 이미지를 빌드한다(런타임 DKMS 설치, 태그 vN). build-precompiled 는
#       지정 커널의 드라이버 모듈을 빌드 시 컴파일해 함께 굽는다.
# 생성일: 2026-09-23
# ============================================================

REGISTRY      ?= ghcr.io/openkcloud
REPO          ?= furiosa-driver-ds

# 기본(비-prebuilt) 이미지의 빌드 개정. 이미지가 자기 태그를 알아야 설치 마커를 판정하므로
# BUILD_VARIANT 로 들어가 이미지 라벨·/opt/furiosa/version.txt 에 기록된다.
BUILD_VARIANT ?= v1
IMAGE_TAG     ?= $(BUILD_VARIANT)
IMG           ?= $(REGISTRY)/$(REPO):$(IMAGE_TAG)

.PHONY: all build push build-precompiled help

all: build

## build: 기본 이미지 빌드 (런타임 호스트 apt→DKMS 설치)
build:
	docker build --build-arg BUILD_VARIANT=$(BUILD_VARIANT) -t $(IMG) .

## push: 레지스트리에 push
push:
	docker push $(IMG)

## build-precompiled: 커널 하나의 드라이버 모듈을 빌드 시 컴파일해 함께 굽는다
##   (예: make build-precompiled KERNEL=6.8.0-138-generic VARIANT=rngd PKG_VERSION=2026.3.1)
build-precompiled:
	@test -n "$(KERNEL)" || { echo "KERNEL 인자 필수 (예: 6.8.0-138-generic)"; exit 1; }
	@test -n "$(VARIANT)" || { echo "VARIANT 인자 필수 (rngd — warboy 는 미지원)"; exit 1; }
	@test "$(VARIANT)" != "warboy" || { echo "warboy 사전 컴파일 미지원: 공개 저장소에 없고 벤더 apt 자격 증명이 필요하다(후속 항목)"; exit 1; }
	@test -n "$(PKG_VERSION)" || { echo "PKG_VERSION 인자 필수 (예: 2026.3.1)"; exit 1; }
	docker build --network=host \
	  --build-arg BUILD_VARIANT=$(PKG_VERSION)-$(VARIANT)-k$(KERNEL)-v1 \
	  --build-arg PREBUILD_KERNEL=$(KERNEL) \
	  --build-arg PREBUILD_VARIANT=$(VARIANT) \
	  --build-arg PREBUILD_PKG_VERSION=$(PKG_VERSION) \
	  --label org.kcloud.prebuild-kernel=$(KERNEL) \
	  -t $(REGISTRY)/$(REPO):$(PKG_VERSION)-$(VARIANT)-k$(KERNEL)-v1 .

## help: 타겟 목록
help:
	@grep -E '^## ' Makefile | sed 's/## //'
