# syntax=docker/dockerfile:1
# MNT-IAAS — FastAPI(OpenStack 중개). 빌드: docker build -t harbor.io:7443/mnt/mnt-iaas:TAG .
# OpenStack TLS: 초기엔 OS_INSECURE=true 로 기동. 추후 사내 root CA(kcloud-root-ca)를
# ConfigMap 으로 마운트하고 REQUESTS_CA_BUNDLE 지정 + OS_INSECURE=false 로 전환.
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app ./app

RUN useradd -u 10001 -r -s /usr/sbin/nologin appuser && chown -R appuser /app
USER appuser

EXPOSE 8000
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
