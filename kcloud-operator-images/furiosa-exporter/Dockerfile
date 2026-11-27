# ============================================================
# Dockerfile: Furiosa exporter 이미지
# 상세: cgo 링크용 libfuriosa_smi.so 는 빌드 단계 벤더 apt 에서만 받는다. 재배포 금지 조건상
#       이미지에 담지 않고, 런타임엔 설치기가 호스트에 둔 같은 .so 를 hostPath 로 마운트한다.
# 생성일: 2026-08-07 | 수정일: 2026-10-11
# ============================================================
FROM golang:1.25 AS build
# 벤더 apt 저장소 등록 — driver-installer/furiosa/entrypoint.sh 의 rngd(anon) 분기와 같은 출처.
# jammy 채널을 쓰지만 furiosa-libsmi 는 libc 외 의존이 없어 Debian 빌더에서도 설치된다.
RUN apt-get update && apt-get install -y --no-install-recommends curl gnupg ca-certificates \
    && curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | gpg --dearmor > /etc/apt/trusted.gpg.d/furiosa-cloud.google.gpg \
    && echo 'deb [arch=amd64] http://asia-northeast3-apt.pkg.dev/projects/furiosa-ai jammy main' > /etc/apt/sources.list.d/furiosa.list \
    && apt-get update && apt-get install -y --no-install-recommends furiosa-libsmi \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# smi 태그가 빠지면 장치를 못 읽는 바이너리가 나온다. 태그는 필수다.
RUN CGO_ENABLED=1 go build -tags smi -o /out/furiosa-exporter .

FROM ubuntu:22.04
COPY --from=build /out/furiosa-exporter /usr/bin/furiosa-exporter
# libfuriosa_smi.so 는 여기 없다. 호스트 /usr/lib/x86_64-linux-gnu/libfuriosa_smi.so 마운트가 필수다.
EXPOSE 9410
ENTRYPOINT ["/usr/bin/furiosa-exporter"]
