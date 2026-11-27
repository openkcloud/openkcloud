# ============================================================
# Dockerfile: kcloud-host-exec — operator 가 호스트에서 명령을 대신 실행하는 특권 Job 이미지
# 상세: nsenter 로 호스트 네임스페이스에 진입해 명령을 실행한다. sh/grep/tr(busybox) + nsenter
#       (util-linux) 만 필요하다. 소비처: NVIDIA MIG 관측·apply/rollback, ACPP 재부팅, NodeReboot, DRA CDI 활성화 init.
# 생성일: 2026-07-24 | 수정일: 2026-10-11
# ============================================================
FROM alpine:3.19
RUN apk add --no-cache util-linux
# Job 이 Command/Args 로 스크립트를 덮어쓰므로 ENTRYPOINT 는 기본 shell.
ENTRYPOINT ["/bin/sh"]
