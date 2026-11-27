# Build the manager binary
FROM golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG GIT_COMMIT=unknown
# RELEASE_VERSION: apply-crds 가 CRD 에 박는 릴리스 표식의 출처.
# 정본은 deploy/helm/Chart.yaml 의 appVersion 이고 Makefile 의 docker-build 가 넘긴다.
# 비어 있으면 표식을 박지 않는다(빈 값을 박으면 다음 적용이 그것을 라인으로 읽는다).
ARG RELEASE_VERSION=""

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/
# pkg/ 는 관리 API(internal/apiserver)가 재사용하는 npuctl 등 공유 패키지 — main.go 빌드에 필요.
COPY pkg/ pkg/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -ldflags "-X main.buildCommit=${GIT_COMMIT} -X main.releaseVersion=${RELEASE_VERSION}" -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
# GHCR 가 패키지를 저장소에 연결할 때 보는 라벨. 개인 fork 리허설은 SOURCE_URL 로 덮는다.
ARG SOURCE_URL="https://github.com/openkcloud/kcloud-operator"
LABEL org.opencontainers.image.source="${SOURCE_URL}"
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
