<!-- PROVENANCE.md: footprint 측정에 쓰는 벤더 차트의 출처·버전·해시 고정 기록 | 생성일: 2026-09-21 | 수정일: 2026-10-11 -->

# 벤더 차트 출처 고정

`measure.sh` 의 정적·라이브 측정이 참조하는 두 벤더 Helm 차트의 출처를 여기 고정한다.
둘 다 이 문서에 적은 URL·버전·sha256 과 다른 파일이 들어오면 측정이 실패해야 한다.

## nvidia-gpu-operator

| 항목 | 값 |
|---|---|
| 출처 | https://github.com/NVIDIA/gpu-operator (Helm 저장소: `https://helm.ngc.nvidia.com/nvidia`) |
| 받는 법 | `helm repo add nvidia https://helm.ngc.nvidia.com/nvidia && helm pull nvidia/gpu-operator --version v25.10.1` |
| 차트 버전 | v25.10.1 |
| 파일 | `gpu-operator-v25.10.1.tgz` |
| sha256 | `e13359580e9e0bfbf560fb38e39911b76a17bdbb5b0110fdd911f22467c8d339` |
| 크기 | 43,942 bytes |
| 라이선스 | Apache License 2.0(저장소 `LICENSE`, 재배포 허용) |
| 저장소 내 위치 | `test/footprint/reference/gpu-operator-v25.10.1.tgz`(커밋됨, 수정 없이 그대로) |
| chart_source() 기본값 | 이 파일. `NVIDIA_TGZ` 환경변수로 다른 경로 override 가능(단 내용은 위 sha256 과 같아야 한다 — sha256 은 코드 상수라 값이 다른 파일은 실행 시 exit 1) |

무수정 재배포이므로 Apache-2.0 4조(파생물이 아닌 원본 배포)를 그대로 만족한다.

## rbln-npu-operator

| 항목 | 값 |
|---|---|
| 출처 | https://github.com/RBLN-SW/rbln-npu-operator |
| 릴리스 | v0.5.0(2026년 8월 28일 게시) |
| 자산 | `rbln-npu-operator-chart-0.5.0.tgz` |
| 다운로드 URL | `https://github.com/RBLN-SW/rbln-npu-operator/releases/download/v0.5.0/rbln-npu-operator-chart-0.5.0.tgz` |
| sha256 | `a33880c79f73b54f837706970ab22fbf901030c1fd11f56a90ed672f5fd6de3c` |
| 크기 | 74,995 bytes |
| 라이선스 | Rebellions "Software User License Agreement"(사유 라이선스). 소스코드 배포 동의 없이 복사·재배포를 금지하는 조항이 있어 재배포 허용 여부가 불명확하다 |
| 저장소 내 위치 | 커밋하지 않음. `test/footprint/reference/rbln-npu-operator-chart-0.5.0.tgz` 는 `.gitignore` 대상이며, `measure.sh` 의 `ensure_rbln_chart()` 가 실행 시 위 URL 에서 받아 이 자리에 캐시하고 sha256 을 대조한다. 이미 있으면 sha256 만 대조하고 재다운로드하지 않는다. 대조 불일치는 손상/변조로 보고 즉시 실패한다. 네트워크가 없어 받지 못하면(오프라인) 그 회차의 rbln 정적 값을 `null` 로 남기고 사유를 limits 에 적는다 |
| chart_source() 기본값 | 위 캐시 경로. `RBLN_TGZ` 환경변수로 override 가능(단 내용은 위 sha256 과 같아야 한다 — sha256 은 코드 상수라 값이 다른 파일은 실행 시 exit 1) |
