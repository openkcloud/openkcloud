# syntax=docker/dockerfile:1

# Override this only after checking the RTX PRO 6000 driver/CUDA compatibility.
ARG VLLM_BASE_IMAGE=vllm/vllm-openai:v0.12.0
FROM ${VLLM_BASE_IMAGE}

LABEL org.opencontainers.image.title="kcloud Llama 3.1 8B Serving"
LABEL org.opencontainers.image.description="OpenAI-compatible Llama 3.1 8B server for the RTX PRO 6000 smoke test"
LABEL org.opencontainers.image.version="0.1.0"

# Model weights are not copied into the image. Mount the Hugging Face cache and
# provide HF_TOKEN at runtime so credentials never become an image layer.
ENV HF_HOME=/var/lib/kcloud/huggingface \
    HUGGINGFACE_HUB_CACHE=/var/lib/kcloud/huggingface/hub \
    VLLM_CACHE_ROOT=/var/lib/kcloud/vllm

RUN mkdir -p /var/lib/kcloud/huggingface /var/lib/kcloud/vllm

EXPOSE 8000

ENTRYPOINT ["vllm", "serve"]
CMD ["meta-llama/Llama-3.1-8B-Instruct", "--dtype", "auto", "--max-model-len", "4096", "--host", "0.0.0.0", "--port", "8000"]
