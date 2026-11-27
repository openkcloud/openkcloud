# ============================================================
# Makefile: NVIDIA driver 컨테이너(v17) 빌드/배포 자동화
# 상세: Dockerfile.v17 로 nvidia-driver-ds 이미지를 빌드한다. 버전별 태그 vN.
#       build-precompiled 는 커널 하나의 Ubuntu linux-modules-nvidia 를 서명 .ko 로
#       링크해 함께 굽는다(태그 <driverVersion>-k<kernel>-<IMAGE_VARIANT>, 기본 v1).
#       자체 미러는 REGISTRY=<자체 미러>/kcloud make ... 로 덮어쓴다.
# 생성일: 2026-09-23 | 수정일: 2026-10-13
# ============================================================

REGISTRY ?= ghcr.io/openkcloud
REPO     ?= nvidia-driver-ds

# nvidia driver 메이저/패치. 이미지 라벨과 verify-image.sh 검증에 쓰인다.
NVIDIA_DRIVER_MAJOR   ?= 580
NVIDIA_DRIVER_VERSION ?= 580.126.09
# 헤더 prebake 대상 커널(선택, 컨테이너 안에서 컴파일하는 경로용). build-precompiled 는 쓰지 않는다.
KERNEL_VERSION        ?=
# 드라이버 패키지 prebake. 기본 false — 기본 이미지에 NVIDIA 바이너리를 넣지 않는다.
PREBAKE_DRIVER        ?= false
# 컴파일 도구(build-essential·dkms)와 PPA. 기본 false — 활성 경로는 호스트 도구를 쓴다.
BUILD_TOOLS           ?= false
IMAGE_TAG             ?= v1
# build-precompiled 태그 접미사. 내용 변경 시 v2 로 바꿔 노드 캐시 재사용 방지.
IMAGE_VARIANT         ?= v1
IMG                   ?= $(REGISTRY)/$(REPO):$(IMAGE_TAG)

.PHONY: all build push build-precompiled test help

all: build

## build: 기본(호스트 설치 경로) 이미지 빌드 (Dockerfile.v17)
build:
	docker build -f Dockerfile.v17 \
	  --build-arg NVIDIA_DRIVER_MAJOR=$(NVIDIA_DRIVER_MAJOR) \
	  --build-arg NVIDIA_DRIVER_VERSION=$(NVIDIA_DRIVER_VERSION) \
	  --build-arg KERNEL_VERSION=$(KERNEL_VERSION) \
	  --build-arg PREBAKE_DRIVER=$(PREBAKE_DRIVER) \
	  --build-arg BUILD_TOOLS=$(BUILD_TOOLS) \
	  -t $(IMG) .

## push: 레지스트리에 push
push:
	docker push $(IMG)

## build-precompiled: 커널 하나의 Ubuntu linux-modules-nvidia 를 서명 .ko 로 링크해 함께 굽는다
##   (예: make build-precompiled KERNEL=5.15.0-171-generic NVIDIA_DRIVER_VERSION=580.126.09)
##   IMAGE_VARIANT (기본 v1): 태그 접미사. 내용 변경 시 v2 로 바꿔 노드 캐시 재사용 방지
build-precompiled:
	@test -n "$(KERNEL)" || { echo "KERNEL 인자 필수 (예: make build-precompiled KERNEL=5.15.0-171-generic)"; exit 1; }
	docker build -f Dockerfile.v17 --network=host \
	  --build-arg NVIDIA_DRIVER_MAJOR=$(NVIDIA_DRIVER_MAJOR) \
	  --build-arg NVIDIA_DRIVER_VERSION=$(NVIDIA_DRIVER_VERSION) \
	  --build-arg PREBUILD_KERNEL=$(KERNEL) \
	  --build-arg PREBAKE_DRIVER=$(PREBAKE_DRIVER) \
	  --build-arg BUILD_TOOLS=$(BUILD_TOOLS) \
	  --label org.kcloud.prebuild-kernel=$(KERNEL) \
	  -t $(REGISTRY)/$(REPO):$(NVIDIA_DRIVER_VERSION)-k$(KERNEL)-$(IMAGE_VARIANT) .

## test: 이 디렉터리의 *_test.sh 를 모두 돌린다 (이미지 부재로 SKIP 하는 시험은 통과로 본다)
test:
	@fail=0; \
	for t in *_test.sh; do \
	  echo "=== $$t ==="; \
	  if out="$$(bash "$$t" 2>&1)"; then \
	    printf '%s\n' "$$out"; \
	  elif printf '%s\n' "$$out" | grep -q '^SKIP:' && ! printf '%s\n' "$$out" | grep -q '^FAIL'; then \
	    printf '%s\n' "$$out"; echo "($$t: 이미지 부재 SKIP — 통과 처리)"; \
	  else \
	    printf '%s\n' "$$out"; echo "FAILED: $$t"; fail=1; \
	  fi; \
	done; \
	if [ "$$fail" -ne 0 ]; then echo "SOME TESTS FAILED"; exit 1; fi; \
	echo "ALL TESTS OK"

## help: 타겟 목록
help:
	@grep -E '^## ' Makefile | sed 's/## //'
