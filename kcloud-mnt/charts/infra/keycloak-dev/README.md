# 개발 서버 Keycloak (Mac Mini)

개발 서버(Mac Mini `172.30.1.200`)에 Keycloak 을 컨테이너 1개로 띄웁니다. DB 는 같은 Mac Mini 의 PostgreSQL 입니다.
테스트베드(helm, `openkcloud-mnt` 의 `keycloak-0~2`)와 **같은 이미지(26.7.4)·같은 DB 구조**입니다.

| 항목 | 테스트베드 | 개발 서버 |
|---|---|---|
| 실행 | helm, 파드 3개 | docker compose, 컨테이너 1개 |
| 접속 | `https://keycloak.kcloud.etri:25443` (Istio 뒤, `KC_PROXY_HEADERS=xforwarded`) | `http://keycloak.dev.okestro:8080` (직접 접속) |
| DB | `openkcloud-db` 의 DB `keycloak` · 스키마 `keycloak` · 계정 `keycloak` | Mac Mini PostgreSQL 의 DB `keycloak` · 스키마 `keycloak` · 계정 `keycloak` |

## 처음 설치(1회)

1. **DB 준비** — `keycloak_db.sql` 의 `<비밀번호>` 를 채워 슈퍼유저로 실행
   ```bash
   psql -U okestro -d postgres -v ON_ERROR_STOP=1 -f keycloak_db.sql
   ```
   확인 쿼리는 파일 아래쪽 주석에 있습니다(테스트베드와 같은 결과여야 함).
2. **이름 등록** — 접속하는 PC(Mac Mini 포함)의 hosts 파일에 한 줄 추가
   ```
   172.30.1.200 keycloak.dev.okestro
   ```
   (macOS·Linux `/etc/hosts`, Windows `C:\Windows\System32\drivers\etc\hosts`)
3. **설정 파일**
   ```bash
   cd charts/infra/keycloak-dev
   cp .env.example .env
   vi .env      # 아래 두 줄만 바꿈(KC_HOSTNAME=http://keycloak.dev.okestro:8080 은 그대로)
   #   KC_DB_PASSWORD='<1번 비밀번호>'
   #   KC_ADMIN_PASSWORD='<첫 관리자 비밀번호>'
   ```
   값은 작은따옴표로 감쌉니다(`!` 같은 특수문자 때문). `.env` 는 git 에 올라가지 않습니다.
4. **실행**
   ```bash
   docker compose up -d
   docker compose logs -f       # "Keycloak 26.7.4 on JVM ... started in" 이 나오면 Ctrl+C
   ```
   처음 실행할 때는 이미지를 받고 테이블 100개를 만드느라 1~2분 걸립니다.
5. **확인**
   - 관리 화면 `http://keycloak.dev.okestro:8080/admin` 에 `admin` / `KC_ADMIN_PASSWORD` 로 로그인
   - `psql -U okestro -d keycloak -c '\dt keycloak.*'` → 테이블 약 100개
   - 다른 PC 에서도 열리는지 확인(안 되면 macOS 방화벽에서 들어오는 연결 허용)
6. **영구 관리자** — `admin` 은 임시 계정입니다. master realm 에 관리자 계정을 새로 만들고 `admin` 은 지웁니다.

## 로그인 테마(openkcloud)

`docker-compose.yaml` 이 `../keycloak-theme/openkcloud` 를 컨테이너에 연결합니다(테마 설명 `charts/infra/keycloak-theme/README.md`).
- **처음 적용(2026-10-14 추가)**: `git pull` → `docker compose up -d`(연결이 추가돼 컨테이너를 새로 만든다) →
  관리 화면 `openkcloud_realm` → Realm settings → Themes → Login theme `openkcloud` → Save
- **테마 파일이 바뀐 뒤**: `git pull` → `docker compose restart`(운영 모드라 테마를 캐시한다)
- colima 기본 설정은 홈 폴더만 컨테이너에 연결합니다 — 저장소를 홈 폴더 아래에 둡니다.

## 문제 해결

| 증상 | 원인·조치 |
|---|---|
| 로그에 `no pg_hba.conf entry for host "<IP>"` | PostgreSQL 이 컨테이너 접속을 거부. `pg_hba.conf` 에 `host keycloak keycloak <IP>/32 scram-sha-256` 추가 후 `SELECT pg_reload_conf();` |
| 로그에 `password authentication failed for user "keycloak"` | `.env` 의 `KC_DB_PASSWORD` 와 1번 비밀번호가 다름 |
| 로그에 `UnknownHostException: host.docker.internal` | Docker 런타임이 이 이름을 모름 → `KC_DB_URL_HOST` 를 Mac Mini IP 로 바꾼다 |
| 로그인 화면이 기본 Keycloak 모양 | Login theme 미지정, 또는 테마 폴더가 연결되지 않음 → `docker compose exec keycloak ls /opt/keycloak/themes/openkcloud/login` 확인 후 Themes 지정 |

## 참고

- 주소는 `.dev` 로 끝나지 않게 정했습니다 — `.dev` 는 브라우저가 무조건 HTTPS 로 바꿔(HSTS) HTTP 로는 열리지 않습니다.
  `.okestro` 는 실제 최상위 도메인이 아니라서 주소창에 `http://` 를 붙여 입력하세요(없으면 검색어로 처리될 수 있음).
- `KC_HOSTNAME` 은 토큰 발급자(issuer) 주소가 됩니다. 바꾸면 이 Keycloak 을 쓰는 앱 설정도 함께 바꿔야 합니다.
- 재부팅 후에도 뜨려면 Docker 런타임이 자동 시작돼야 합니다(OrbStack: 로그인 시 시작 / colima: `brew services start colima`).
- 중지 `docker compose stop`, 삭제 `docker compose down`(DB 데이터는 PostgreSQL 에 남음).
- 2026-10-14 `infra/keycloak-dev` 에서 이 위치로 옮겼다. 이미 설치한 서버는 `git pull` 뒤 git 에 없는 `.env` 를 새 폴더로 옮긴다
  (`mv infra/keycloak-dev/.env charts/infra/keycloak-dev/`). compose 프로젝트 이름(`okc-keycloak`)이 같아 실행 중인 컨테이너는 그대로 이어진다.
