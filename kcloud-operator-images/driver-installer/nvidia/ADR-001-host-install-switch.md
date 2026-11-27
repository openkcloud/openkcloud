<!-- ADR-001-host-install-switch.md: NVIDIA 드라이버 활성 경로를 v16 host-install 로 전환 | 생성일: 2026-07-16 | 수정일: 2026-10-11 -->

# ADR-001 — NVIDIA 드라이버 활성 경로를 v16 nsenter host-install 로 전환

- 상태: 채택(세 조건 이행을 전제로 함 — 아래 "조건 이행" 참조)
- 배경: L40S fresh 서비스 클러스터에서 operator 배포만으로 GPU 파드가 전부 실패한 제보
- 관련 이미지: `nvidia-driver-ds` (Dockerfile.v17 빌드), 신규 태그 `580.159.03-v176`

## 맥락

사용자 L40S fresh 서비스 클러스터에서 operator 배포만으로는 GPU 파드가 전부 실패했다.
개발 클러스터는 <nvidia-node> 의 **host 드라이버 설치 이력**이 결함을 은폐해 왔다(fresh 경로 미검증).

근본 원인 (실측):

- 시스템 전체가 **driver-root=`/` (host 설치) 전제**로 구성됨.
  - toolkit DS: `NVIDIA_DRIVER_ROOT=/` (`toolkit_daemonset_controller.go`).
  - driver DS spec: host `/lib/modules`·`/usr/src`·`/etc`·`/var/lib/kcloud-operator` 를 컨테이너
    루트에 bind, `HostPID=true`. `/host/*`·`/run/nvidia/driver`·`/dev/nvidia*` **미마운트**.
- 그러나 활성 entrypoint 였던 **v17.2 containerized** 경로는 컨테이너 내부에서 DKMS 빌드 후
  `/run/nvidia/driver` 로 **sideload** 한다. DS 가 그 경로를 마운트하지 않으므로 sideload 산출물이
  컨테이너 임시 fs 에 고아로 남고, **host userspace(libnvidia-*, nvidia-smi) 는 노출되지 않는다(⑤)**.
- 또한 idempotent-skip 후 `/dev/nvidia*` 디바이스 노드 생성 주체가 없어 toolkit CDI generate·
  GPU 파드가 데드락한다(④).
- <nvidia-node> 의 host 설치 이력이 v17.2 의 idempotent-skip 으로 이 경로를 항상 단락시켜 불일치를 은폐.

## 결정

활성 경로를 **v16 `entrypoint.sh` (nsenter host-install)** 로 전환한다.
v16 은 host apt 로 드라이버 패키지를 설치하므로 **userspace 까지 host 에 설치**되어 ⑤ 가 해소되고,
시스템의 driver-root=`/` 전제와 정합한다.

### 전환 방식 (operator 무변경)

operator 는 driver 컨테이너에 `command` 를 지정하지 않고 이미지 ENTRYPOINT 에 의존하며,
`INSTALL_METHOD` env 를 설정하지 않는다. 따라서 **Dockerfile.v17 에 `ENV INSTALL_METHOD=hostnamespace`**
를 추가하는 것만으로 `entrypoint-v17.sh` 가 진입 즉시 `entrypoint.sh`(v16) 로 위임한다(기존 분기 재사용).
operator/helm 변경이 필요 없다.

### 조건 이행

1. **런타임 설정 소유권 분리** — v16 경로의 컨테이너 툴킷 설치·`nvidia-ctk` containerd/CDI 설정·
   containerd/kubelet 재시작을 `MANAGE_CONTAINER_TOOLKIT`(기본 `false`) 게이트 뒤로 비활성화.
   런타임 lifecycle 은 상주 `nvidia-container-toolkit` DS 단독 소유(이중 소유 금지, ③ cordoned hang 회피).
   toolkit DS 미상주 클러스터는 `MANAGE_CONTAINER_TOOLKIT=true` 로 legacy 동작 복원(opt-in).
2. **안전장치 a/b/c 패리티** — (a) passthrough(전량 vfio) skip, (b) 다운그레이드 방지,
   (c) `VERSION_SOURCE=Host` 존중 — 세 가드 모두 v16 `entrypoint.sh` 에 이미 존재함을 확인(이식 불필요).
3. **v17.2 dormant 보존** — containerized 경로 코드는 이미지에 그대로 유지.
   `INSTALL_METHOD=containerized` 로 언제든 재활성 가능.

### ④ 처방

`ensure_device_nodes()` — 설치/skip 무관하게 `nvidia-modprobe -c0 -u -m` 로 `/dev/nvidia*` 노드를
보장하고, 부재 시 `nvidia-smi` 로 fallback(다중 GPU minor 커버). host namespace 에서 host userspace
바이너리 사용.

## 결과 / 트레이드오프

- fresh 노드에서 "operator 배포만으로 GPU 사용" 무개입 성립(AC).
- host `/etc`·apt·커널모듈을 v16 이 직접 변경 — host immutable 이 아님(v17.2 의 설계 목표였음).
  이 결정은 시스템 전제(driver-root=/)와의 정합을 host immutability 보다 우선했다.
- daemonset 노드에서 host 드라이버가 상주하므로 노드 재사용/드라이버 교체 시 v16 의 purge 경로에 의존.

## 참조

- `docs/impl/nvidia-install-gaps-20260716.md` (kcloud-operator repo) — 갭 분석 원본.
- `entrypoint.sh` — v16 활성 경로. `entrypoint-v17.sh` — dormant containerized 경로.
- ADR-002(docs 저장소) — 사전 컴파일 경로의 사용자 공간 출처를 이미지로 옮긴 결정. 이 문서의
  host 설치 전제는 유지된다.
