# syntax=docker/dockerfile:1
# MNT-CORE — FastAPI. 빌드: docker build -t harbor.io:7443/mnt/mnt-core:TAG .
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app ./app

# 비루트 실행
RUN useradd -u 10001 -r -s /usr/sbin/nologin appuser && chown -R appuser /app
USER appuser

EXPOSE 8000
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
