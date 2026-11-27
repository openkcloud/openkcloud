<!--
============================================================
MIGRATION_v17.md: v16 → v17 NVIDIA driver image 전환 가이드
상세: containerized install (host immutable) 로의 incremental 전환 방법.
생성일: 2026-04-27 | 수정일: 2026-10-12
============================================================
-->

# v16 → v17 NVIDIA driver image migration

## 1. 핵심 변경 요약

| 항목 | v16 (legacy) | v17 (containerized) |
|------|--------------|---------------------|
| `nsenter --target 1` | 사용 (host namespace 진입) | **미사용** |
| host `/etc`, `/var/lib/dpkg`, `/var/lib/apt` | 변경 (apt install / purge) | **변경 0 byte** (immutable) |
| nvidia 패키지 install 위치 | host apt | container apt (image 내부) |
| DKMS 빌드 | host (`dkms autoinstall`) | container (`dkms build/install nvidia/<ver> -k <kernel>` 명시) |
| 모듈 sideload 위치 | host `/lib/modules/<kver>/updates/dkms/` (DKMS 부산물) | host `/run/nvidia/driver/lib/modules/<kver>/kernel/drivers/video/` |
| 모듈 로드 방법 | host `modprobe` (nsenter) | container 안 `chroot ${DRIVER_ROOT} modprobe` 또는 직접 `insmod` |
| 다른 vendor (furiosa) DKMS 영향 | `dkms autoinstall` 로 영향 받음 | 격리됨 (autoinstall 미사용) |
| rollback | host state 복구 필요 | image rollback 만으로 충분 (`spec.driver.image: ...:v16`) |
| host immutable invariant | ❌ | ✅ (entrypoint 실행 전후 host dpkg state byte-identical) |

## 2. 전환 절차 (incremental)

### 2.1 image 빌드 + push

```bash
cd driver-installer/nvidia
# KERNEL_VERSION 은 노드 uname -r 값
docker build \
  --build-arg KERNEL_VERSION="$(uname -r)" \
  --build-arg NVIDIA_DRIVER_MAJOR=590 \
  -f Dockerfile.v17 \
  -t <your-registry>/nvidia-driver-ds:590.48.01-v17 .
docker push <your-registry>/nvidia-driver-ds:590.48.01-v17
```

> 이미지 빌드 + push 는 이 가이드가 다루는 절차 범위 밖이며, 사용자 승인 후 별도로 진행한다.

### 2.2 DriverInstallPolicy CR 변경

```yaml
apiVersion: npu.ai/v1alpha1
kind: DriverInstallPolicy
metadata:
  name: nvidia
spec:
  driver:
    vendor: nvidia
    version: "590.48.01"
    image: <your-registry>/nvidia-driver-ds:590.48.01-v17   # ← v17 tag
    installMethod: containerized                                # ← 신설 필드 (DIP API 변경 시)
    prebuiltFallback: true
  upgradePolicy:
    rollbackTarget: previousValidated
```

> `installMethod` 필드는 DIP API 가 추후 추가하면 바인딩됨.
> 당장은 image tag (`-v17`) 만으로 구분 가능.

### 2.3 DS spec 변경 (operator 책임)

operator 의 `internal/controller/driver_daemonset_controller.go` 가 v17 image label
(`kcloud.ai/v17.install-method=containerized`) 을 감지하면 다음 마운트를 자동 추가:

| target | source | mode |
|--------|--------|------|
| `/host/lib/modules` | `/lib/modules` | ro |
| `/host/sys` | `/sys` | rw |
| `/host/proc/driver/nvidia` | `/proc/driver/nvidia` | ro |
| `/run/nvidia/driver` | `/run/nvidia/driver` | rw (HostPath, **DirectoryOrCreate**) |
| `/dev/nvidia*` | `/dev/nvidia*` | rw |
| `/var/lib/kcloud-operator` | `/var/lib/kcloud-operator` | rw |

또한 securityContext 에 `privileged: true`, `capabilities: SYS_MODULE,SYS_ADMIN` 부여 필요.

> 이 변경은 별도 PR 로 통합 시 처리하며, 이 문서의 산출물 범위 밖이다.

### 2.4 노드 단위 rolling update

`updateStrategy: OnDelete` 가정. 노드 1개씩:

1. operator 가 DS image 를 `:v17` 로 patch
2. 기존 driver pod delete
3. 새 v17 pod 가 spawn → entrypoint-v17.sh 가 컨테이너 안에서 빌드/sideload/load
4. healthcheck-v17.sh 가 livenessProbe 통과 시 다음 노드로 진행

## 3. 동작 원리 요약

```
┌────────────────── container (v17) ──────────────────┐
│ 1. detect host kernel: /host/sys/module/kernel/version │
│ 2. (옵션) host /lib/modules ro 검사 → prebuilt 가용?    │
│ 3. container apt 로 nvidia-driver-${MAJOR} install     │
│ 4. dkms build nvidia/<ver> -k <kernel>   ★ autoinstall 금지
│ 5. dkms install nvidia/<ver> -k <kernel>             │
│ 6. /lib/modules/<kver>/updates/dkms/nvidia*.ko        │
│      → cp → /run/nvidia/driver/lib/modules/<kver>/... │
│ 7. depmod -b /run/nvidia/driver                       │
│ 8. chroot /run/nvidia/driver modprobe nvidia ...      │
│    (mountpoint 아니면 insmod 직접)                    │
│ 9. nvidia-smi -L (container 안 — /dev/nvidia* 마운트)  │
│ 10. /var/lib/kcloud-operator/nvidia.ok 마커              │
│ 11. 상시 모니터링 루프 (healthcheck-v17.sh)           │
└──────────────────────────────────────────────────────┘
host /etc, /var/lib/dpkg, /var/lib/apt → READ NEVER, WRITE NEVER
```

## 4. Rollback

| 상황 | 절차 |
|------|------|
| 새 v17 image 가 노드에서 실패 | DIP `spec.driver.image` 를 `:v16` 으로 되돌림 → 즉시 legacy 동작 |
| v17 entrypoint 가 partial state 남김 | `cleanup_on_error` trap 이 sideload 한 module `rmmod` 만 — host dpkg/etc 변경 0 |
| host module 손상 의심 | `/run/nvidia/driver` 디렉토리 삭제 후 pod restart |

긴급 우회: DIP CR 에 `INSTALL_METHOD=hostnamespace` env 만 추가하면 entrypoint-v17.sh 가
v16 entrypoint.sh 로 즉시 위임 (이미지 rebuild 불필요).

```yaml
spec:
  template:
    spec:
      containers:
        - name: nvidia-driver
          env:
            - name: INSTALL_METHOD
              value: "hostnamespace"      # ← v16 동작으로 즉시 위임
```

## 5. 알려진 제약

1. **kernel header pre-bake 권장**
   - `Dockerfile.v17` 의 `--build-arg KERNEL_VERSION=...` 미지정 시 entrypoint 가 런타임 apt 로 install.
   - 이 경우 container apt 만 사용하므로 host 변경은 없지만 pod 시작 시간이 길어짐 (수 분).
   - 노드 kernel 이 균질한 클러스터는 빌드 시점에 박는 것을 권장.

2. **GPU 노드 reboot 필요한 경우**
   - 기존 v16 으로 빌드된 nvidia 모듈이 메이저 버전 차이로 unload 안 되는 케이스
     (process refcount > 0).
   - `driver-manager.sh` (v16 initContainer) 가 rmmod 시도하지만 실패 가능.
   - `rebootStrategy: Require` 시 `needs-reboot` 마커 생성 → 외부 reboot agent 처리.
   - v17 자체는 host module unload 권한 (privileged) 으로 시도하지만,
     다른 GPU process 가 GPU 사용 중이면 동일하게 실패 — workload eviction 선행 필요.

3. **다른 vendor (furiosa) 격리**
   - v17 은 `dkms autoinstall` 절대 호출 안 함. furiosa-driver-warboy 등 다른 DKMS 가
     /var/lib/dkms 에 존재해도 nvidia 빌드에 영향 없음.
   - 단, container 자체 apt 는 nvidia 패키지만 install 하므로 furiosa 패키지는 image 에 없음.

4. **prebuilt fallback (`linux-modules-nvidia-<ver>-<kernel>`)**
   - 기본 활성화 (`PREBUILT_FALLBACK=true`).
   - host `/lib/modules/<kver>/kernel/drivers/video/nvidia*.ko` 가 read-only mount 로 보이고
     메이저가 일치하면 DKMS 빌드 skip → 더 빠름.
   - 비활성화: `PREBUILT_FALLBACK=false` env.

5. **v16 파일은 절대 수정 금지**
   - `entrypoint.sh`, `Dockerfile`, `driver-manager.sh`, `healthcheck.sh`,
     `check-kernel-headers.sh` — coexistence 필수.
   - 추후 v17 stabilize 후 별도 cycle 에서 v16 deprecate.

## 6. 검증 체크리스트 (운영자)

- [ ] image 빌드 성공 (`docker build -f Dockerfile.v17`)
- [ ] image push 성공 (대상 레지스트리)
- [ ] 1개 GPU 노드에서 v17 DS pod Ready (startupProbe 통과)
- [ ] `/proc/driver/nvidia/version` 메이저 일치
- [ ] host `dpkg-query -l nvidia*` 출력이 v17 적용 전후 동일 (host immutable invariant)
- [ ] `nvidia-smi -L` 정상 응답
- [ ] device-plugin / NDR 갱신 확인
- [ ] sample GPU workload 정상
- [ ] 5개 노드 rolling update 완료 후 cluster GPU 가용량 100%
- [ ] rollback path: DIP image `:v16` 로 되돌리면 즉시 v16 동작

## 7. 관련 문서

- v16 entrypoint: `entrypoint.sh` (수정 금지)

## 마커 디렉터리 개명

호스트 마커 디렉터리가 `/var/lib/npu-operator` 에서 `/var/lib/kcloud-operator` 로 바뀌었다. 기본 경로(`INSTALL_METHOD=hostnamespace`)는 `entrypoint-v17.sh` 가 `entrypoint.sh` 로 위임하므로 그 안의 `marker-migrate.sh` 가 옛 디렉터리의 마커를 자동으로 이관한다. `INSTALL_METHOD=containerized` 경로는 nsenter 를 쓰지 않으므로 이관하지 않는다. 그 경로를 쓰는 노드는 새 경로에 마커를 새로 쓰고, node-device-detector 는 새 경로가 없을 때 옛 경로를 읽는다. 옛 디렉터리를 정리하려면 관리자가 호스트에서 한 번 `mv /var/lib/npu-operator/* /var/lib/kcloud-operator/` 를 실행한다.
