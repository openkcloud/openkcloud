# kcloud-mlperf

`kcloud-mlperf`는 LLM 추론 성능 평가를 위한 **MLPerf Inference 벤치마크 하니스** 저장소입니다.
`Llama-3.1-8B-Instruct` 모델을 대상으로 하며, MLPerf Inference 기반 LLM 벤치마크 실행에
필요한 구성요소만을 포함합니다.

> **저장소 범위 안내**
> 본 저장소에는 웹 애플리케이션, 클러스터 인프라, 원커맨드 설치 도구 등 전체 kcloud / ETRI
> LLM 평가 플랫폼 구성요소가 포함되지 않습니다. 해당 구성요소는 향후 별도 저장소인
> `kcloud-tool`을 통해 공개될 예정이며, 본 저장소는 MLPerf 추론 벤치마크 하니스 제공에 한정됩니다.

## 1. 벤치마크 구성 (`benchmarks/`)

| 벤치마크 | 측정 내용 | 구현 |
|---|---|---|
| **MLPerf Inference** | CNN/DailyMail 요약 → ROUGE (전체 데이터셋) | MLCommons 공식 LoadGen |
| **MMLU-Pro** | 5-shot Chain-of-Thought → 정확도 | TIGER-Lab 공식 (`mmlu_pro` 서브모듈) |
| **LLM Inference** | vLLM 추론 처리량(throughput) | vLLM 백엔드 |

정식 실행 전 10개 샘플 **smoke** 테스트를 먼저 수행하는 워크플로를 권장합니다
(`run_benchmarks.sh --smoke`) — 환경 설정·모델 접근 권한·런타임·Job 실행 가능 여부를 사전 확인합니다.

## 2. 멀티 가속기 지원

기존 NVIDIA GPU 경로에 더해 국내 NPU 실행 경로를 추가했습니다.

- **NVIDIA A40 / L40** (GPU)
- **FuriosaAI RNGD** (NPU)
- **Rebellions Atom+** (NPU)

가속기 선택은 `mlperf_cnndm100_fp8.py --hw <l40|a40|rngd|atomplus>`로 지정합니다.
FP8 MLPerf CNN/DailyMail Job 매니페스트(`benchmarks/jobs/`):
`mlperf-cnndm100-fp8-a40.yaml`, `mlperf-cnndm100-fp8-l40.yaml`,
`mlperf-cnndm100-fp8-rngd.yaml`, `mlperf-cnndm5-fp8-l40-dryrun.yaml`,
MMLU-Pro: `mmlu-pro-l40-fp8.yaml`.

## 3. Kubernetes Bare-metal 벤치마크 스위트

마스터/워커 노드 IP를 설정한 뒤 벤치마크 실행 환경을 구성합니다 (`benchmarks/scripts/`):
`setup_master.sh`, `setup_worker.sh`, `install_pilot_k8s.sh`(벤치마크 최소 구성),
`install_kcloud_stack.sh`(전체 스택), `preflight.sh`, `validate_pilot_installer.sh`,
`validate_full_stack.sh`, `run_benchmarks.sh`.

배포 템플릿(`benchmarks/deploy/templates/`): HF 토큰 Secret placeholder, smoke Job, benchmark Job.
신규 서버 셋업·트러블슈팅 한국어 가이드는 [`benchmarks/README.md`](benchmarks/README.md) 참고.

## 4. 서브모듈

- `benchmarks/mlcommons_inference` — [MLCommons Inference](https://github.com/mlcommons/inference) 공식 구현
- `benchmarks/mmlu_pro` — [TIGER-Lab MMLU-Pro](https://github.com/TIGER-AI-Lab/MMLU-Pro) 공식 구현

재현 가능한 실행을 위해 지정된 버전으로 고정됩니다.

## 5. 보안 및 자격증명

실제 자격증명은 포함하지 않습니다. 모든 비밀값은 placeholder 또는 Kubernetes Secret으로
외부에서 주입합니다. Hugging Face 모델 접근을 위해 사용자가 발급받은 토큰을 설정해야 합니다.

## 6. 빠른 시작

```bash
git clone --recursive https://github.com/openkcloud/kcloud-mlperf.git
cd kcloud-mlperf/benchmarks
# 마스터/워커 IP + HF_TOKEN 설정 후 항상 --smoke(10샘플) 먼저 실행
```

**요구사항:** Hugging Face에서 `meta-llama/Llama-3.1-8B-Instruct` 접근 권한(라이선스 승인)과
`HF_TOKEN` 또는 `HUGGINGFACE_HUB_TOKEN` 환경변수 설정이 필요합니다.

## License

See [LICENSE](LICENSE).
