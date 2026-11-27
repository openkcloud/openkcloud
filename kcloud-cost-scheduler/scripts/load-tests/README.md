# load-tests

GPU·NPU LLM 서빙의 지속 부하 시험과 서버 전력 측정 도구입니다. 결과는 Catalog 적재(`../catalog/ingest_run.py`)와 성능·전력 분석에 사용합니다.

서버 주소, 전력계 URL, 결과 경로는 파일에 넣지 않고 실행할 때 인자나 환경변수로 지정하십시오.

## 파일

| 파일 | 내용 |
|---|---|
| `netio_energy_snapshot.py` | NETIO PDU Out1+Out2 누적 Energy를 한 번 읽어 구간 경계값으로 저장 |
| `netio_meter_validate.py` | 일정 시간 동안 NETIO 누적 Energy를 반복 읽어 계측기 상태 확인 |
| `token_length_campaign.py` | 입력 토큰 길이별 단회 시험. 첫 SSE와 첫 콘텐츠 시각을 따로 기록하고 장치 전력과 서버 전력을 합산하지 않음 |
| `heterogeneous_serving_experiment.py` | GPU·NPU 이기종 LLM 서빙 배치(B0/B1/B2/P1) 시험. 동시성별 p50/p95/p99, TTFT, 전력 기록 |
| `run_device_sustained_matrix.py` | GPU 또는 NPU 한 장치에 동일 절차의 지속 부하 행렬 실행 |
| `run_gpu_sustained_matrix.py` | Kubernetes 위 GPU LLM 지속 부하 행렬. 런타임 정보 수집, 이어 실행, 정해진 시각 정지 |
| `summarize_experiment.py` | CNN+LLM 혼합 실행 결과를 `summary.csv`와 `result.md`로 정리 |
| `tests/` | 토큰 길이 시험의 SSE 해석·적분·p99 단위 시험 |

## 공통 절차

지속 부하 시험은 `warmup_s`(집계 제외) 뒤 `duration_s` 동안 닫힌 루프 동시성을 유지합니다. 요청별 결과는 `requests.jsonl`, 조건별 요약은 `performance.json`에 남깁니다.

전력은 두 가지를 따로 기록합니다.
- 장치 전력: `nvidia-smi`, `furiosa-smi` 또는 metrics exporter
- 서버 입력 전력: NETIO PDU 누적 Energy (`NETIO_URL`)

## 예시

실행 전에 `NETIO_URL`을 실제 PDU 주소로 바꾸고, GPU 서빙 URL에 포트 번호를 포함합니다. GPU 지속 부하 시험에는 `source`가 `gpu`인 행과 `watts` 열을 포함한 유휴 전력 CSV가 필요합니다.

```bash
export NETIO_URL="http://pdu.example.com/netio.json"

# 전력계 확인 (60초)
python3 scripts/load-tests/netio_meter_validate.py --duration-s 60 --out-dir results/meter-check

# GPU 지속 부하 행렬
python3 scripts/load-tests/run_gpu_sustained_matrix.py \
  --gpu-url http://127.0.0.1:8000 --concurrencies 10,20,30 --repeats 3 \
  --idle-csv results/idle-power.csv --out-root results/gpu-sustained

# 결과 정리
python3 scripts/load-tests/summarize_experiment.py --root results/mixed-cnn-llm

# 단위 시험
cd scripts/load-tests && python3 -m unittest discover -s tests
```

각 스크립트의 전체 인자는 `--help`로 확인하십시오.
