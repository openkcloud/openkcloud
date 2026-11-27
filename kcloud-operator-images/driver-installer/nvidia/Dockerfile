# ============================================================
# Dockerfile: NVIDIA GPU DaemonSet용 long-running 드라이버 컨테이너
# 상세: 커널 모듈 설치 후 상시 실행하며 드라이버 상태 감시
#       v17 image label 검증을 위한 ARG/LABEL/version.txt prebake 포함
# 생성일: 2026-04-13 | 수정일: 2026-10-11
# ============================================================
FROM ubuntu:22.04

# ----------------------------------------------------------- #
# image label 검증용 — patch 까지 명시 (R5 차단)
#   사용: docker build --build-arg NVIDIA_DRIVER_VERSION=590.48.01 ...
# ----------------------------------------------------------- #
ARG NVIDIA_DRIVER_MAJOR="590"
ARG NVIDIA_DRIVER_VERSION="590.48.01"

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      wget \
      gnupg \
      software-properties-common \
      iproute2 \
      util-linux \
      procps \
      kmod \
      pciutils \
      jq \
      ubuntu-drivers-common \
      build-essential \
      dkms \
    && add-apt-repository -y ppa:graphics-drivers/ppa \
    && apt-get update \
    && rm -rf /var/lib/apt/lists/*

# initContainer용 커널 헤더 확인 스크립트
COPY check-kernel-headers.sh /usr/local/bin/check-kernel-headers.sh
RUN chmod +x /usr/local/bin/check-kernel-headers.sh

# initContainer용 driver-manager 스크립트 (커널 모듈 해제)
COPY driver-manager.sh /usr/local/bin/driver-manager.sh
RUN chmod +x /usr/local/bin/driver-manager.sh

# 메인 드라이버 entrypoint (DaemonSet long-running 모드)
COPY marker-migrate.sh /usr/local/bin/marker-migrate.sh
COPY nvidia-blacklist.sh /usr/local/bin/nvidia-blacklist.sh
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# 헬스체크 스크립트 (livenessProbe 대상)
COPY healthcheck.sh /usr/local/bin/healthcheck.sh
RUN chmod +x /usr/local/bin/healthcheck.sh

# ----------------------------------------------------------- #
# image label 검증용 — verify-image.sh 가 ai.npu.driver.full 비교
# ----------------------------------------------------------- #
RUN mkdir -p /opt/nvidia && echo "${NVIDIA_DRIVER_VERSION}" > /opt/nvidia/version.txt
LABEL org.opencontainers.image.version="${NVIDIA_DRIVER_VERSION}"
LABEL ai.npu.driver.major="${NVIDIA_DRIVER_MAJOR}"
LABEL ai.npu.driver.full="${NVIDIA_DRIVER_VERSION}"
LABEL ai.npu.driver.vendor="nvidia"

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

