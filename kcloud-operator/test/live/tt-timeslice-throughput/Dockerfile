# ============================================================
# Dockerfile: tt-metalium 파생 이미지 — ttnn import 용 torch 추가(베이스 태그 인자화)
# 상세: 베이스 ttnn 이 torch 를 무조건 import 해 torch 없이는 import 가 실패한다. venv 에 pip
#       모듈이 없는 베이스는 시스템 pip 을 빌려 설치한다(배경은 README 의 "이미지와 버전 선택" 참고).
# 생성일: 2026-09-22 | 수정일: 2026-10-11
# ============================================================
ARG BASE_IMAGE=<레지스트리>:5100/kcloud/tenstorrent/tt-metalium:v0.58.0-rc4
FROM ${BASE_IMAGE}
RUN PYTHONPATH=/usr/lib/python3/dist-packages python3 -m pip install --no-cache-dir torch==2.2.1 --index-url https://download.pytorch.org/whl/cpu
