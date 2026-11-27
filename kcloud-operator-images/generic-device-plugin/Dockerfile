# ============================================================
# Dockerfile: 일반 device-plugin 이미지
# 상세: 벤더 지식 0. 설정 파일과 호스트 /dev 만 있으면 동작한다.
# 생성일: 2026-08-11
# ============================================================
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/generic-device-plugin .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/generic-device-plugin /usr/bin/generic-device-plugin
ENTRYPOINT ["/usr/bin/generic-device-plugin"]
