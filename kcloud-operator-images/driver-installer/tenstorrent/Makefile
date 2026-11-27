# ============================================================
# Makefile: Tenstorrent tt-kmd 드라이버 컨테이너 빌드/배포 자동화
# 상세: baked-source(air-gap) + job모드 이미지. REGISTRY 가 가리키는 레지스트리로 push.
#       버전별 태그 <kmd>-vN (images.yaml 규약, 예: 2.11.0-v1).
#       build-precompiled 는 커널 하나의 prebuilt .ko 를 함께 굽는다(태그 <kmd>-k<kernel>-vN).
#       자체 미러는 REGISTRY=<자체 미러>/kcloud make ... 로 덮어쓴다.
# 생성일: 2026-04-22 | 수정일: 2026-10-11
# ============================================================

REGISTRY  ?= ghcr.io/openkcloud
REPO      ?= tenstorrent-driver-ds

# 빌드할 tt-kmd 버전 (override: make build TT_KMD_VERSION=2.11.0 TT_KMD_GIT_TAG=ttkmd-2.11.0)
TT_KMD_VERSION ?= 2.11.0
TT_KMD_GIT_TAG ?= ttkmd-$(TT_KMD_VERSION)
IMAGE_TAG      ?= $(TT_KMD_VERSION)-v1
IMG            ?= $(REGISTRY)/$(REPO):$(IMAGE_TAG)

.PHONY: all build push build-all build-precompiled help

all: build

## build: 지정 버전 이미지 빌드
build:
	docker build \
	  --build-arg TT_KMD_VERSION=$(TT_KMD_VERSION) \
	  --build-arg TT_KMD_GIT_TAG=$(TT_KMD_GIT_TAG) \
	  -t $(IMG) .

## push: 레지스트리에 push
push:
	docker push $(IMG)

## build-all: 2.8.0(롤백 대상) + 2.11.0(기본) 두 버전 빌드
build-all:
	$(MAKE) build push TT_KMD_VERSION=2.8.0
	$(MAKE) build push TT_KMD_VERSION=2.11.0

## build-precompiled: 커널 하나의 prebuilt .ko 를 함께 굽는다 (예: make build-precompiled KERNEL=5.15.0-190-generic)
build-precompiled:
	docker build --network=host \
	  --build-arg TT_KMD_VERSION=$(TT_KMD_VERSION) \
	  --build-arg TT_KMD_GIT_TAG=$(TT_KMD_GIT_TAG) \
	  --build-arg PREBUILD_KERNEL=$(KERNEL) \
	  --label org.kcloud.prebuild-kernel=$(KERNEL) \
	  -t $(REGISTRY)/$(REPO):$(TT_KMD_VERSION)-k$(KERNEL)-v1 .

## help: 타겟 목록
help:
	@grep -E '^## ' Makefile | sed 's/## //'
