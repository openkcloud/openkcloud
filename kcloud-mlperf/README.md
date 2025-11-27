# MLPerf LLaMA-3.1-8B Runner (Clean Slate)

Minimal, universal, easy-to-run benchmark suite for MLPerf Inference v5.1 using vLLM.

> Important: You must have access to the reference model `meta-llama/Llama-3.1-8B-Instruct` on Hugging Face (accept the model license). Set `HF_TOKEN` and `HUGGINGFACE_HUB_TOKEN` to your token to enable downloads. See the model page: [Hugging Face: Llama-3.1-8B-Instruct](https://huggingface.co/meta-llama/Llama-3.1-8B-Instruct).

## Table of contents
- Quickstart (Docker)
- Files
- Results layout
- Behavior
- CLI flags + Flags explained (non‑experts)
- Local (no Docker)
- Expected metrics (targets)
- Metrics parity with official MLPerf
- Official MLPerf bench for Llama‑3.1‑8B (overview)
- Sample results (what you will see)
- 한국어 안내 (동일 내용의 한국어 정리)

## Quickstart (Docker)

```bash
# Get the code
git clone https://github.com/jshim0978/MLPerf_local_test.git
cd MLPerf_local_test

git submodule update --init --recursive --depth 1
docker build -t mlperf-llama31:clean .

# Accuracy (Datacenter/Offline)
docker run --gpus all --rm --env-file .env -v $PWD/results:/app/results mlperf-llama31:clean \
  python run.py --model meta-llama/Llama-3.1-8B-Instruct \
  --category datacenter --scenario offline --mode accuracy \
  --tensor-parallel-size auto --max-model-len 4096 --precision bf16

# Performance (Datacenter/Offline)
docker run --gpus all --rm --env-file .env -v $PWD/results:/app/results mlperf-llama31:clean \
  python run.py --category datacenter --scenario offline --mode performance \
  --tensor-parallel-size auto --max-model-len 4096 --precision bf16

# Server performance (auto QPS from last Offline)
docker run --gpus all --rm --env-file .env -v $PWD/results:/app/results mlperf-llama31:clean \
  python run.py --category datacenter --scenario server --mode performance \
  --server-target-qps auto --tensor-parallel-size auto --max-model-len 4096 --precision bf16

# Edge SingleStream performance
docker run --gpus all --rm --env-file .env -v $PWD/results:/app/results mlperf-llama31:clean \
  python run.py --category edge --scenario singlestream --mode performance \
  --tensor-parallel-size auto --max-model-len 4096 --precision bf16 --total-sample-count 512

# Combined accuracy + performance for selected scenario
docker run --gpus all --rm --env-file .env -v $PWD/results:/app/results mlperf-llama31:clean \
  python run.py --category datacenter --scenario offline --mode both \
  --tensor-parallel-size auto --max-model-len 4096 --precision bf16

# Clean re-clone (distribution) smoke test
cd ~
rm -rf MLPerf_local_test
git clone https://github.com/jshim0978/MLPerf_local_test.git
cd MLPerf_local_test
git submodule update --init --recursive --depth 1
printf "HF_TOKEN=%s\n" "<YOUR_HF_TOKEN>" > .env
printf "HUGGINGFACE_HUB_TOKEN=%s\n" "<YOUR_HF_TOKEN>" >> .env
docker build -t mlperf-llama31:clean .
set -e

# Datacenter Offline (20 samples)
docker run --gpus all --rm --env-file .env -v "$PWD/results:/app/results" mlperf-llama31:clean \
  python run.py --model meta-llama/Llama-3.1-8B-Instruct \
  --category datacenter --scenario offline --mode both \
  --tensor-parallel-size auto --max-model-len 4096 --gpu-memory-utilization 0.92 \
  --precision bf16 --total-sample-count 20 --keep-all 1

# Datacenter Server (20 samples; auto QPS from last Offline)
docker run --gpus all --rm --env-file .env -v "$PWD/results:/app/results" mlperf-llama31:clean \
  python run.py --model meta-llama/Llama-3.1-8B-Instruct \
  --category datacenter --scenario server --mode both --server-target-qps auto \
  --tensor-parallel-size auto --max-model-len 4096 --gpu-memory-utilization 0.92 \
  --precision bf16 --total-sample-count 20 --keep-all 1

# MMLU (100 samples, detailed)
docker run --gpus all --rm --env-file .env -v "$PWD/results:/app/results" mlperf-llama31:clean \
  python mmlu.py --total-limit 100 --max-model-len 4096 --gpu-memory-utilization 0.92 --precision bf16 --details 1
```

## Files
- `run.py`: single CLI for accuracy/performance across scenarios
- `mmlu.py`: MMLU inference-only evaluator
- `util_logs.py`: parse LoadGen logs to structured JSON
- `report.py`: summary.json + report.md + basic matplotlib plots
- `requirements.txt`, `Dockerfile`

## Results Layout
```
results/
  latest -> 2025MMDD-hhmmss
  index.md               # list of historical runs
  2025MMDD-hhmmss/
    config.json
    summary.json
    report.md
    plots/
    Performance/{mlperf_log_summary.txt, mlperf_log_detail.txt}
    Accuracy/{mlperf_log_accuracy.json, rouge.json}
```

## Behavior
- `--mode accuracy`: runs deterministic generation, computes ROUGE, writes `Accuracy/*`, renders report.
- `--mode performance`: runs selected scenario, writes `Performance/*`, renders report.
- `--mode both`: runs accuracy first then performance and renders a combined report.
- Historical index: `results/index.md` is updated after each run; `results/latest` points to the newest.

## CLI flags
- `--version`: MLPerf version string (default 5.1)
- `--model`: HF repo or alias (default `llama3.1-8b-instruct` → `meta-llama/Llama-3.1-8B-Instruct`)
- `--backend`: only `vllm` supported
- `--category`: `datacenter` or `edge`
- `--scenario`: `offline`, `server`, `singlestream`
- `--mode`: `accuracy`, `performance`, `both`
- `--precision`: `fp16` or `bf16`
- `--tensor-parallel-size`: integer or `auto` (GPU count)
- `--max-new-tokens`: generation length (default 128)
- `--total-sample-count`: integer or `auto` (13368 datacenter / 5000 edge)
- `--server-target-qps`: float or `auto` (0.8× last Offline)
- `--dataset`: `cnndm`
- `--results-dir`: output root (default `./results`)
- `--keep-all`: keep historical runs (1) or keep latest only (0)
- `--high-accuracy`: tighten ROUGE gate to 99.9%
- `--max-model-len`: effective context window passed to vLLM (helps avoid KV cache OOM)
- `--gpu-memory-utilization`: fraction [0..1] for vLLM KV cache sizing
- `--extra-metrics`: reserved flag for non-official extra metrics (default 0)

### MMLU flags
- `--total-limit`: subset size
- `--max-model-len`, `--gpu-memory-utilization`, `--precision`: same semantics as runner
- `--details`: 1 to emit `samples.csv`, subject breakdown, and plots

## Flags explained (non-experts)
- **category**: where you plan to run it. Datacenter allows `server` (QPS/latency). Edge allows `singlestream` (single‑user latency). Also changes default sample counts.
- **scenario**: what we measure.
  - `offline`: raw throughput (tokens/sec) with batching, latency not emphasized.
  - `server`: under a target request rate (QPS); reports latency percentiles.
  - `singlestream`: one request at a time; reports latency percentiles.
- **mode**:
  - `accuracy`: checks ROUGE and gates pass/fail.
  - `performance`: measures speed only.
  - `both`: runs accuracy then performance in one go.
- **precision**: numeric format.
  - `bf16`: good default on newer NVIDIA GPUs; stable and fast.
  - `fp16`: older alternative; similar quality.
- **tensor-parallel-size**: split one model across multiple GPUs. `auto` = use all visible GPUs.
- **max-new-tokens**: how long each answer can be. 128 is plenty for summaries.
- **max-model-len**: how long inputs + outputs can be (context window). Lower this if you hit GPU memory limits (e.g., 4096).
- **gpu-memory-utilization**: how much of VRAM vLLM should use for its caches. If you see out‑of‑memory, reduce this; if underutilized, increase slightly.
- **server-target-qps**: desired load for `server`. `auto` = 0.8× the last measured Offline throughput.
 - **server-target-qps**: desired load for `server`. `auto` = 0.8 × (last Offline tokens/sec ÷ avg output tokens/request). This avoids the common tokens/sec → QPS unit mix-up.
- **total-sample-count**: how many items to run. Use small numbers (e.g., 20–200) to smoke test; full runs use 13368 (datacenter) or 5000 (edge).
- **keep-all**: if 1, keeps every run with its own timestamped folder and updates `results/index.md` for history.
- **dataset**: we use CNN/DailyMail (`cnndm`) validation split.
- Environment: set `HF_TOKEN` and `HUGGINGFACE_HUB_TOKEN=$HF_TOKEN` to download gated models automatically.

## Local (no Docker)
```bash
git clone https://github.com/jshim0978/MLPerf_local_test.git
cd MLPerf_local_test
git submodule update --init --recursive --depth 1

python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
export HF_TOKEN=...; export HUGGINGFACE_HUB_TOKEN=$HF_TOKEN

# Datacenter Offline (20 samples; accuracy + performance)
python run.py --model meta-llama/Llama-3.1-8B-Instruct \
  --category datacenter --scenario offline --mode both \
  --tensor-parallel-size auto --max-model-len 4096 --gpu-memory-utilization 0.92 \
  --precision bf16 --total-sample-count 20 --keep-all 1

# Datacenter Server (20 samples; auto QPS from last Offline)
python run.py --model meta-llama/Llama-3.1-8B-Instruct \
  --category datacenter --scenario server --mode both --server-target-qps auto \
  --tensor-parallel-size auto --max-model-len 4096 --gpu-memory-utilization 0.92 \
  --precision bf16 --total-sample-count 20 --keep-all 1

# MMLU (100 samples, detailed)
python mmlu.py --total-limit 100 --max-model-len 4096 --gpu-memory-utilization 0.92 --precision bf16 --details 1
```

## Expected metrics (targets)
- Accuracy gate: ROUGE-Lsum >= 0.99 (>= 0.999 if `--high-accuracy 1`).
- Datacenter Offline: Tokens/sec reported in `summary.json` under `run.performance.tokens_per_sec`.
- Datacenter Server: Target vs Achieved QPS and latency percentiles in report; official MLPerf also considers TTFT/TPOT constraints for Server.
- Edge SingleStream: Latency p50/p90/p95/p99 in report; CDF plot in `plots/`.

Reference model: `meta-llama/Llama-3.1-8B-Instruct` (access required).


## Metrics parity with official MLPerf
- This runner adheres to the core MLPerf semantics per scenario (Offline tokens/sec; Server/SingleStream latency distributions).
- In the official benchmark, Server scenario additionally evaluates TTFT (time‑to‑first‑token) and TPOT (time‑per‑output‑token). Our default logs focus on the core fields; TTFT/TPOT are part of the official MLPerf Server checks and can be surfaced via a stricter MLPerf output mode (planned) or by using the vendored official repo.

## Official MLPerf bench for Llama‑3.1‑8B (overview)
- **Task**: CNN/DailyMail abstractive summarization (validation split). Accuracy is computed with ROUGE and must meet the gate before performance is considered valid.
- **Model**: Llama‑3.1‑8B‑Instruct. The reference harness provides a vLLM SUT and defaults to vLLM for this model [link](https://github.com/mlcommons/inference/tree/master/language/llama3.1-8b).
- **Scenarios and sample counts**:
  - Datacenter: Offline and Server, 13,368 samples
  - Edge: Offline and SingleStream, 5,000 samples
- **Accuracy run**: deterministic decode (temperature=0, top_p=1, top_k=1, e.g., max new tokens 128). Gate is defined relative to a baseline (≥99%, tighter in high‑accuracy).
- **Performance metrics**:
  - Offline: tokens/sec (result_tokens_per_second)
  - Server: achieved QPS under target load and latency percentiles. MLPerf also checks TTFT/TPOT at p99 and fails runs that exceed limits (per submission checker).
  - SingleStream: end‑to‑end latency distribution (p50/p90/p95/p99)
- **LoadGen** controls query issuance and timing; logs include mlperf_log_summary.txt and mlperf_log_detail.txt used by the submission checker.
- **Closed‑division constraints**: same model/dataset/preprocessing; accuracy must pass; only then is performance valid.


---

# 한국어 안내 (고가독성)

## 개요
이 저장소는 MLPerf Inference v5.1 LLM(LLAMA‑3.1‑8B‑Instruct) 벤치마크를 vLLM 백엔드로 최소 구성으로 재현할 수 있게 해줍니다. 정확도(ROUGE) 검증을 먼저 통과해야 성능 결과가 유효합니다.

## 빠른 시작 (Docker)
```bash
git submodule update --init --recursive --depth 1
docker build -t mlperf-llama31:clean .
docker run --gpus all --rm --env-file .env -v $PWD/results:/app/results mlperf-llama31:clean \
  python run.py --model meta-llama/Llama-3.1-8B-Instruct \
  --category datacenter --scenario offline --mode accuracy \
  --tensor-parallel-size auto --max-model-len 4096 --precision bf16
```

## 결과 구조
```
results/
  latest -> 가장 최근 실행 디렉터리
  index.md            # 과거 실행 이력
  YYYYMMDD-hhmmss-카테고리-시나리오/
    config.json
    summary.json
    report.md
    plots/
    Performance/{mlperf_log_summary.txt, mlperf_log_detail.txt}
    Accuracy/{mlperf_log_accuracy.json, rouge.json}
```

## 동작 모드
- `accuracy`: 결정론적 생성(temperature=0, top_p=1, top_k=1)으로 정답(ROUGE)을 산출하고 보고서를 생성합니다.
- `performance`: 선택한 시나리오(Offline/Server/SingleStream)로 성능을 측정하고 보고서를 생성합니다.
- `both`: 정확도 → 성능 순으로 연속 실행하고, 결합 보고서를 생성합니다.

## 주요 플래그 설명
- `--tensor-parallel-size auto`: GPU 개수에 맞춰 자동 병렬화
- `--max-model-len`: vLLM 컨텍스트 창. GPU 메모리가 작은 경우 2048~4096 권장
- `--gpu-memory-utilization`: KV 캐시 메모리 비율(예: 0.9~0.95)
- `--total-sample-count`: 샘플 수(스모크 테스트는 20~200, 공식 검증은 13368/5000)
- `--keep-all 1`: 과거 결과(디렉터리)를 보존하고 `results/index.md` 갱신

## 기대 지표(타깃)
- 정확도 게이트: ROUGE‑Lsum ≥ 0.99 (고정밀 `--high-accuracy 1` 시 0.999)
- Datacenter/Offline: tokens/sec
- Datacenter/Server: 목표/달성 QPS, 지연(percentile)
- 공식 MLPerf에서는 Server 시나리오에서 TTFT/TPOT(첫 토큰/출력 토큰 시간)도 검증 항목에 포함됩니다.
- Edge/SingleStream: 지연(percentile)

## MMLU
```bash
python mmlu.py --total-limit 100 --max-model-len 4096 --gpu-memory-utilization 0.92 --precision bf16 --details 1
```
결과 폴더에는 전체/도메인/과목별 정확도 JSON, per‑sample CSV, 기본 플롯이 생성됩니다.

## Sample results (what you will see)

### summary.json (excerpt)
```
{
  "meta": { "category": "datacenter", "scenario": "offline", "model": "meta-llama/Llama-3.1-8B-Instruct" },
  "system": { "gpu_count": 1, "torch_version": "2.5.1", "vllm_version": "0.6.6" },
  "run": {
    "accuracy": {
      "total_samples": 20,
      "rouge": { "rouge1": 0.40, "rouge2": 0.16, "rougeL": 0.24, "rougeLsum": 0.36 },
      "passed": false,
      "run_gen_len": 2540,
      "run_gen_num": 20
    },
    "performance": {
      "scenario": "offline",
      "duration_s": 8.23,
      "total_new_tokens": 9876,
      "tokens_per_sec": 1200.48
    }
  },
  "logs": {
    "summary_txt": ".../Performance/mlperf_log_summary.txt",
    "detail_txt": ".../Performance/mlperf_log_detail.txt",
    "accuracy_json": ".../Accuracy/mlperf_log_accuracy.json",
    "rouge_json": ".../Accuracy/rouge.json"
  },
  "plots": {
    "tokens_per_sec": ".../plots/tokens_per_sec.png",
    "latency_cdf": null
  }
}
```

### Performance/mlperf_log_summary.txt (offline)
```
scenario=offline
duration_ms=8230
total_new_tokens=9876
tokens_per_sec=1200.48
num_samples=20
```

### Accuracy/rouge.json (excerpt)
```
{
  "rouge1": 0.4079,
  "rouge2": 0.1550,
  "rougeL": 0.2450,
  "rougeLsum": 0.3580,
  "baseline": { "rouge1": 38.7792, "rouge2": 15.9075, "rougeL": 24.4957, "rougeLsum": 35.793, "gen_len": 8167644, "gen_num": 13368 },
  "gate_multiplier": 0.99,
  "threshold_rougeLsum": 0.3540,
  "run_gen_len": 2540,
  "run_gen_num": 20
}
```

### MMLU outputs
- `overall.json`: `{ "overall_accuracy": 0.694 }`
- `by_domain.json`: `{ "STEM": 0.70, "Humanities": 0.68, ... }`
- `by_subject.json`: per‑subject accuracy (e.g., `abstract_algebra`, `anatomy`, ...)
- `samples.csv`: per‑sample row with subject/domain/answer/pred/latency/tokens
- `plots/score_by_subject.png`: 막대 그래프(과목별 정확도)

---

## 결과 예시 (한국어)
- summary.json: 실행 메타/시스템/정확도/성능 요약과 로그·플롯 경로가 담겨 있습니다.
- Performance/mlperf_log_summary.txt: 시나리오별 핵심 수치(Offline=토큰/초, Server/SingleStream=지연 백분위수 등). 공식 MLPerf Server는 TTFT/TPOT도 확인합니다.
- Accuracy/rouge.json: ROUGE 점수와 기준값(베이스라인), 게이트 임계치, 이번 실행의 생성 토큰/샘플 수(run_gen_len/run_gen_num).
- MMLU: 전체/도메인/과목별 정확도, per‑sample CSV, 과목별 정확도 그래프.


