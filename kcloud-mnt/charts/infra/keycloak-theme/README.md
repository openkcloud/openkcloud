# Keycloak 로그인 테마 `openkcloud`

포털 옛 `/login` 화면 모양(왼쪽 브랜드 영역 + 오른쪽 로그인 카드)을 Keycloak 로그인 화면으로 옮긴 테마입니다.
realm `openkcloud_realm` 의 로그인 테마로 씁니다(realm 파일 `charts/infra/keycloak/realm/openkcloud_realm.json` 에 `loginTheme` 지정).

| 환경 | 넣는 방법 |
|---|---|
| 개발(Mac Mini) | `charts/infra/keycloak-dev/docker-compose.yaml` 이 이 폴더를 컨테이너에 연결 |
| 테스트배드 | 이 폴더의 `Dockerfile` 로 만든 이미지(공식 26.7.4 + 테마) `harbor.io:7443/mnt/infra/quay.io/keycloak/openkcloud-theme` 를 차트 이미지로 사용(`docs/포털_배포_가이드.md` 6.1). 테마를 바꾸면 새 태그로 빌드 |

## 구성

```
openkcloud/login/
├── theme.properties         base 를 이어받음, 화면 요소 → CSS 클래스 이름 연결
├── template.ftl             모든 로그인 계열 화면의 공통 틀 — base 26.7.4 복사본에 브랜드 영역·부제목만 더함
├── login.ftl                로그인 화면 — base 26.7.4 복사본(부제목, '로그인 상태 유지' 기본 체크, 아이콘 버튼)
├── messages/messages_ko.properties   바꾸는 문구만('사용자 이름' → '아이디' 등)
└── resources/
    ├── css/openkcloud.css   포털 색·크기(MNT-APP globals.css·components/ui 와 같은 값)
    ├── js/okc-address.js    포털 주소 밑(테스트배드)으로 열리면 주소창을 /auth/login/ 로 표시(요청은 그대로 Keycloak)
    ├── fonts/PretendardVariable.woff2   포털과 같은 글꼴(테스트배드는 폐쇄망이라 외부 글꼴 불가)
    └── img/favicon.ico      포털 아이콘
```

- 오류·만료·로그아웃 확인 같은 다른 화면은 base 템플릿을 그대로 쓰고, 공통 틀·CSS 로 같은 모양이 된다.
- '로그인 상태 유지'는 처음 열 때 체크(2026-10-14 결정 — 체크하지 않으면 브라우저를 껐다 켤 때 로그인이 풀림), 공용 PC 는 해제할 수 있다.
- 주소창 표시: 테스트배드는 Keycloak 이 포털 주소 밑(`/auth/realms/…`)이라 로그인 화면 주소를 `/auth/login/` 로 바꿔 보여 준다(같은 주소 안이라 가능,
  새로고침하면 포털 /auth/login 이 로그인을 다시 시작). 개발은 Keycloak 이 별도 주소라 그대로 둔다.
- **Keycloak 버전을 올릴 때**: `template.ftl`·`login.ftl` 은 base 복사본이라 새 버전 base 와 비교해 바뀐 부분을 옮긴다
  (`docker create quay.io/keycloak/keycloak:<버전>` → `/opt/keycloak/lib/lib/main/org.keycloak.keycloak-themes-<버전>.jar` 의 `theme/base/login/`).
  `Dockerfile` 의 `FROM` 은 Harbor 에 옮겨 둔 공식 이미지라, 새 버전 공식 이미지를 Harbor `mnt/infra/quay.io/keycloak/keycloak` 에 먼저 옮긴다.

## 이 PC 에서 미리 보기

공용 개발 Keycloak 을 건드리지 않고 같은 버전을 개발 모드로 띄워 본다(개발 모드는 테마를 캐시하지 않아 파일을 고치면 새로고침만 하면 됨).

```bash
# 저장소 루트에서
docker run -d --name okc-kc-theme -p 18080:8080 \
  -e KC_BOOTSTRAP_ADMIN_USERNAME=tmpadmin -e KC_BOOTSTRAP_ADMIN_PASSWORD='<임시 비밀번호>' \
  -v "$PWD/charts/infra/keycloak-theme/openkcloud:/opt/keycloak/themes/openkcloud:ro" \
  quay.io/keycloak/keycloak:26.7.4 start-dev
# realm 넣기(loginTheme 포함) + 시험 사용자
docker cp charts/infra/keycloak/realm/openkcloud_realm.json okc-kc-theme:/tmp/realm.json
K="docker exec okc-kc-theme /opt/keycloak/bin/kcadm.sh"
$K config credentials --server http://localhost:8080 --realm master --user tmpadmin --password '<임시 비밀번호>'
$K create realms -f /tmp/realm.json
$K create users -r openkcloud_realm -s username=theme-test -s firstName=테마시험 -s enabled=true
$K set-password -r openkcloud_realm --username theme-test --new-password '<임시 비밀번호>'
# 열기: http://localhost:18080/realms/openkcloud_realm/account   지우기: docker rm -f okc-kc-theme
```

## 글꼴

Pretendard 는 SIL Open Font License 1.1 글꼴이다(MNT-APP `public/fonts` 와 같은 파일).
