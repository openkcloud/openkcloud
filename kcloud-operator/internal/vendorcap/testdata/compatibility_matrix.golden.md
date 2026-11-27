---
title: "호환성 매트릭스"
description: "internal/vendorcap 레지스트리에서 생성. 손으로 고치지 말고 make capmatrix 로 재생성."
status: generated
doc_id: operator-compatibility-matrix
---

# 호환성 매트릭스

상태: Native=벤더 제공, Software=kcloud SW 구현, Unsupported=미지원(사유), Unknown=조사 필요. 검증은 Support.Assurance() 의 확인 수준(none/unit/cluster/hardware)이며, `kubectl npu capabilities` 의 라이브 Verified 열(실행 중인 노드에서 다시 확인한 값)과는 별개다. Unsupported 행의 검증 값도 같은 기준이다 — 지원 여부가 아니라 그 미지원 판정을 어디까지 확인했는지를 뜻한다(예: hardware 는 실제 장치로 재현해 확인한 미지원).

| 벤더 | 제품 | 능력 | 상태 | 검증 | 사유/근거 |
|---|---|---|---|---|---|
| furiosa | rngd | discovery | Native | none | NodeDeviceReport furiosa/rngd |
| furiosa | rngd | driver | Native | none | DriverInstallPolicy furiosa-rngd-ds |
| furiosa | rngd | firmware | Unsupported | none | 펌웨어 관리 미구현 |
| furiosa | rngd | runtime | Unsupported | none | 런타임(furiosa-llm·libnux)은 워크로드 컨테이너 이미지가 담음 — 노드엔 드라이버·device-plugin·DRA 만 설치, operator 가 노드에 설치·관리할 런타임 없음 |
| furiosa | rngd | containerToolkit | Unsupported | none | 해당 없음 |
| furiosa | rngd | devicePlugin | Native | none | furiosa-rngd-device-plugin (ensureFuriosaRngdDevicePlugin) |
| furiosa | rngd | dra | Native | none | furiosaai/furiosa-dra-driver, 라이브 PASS |
| furiosa | rngd | partition | Native | none | PE-fusion — internal/partition/rngd, 라이브 |
| furiosa | rngd | timeSlicing | Unsupported | none | 장치가 배타적 open — 두 번째 프로세스 EBUSY(2026-08-11 실측) (test/live/rngd-multiprocess/probe.sh) |
| furiosa | rngd | multiProcess | Unsupported | cluster | 배타적 open — 두 프로세스가 같은 장치 노드를 동시에 열면 두 번째가 EBUSY(ResourceBusy)로 튕김(2026-08-11 실측), 벤더 broker 없어 중재할 대상 없음 (test/live/rngd-multiprocess/probe.sh) |
| furiosa | rngd | brokered | Unsupported | none | broker not implemented |
| furiosa | rngd | isolation | Native | cluster | PE 파티션 격리 부분 실측 — 메모리 용량(조각 11.88/47.50 GiB 관측)·장애 봉쇄(이웃 생존) measured, 연산·대역폭은 벤더 부하 도구 한계로 blocked, evidence 원본은 이 저장소 밖(재현 하네스 미작성) |
| furiosa | rngd | telemetry | Native | none | furiosa-exporter |
| furiosa | rngd | health | Native | none | AcceleratorHealth 컨트롤러 |
| furiosa | rngd | reset | Unsupported | none | 공개 인터페이스(furiosa-smi CLI 2026.1·furiosa-smi-go SDK C API v2026.1.1)에 장치 리셋 수단 미발견 — drain 은 장치 활성/비활성 표시이지 HW 리셋 아님 (test/live/device-reset/run.sh) |
| furiosa | warboy | discovery | Native | none | NodeDeviceReport furiosa/warboy |
| furiosa | warboy | driver | Native | none | DriverInstallPolicy furiosa-warboy-ds |
| furiosa | warboy | firmware | Unsupported | none | 펌웨어 관리 미구현 |
| furiosa | warboy | runtime | Unsupported | none | 런타임(furiosa-llm·libnux)은 워크로드 컨테이너 이미지가 담음 — 노드엔 드라이버·device-plugin·DRA 만 설치, operator 가 노드에 설치·관리할 런타임 없음 |
| furiosa | warboy | containerToolkit | Unsupported | none | 해당 없음(벤더 툴킷 없음) |
| furiosa | warboy | devicePlugin | Native | none | ghcr.io/furiosa-ai/k8s-device-plugin (values furiosa.devicePluginImage) |
| furiosa | warboy | dra | Native | none | furiosaai/furiosa-dra-driver (values furiosa.dra) |
| furiosa | warboy | partition | Unsupported | none | 단일 코어 — 파티션 수단 없음 |
| furiosa | warboy | timeSlicing | Unsupported | none | 두 번째 open EBUSY(동시 open 실측 2026-09-19) (test/live/concurrent-open/probe.sh) |
| furiosa | warboy | multiProcess | Unsupported | none | 배타적 open — 두 번째 프로세스 open 시 EBUSY(동시 open 실측 2026-09-19)로 동시 실행 불가, 벤더 broker 없어 중재 대상 없음 (test/live/concurrent-open/probe.sh) |
| furiosa | warboy | brokered | Unsupported | none | broker 미구현 |
| furiosa | warboy | isolation | Unsupported | none | 배타적 open 으로 공유 불가 — 두 프로세스 동시 실행이 안 돼(EBUSY, 동시 open 실측 2026-09-19) 격리를 잴 대상이 없음 (test/live/concurrent-open/probe.sh) |
| furiosa | warboy | telemetry | Native | none | furiosa-exporter (deploy/helm/templates/furiosa-exporter.yaml) |
| furiosa | warboy | health | Native | none | AcceleratorHealth 컨트롤러 |
| furiosa | warboy | reset | Unsupported | none | 공개 인터페이스(furiosa-smi CLI 2026.1·furiosa-smi-go SDK C API v2026.1.1)에 장치 리셋 수단 미발견 — drain 은 장치 활성/비활성 표시이지 HW 리셋 아님 (test/live/device-reset/run.sh) |
| nvidia |  | discovery | Native | none | NodeDeviceReport nvidia, kcloud-node-manager PCI 스캔 |
| nvidia |  | driver | Native | unit | DriverInstallPolicy nvidia-gpu-ds, driver-installer/nvidia |
| nvidia |  | firmware | Unsupported | none | 펌웨어 관리 미구현 |
| nvidia |  | runtime | Native | none | NVIDIA container toolkit reconciler(toolkitdaemonset) |
| nvidia |  | containerToolkit | Native | none | deploy/helm/values.yaml driverInstallPolicies.nvidia.toolkit |
| nvidia |  | devicePlugin | Native | none | nvcr.io/nvidia/k8s-device-plugin (values nvidia.devicePluginImage) |
| nvidia |  | dra | Native | none | dra-driver-nvidia (values nvidia.dra), internal/controller/dra_driver.go |
| nvidia |  | partition | Native | none | MIG — internal/partition/nvidia, A30 라이브 PASS |
| nvidia |  | timeSlicing | Software | unit | device-plugin replicas — internal/partition/nvidia/timeslicing.go, 라이브 검증 |
| nvidia |  | multiProcess | Native | none | MPS control daemon(upstream k8s-device-plugin) — internal/partition/nvidia/timeslicing.go(ApplySharingConfig·MPSPipeDir), 4-tenant 검증 |
| nvidia |  | brokered | Unsupported | none | brokered 공유 미구현 |
| nvidia |  | isolation | Native | cluster | MIG hardware 격리 — co-run 전량 점유(tenants 4)에서 연산·대역폭 비 ≈1.0 실측, evidence 원본은 이 저장소 밖(재현 하네스 미작성) |
| nvidia |  | telemetry | Native | none | dcgm-exporter (values nvidia.dcgmExporter) + ServiceMonitor |
| nvidia |  | health | Native | none | AcceleratorHealth 컨트롤러 |
| nvidia |  | reset | Native | none | nvidia-smi -r/--gpu-reset 로 장치 HW·SW 상태 초기화(root·소비자 없음 조건, MIG vGPU guest 미지원) 문서 확인 — 실측(2026-09-28, k8s-worker1 A30) 시도, device-plugin·dcgm-exporter 의 상시 NVML 핸들로 매번 "In use by another client"(exit=255) 거부, cordon+DS 파드 삭제(승인 조치)로 점유 해제 실패; kill·taint·operator 라벨 재사용 세 건은 승인 범위 밖 수동 조치로 시도됐고 전부 되돌림 |
| rebellions |  | discovery | Native | none | NodeDeviceReport rebellions(node5 ATOM) |
| rebellions |  | driver | Unsupported | none | TrackOnly DriverInstallPolicy(rebellions-atom-ds) — 버전 관측만, 설치 경로 없음. 드라이버 패키지(rebellions-dkms)는 벤더 포털 인증이 필요해 호스트 관리자가 설치 (deploy/helm/templates/driverinstallpolicy.yaml) |
| rebellions |  | firmware | Unsupported | none | 펌웨어 관리 미구현 |
| rebellions |  | runtime | Unsupported | none | 런타임(rebel-compiler·optimum-rbln·vllm-rbln)은 컨테이너에 pip wheel 로 담기고 드라이버·펌웨어는 host-managed — operator 가 노드에 설치·관리할 런타임 없음 |
| rebellions |  | containerToolkit | Unsupported | none | 해당 없음 |
| rebellions |  | devicePlugin | Native | none | rebellions device-plugin (ensureRebellionsDevicePlugin, node5 라이브) |
| rebellions |  | dra | Native | none | rebellions/k8s-dra-driver-npu (values rebellions.dra) — ResourceSlice 발행은 벤더 바이너리 결함으로 차단(외부 제출 대기) |
| rebellions |  | partition | Unsupported | none | 공개 인터페이스(rbln-smi/rbln-stat/OSS device-plugin·DRA)와 rblnlib-go v0.3.0 소스(pkg/device·pkg/rsdgroup·pkg/rblnsmi) 검토 결과 분할 수단 미발견 — vendor-capability-not-found(공개 소스 조사 2026-09-15); RSD 그룹은 여러 장치를 하나로 묶는 집약이고 장치 분할 API 는 없음 |
| rebellions |  | timeSlicing | Software | hardware | 실 워크로드 동시 실행 실측(node5 2026-09-25) — 두 프로세스가 같은 ATOM(device 0)을 동시에 열고 device_open 이 두 프로세스 모두 0.01s 로 배리어에 비례해 늘지 않음(배타 락 부재, tt 의 진 파드 init 팽창과 대비), rbln-smi 가 NPU 0 에 두 컨텍스트 확인(NPU 1 유휴)이 등급의 주근거; 보조로 겹치는 구간의 처리량이 각각 solo 의 0.92배·합 1.83배(rt 동기 실행 실측 확인); 파드 경로 재확인(2026-09-29): descriptor 광고 자원으로 스케줄한 파드 셋(solo·co-run 2)에서 co-run 처리량이 각각 solo 의 0.85배·0.82배, 합 1.66배, 겹침 구간 30초, rbln-smi 컨텍스트 2 확인; 재현은 test/live/rebellions-timeslice/run.sh |
| rebellions |  | multiProcess | Unsupported | none | 벤더 broker 없음 — 동시 실행은 되나(2026-09-25 실측) 프로세스별 상한을 강제할 중재 수단 없음 (test/live/rebellions-timeslice/run.sh) |
| rebellions |  | brokered | Unsupported | none | broker 미구현 |
| rebellions |  | isolation | Unsupported | hardware | 연산·용량·대역폭·장애 네 항목 co-run 실측(node5 2026-09-28, 장치 0=0000:c3:00.0·장치 1=0000:c4:00.0) — 용량(장치 1 이 K-chain(CHAIN_K=32)·512MiB 를 점유한 채 장치 0 도 같은 크기로 성공, rbln-smi 로 NPU0·NPU1 메모리 독립 확인)·장애(장치 1 kill -9+구성 불일치 실패 동안 장치 0 30초 끝까지 실행, 사후 rbln-smi 정상)는 격리 확인. 연산(비 0.7883)·대역폭(비 0.8829)은 기준(비 ≥0.95)에 못 미쳐 장치 1 부하가 장치 0 처리량으로 빠져나간다 — 별도 물리 장치라도 완전한 성능 격리는 아니다(호스트측 공유 자원 간섭 추정) (test/live/rebellions-isolation/run.sh) |
| rebellions |  | telemetry | Unsupported | none | exporter 없음 |
| rebellions |  | health | Native | none | AcceleratorHealth 컨트롤러 |
| rebellions |  | reset | Unsupported | none | 공개 인터페이스(rbln-smi/rbln-stat CLI·rblnlib-go v0.3.0 pkg/device·rblnsmi·rsdgroup·device-plugin·DRA)에 장치 리셋 수단 미발견 — tdr 하위 명령은 공개 문서 미확인 (test/live/device-reset/run.sh) |
| tenstorrent |  | discovery | Native | none | NodeDeviceReport tenstorrent(k8s-worker3 blackhole-p150) |
| tenstorrent |  | driver | Software | hardware | driver-installer/tenstorrent 2.11.0 — 2.8.0→2.11.0 라이브 업그레이드 2026-09-21; 상태 기계 전이(install Job·검증·Idle 수렴) 라이브 확인; 재현은 test/live/tt-kmd-upgrade/run.sh |
| tenstorrent |  | firmware | Unsupported | none | 펌웨어 관리 미구현 |
| tenstorrent |  | runtime | Unsupported | none | 런타임(tt-metal)은 컨테이너 이미지·PyPI wheel 로 담기고 노드엔 tt-kmd 드라이버·device-plugin 만 설치 — operator 가 노드에 설치·관리할 런타임 없음 |
| tenstorrent |  | containerToolkit | Unsupported | none | 해당 없음 |
| tenstorrent |  | devicePlugin | Software | none | kcloud 자체 tenstorrent-device-plugin (images/kcloud-operator-images/tenstorrent-device-plugin) |
| tenstorrent |  | dra | Native | none | tenstorrent/tt-dra-driver + tt-fabric-manager (values tenstorrent.dra), 라이브 PASS 2026-09-18 |
| tenstorrent |  | partition | Unsupported | none | 장치 분할 API 없음 — tt-smi·tt-kmd 2.11.0 소스·tt-metal 문서 조사(2026-09-22) |
| tenstorrent |  | timeSlicing | Unsupported | hardware | 증명: spec.sharing→generic device-plugin replicas 광고·주입 기제 동작. 반증: 벤더 런타임(tt-metal)을 쓰는 실제 워크로드 두 개가 UMD 의 /dev/tenstorrent/0 KMD 락에서 직렬화돼 동시 실행 불가(co-run 실측 2026-09-23; 배리어 180/420/900s 에 진 파드 init 251/490/971s, 배리어 증가로도 미해소). 미판단: 락을 잡지 않는 소비자. 재등급 기준: 락을 잡지 않거나 공유하는 소비자 두 개의 동시 워크로드 실측 (test/live/tt-timeslice-throughput/run.sh) |
| tenstorrent |  | multiProcess | Unsupported | none | 단일 카드 broker 데몬 없음(tt-metal mesh 는 다중 장치 scale-out) — tt-smi·tt-kmd 2.11.0 소스·tt-metal 문서 조사(2026-09-22) |
| tenstorrent |  | brokered | Unsupported | none | broker 미구현 |
| tenstorrent |  | isolation | Unsupported | hardware | tt-metal UMD 가 /dev/tenstorrent/0 에 KMD 락을 잡아 두 워크로드가 직렬화 — 공유가 불가해 격리를 잴 대상이 없음(co-run 실측 2026-09-23) (test/live/tt-timeslice-throughput/run.sh) |
| tenstorrent |  | telemetry | Unsupported | none | exporter 없음 |
| tenstorrent |  | health | Native | none | AcceleratorHealth 컨트롤러 |
| tenstorrent |  | reset | Unsupported | hardware | 쓸 수 있는 장치 리셋 수단 미제공으로 확정(2026-10-02) — 벤더가 tt-smi -r 을 제공하지만 실행하면 호스트가 내려갔다 스스로 재부팅한다(2026-09-28 두 번 재현 08:33:09Z·13:55:57Z, 두 시각 모두 BMC SEL 에 그 정각 Critical Interrupt PCIe Fatal Error Asserted 뒤 53~56초 만에 OEM System boot event, 실시간 dmesg -w·journalctl -kf 캡처에서 리셋 전후 새 커널 로그 0줄). 노드 전체를 내리는 수단은 카드 하나만 되살리려는 리셋의 목적을 충족하지 못한다. PCIe Fatal Error 의 근본 원인은 미규명이고, 등급 판단과 분리해 2026-09-29 읽기 전용 원인 조사의 후보 순위로 남겨 뒀다(후보 1 은 호스트 firmware-first PCIe 오류 처리 정책) (test/live/device-reset/run.sh) |
