# ============================================================
# Dockerfile: Furiosa NPU(Warboy·RNGD 공용) 드라이버 설치 컨테이너 — furiosa-driver-ds
# 상세: entrypoint.sh 가 모델에 맞는 드라이버 패키지를 호스트에 설치하고 커널 모듈을 로드한다.
#       드라이버 패키지는 이미지에 담지 않고 런타임에 APT 저장소에서 받는다.
# 생성일: 2026-09-09 | 수정일: 2026-10-11
# ============================================================
FROM ubuntu:22.04

# image label 검증용 — verify-image.sh 와 짝. 이미지는 드라이버 버전을 담지 않으므로
# 이 값은 빌드 개정 번호(vN)다. 드라이버 버전은 DRIVER_VERSION env 로 들어온다.
ARG BUILD_VARIANT="v0"

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl wget gnupg iproute2 \
    util-linux procps kmod pciutils jq dkms && \
    rm -rf /var/lib/apt/lists/*

# 사전 컴파일(prebuilt) 옵션: PREBUILD_KERNEL·PREBUILD_VARIANT 가 모두 지정되면 그 커널용 드라이버
# 모듈을 빌드해 굽는다(벤더 DKMS 소스를 커널 헤더로 직접 컴파일, 산출물은
# /opt/furiosa-ko/<variant>/<kernel>/ 에 .ko·vermagic, 태그 <pkgVersion>-<variant>-k<kernel>-vN).
# Warboy 는 인증이 필요한 사설 저장소라 secret 을 담지 않는 이 경로에서 제외한다(RNGD 만 공개).
# KERNEL_VERSION 이 빌드 호스트와 다르면 넘기고 vermagic 으로 불일치를 막는다. 빌드 후
# linux-headers·build-essential 만 purge 한다 — kmod·dkms 는 비-prebuilt 경로가 쓰므로 유지한다.
ARG PREBUILD_KERNEL=""
ARG PREBUILD_VARIANT=""
ARG PREBUILD_PKG_VERSION=""
RUN if [ -n "${PREBUILD_KERNEL}" ] && [ -n "${PREBUILD_VARIANT}" ]; then \
      set -eu; \
      test -n "${PREBUILD_PKG_VERSION}" || { echo "PREBUILD_PKG_VERSION 필수 (예: 2026.3.1)"; exit 1; }; \
      mkdir -p /etc/apt/trusted.gpg.d /etc/apt/sources.list.d; \
      curl -fsSL "https://packages.cloud.google.com/apt/doc/apt-key.gpg" \
        | gpg --dearmor > /etc/apt/trusted.gpg.d/furiosa-cloud.google.gpg; \
      echo 'deb [arch=amd64] http://asia-northeast3-apt.pkg.dev/projects/furiosa-ai jammy main' \
        > /etc/apt/sources.list.d/furiosa-rngd.list; \
      apt-get update; \
      apt-get install -y --no-install-recommends build-essential "linux-headers-${PREBUILD_KERNEL}"; \
      cd /tmp; \
      apt-get download "furiosa-driver-${PREBUILD_VARIANT}=${PREBUILD_PKG_VERSION}"; \
      dpkg-deb -x furiosa-driver-${PREBUILD_VARIANT}_${PREBUILD_PKG_VERSION}_*.deb /; \
      src="/usr/src/furiosa-driver-${PREBUILD_VARIANT}-${PREBUILD_PKG_VERSION}"; \
      ( cd "${src}/src" && make KERNELDIR="/lib/modules/${PREBUILD_KERNEL}/build" KERNEL_VERSION="${PREBUILD_KERNEL}" vf=0 ); \
      dest="/opt/furiosa-ko/${PREBUILD_VARIANT}/${PREBUILD_KERNEL}"; \
      mkdir -p "${dest}"; \
      cp "${src}/src"/*.ko "${dest}/"; \
      vm="$(modinfo -F vermagic "${dest}"/*.ko | head -1)"; \
      case "${vm}" in "${PREBUILD_KERNEL} "*) : ;; *) echo "[ERR] vermagic '${vm}' 가 대상 커널 ${PREBUILD_KERNEL} 로 시작하지 않는다"; exit 1 ;; esac; \
      echo "${vm}" > "${dest}/vermagic"; \
      echo "[INFO] prebuilt ${PREBUILD_VARIANT} ${PREBUILD_PKG_VERSION} for ${PREBUILD_KERNEL}: ${vm}"; \
      rm -rf "${src}"; \
      apt-get purge -y build-essential "linux-headers-${PREBUILD_KERNEL}"; \
      apt-get autoremove -y; \
      rm -rf /var/lib/apt/lists/* /tmp/*.deb; \
    fi

COPY install-decision.sh     /usr/local/bin/install-decision.sh
COPY check-kernel-headers.sh /usr/local/bin/check-kernel-headers.sh
COPY driver-manager.sh       /usr/local/bin/driver-manager.sh
COPY marker-migrate.sh       /usr/local/bin/marker-migrate.sh
COPY entrypoint.sh           /usr/local/bin/entrypoint.sh
COPY healthcheck.sh          /usr/local/bin/healthcheck.sh

RUN chmod +x /usr/local/bin/check-kernel-headers.sh \
             /usr/local/bin/driver-manager.sh \
             /usr/local/bin/entrypoint.sh \
             /usr/local/bin/healthcheck.sh

RUN mkdir -p /opt/furiosa && echo "${BUILD_VARIANT}" > /opt/furiosa/version.txt
LABEL org.opencontainers.image.version="${BUILD_VARIANT}"
LABEL ai.npu.driver.vendor="furiosa"

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
