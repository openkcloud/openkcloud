# Llama 3.1 8B RNGD Serving Container

실서버의 `furiosa-smi info`에서 확인한 RNGD 한 장으로 `furiosa-ai/Llama-3.1-8B-Instruct`를 실행한다.

이 모델은 Furiosa가 RNGD용으로 미리 컴파일한 FXB를 제공한다. Warboy용 ONNX→INT8→ENF 변환 코드를 사용하지 않는다. 모델과 FXB Cache, `HF_TOKEN`은 Image Layer에 포함하지 않는다.

## 1. RNGD 확인

```bash
furiosa-smi info
furiosa-smi --version
docker version
furiosa-cdi list
```

RNGD Container는 `io_uring`을 사용하므로 단독 Docker 시험에서는 `seccomp=unconfined`와 CDI Device를 지정한다.

## 2. Build

```bash
docker pull furiosaai/furiosa-llm:latest
docker image inspect furiosaai/furiosa-llm:latest \
  --format '{{.Id}} {{json .RepoDigests}}'

docker build \
  -t kcloud/llama31-8b-serving-rngd:0.1.0 \
  containers/llama31-8b-serving-rngd
```

반복 측정 전에는 `latest` 대신 위에서 확인한 Base Image Digest를 Dockerfile Build Argument로 지정한다.

## 3. 단독 기동

```bash
docker run --rm \
  --security-opt seccomp=unconfined \
  --device=furiosa.ai/npu=npu0 \
  -e HF_TOKEN \
  -v /data/huggingface-rngd:/root/.cache/huggingface \
  -p 8000:8000 \
  kcloud/llama31-8b-serving-rngd:0.1.0

curl -sS http://127.0.0.1:8000/v1/models
```

## 4. Kubernetes

Furiosa Device Plugin이 RNGD 한 장을 `furiosa.ai/rngd: 1`로 등록해야 한다. Manifest는 해당 Extended Resource를 requests와 limits에 모두 지정하고 `seccompProfile: Unconfined`를 사용한다.

Manifest: `cost-based-scheduler/deployments/llama31-8b-serving-rngd.yaml`
