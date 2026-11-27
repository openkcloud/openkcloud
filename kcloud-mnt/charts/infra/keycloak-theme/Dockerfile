# 테스트배드 Keycloak 이미지 — 공식 이미지(개발·테스트배드와 같은 26.7.4)에 로그인 테마 openkcloud 만 더한다.
# 기반 이미지는 Harbor 에 옮겨 둔 공식 이미지(quay.io/keycloak/keycloak:26.7.4 와 같음).
# 빌드·푸시(master3, 저장소 루트에서):
#   docker build --platform linux/amd64 -t harbor.io:7443/mnt/infra/quay.io/keycloak/openkcloud-theme:26.7.4-<git sha> charts/infra/keycloak-theme
#   docker push harbor.io:7443/mnt/infra/quay.io/keycloak/openkcloud-theme:26.7.4-<git sha>
FROM harbor.io:7443/mnt/infra/quay.io/keycloak/keycloak:26.7.4
COPY openkcloud /opt/keycloak/themes/openkcloud
