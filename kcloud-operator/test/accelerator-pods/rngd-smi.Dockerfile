# ============================================================
# Dockerfile: rngd-smi — RNGD 를 할당받은 파드 안에서 furiosa-smi 를 실행하는 확인용 이미지
# 상세: 벤더 apt(익명)에서 furiosa-smi 만 설치한다. 드라이버는 담지 않는다(호스트 커널 모듈 사용).
# 생성일: 2026-09-11
# ============================================================
FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends curl gnupg ca-certificates \
    && curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | gpg --dearmor > /etc/apt/trusted.gpg.d/furiosa-cloud.google.gpg \
    && echo 'deb [arch=amd64] http://asia-northeast3-apt.pkg.dev/projects/furiosa-ai jammy main' > /etc/apt/sources.list.d/furiosa.list \
    && apt-get update && apt-get install -y --no-install-recommends furiosa-smi \
    && rm -rf /var/lib/apt/lists/*
CMD ["sleep", "infinity"]
