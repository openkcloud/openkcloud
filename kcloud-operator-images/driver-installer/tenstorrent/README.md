<!-- README.md: Tenstorrent tt-kmd 컨테이너화 드라이버 설치기 사용법 | 생성일: 2026-04-22 | 수정일: 2026-10-11 -->

# Tenstorrent tt-kmd 드라이버 컨테이너

Tenstorrent Blackhole NPU를 위한 컨테이너화 드라이버 설치기입니다.
Kubernetes DaemonSet으로 배포하여 호스트 커널 모듈 상태를 지속 관리합니다.

## 개요

| 항목 | 내용 |
|------|------|
| 대상 하드웨어 | Tenstorrent Blackhole (n150 / n300 계열) |
| 커널 모듈 | `tenstorrent` (tt-kmd) |
| 업스트림 | <https://github.com/tenstorrent/tt-kmd> |
| 라이선스 | GPL-2.0 |

## 동작 방식

```
[컨테이너 시작]
      │
      ▼
[호스트 네임스페이스 진입 (nsenter)]
      │
      ▼
[lsmod | grep tenstorrent ?]
   YES ──► [no-op] ──────────────────────────────┐
   NO                                            │
      │                                          │
      ▼                                          │
[modprobe tenstorrent]                           │
   OK  ──────────────────────────────────────────┤
   FAIL                                          │
      │                                          │
      ▼                                          │
[DKMS 빌드 (SKIP_DKMS=1이면 건너뜀)]            │
      │                                          │
      ▼                                          │
[ready 마커 생성]  ◄────────────────────────────┘
/tmp/driver-ready
/var/lib/kcloud-operator/driver.ready
      │
      ▼
[헬스 모니터링 루프 (30초 간격)]
lsmod | grep tenstorrent
ls /dev/tenstorrent/
```

## 환경 변수

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `HEALTH_CHECK_INTERVAL` | `30` | health check 간격 (초) |
| `SKIP_DKMS` | `0` | `1`이면 DKMS 빌드 건너뜀 |
| `TT_KMD_VERSION` | `1.0` | DKMS 빌드 시 사용할 tt-kmd 버전 |
| `TT_KMD_REPO` | `https://github.com/tenstorrent/tt-kmd` | 소스 클론 URL |
| `DEBUG` | `0` | `1`이면 `set -x` 활성화 |

## 빌드 & 배포

```bash
# 이미지 빌드
make docker-build

# 레지스트리 푸시
make docker-push

# 로컬 테스트 (privileged + pid=host)
make local-run

# 커스텀 이미지 태그
make docker-build IMG=<your-registry>/tt-kmd-installer:v0.2.0
```

## 사전 컴파일 이미지 (prebuilt module)

특정 노드 커널용 `tenstorrent.ko` 를 이미지 빌드 시점에 미리 컴파일해 함께 굽는 옵션입니다.
air-gap 클러스터에서 노드마다 DKMS 로 빌드하는 시간과 빌드 도구 의존을 줄입니다.
노드 커널이 이미지에 구워진 커널과 일치하면 entrypoint 가 DKMS 빌드 없이 그 모듈을 바로
설치하고, 일치하지 않으면 기존 DKMS 경로로 자동 폴백합니다.

### 빌드

```bash
make build-precompiled \
  REGISTRY=<자체 미러>/kcloud \
  TT_KMD_VERSION=2.11.0 \
  KERNEL=5.15.0-190-generic
```

`KERNEL` 은 대상 노드의 `uname -r` 값입니다. 빌드 단계에서 `linux-headers-<KERNEL>` 패키지를
apt 로 설치해 kbuild 로 모듈을 컴파일하므로 빌드 러너에 인터넷 접근이 필요합니다(런타임에는 불필요).
롤백 대상인 2.8.0 은 `TT_KMD_GIT_TAG=ttkmd-2.8.0` 을 함께 지정합니다.

### 태그 규약

`<kmd>-k<kernel>-vN` 형식입니다(예: `2.11.0-k5.15.0-190-generic-v1`).
기본(DKMS 전용) 이미지의 `<kmd>-vN` 태그와 구분됩니다.

### 이미지 내부 산출물

- `/opt/tt-kmd-ko/<kernel>/tenstorrent.ko` — 사전 컴파일된 커널 모듈
- `/opt/tt-kmd-ko/<kernel>/vermagic` — 해당 모듈의 vermagic 문자열
- `/opt/tt-kmd-src/<ver>/` — 기존 DKMS 폴백용 baked 소스(그대로 유지)

이미지에는 `org.kcloud.prebuild-kernel=<kernel>` 라벨이 붙습니다(사전 컴파일 이미지 식별용).

### entrypoint 동작

1. 노드의 `uname -r` 로 `/opt/tt-kmd-ko/<커널>/tenstorrent.ko` 존재를 확인합니다.
2. 있으면 host `/lib/modules/<커널>/updates/dkms/` 에 복사한 뒤 `depmod` · `modprobe tenstorrent`
   로 설치하고, 로그에 `prebuilt module for <커널> installed (dkms skipped)` 를 남깁니다.
3. 없으면(커널 불일치) 기존 DKMS 빌드/설치 경로로 진행합니다.

install Job 은 host `/lib/modules` 를 컨테이너 `/lib/modules` 로 마운트하므로,
컨테이너에서 그 경로에 쓰면 host 에 그대로 반영됩니다.

### 전제 (Secure Boot)

Secure Boot 가 켜진 노드는 서명되지 않은 모듈 로드를 거부합니다. prebuilt 모듈도 서명되지
않으므로 이 경로는 Secure Boot 가 꺼진 노드에서 동작합니다(DKMS 경로도 동일 전제).
서명이 필요하면 별도 서명 단계를 추가해야 합니다.

## DaemonSet Probe 설정 예시

```yaml
startupProbe:
  exec:
    command: ["test", "-f", "/tmp/driver-ready"]
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 60   # 최대 5분 대기

livenessProbe:
  exec:
    command: ["/usr/local/bin/healthcheck.sh"]
  initialDelaySeconds: 30
  periodSeconds: 30
  failureThreshold: 3
```

## 디렉토리 구조

```
tenstorrent/
├── Dockerfile        # 컨테이너 이미지 정의
├── entrypoint.sh     # 메인 드라이버 설치/모니터링 스크립트
├── healthcheck.sh    # livenessProbe용 헬스체크
├── Makefile          # 빌드/배포 자동화
├── .gitignore        # 제외 파일 목록
├── LICENSE           # GPL-2.0 라이선스
└── README.md         # 이 파일
```

## 참고

- [tt-kmd GitHub](https://github.com/tenstorrent/tt-kmd)
- [Tenstorrent 공식 문서](https://docs.tenstorrent.com/)
