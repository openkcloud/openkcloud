# Dockerfile: install/rollback Job 스텁 이미지 — DRIVER_VERSION 과 무관하게 baked exit code 로 즉시 종료
# 상세: fault-injection 시나리오 02 의 실제 드라이버 설치 대신 쓰는 최소 이미지. CMD 오버라이드가
#       없어(RenderInstallJob/RenderRollbackJob) 이 CMD 가 유일한 동작이다. 같은 Job 이름을
#       재사용하는 상태기계라 태그(버전)별로 다른 이미지를 구워 성공/실패를 나눈다(0=최초
#       install 성공 흉내, 1=rollback 실패 흉내). tt-upgrade-transition/stub 과 같은 기법.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
FROM busybox:1.36
ARG STUB_EXIT_CODE=0
ENV STUB_EXIT_CODE=${STUB_EXIT_CODE}
CMD ["sh", "-c", "echo \"fault02-upgrade-stub: exiting ${STUB_EXIT_CODE} (DRIVER_VERSION=${DRIVER_VERSION} VENDOR=${VENDOR} RUN_MODE=${RUN_MODE})\"; exit ${STUB_EXIT_CODE}"]
