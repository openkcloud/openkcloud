FROM pytorch/pytorch:2.5.1-cuda12.1-cudnn9-devel

# Avoid interactive prompts
ENV DEBIAN_FRONTEND=noninteractive

# Install system deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    git git-lfs tini build-essential cmake wget unzip python3-dev && \
    rm -rf /var/lib/apt/lists/* && \
    git lfs install

WORKDIR /app
COPY requirements.txt /app/requirements.txt
RUN pip install --upgrade pip && pip install -r /app/requirements.txt

# Copy minimal sources
COPY README.md /app/README.md
COPY run.py /app/run.py
COPY mmlu.py /app/mmlu.py
COPY util_logs.py /app/util_logs.py
COPY report.py /app/report.py

# Bring in official MLPerf Inference (LoadGen)
COPY mlcommons_inference /app/mlcommons_inference
RUN pip install pybind11 && \
    cd /app/mlcommons_inference/loadgen && \
    CXX=g++ python setup.py bdist_wheel && \
    pip install dist/*.whl

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["python", "run.py", "--help"]
