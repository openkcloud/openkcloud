# ============================================================
# Dockerfile: generic-dra-driver 이미지
# 상세: 벤더 중립 DRA kubeletplugin. CGO 불필요 — distroless 정적 바이너리.
# 생성일: 2026-08-11
# ============================================================
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/generic-dra-driver .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/generic-dra-driver /generic-dra-driver
USER 0
ENTRYPOINT ["/generic-dra-driver"]
