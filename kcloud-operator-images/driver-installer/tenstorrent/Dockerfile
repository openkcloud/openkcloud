# ============================================================
# Dockerfile: Tenstorrent Blackhole DaemonSet/install-Job 겸용 tt-kmd 드라이버 컨테이너
# 상세: 빌드 시 tt-kmd 소스를 특정 태그로 clone → /opt/tt-kmd-src/<ver> 에 baked
#       (air-gap: 런타임 git clone 제거). entrypoint 가 DKMS 로 빌드/설치.
#       PREBUILD_KERNEL 지정 시 그 커널용 tenstorrent.ko 를 /opt/tt-kmd-ko/<kernel> 에 함께 굽는다.
# 생성일: 2026-04-22 | 수정일: 2026-10-11
# ============================================================
FROM ubuntu:22.04

# tt-kmd upstream 태그(ttkmd-<ver>)와 그에 대응하는 semver 버전.
#   TT_KMD_GIT_TAG : git clone 대상 태그 (예: ttkmd-2.8.0)
#   TT_KMD_VERSION : DKMS PACKAGE_VERSION / 이미지 라벨 (예: 2.8.0)
ARG TT_KMD_GIT_TAG="ttkmd-2.11.0"
ARG TT_KMD_VERSION="2.11.0"

ENV DEBIAN_FRONTEND=noninteractive
# 런타임에서 entrypoint 가 사용할 권위 버전 (baked 소스와 1:1).
ENV TT_KMD_VERSION="${TT_KMD_VERSION}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    gnupg \
    iproute2 \
    util-linux \
    procps \
    kmod \
    pciutils \
    dkms \
    build-essential \
    git \
    && rm -rf /var/lib/apt/lists/*

# tt-kmd 소스를 특정 태그로 baked (air-gap 클러스터에서 런타임 clone 불필요).
# /opt 하위에 둔다 — install Job 은 host /usr/src 를 컨테이너에 bind-mount 하므로
# /usr/src 아래 baked 하면 런타임에 가려진다. entrypoint 가 /opt→host/usr/src 로 복사.
RUN git clone --depth=1 --branch "${TT_KMD_GIT_TAG}" \
      https://github.com/tenstorrent/tt-kmd "/opt/tt-kmd-src/${TT_KMD_VERSION}" \
    && rm -rf "/opt/tt-kmd-src/${TT_KMD_VERSION}/.git"

# 사전 컴파일(prebuilt) 옵션: PREBUILD_KERNEL 이 지정되면 해당 커널용 tenstorrent.ko 를 이미지에
# 함께 굽는다. tt-kmd 는 kbuild 외부 모듈 빌드를 지원하므로(obj-m += tenstorrent.o) 커널 헤더로
# 직접 컴파일하고, 산출물은 /opt/tt-kmd-ko/<kernel>/ 에 .ko·vermagic 로 둔다(태그
# <kmd>-k<kernel>-vN). 비어 있으면 기존 DKMS 전용 이미지와 동일하게 동작한다. 빌드 후
# linux-headers 는 purge 하되 kmod 는 유지한다(healthcheck.sh 의 lsmod 가 쓴다). 라벨
# org.kcloud.prebuild-kernel 은 Makefile build-precompiled 이 --label 로 부여한다.
ARG PREBUILD_KERNEL=""
RUN if [ -n "${PREBUILD_KERNEL}" ]; then \
      apt-get update && apt-get install -y --no-install-recommends \
        "linux-headers-${PREBUILD_KERNEL}" kmod && \
      make -C "/lib/modules/${PREBUILD_KERNEL}/build" \
        M="/opt/tt-kmd-src/${TT_KMD_VERSION}" modules && \
      install -D -m 0644 "/opt/tt-kmd-src/${TT_KMD_VERSION}/tenstorrent.ko" \
        "/opt/tt-kmd-ko/${PREBUILD_KERNEL}/tenstorrent.ko" && \
      modinfo -F vermagic "/opt/tt-kmd-ko/${PREBUILD_KERNEL}/tenstorrent.ko" \
        | tee "/opt/tt-kmd-ko/${PREBUILD_KERNEL}/vermagic" && \
      make -C "/lib/modules/${PREBUILD_KERNEL}/build" \
        M="/opt/tt-kmd-src/${TT_KMD_VERSION}" clean && \
      apt-get purge -y "linux-headers-${PREBUILD_KERNEL}" && apt-get autoremove -y && \
      rm -rf /var/lib/apt/lists/*; \
    fi

COPY marker-migrate.sh /usr/local/bin/marker-migrate.sh
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY healthcheck.sh /usr/local/bin/healthcheck.sh
COPY driver-manager.sh /usr/local/bin/driver-manager.sh
COPY check-kernel-headers.sh /usr/local/bin/check-kernel-headers.sh

RUN chmod +x /usr/local/bin/entrypoint.sh \
             /usr/local/bin/healthcheck.sh \
             /usr/local/bin/driver-manager.sh \
             /usr/local/bin/check-kernel-headers.sh

# image label 검증용 — verify-image.sh 가 ai.npu.driver.full 비교
RUN mkdir -p /opt/tenstorrent && echo "${TT_KMD_VERSION}" > /opt/tenstorrent/version.txt
LABEL org.opencontainers.image.version="${TT_KMD_VERSION}"
LABEL ai.npu.driver.full="${TT_KMD_VERSION}"
LABEL ai.npu.driver.vendor="tenstorrent"

# startupProbe: /tmp/driver-ready 존재 확인
# livenessProbe: /usr/local/bin/healthcheck.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
