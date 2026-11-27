# ============================================================
# Dockerfile: rbln-workload — <rebellions-node> 호스트 rebel SDK 를 hostPath 로 쓰는 Ubuntu 22.04 실행 이미지
# 상세: 벤더 SDK(rebel, pyarmor 보호)는 이미지에 안 담고 호스트(Ubuntu 22.04, glibc 2.35, python
#       3.10)의 dist-packages 를 hostPath 로 쓴다. 다른 glibc(2.41)에서는 create_runtime 이
#       SIGSEGV 로 죽는다. 시스템 라이브러리 목록은 대상 노드의 `/etc/cdi/rbln.yaml`(벤더 CDI
#       스펙)이 요구하는 것과 맞춘다.
# 생성일: 2026-09-29 | 수정일: 2026-10-11
# ============================================================
FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 python3-distutils libpython3.10 \
      libgomp1 libatomic1 libstdc++6 libgcc-s1 libnuma1 libssl3 libffi8 libpci3 \
      zlib1g libbz2-1.0 liblzma5 libsqlite3-0 ca-certificates \
      libibverbs1 librdmacm1 ibverbs-providers libdrm2 libnl-3-200 libnl-route-3-200 \
    && rm -rf /var/lib/apt/lists/*
# 벤더 라이브러리(librbln-thunk.so 등)는 이미지에 담지 않는다 — 파드가 <rebellions-node> 의 실제 파일을
# hostPath 로 넣는다. 여기서는 SONAME 링크만 미리 둬 dlopen("librbln-thunk.so.3") 이 그 hostPath
# 파일을 찾게 한다(CDI 스펙의 라이브러리 파일명·마운트 경로 기준).
RUN ln -s librbln-thunk.so.3.0.0 /usr/lib/librbln-thunk.so.3 && ln -s librbln-thunk.so.3 /usr/lib/librbln-thunk.so \
 && ln -s librbln-ccl.so.3.0.0 /usr/lib/librbln-ccl.so.3 && ln -s librbln-ccl.so.3 /usr/lib/librbln-ccl.so
# workload.py 는 ConfigMap 으로 /work 에 들어온다. SDK 는 hostPath(/host-local, /host-lib).
ENV PYTHONPATH=/host-local:/host-lib
ENTRYPOINT ["python3"]
CMD ["-c", "import sys; print(sys.version)"]
