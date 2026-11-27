# ============================================================
# Dockerfile: Tenstorrent Blackhole device plugin 멀티스테이지 빌드
# 상세: golang:1.24-alpine 빌드 → scratch 최종 이미지 (CGO_ENABLED=0)
# 생성일: 2026-04-22 | 수정일: 2026-04-22
# ============================================================

# ── 1단계: 빌드 ──────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /workspace

# 의존성 캐시 레이어 분리
COPY go.mod go.sum ./
RUN go mod download

# 소스 복사 + 빌드
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o tenstorrent-device-plugin .

# ── 2단계: 최종 이미지 (scratch) ────────────────────────────
FROM scratch

# CA 인증서 (필요 시 주석 해제)
# COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /workspace/tenstorrent-device-plugin /tenstorrent-device-plugin

ENTRYPOINT ["/tenstorrent-device-plugin"]
