# Multi-stage build for kcloud-cost-estimator
FROM python:3.12-slim AS builder

WORKDIR /app
COPY requirements.txt .
RUN pip install --user --no-cache-dir -r requirements.txt

FROM python:3.12-slim
WORKDIR /app
COPY --from=builder /root/.local /root/.local
COPY src/ ./src/

ENV PATH=/root/.local/bin:$PATH
ENV PYTHONPATH=/app/src

EXPOSE 8001
ENTRYPOINT ["uvicorn", "ml_power_predictor.api.server:app", "--host", "0.0.0.0", "--port", "8000"]
