FROM python:3.11-slim

WORKDIR /app

# gRPC, protobuf 기본 의존성
RUN pip install --no-cache-dir grpcio protobuf numpy

# proto 파일 재컴파일 (proto → pb2 생성)
RUN pip install --no-cache-dir grpcio-tools
COPY ../proto/benchmark.proto /proto/benchmark.proto
RUN python -m grpc_tools.protoc \
      -I /proto \
      --python_out=/app \
      --grpc_python_out=/app \
      /proto/benchmark.proto

# runner 코드 복사
COPY benchmark-runner/runner.py /app/runner.py

# GPU 노드: torch + pynvml (빌드 시 --build-arg DEVICE=gpu)
# NPU 노드: furiosa-sdk + furiosa-models (워커에서 직접 설치)
# torch/pynvml은 GPU 노드의 호스트 Python 환경에서 사용하거나
# 별도로 pip install 가능 — 이미지 경량화를 위해 기본 포함하지 않음
# GPU 노드에서 필요 시: pip install torch torchvision pynvml

CMD ["python", "runner.py"]
