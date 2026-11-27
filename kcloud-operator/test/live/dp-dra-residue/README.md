<!-- README.md: device-plugin↔DRA 전환 잔재 스냅샷 도구 사용법 | 생성일: 2026-09-28 | 수정일: 2026-10-11 -->
# device-plugin ↔ DRA 전환 잔재 스냅샷

## 왜

벤더가 광고 주체를 device-plugin 에서 DRA allocation API 로(또는 그 반대로) 전환하면 세
종류의 잔재가 남을 수 있다 — 0 으로 남은 채 등록만 유지되는 extended resource(무해),
전환 후에도 여전히 0 이 아닌 옛 광고(유해 — 이중 할당 위험), 그리고 `ResourceSlice`나 CDI
파일처럼 판단이 필요한 고아 오브젝트다. `snapshot.sh` 는 전환 전후 비교(다음 작업 몫)가 이
셋을 가려낼 수 있도록, 노드 하나에 대해 관련 자원 전부를 파일로 남기는 읽기 전용 도구다.

`kubectl get`·`ssh … ls/cat` 외의 명령을 쓰지 않는다. 클러스터에 아무것도 만들거나 바꾸지
않는다.

## 사용법

```bash
test/live/dp-dra-residue/snapshot.sh <출력디렉터리> <노드> <벤더>

# 예
test/live/dp-dra-residue/snapshot.sh /path/to/before <nvidia-node> nvidia
# ... 전환 실행(별도 작업) ...
test/live/dp-dra-residue/snapshot.sh /path/to/after <nvidia-node> nvidia
diff -r /path/to/before /path/to/after
```

세 번째 인자(벤더)는 manifest 기록용일 뿐 조회를 좁히는 데 쓰지 않는다 — 벤더로 필터링하면
그 벤더가 아닌 이름으로 남은 잔재(예: 이 클러스터에서 실제로 관측된 `rngd/rngd`,
`tenstorrent/blackhole-p150` 같은, 정식 리소스명과 다른 옛 등록)를 놓친다. allocatable·
capacity·라벨도 같은 이유로 필터 없이 전부 남긴다 — 무엇을 남길지 미리 좁히면 그 바깥의
잔재는 애초에 볼 수 없다.

출력 디렉터리는 스크립트가 없으면 만든다. 실행할 때마다 아래 12개 파일과 `manifest.txt` 를
남긴다(이미 있는 파일은 덮어쓴다).

## 남기는 파일

| 파일 | 내용 | 수집 방법 |
|---|---|---|
| `node-allocatable.json` | 노드 `status.allocatable`·`status.capacity` 전부 | kubectl |
| `node-labels.json` | 노드 `metadata.labels` 전부(`kcloud.ai/*` present·dra-owned·driver-* 포함) | kubectl |
| `resourceslices.json` | `ResourceSlice` 전체(cluster-scoped) | kubectl |
| `resourceclaims.json` | `ResourceClaim` 전체(전 네임스페이스) | kubectl |
| `resourceclaimtemplates.json` | `ResourceClaimTemplate` 전체(전 네임스페이스) | kubectl |
| `deviceclasses.json` | `DeviceClass` 전체(cluster-scoped) | kubectl |
| `daemonsets.json` | `DaemonSet` 전체(전 네임스페이스) | kubectl |
| `pods.json` | 그 노드에 배치된 파드 전체 | kubectl (`--field-selector spec.nodeName=`) |
| `npuclusterpolicy.json` | `NPUClusterPolicy` 전체(전 벤더 블록 + `status.advertiseSwitch`) | kubectl |
| `cdi-files.txt` | 노드의 `/var/run/cdi`, `/etc/cdi` 목록 | ssh |
| `dev-nodes.txt` | 노드의 `/dev` 아래 벤더 장치 노드 목록 | ssh |
| `kubelet-sockets.txt` | `/var/lib/kubelet/device-plugins/` 목록 | ssh |
| `manifest.txt` | 위 12개 파일의 상태 + 수집 시각·노드·벤더·kubectl 컨텍스트·이 저장소 커밋 | — |

## 종류 부재 vs 존재+0개

`ResourceSlice`·`ResourceClaim`·`ResourceClaimTemplate`·`DeviceClass` 네 종류는 이 클러스터에
그 kind 자체가 없을 수도(오래된 클러스터에서 DRA API 가 등록되지 않은 경우 등), 있지만
0개일 수도 있다. `kubectl get <kind> -o json` 이 0 이 아닌 코드로 끝나면(kind 를 못 찾음)
`{"collected":false,"reason":"..."}` 로, 0 이든 아니든 성공하면
`{"collected":true,"items":[...]}` 로 남긴다. `manifest.txt` 는 전자를 `absent(...)`, 후자를
`ok(N)` 으로 표시한다 — 둘 다 실패로 보지 않는다. 이 구분이 없으면 kind 부재를 오류로 죽여
DRA 객체가 하나도 없는 클러스터에서 이 도구가 한 번도 끝까지 돌지 못한다. 같은 이유로
node·SSH 대상이 없을 때도(`absent(...)`, `skipped(no-ip)`) 도구는 종료하지 않고 나머지 파일을
계속 남긴다 — 도구 자체가 실행에 실패했을 때만(잘못된 인자 수 등) 0 이 아닌 코드로 끝난다.

## SSH

`cdi-files.txt`·`dev-nodes.txt`·`kubelet-sockets.txt` 는 `${SSH_USER}@<노드 InternalIP>` 로 읽기만
한다. SSH 접속 자체의 성공/실패와 원격 명령 자체의 성공/실패를 분리해서 본다 — 접속은 됐지만
원격 명령이 `Permission denied` 등으로 실패하면 `manifest.txt` 는 `ok`(관측 성공)가 아니라
`partial(사유)`로 적는다. 접속 자체가 안 되면 `ssh-failed`, 노드 IP 를 못 찾으면
`skipped(no-ip)`다. `partial` 은 "관측 불가"이지 "변화 없음"이 아니다 — 전후 스냅샷 둘 다
`partial` 이면 그 파일의 diff 가 같아 보여도 실제로는 아무것도 확인하지 못한 것이다(비교는
`run.sh`의 `diff_snapshots`가 이 경우를 따로 표시한다).

## run.sh — 전환 왕복 하네스

`snapshot.sh`는 한 시점을 읽기만 한다. `run.sh`는 그 위에서 실제로
`NPUClusterPolicy.spec.<vendor>.advertiseBy`를 `devicePlugin → dra → devicePlugin`으로
왕복시키며 안전 게이트·전환·정착 대기·스냅샷을 하나로 묶는다(대상은 지금 `rngd`·
`tenstorrent` 둘). `kubectl get`/`patch`, `kubectl-npu` CLI,
`snapshot.sh`(ssh 읽기) 외의 쓰기는 하지 않고, 지우는 코드는 담지 않는다 — 잔재의 해로움
판정은 `diff_snapshots`가 낸 표를 보고 사람이 한다.

```bash
# 미리보기(클러스터 변경 없음, 안전 게이트와 스냅샷은 실제로 돈다)
DRY_RUN=1 VENDOR=tenstorrent NODE=<tenstorrent-node> test/live/dp-dra-residue/run.sh

# 실행(왕복 앞부분: before → dra 전환 → after-dra → devicePlugin 로 복귀 → after-dp)
VENDOR=tenstorrent NODE=<tenstorrent-node> test/live/dp-dra-residue/run.sh

# 단계별 실행(PHASE). all 은 dra.enabled 되돌리기·kubelet 재시작을 포함하지 않는다 —
# ③ 스냅샷의 판정에 따라 갈리므로 별도 단계다.
PHASE=safety-gate  VENDOR=tenstorrent NODE=<tenstorrent-node> test/live/dp-dra-residue/run.sh
PHASE=disable-dra  VENDOR=tenstorrent NODE=<tenstorrent-node> test/live/dp-dra-residue/run.sh
PHASE=kubelet-restart VENDOR=tenstorrent NODE=<tenstorrent-node> test/live/dp-dra-residue/run.sh
PHASE=snapshot LABEL=after-kubelet VENDOR=tenstorrent NODE=<tenstorrent-node> test/live/dp-dra-residue/run.sh
PHASE=diff LABEL_A=before LABEL_B=after-dp VENDOR=tenstorrent NODE=<tenstorrent-node> test/live/dp-dra-residue/run.sh
```

안전 게이트는 그 노드에서 그 벤더 확장 자원(`spec.<vendor>.resourceName`)을 `requests`에
담은 파드 수를 세어 0이 아니면 멈춘다 — `dra.enabled` patch·`allocation switch`·kubelet
재시작 앞에 매번 다시 돈다. `switch-to-*`는 CLI 실행 직후 `kubectl get npuclusterpolicy`로
같은 값을 교차 확인해 CLI의 성공 출력줄만 믿지 않는다. `wait-dra`/`wait-dp`는
`status.advertiseSwitch`와 `kcloud.ai/<vendor>.dra-owned` 노드 라벨을 최대 `SETTLE_TIMEOUT`초
(기본 300) 폴링한다 — 거절 사유(`status.advertiseSwitch[].reason`)가 나오면 그 자리에서
멈춘다.
