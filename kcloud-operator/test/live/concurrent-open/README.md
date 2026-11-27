<!-- README.md: 벤더 중립 동시 open 탐침 사용법 | 생성일: 2026-09-19 | 수정일: 2026-10-11 -->
# 동시 open 탐침

## 왜

`internal/vendorcap/registry.go` 의 `CapTimeSlicing`(SW 시분할)을 `Software` 로 선언하려면
"두 프로세스가 같은 장치 파일을 동시에 열 수 있다"(EBUSY 없음)는 실측 근거가 필요하다.
`test/live/rngd-multiprocess/probe.sh` 가 RNGD 전용으로 이미 이 방식을 썼다(결과는
배타적 open으로 `Unsupported`) — 이 디렉터리는 그 `os.open(O_RDWR)` 판정 방식을 벤더 중립으로
일반화해 Warboy·Tenstorrent·Rebellions 에도 쓴다.

## 방법

`probe.sh VENDOR HOST DEVICE` — SSH 로 노드에 접속해 python3 `os.open(O_RDWR)` 로 장치를 연다.
부하를 주지 않는다(ioctl 없음, HOLD 초 뒤 close, 기본 3초·최대 5초). 두 가지 모드가 있다:

- **`MODE=hold`(기본)**: 이 스크립트가 프로세스 A(장치를 열고 HOLD 초 보유)와 프로세스 B(0.5초
  뒤 같은 장치를 여는 2차 open)를 모두 SSH 로 띄운다. B 의 결과가 판정 대상이다. 노드에 그
  장치를 이미 쓰는 다른 프로세스가 없을 때 쓴다.
- **`MODE=single`**: 장치를 이미 외부 프로세스가 쥐고 있어 A 를 새로 띄우면 그 외부 프로세스를
  방해할 때 쓴다(예: <rebellions-node> 의 `atomplus-vllm.service`). 이 스크립트는 B 역할의 단발 open 만
  한다 — 우리 쪽 첫 open 이 곧 "두 번째 opener" 다. 이 경우 호출자가 `CONFOUND=` 로 confound
  사유를 채워야 한다(외부 holder 가 이미 A 역할이라 통제된 2-프로세스 프로토콜이 아니라는 뜻).
  **`CONFOUND` 가 비어 있으면 스크립트는 아무것도 열지 않고 사용법 오류(exit 1)로 종료한다** —
  confound 없는 `single` 실행은 기록할 근거가 없다는 뜻이라 아예 막는다.

기타 옵션: `HOLD=<초>`(기본 3, **5 초로 자동 상한** — 그 이상을 넣어도 조용히 5 로 clamp. 정수가
아닌 값(빈 값·문자·`3.5` 같은 소수)을 넣으면 clamp 전에 기본값 3 으로 되돌리고, `089` 처럼 선행
0 이 붙은 값은 `10#` 으로 10진수를 강제한 뒤 clamp 한다 — `(( ))` 산술 컨텍스트가 비정수 입력에
죽거나, 선행 0 을 8진수로 읽어 clamp 를 통째로 건너뛰는 것을 막기 위함. 선행 0 을 처리하기 전에는
`HOLD=089` 가 `((: 089: 해당 진법에서 표현할 수 없는 값`으로 실패하면서 clamp 가 적용되지 않아
그대로 89초를 보유했다),
`USE_SUDO=1`(장치가 root:root 0600 이라 `sudo -n python3` 로 열어야 할 때 — `sudo -n true` 로
NOPASSWD 확인 필수), `SSH_OPTS=<ssh 옵션>`(예 `"-o ConnectTimeout=2"` — 접속 자체가 오래
걸리거나 실패하는 노드를 빨리 판정하고 싶을 때).

## 판정

두 번째 open 의 결과 문자열로 판정한다. **ssh 연결 자체가 실패해도(권한·호스트 키·타임아웃·
unreachable) 스크립트는 멈추지 않고 그 실패 메시지를 `secondOpen` 에 `SSH_FAILED: ` 접두로
담아 `error` 로 판정한 JSON 한 줄을 낸다** — 결과가 없어 침묵하는 대신 항상 한 줄이 나온다.

| 두 번째 open 결과 | verdict | 의미 |
|---|---|---|
| `OPEN_OK` | `ok` | 동시 open 허용 — SW 시분할 후보(단, Software 승격은 연결 확인 후) |
| `OPEN_FAIL EBUSY` | `exclusive` | 드라이버가 배타적으로 잠금 — `Unsupported` 근거 |
| `OPEN_FAIL EACCES` | `error` | **권한 문제**(예: sudo 필요한데 안 씀) — 배타적 open 의 근거가
  아니므로 `exclusive` 로 오판정하지 않는다. `USE_SUDO`/장치 권한을 고쳐 재실행 |
| `SSH_FAILED: ...`(연결 실패) / 그 외 errno | `error` | 판정 불가 — `Unknown` 유지, 사유만 기록 |

`hold` 모드는 프로세스 A(보유)의 결과도 `firstOpen` 에 함께 남긴다. **A 가 장치를 열지 못했으면
B 가 성공했어도 `verdict` 는 `error` 다** — 보유자가 없는 상태에서 성공한 open 은 "동시" 근거가
아니라 단발 open 이라 `MODE=single` 과 구분되지 않기 때문이다. `MODE=single` 에는 A 역할이
외부 holder 라 `firstOpen` 이 `n/a` 이고, ssh 가 아예 실패해 A 의 줄이 없으면 `MISSING` 이다.

결과는 JSON 한 줄로 찍는다: `{"vendor","host","device","mode","firstOpen","secondOpen","verdict","confound","date"}`.

`verdict=ok` 는 그 벤더를 **SW 시분할 후보**로 만든다 — 레지스트리 `Software` 승격 자체는 다음
규칙을 따른다.

- **승격한다**: 연결 동작을 라이브로 보인 뒤. descriptor `spec.sharing` → 생성 device-plugin replicas
  → 파드 여럿이 같은 장치를 나눠 쓰는 것까지 확인하면 그때 올린다. Tenstorrent 가 이 경로로
  승격됐다.
- **보류한다**: 탐침 자체가 confound 된 경우. Rebellions 는 외부 holder(`atomplus-vllm.service`)가
  이미 장치를 쥐고 있어 통제된 2-프로세스 프로토콜을 못 돌렸으므로 `ok` 여도 `Unknown` 이다.
  연결 여부가 아니라 근거의 성격이 보류 사유다 — 외부 holder 없는 구간에서 재실측해야 풀린다.

레지스트리 값은 descriptor sharing 게이트(`internal/controller/acceleratordescriptor_controller.go`
의 `sharingRefusal`)와 ACPP sharing 게이트(`internal/webhook/acceleratorpartitionpolicy.go`)가 그대로
소비하므로, 승격·보류 어느 쪽이든 그 두 게이트의 회귀 시험 기대값을 함께 갱신해야 한다.

## 실행 예시

```bash
# Warboy — 장치가 root:root 0600
USE_SUDO=1 bash test/live/concurrent-open/probe.sh warboy <nvidia-node> /dev/npu0

# Tenstorrent — 0666, sudo 불필요
bash test/live/concurrent-open/probe.sh tenstorrent <노드 IP> /dev/tenstorrent/0

# Rebellions <rebellions-node> — 외부 holder(vLLM) 로 confound, single 모드
MODE=single CONFOUND="external holder atomplus-vllm.service already held the device" \
  bash test/live/concurrent-open/probe.sh rebellions <노드 IP> /dev/rbln0
```

## 하드웨어 없이 검증(로컬 self-check)

ssh 실패·오류 경로는 실제 벤더 장치 없이도 로컬에서 확인할 수 있다:

```bash
# 존재하지 않는 장치 경로 — 연결은 되지만 open 이 ENOENT 로 실패 → verdict:"error"
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5" \
  bash test/live/concurrent-open/probe.sh selfcheck localhost /no/such/device
# → {"...,"secondOpen":"OPEN_FAIL ENOENT","verdict":"error",...}

# 연결 자체가 불가능한 호스트(RFC 5737 TEST-NET-3, 응답 없음) — ConnectTimeout 으로 빨리 종료
SSH_OPTS="-o ConnectTimeout=2 -o BatchMode=yes" \
  bash test/live/concurrent-open/probe.sh selfcheck 203.0.113.1 /dev/whatever
# → {"...,"secondOpen":"SSH_FAILED: ssh: connect to host 203.0.113.1 port 22: Connection timed out","verdict":"error",...}
```

`HOLD` 정규화도 장치 없이 확인한다. `/dev/null` 은 어느 리눅스에나 있고 두 프로세스가 동시에 열 수
있어 `hold` 모드 왕복을 그대로 돌릴 수 있다 — 벽시계 시간이 곧 적용된 `HOLD` 값이다.

```bash
# 선행 0: 089 → 10진 89 → 5 로 clamp (약 5초)
time HOLD=089 bash test/live/concurrent-open/probe.sh selfcheck localhost /dev/null
# 비정수: abc → 기본값 3 (약 3초)
time HOLD=abc bash test/live/concurrent-open/probe.sh selfcheck localhost /dev/null
```

넷 다 스크립트가 중간에 죽지 않고(`set -e` 하에서도) JSON 한 줄을 끝까지 낸다는 것을 보여준다.

## 한계

- open 성공/실패만 본다 — 실제 동시 추론 처리량, 지연, 격리는 측정하지 않는다(2단계 측정은
  `test/live/rngd-multiprocess/README.md` 의 "2단계 측정" 절 참고).
- 대조군(idle 상태의 다른 장치로 같은 프로토콜을 돌려 계측기 자체가 유효한지 확인)은 벤더 노드에
  같은 성격의 두 번째 idle 장치가 있을 때만 의미가 있다 — 세 벤더 모두 그런
  장치가 없어(Warboy·Tenstorrent 는 장치가 하나뿐, Rebellions 는 둘 다 이미 vLLM 이 점유) 생략했다.
- `MODE=single` 은 외부 holder 의 정체·개수를 검증하지 않는다 — 호출자가 사전에 `fuser`/
  `systemctl status` 등으로 직접 확인하고 `CONFOUND` 에 근거를 남겨야 한다.
- 절대 재부팅하지 않고, 드라이버를 건드리지 않는다.
