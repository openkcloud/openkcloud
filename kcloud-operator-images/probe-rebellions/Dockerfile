# ============================================================
# Dockerfile: probe-rebellions — ACPP 기능 probe(rbln-stat) 종료코드 판정용 one-shot 이미지
# 상세: 이미지에는 rbln-stat 바이너리가 없다 — nsenter 로 호스트 네임스페이스에 진입해 호스트의
#       실 바이너리를 1회 실행하고 종료코드로 판정한다(acpp_verifier.go). 특권 파드라 기본은 꺼져 있다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends util-linux \
    && rm -rf /var/lib/apt/lists/*
COPY rbln-stat /usr/local/bin/rbln-stat
RUN chmod +x /usr/local/bin/rbln-stat
ENTRYPOINT ["/usr/local/bin/rbln-stat"]
