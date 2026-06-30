FROM python:3.11-slim

WORKDIR /app

COPY catalog-service/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# proto 컴파일 (build context = cost-scope/)
COPY proto/benchmark.proto /proto/benchmark.proto
RUN python -m grpc_tools.protoc \
      -I /proto \
      --python_out=/app \
      --grpc_python_out=/app \
      /proto/benchmark.proto

COPY catalog-service/server.py .

CMD ["python", "server.py"]
