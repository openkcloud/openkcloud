# Prometheus NPU 수집 설정 (Furiosa RNGD)

NPU 모니터링은 **Furiosa Metrics Exporter**를 1차 소스로, **node_exporter hwmon**을 보조로 사용한다. 테스트 환경에는 exporter 미설치 상태이므로 설치 후 메트릭명·라벨·단위를 재확인한다.

## 1. Furiosa Metrics Exporter (1차)

Furiosa NPU Operator / Metrics Exporter를 Helm으로 설치한다.

- NPU Operator: <https://developer.furiosa.ai/latest/en/cloud_native_toolkit/kubernetes/npu_operator.html>
- Metrics Exporter: <https://developer.furiosa.ai/v2025.1.0/en/cloud_native_toolkit/kubernetes/metrics_exporter.html>

Prometheus는 exporter Pod(DaemonSet)를 scrape하도록 ServiceMonitor 또는 scrape config를 추가한다.

### 공식 제공 메트릭 (4종)

| 메트릭 | 설명 | 비고 |
|--------|------|------|
| `furiosa_npu_alive` | liveness (1=alive) | 장치 식별 라벨 동반 |
| `furiosa_npu_hw_temperature{label="peak"}` | 코어 온도 | `peak`=core |
| `furiosa_npu_hw_temperature{label="ambient"}` | 보드 온도 | `ambient`=board |
| `furiosa_npu_hw_power{label="rms"}` | 칩 전체 전력(W) | **per-PE 전력 없음** |
| `furiosa_npu_core_utilization` | 코어별 사용률(%) | per-core |

식별자는 `serial`(device_sn) → `pci_bdf` → `uuid` 순으로 사용한다(`device_uuid` 재부팅 안정성 미검증, open_issues G-1).

## 2. node_exporter hwmon (보조)

exporter 미설치/누락 값은 node_exporter hwmon collector로 보완한다(드라이버가 표준 hwmon으로 노출).

- chip 라벨: `rngd0`, `rngd1`
- 온도: `node_hwmon_temp_celsius{chip="rngd0",sensor="temp1"}`(PEAK), `sensor="temp12"`(AMBIENT)
- 전력: `node_hwmon_power_average_watt`(hwmon `power1_average`)

node_exporter 기동 시 `--collector.hwmon` 활성화. 메트릭명은 node_exporter 버전에 따라 다를 수 있으니 확인한다.

## 3. exporter 미제공 항목 (aux 보완)

memory(DRAM/SRAM), throttle_reason, clock, governor, pcie_link, AER 오류는 exporter가 제공하지 않는다. libsmi/sysfs 보조 collector 또는 recording rule로 보완한다.

- 메모리: libsmi `get_memory_utilization` (bytes → MB 정규화)
- throttle: `throttle_reason` 비트 → boolean 메트릭(recording rule)
- AER: sysfs `aer_dev_*` → textfile collector

## 4. 파티션(slice)

RNGD PE 분할(`npu-slice`)은 미검증이다. 파티션 전력은 per-PE 전력 부재로 `partition_power = chip_power × (partition_pe_util / total_pe_util)` 비례 추정 + `power_estimation: "proportional"` 표기로 처리한다.

> 상세 근거: `docs/temp/02-decisions/furiosa_npu_findings.md`.
