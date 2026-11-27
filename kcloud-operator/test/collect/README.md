<!-- README.md: test/collect/ evidence 수집 스크립트 사용법 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->

# test/collect — evidence 레코드 수집

`evidence.sh` 는 라이브 클러스터 메타데이터와 한 테스트 명령의 실행 결과를 모아
증거 수집 설계 문서
의 최소 필드를 JSON·Markdown 짝으로 남긴다. 측정값을 지어내지 않는다 — NodeDeviceReport
에 없는 필드는 `미수집` 으로 남긴다.

## 수집

```bash
./evidence.sh --node <노드> --vendor <벤더> \
  --command "<테스트 명령>" --expected "<출력에 있어야 할 문자열>" \
  [--backend devicePlugin|dra] [--mode <partition/sharing>] \
  [--resource <리소스명>] [--limit "<한계>"]... [-o <출력 prefix>] [--schema <path>]
```

`<prefix>.json` 과 `<prefix>.md` 를 만들고, 생성 직후 `schema.json` 으로 자체 검증한다.
`-o` 를 생략하면 `./evidence-<벤더>-<노드>-<날짜>` 를 쓴다.

- **actual**: `--command` 실행의 stdout+stderr 결합(전체를 그대로 보존 — 오류도 증거다).
- **verdict**: actual 에 `--expected` 문자열이 포함되면 `pass`, 아니면 `fail`.
  노드·NodeDeviceReport·벤더 엔트리가 없으면 명령을 실행하지 않고 `blocked`.
- **종료 코드**: `pass`=0, `fail`=1, `blocked`=2.

## 검증

```bash
./evidence.sh --validate <FILE.json> [--schema <path>]
```

`schema.json` 의 `requiredKeys`(전부 존재·비-null) 와 `verdictEnum`(`pass|fail|blocked`) 을
`jq` 로 검사한다. 통과 시 exit 0, 실패 시 누락 키·잘못된 verdict 를 출력하고 exit 1.

```bash
bash test/collect/read-commit-test.sh   # commit 폴백 순서 자체 점검(클러스터 불필요)
```

## 데이터 소스

| 필드 | 소스 |
|---|---|
| date | 실행일(`date +%F`) |
| kcloudVersion | operator Deployment 이미지 태그(`kubectl -n kcloud get deploy kcloud-operator`) |
| commit | Deployment 로그 `starting manager` 줄 → `/manager --version` → 체크아웃 HEAD(`-checkout`) |
| k8sVersion | `kubectl version -o json` 의 serverVersion.gitVersion |
| os / kernel / containerRuntime | `kubectl get node <노드>` 의 status.nodeInfo |
| device / driver / firmware / sdk | `kubectl get nodedevicereport <노드>` 의 벤더 엔트리(없는 필드는 `미수집`) |
| resourceName | `--resource` 또는 노드 allocatable 에서 도메인에 벤더+`.` 포함 키(예: `tenstorrent.com/blackhole`) |
| backend / mode | `--backend` / `--mode`(미지정 시 `해당 없음`) |
| command / expected / actual / duration | `--command` 와 그 실행 결과·소요 시간(초) |
| verdict / reason | 위 판정 규칙 |
| node / vendor | `--node` / `--vendor` |
| limits | `--limit`(반복 가능, 자동 도출 아님 — 사람이 아는 한계를 명시) |

`sdk` 는 NodeDeviceReport 에 대응 필드가 없어 현재는 항상 `미수집` 이다.

## 두 레코드 형식(이 스키마가 검증하지 않는 것)

이 `schema.json` 은 **점검 단위(per-check) 레코드**를 고정한다 — (노드, 벤더, 명령)
하나에 레코드 하나. **라운드 요약(round summary) 레코드**는 형식이 달라 이 스키마로
검증되지 않는다(요약 파일의 `verdict` 는 `PASS` 대문자에 사유 산문이 붙고, `nodes[]`·
`tests[]` 배열을 담는다). 두 형식의 필드 대응:

| per-check(이 스키마) | round summary |
|---|---|
| date | date |
| kcloudVersion | kcloud_version |
| commit | commits.kcloud-operator |
| k8sVersion | kubernetes |
| os / kernel / containerRuntime | nodes[].os / .kernel / .runtime |
| device / driver | nodes[].drivers(벤더별 맵) |
| firmware | firmware |
| sdk | sdk_runtime |
| resourceName | resource_names[] |
| backend | allocation_backend |
| mode | partition_sharing_mode |
| command / expected / actual | tests[].command / .expected / .actual |
| duration | duration(라운드 전체) |
| verdict / reason | verdict(라운드 종합 + 사유 산문) |
| node / vendor | nodes[].node / (drivers 키) |
| limits | 대응 필드 없음(요약은 각 test 의 actual 산문에 한계를 섞어 적음) |

## 예: Tenstorrent install-Job 재생성

`--command` 값이 저장소 루트 기준 경로이므로 저장소 루트에서 실행한다.

```bash
cd kcloud-operator
./test/collect/evidence.sh --node <tenstorrent-node> --vendor tenstorrent \
  --command "bash test/live/tt-install-job/run.sh" \
  --expected '"verdict":"PASS"' --backend devicePlugin \
  -o <결과 디렉터리>/<날짜>-tt-install-job
```

`test/live/tt-install-job/run.sh` 는 현재 로드된 드라이버 버전과 DIP 선언 버전이 같을 때
설치를 건너뛰는 멱등 경로다(<tenstorrent-node> 에 Job 하나를 만들고 지운다).
