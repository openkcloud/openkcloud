# scripts

keti-cost-scheduler의 Catalog API와 측정·검증 도구입니다. 스케줄러 본체는 [`../cost-based-scheduler/`](../cost-based-scheduler/)에 있습니다.

측정 도구는 실험 조건(노드·장치·이미지)을 인자나 환경변수로 받습니다. 서버 주소·계정·토큰은 파일에 넣지 않고 실행할 때 지정하십시오.

## Catalog (`catalog/`)

실측 Profile을 저장하고 스케줄러가 HTTP로 조회하는 서비스입니다. 스케줄러는 SQLite를 직접 열지 않습니다.

| 파일 | 내용 |
|---|---|
| `catalog_api.py` | HTTP Catalog API. `--db`, `--host`(기본 127.0.0.1), `--port`(기본 8099) |
| `repository.py`, `schema.sql` | 저장소 인터페이스와 SQLite 스키마 |
| `ingest_run.py` | 실험 결과 디렉터리 하나를 measured로 적재 |
| `ingest_legacy.py` | 이전 `performance.json` 결과를 `source=legacy_import`로 적재 |
| `workload_context.py`, `env_lookup.py` | 조회 키 생성: `environment_key` + `:cN`(동시성) |
| `link_placement_to_measured.py` | Shadow 배치 결정과 새 실측 결과를 연결해 예측 오차 기록 |
| `record_regret.py` | Shadow 결정과 실제 결과를 비교해 오차 기록 |
| `test_environment_key_golden.py`, `test_environment_key_r1.py` | Python·Go 조회 키 일치, 실측 조건 분리 시험 |

```bash
python3 scripts/catalog/catalog_api.py --db ./kcloud.db --port 8099
python3 scripts/catalog/test_environment_key_golden.py
```

## 종단 시험

| 파일 | 내용 |
|---|---|
| `e2e_shadow_score.sh` | Catalog API(Python) → Go Shadow Score 연결 확인. 포트는 `E2E_CATALOG_PORT`(기본 18199) |
| `test-npu-admission-e2e.sh` | 실제 API Server Admission 경로에서 NPU 요청 처리 확인 |

## 부하 생성

| 파일 | 내용 |
|---|---|
| `llm_fixed_request_list.py` | 고정 LLM 요청 목록을 끝까지 실행(스트리밍). TTFT·TPOT·토큰 수 기록 |
| `build_fixed_request_list.py` | 모델 토크나이저로 입력 토큰 수를 맞춘 고정 요청 목록 생성 |
| `cnn_gpu_fixed_images.py` | ResNet50 GPU 추론, 고정 이미지 수에서 종료 |
| `cnn_gpu_background.py` | ResNet50 GPU 배경 부하(CNN+LLM 혼합 실험용) |
| `gpu_train_fixed_steps.py` | ResNet50 고정 step GPU 학습 |
| `run_serve_train_colocate.py` | LLM Serving 부하와 GPU 학습 동시 실행 |
| `llm_gpu_job_entry.sh`, `llm_rngd_job_entry.sh` | Job 진입점: 엔진 시작 → 고정 요청 실행 → 엔진 종료(GPU vLLM / RNGD furiosa-llm) |
| `l40s_lsu_cnn_benchmark.py` | L40S LSU 기준 CNN 처리량(별도 환경 기준값) |

## 스케줄링 측정

| 파일 | 내용 |
|---|---|
| `measure_q2_schedule_latency.py` | 제출 → `spec.nodeName` 첫 관측까지 시간(폴링) |
| `measure_q2_schedule_latency_v2.py` | 생성 전에 watch를 시작해 배정 시각 측정 |
| `pod_watch_assign.py` | Pod 생성 요청·응답 시각 기록, 이름·UID 반환 |
| `job_phase_timing.py` | Job별 `<phase>_start_unix`·`<phase>_end_unix` 집계 |
| `dump_pending_pods.py` | Pending 시점의 할당량·요청량·이벤트 스냅샷 |
| `pod_cgroup_memory_peak.py` | 노드에서 컨테이너 cgroup 메모리 최대값 기록 |

## 결과 검증·분석

| 파일 | 내용 |
|---|---|
| `validate_run.py` | 실험 결과 파일 구성 검증 |
| `token_usage_check.py` | 요청별 입력·출력 토큰 수 확인 |
| `scrape_vllm_metrics.py` | vLLM `/metrics`를 주기적으로 CSV로 저장 |
| `k2_gate_equal_blocks.py` | 두 엔진의 `num_gpu_blocks` 일치 확인 |
| `verify_k2_jtoken_from_raw.py` | 원본으로 p99·토큰 합계·J/token 재계산 |
| `rescore_energy.py` | 보정한 유휴 전력으로 `net_energy_j` 재계산 |
| `analyze_g1_g2_g3.py` | G1/G2/G3 비교와 비용 분해 |
| `find_g1_knee.py`, `find_npu_knee.py` | 포화 지점(knee) 탐색. NPU는 RNGD 단독 곡선 기준 |

## 실행 구성 선택 (`exec_config_select/`)

| 파일 | 내용 |
|---|---|
| `selector.py` | LLM+CNN 묶음의 실행 구성 선택(시험용) |
| `fixtures.py` | 수동 비교 결과로 만든 시험 Profile |
| `test_selector.py` | 정상·SLO 실패·조회 누락·동점·후보 없음·장치 고정 시험 |

```bash
python3 -m unittest scripts.exec_config_select.test_selector
```

## Operator 배포 (이전 구성)

`deploy-crd.sh`, `deploy-rbac.sh`, `deploy-helm.sh`는 Workload Optimizer Operator용입니다. keti-cost-scheduler 배포는 `cost-based-scheduler/scripts/2.deploy.sh`를 사용합니다.
