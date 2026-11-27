# ============================================================
# Dockerfile: tt-smi — Tenstorrent 를 할당받은 파드 안에서 tt-smi 를 실행하는 확인용 이미지
# 상세: PyPI 의 tt-smi 만 설치한다. 커널 모듈(tt-kmd)은 호스트 것을 쓰고 /dev/tenstorrent/N 은 device-plugin 이 주입한다.
# 생성일: 2026-09-11
# ============================================================
FROM python:3.10-slim
RUN apt-get update && apt-get install -y --no-install-recommends pciutils libatomic1 libgomp1 && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir tt-smi
CMD ["sleep", "infinity"]
