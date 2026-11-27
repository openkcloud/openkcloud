# syntax=docker/dockerfile:1
# MNT-AINB — FastAPI(AI 노트북·Kubeflow 중개). 빌드: docker build -t harbor.io:7443/mnt/mnt-ainb:TAG .
# 노트북 IDE 연결·파일 복사에 kubectl(port-forward, exec)을 쓴다 — 서비스 클러스터 버전에 맞춘다.
FROM python:3.12-slim

ARG KUBECTL_VERSION=v1.36.2
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates \
 && curl -fsSL -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
 && chmod +x /usr/local/bin/kubectl \
 && apt-get purge -y curl && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    CMP_SECRET_FILE=/tmp/.cmp-secret

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app ./app
COPY pipelines ./pipelines

RUN useradd -u 10001 -r -s /usr/sbin/nologin appuser && chown -R appuser /app
USER appuser

EXPOSE 8000
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
