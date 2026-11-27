# node-device-detector(= kcloud node-agent) 멀티파일 빌드: scan/report/validate/main 등 *.go 전체 COPY.
# /podresources-writer 심볼릭 링크: 구 이미지에는 없는 경로라, 혼용되면 사이드카만 시작 실패하고 detector 본체는 영향받지 않는다.
# 링크는 디렉터리째 COPY 해야 보존된다 — 파일 하나만 COPY 하면 링크를 따라가 복사본이 된다.
FROM golang:1.22 as build
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
RUN go mod download
RUN mkdir -p /out && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/detector . && ln -s detector /out/podresources-writer

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/ /
USER 65532
ENTRYPOINT ["/detector"]
