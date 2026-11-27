# syntax=docker/dockerfile:1

# Furiosa's current Kubernetes guide publishes the official image as latest.
# Record the pulled digest on the RNGD server and replace this value with that
# immutable digest before repeat measurements.
ARG FURIOSA_LLM_BASE_IMAGE=furiosaai/furiosa-llm:latest
FROM ${FURIOSA_LLM_BASE_IMAGE}

LABEL org.opencontainers.image.title="kcloud Llama 3.1 8B RNGD Serving"
LABEL org.opencontainers.image.description="OpenAI-compatible Llama 3.1 8B server for one Furiosa RNGD"
LABEL org.opencontainers.image.version="0.1.0"

# The pre-compiled FXB and gated model files remain in the runtime cache/PVC.
# HF_TOKEN is injected at runtime and must never be written to an image layer.
EXPOSE 8000

# The official base image supplies the furiosa-llm entrypoint.
CMD ["serve", "furiosa-ai/Llama-3.1-8B-Instruct", "--host", "0.0.0.0", "--port", "8000"]
