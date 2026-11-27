# Dockerfile: install-Job 스텁 이미지 — DRIVER_VERSION 과 무관하게 baked exit code 로 즉시 종료
# 상세: RenderInstallJob/RenderRollbackJob 은 Command 를 오버라이드하지 않으므로 이 이미지의
#       CMD 가 유일한 동작이다. STUB_EXIT_CODE 를 빌드 시 굽어 태그(버전)별로 성공/실패를 분리한다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
FROM busybox:1.36
ARG STUB_EXIT_CODE=0
ENV STUB_EXIT_CODE=${STUB_EXIT_CODE}
CMD ["sh", "-c", "echo \"tt-upgrade-stub: exiting ${STUB_EXIT_CODE} (DRIVER_VERSION=${DRIVER_VERSION} VENDOR=${VENDOR} RUN_MODE=${RUN_MODE})\"; exit ${STUB_EXIT_CODE}"]
