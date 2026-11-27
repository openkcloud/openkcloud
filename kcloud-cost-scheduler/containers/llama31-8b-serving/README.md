# Llama 3.1 8B Serving Container

발표자료에서 확인한 `meta-llama/Llama-3.1-8B-Instruct`를 NVIDIA RTX PRO 6000에서 기동하기 위한 시험용 Container 정의다. 7B 모델은 사용하지 않는다.

모델 Weight와 `HF_TOKEN`은 Image Layer에 포함하지 않는다. 실행 시 Hugging Face Cache Volume과 환경변수로 전달한다.

## 1. 서버 확인

```bash
nvidia-smi --query-gpu=name,uuid,memory.total,power.limit,driver_version --format=csv
docker version
docker run --rm --gpus all nvidia/cuda:12.8.1-base-ubuntu24.04 nvidia-smi
```

## 2. Build

기본 Base Image는 `vllm/vllm-openai:v0.12.0`이다. 실제 Driver와 호환되는 Tag를 확인한 뒤 필요하면 Build Argument로 바꾼다.

```bash
docker build \
  -t kcloud/llama31-8b-serving:0.1.0 \
  containers/llama31-8b-serving

docker image inspect kcloud/llama31-8b-serving:0.1.0 \
  --format '{{.Id}} {{json .RepoDigests}}'
```

## 3. 단독 기동

```bash
docker run --rm --gpus all \
  -e HF_TOKEN \
  -v /data/huggingface:/var/lib/kcloud/huggingface \
  -v /data/vllm-cache:/var/lib/kcloud/vllm \
  -p 8000:8000 --ipc=host \
  kcloud/llama31-8b-serving:0.1.0

curl -sS http://127.0.0.1:8000/v1/models
```

`HF_TOKEN` 값은 파일, Dockerfile, Image, 연구노트에 기록하지 않는다. Meta 모델 접근 승인이 끝나지 않았으면 401/403 결과를 별도로 기록한다.

## 4. Kubernetes

단일 Node 시험은 대상 Node에 Image를 Build한 뒤 `imagePullPolicy: IfNotPresent`로 실행한다. 여러 Node에서는 내부 Registry에 Push하고 YAML의 Image를 해당 Registry 주소로 교체한다.

Manifest: `cost-based-scheduler/deployments/llama31-8b-serving-pro6000.yaml`
