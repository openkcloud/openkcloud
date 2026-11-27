# VERSION defines the project version for the bundle.
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
VERSION ?= 0.0.1

# CHART_APP_VERSION 은 이미지에 박을 릴리스 버전이다. 정본은 Chart.yaml 의 appVersion
# 이고(release.yml 이 태그와 같은지 검사한다), apply-crds 가 이 값으로 CRD 의 릴리스
# 표식을 대조한다. release/k8s-1.28 브랜치에서는 같은 줄이 v0.6.2-k8s1.28 을 읽는다.
CHART_APP_VERSION := $(shell grep -oP '^appVersion:\s*\K\S+' deploy/helm/Chart.yaml)

# CHANNELS define the bundle channels used in the bundle.
# Add a new line here if you would like to change its default config. (E.g CHANNELS = "candidate,fast,stable")
# To re-generate a bundle for other specific channels without changing the standard setup, you can:
# - use the CHANNELS as arg of the bundle target (e.g make bundle CHANNELS=candidate,fast,stable)
# - use environment variables to overwrite this value (e.g export CHANNELS="candidate,fast,stable")
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif

# DEFAULT_CHANNEL defines the default channel used in the bundle.
# Add a new line here if you would like to change its default config. (E.g DEFAULT_CHANNEL = "stable")
# To re-generate a bundle for any other default channel without changing the default setup, you can:
# - use the DEFAULT_CHANNEL as arg of the bundle target (e.g make bundle DEFAULT_CHANNEL=stable)
# - use environment variables to overwrite this value (e.g export DEFAULT_CHANNEL="stable")
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

# IMAGE_TAG_BASE defines the docker.io namespace and part of the image name for remote images.
# This variable is used to construct full image tags for bundle and catalog images.
#
# For example, running 'make bundle-build bundle-push catalog-build catalog-push' will build and push both
# ai/kcloud-operator-bundle:$VERSION and ai/kcloud-operator-catalog:$VERSION.
IMAGE_TAG_BASE ?= ai/kcloud-operator

# BUNDLE_IMG defines the image:tag used for the bundle.
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= $(IMAGE_TAG_BASE)-bundle:v$(VERSION)

# BUNDLE_GEN_FLAGS are the flags passed to the operator-sdk generate bundle command
BUNDLE_GEN_FLAGS ?= -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)

# USE_IMAGE_DIGESTS defines if images are resolved via tags or digests
# You can enable this value if you would like to use SHA Based Digests
# To enable set flag to true
USE_IMAGE_DIGESTS ?= false
ifeq ($(USE_IMAGE_DIGESTS), true)
	BUNDLE_GEN_FLAGS += --use-image-digests
endif

# Set the Operator SDK version to use. By default, what is installed on the system is used.
# This is useful for CI or a project to utilize a specific version of the operator-sdk toolkit.
OPERATOR_SDK_VERSION ?= v1.41.1
# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories ('##@') and per-target descriptions ('##'). The awk
# command parses target:##description lines for pretty-printing.
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

# HELM_CRDS_DIR: 헬름 차트의 crds/ 디렉토리.
# 이 Makefile이 위치한 디렉토리($(MAKEFILE_DIR)) 기준으로 앵커링하여,
# 어느 디렉토리에서 `make -C` 또는 CI 상의 다른 cwd로 실행해도 동일 위치를 가리킴.
# (kcloud-operator는 자체 git repo이므로 git-toplevel로는 상위 operator repo에 도달 불가.)
MAKEFILE_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
HELM_CRDS_DIR ?= $(MAKEFILE_DIR)/deploy/helm/crds

.PHONY: embed-crds
embed-crds: ## Copy generated CRDs into internal/crdapply/crd/ for go:embed bundling.
	@mkdir -p internal/crdapply/crd
	@cp config/crd/bases/*.yaml internal/crdapply/crd/

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	$(MAKE) embed-crds
	$(MAKE) sync-helm-crds

.PHONY: capmatrix
capmatrix: ## Generate compatibility matrix into the docs repo (../docs/features/compatibility_matrix.md).
	go run ./cmd/capmatrix -out $(MAKEFILE_DIR)/../docs/features/compatibility_matrix.md

.PHONY: capmatrix-check
capmatrix-check: ## Verify the compatibility matrix is up to date (로컬 전용 — CI 환경엔 docs 저장소가 없다).
	go run ./cmd/capmatrix | cmp - $(MAKEFILE_DIR)/../docs/features/compatibility_matrix.md

.PHONY: private-ip-check
private-ip-check: ## Verify no internal addresses or accounts are in tracked files.
	bash hack/private-ip-check.sh --self-test
	bash hack/private-ip-check.sh

.PHONY: docs-path-check
docs-path-check: ## Verify no sibling docs/ paths leak into code, charts, or scripts.
	bash hack/docs-path-check.sh --self-test
	bash hack/docs-path-check.sh

.PHONY: version-check
version-check: ## Verify docs release-set matches Chart.yaml (로컬 전용 — CI 엔 docs 저장소가 없다).
	bash hack/version-check.sh

.PHONY: manifest-check
manifest-check: ## Verify images.yaml kcloud images are all in image-manifest.yaml, and vice versa (로컬 전용 — CI 엔 images 저장소가 없어 values.yaml 폴백만 검사한다. 역방향 미등재는 기본 경고, CHECK_REVERSE_COVERAGE=1 로 실패로 올린다).
	bash test/airgap/manifest-coverage.sh

.PHONY: digest-check
digest-check: ## Verify image-manifest.yaml digests match the registry (레지스트리 조회가 필요해 CI 게이트엔 넣지 않는다).
	bash hack/refresh-image-digests.sh --check

.PHONY: image-tag-check
image-tag-check: ## 차트가 당기는 이미지 태그의 레지스트리 실재 확인 (레지스트리 조회가 필요해 기본 게이트엔 꺼둔다. CHECK_REGISTRY=1 로 켠다, release-prepare·CI 는 항상 켠다).
	@if [ "$${CHECK_REGISTRY:-0}" = "1" ]; then \
		bash hack/image-tag-check.sh; \
	else \
		echo "image-tag-check: SKIP(CHECK_REGISTRY=1 로 켠다 — 미실행이며 통과 아님)"; \
	fi

.PHONY: release-prepare
release-prepare: ## 발행 준비 — 선행 조건·정합 검사와 push 명령 출력(기본 dry-run, 로컬 태그 생성은 --apply, 형제 images 저장소가 필요해 로컬 전용).
	bash hack/release-prepare.sh

.PHONY: crd-check
crd-check: ## Verify config/crd/kustomization.yaml lists every CRD in config/crd/bases.
	bash hack/crd-kustomize-check.sh

.PHONY: doc-cite-check
doc-cite-check: ## 주석·values·워크플로가 가리키는 docs/*.md 경로의 실재 검사(스크립트 자가시험 먼저)
	bash hack/doc-cite-check.sh --self-test
	bash hack/doc-cite-check.sh

.PHONY: workspace-cite-check
workspace-cite-check: ## 추적 파일이 커밋되지 않는 계획 경로나 보고서 파일명을 인용하면 거부(스크립트 자가시험 먼저)
	bash hack/workspace-cite-check.sh --self-test
	bash hack/workspace-cite-check.sh

.PHONY: notices
notices: embed-crds ## 서드파티 고지문 THIRD_PARTY_NOTICES.txt 재생성(모듈 캐시를 읽는다)
	bash hack/third-party-notices.sh --write THIRD_PARTY_NOTICES.txt .:./cmd

.PHONY: notices-check
notices-check: embed-crds ## 고지문이 현재 의존성과 같은지 확인
	bash hack/third-party-notices.sh --check THIRD_PARTY_NOTICES.txt .:./cmd

.PHONY: comment-check
comment-check: ## 주석·문서의 이력 서술·세션 식별자·날짜·긴 블록 거부(스크립트 자가시험 먼저)
	bash hack/comment-check.sh --self-test
	bash hack/comment-check.sh

.PHONY: comment-only-check
comment-only-check: ## BASE..HEAD 가 주석·문서만 바꿨는지 확인 (make comment-only-check BASE=<커밋>)
	bash hack/comment-only-check.sh --self-test
	bash hack/comment-only-check.sh $(BASE)

.PHONY: conformance
conformance: ## Verify every registered vendor satisfies the onboarding contract (test/conformance + internal/controller).
	go test ./test/conformance/... -v
	go test ./internal/controller/... -run 'TestIsAcceleratorNodeRecognizesEveryVendorPresentLabel|TestAcceleratorOperationVendorsMatchesCRDEnum' -count=1 -v

# footprint 회귀 게이트 — test/footprint/measure.sh 로 kcloud 차트 정적 렌더를 재고
# baseline.json 과 상주 operand(Deployment+DaemonSet+StatefulSet) 수를 대조한다.
# 라이브 모드는 클러스터가 필요해 CI 에서는 static 만 돌린다.
.PHONY: footprint
footprint: ## Static K8s footprint of the kcloud chart (JSON) — test/footprint
	OUT=$(CURDIR)/footprint.json bash test/footprint/measure.sh -m static --charts kcloud

.PHONY: footprint-check
footprint-check: footprint ## Fail if long-running operands grew vs test/footprint/baseline.json
	jq -e --slurpfile b test/footprint/baseline.json '(.targets.kcloud.static|type)=="object" and ($$b[0].targets.kcloud.static|type)=="object"' footprint.json >/dev/null \
	  || (echo "footprint 측정 없음(static null) — helm template 실패 여부 확인" && exit 1)
	jq -e --slurpfile b test/footprint/baseline.json '.targets.kcloud.static as $$s | $$b[0].targets.kcloud.static as $$base | ($$s.Deployment+$$s.DaemonSet+$$s.StatefulSet) <= ($$base.Deployment+$$base.DaemonSet+$$base.StatefulSet)' footprint.json \
	  || (echo "footprint 회귀: 상주 operand 수가 baseline 을 넘었다 — 의도면 baseline.json 갱신" && exit 1)

.PHONY: sync-helm-crds
sync-helm-crds: ## Sync generated CRDs to helm chart crds/ directory.
	@mkdir -p $(HELM_CRDS_DIR)
	@cp config/crd/bases/*.yaml $(HELM_CRDS_DIR)/
	@echo "Helm CRDs synchronized to $(HELM_CRDS_DIR)"

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" KCLOUD_EXPECT_K8S_MINOR="$(ENVTEST_K8S_VERSION)" go test -p 1 $$(go list ./... | grep -v /e2e) -coverprofile cover.out
# cmd/kubectl-npu 는 자체 go.mod 를 가진 중첩 모듈이라 위의 go list ./... 에 잡히지 않는다.
# 따로 부르지 않으면 그 안의 시험은 있는데 한 번도 돌지 않는다.
	cd cmd/kubectl-npu && go test ./...

.PHONY: fault-test
fault-test: manifests generate setup-envtest ## Run the fault-injection harness only (test/fault).
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./test/fault/ -v

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= kcloud-operator-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND_CLUSTER=$(KIND_CLUSTER) go test ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter (root module + cmd/kubectl-npu plugin module)
	$(GOLANGCI_LINT) run
	cd cmd/kubectl-npu && $(GOLANGCI_LINT) run ./...

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: embed-crds ## Build docker image with the manager.
	$(CONTAINER_TOOL) build $(DOCKER_BUILD_ARGS) --build-arg GIT_COMMIT=$(shell git rev-parse --short HEAD) --build-arg RELEASE_VERSION=$(CHART_APP_VERSION) -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name kcloud-operator-builder
	$(CONTAINER_TOOL) buildx use kcloud-operator-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) \
		--build-arg GIT_COMMIT=$(shell git rev-parse --short HEAD) \
		--build-arg RELEASE_VERSION=$(CHART_APP_VERSION) \
		--tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm kcloud-operator-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default > dist/install.yaml

.PHONY: disk-check
disk-check: ## 빌드 호스트 디스크·docker 사용량 확인(여유 20% 미만이면 실패)
	bash hack/build-host-disk-check.sh

.PHONY: helm-option-check
helm-option-check: ## helm upgrade 의 --reuse-values 단독 사용을 금지한다
	bash hack/helm-values-option-check.sh

.PHONY: chart-default-check
chart-default-check: ## 차트 기본값만으로 드라이버 설치가 가능한 조합인지 검사
	bash hack/chart-default-installable.sh

.PHONY: vendor-branch-check
vendor-branch-check: ## 컨트롤러의 벤더 조건 분기 수가 상한을 넘으면 실패
	bash hack/vendor-branch-count.sh

KEEP ?= 3
.PHONY: image-gc
image-gc: ## 로컬 빌드 이미지 저장소별 최신 3개만 남기고 정리(KEEP=N)
	bash hack/local-image-gc.sh --keep $(KEEP)

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.6.0
CONTROLLER_TOOLS_VERSION ?= v0.18.0
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.1.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef

.PHONY: operator-sdk
OPERATOR_SDK ?= $(LOCALBIN)/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
ifeq (,$(wildcard $(OPERATOR_SDK)))
ifeq (, $(shell which operator-sdk 2>/dev/null))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPERATOR_SDK)) ;\
	OS=$(shell go env GOOS) && ARCH=$(shell go env GOARCH) && \
	curl -sSLo $(OPERATOR_SDK) https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk_$${OS}_$${ARCH} ;\
	chmod +x $(OPERATOR_SDK) ;\
	}
else
OPERATOR_SDK = $(shell which operator-sdk)
endif
endif

.PHONY: bundle
bundle: manifests kustomize operator-sdk ## Generate bundle manifests and metadata, then validate generated files.
	$(OPERATOR_SDK) generate kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | $(OPERATOR_SDK) generate bundle $(BUNDLE_GEN_FLAGS)
	$(OPERATOR_SDK) bundle validate ./bundle

.PHONY: bundle-build
bundle-build: ## Build the bundle image.
	$(CONTAINER_TOOL) build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push: ## Push the bundle image.
	$(MAKE) docker-push IMG=$(BUNDLE_IMG)

.PHONY: opm
OPM = $(LOCALBIN)/opm
opm: ## Download opm locally if necessary.
ifeq (,$(wildcard $(OPM)))
ifeq (,$(shell which opm 2>/dev/null))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPM)) ;\
	OS=$(shell go env GOOS) && ARCH=$(shell go env GOARCH) && \
	curl -sSLo $(OPM) https://github.com/operator-framework/operator-registry/releases/download/v1.55.0/$${OS}-$${ARCH}-opm ;\
	chmod +x $(OPM) ;\
	}
else
OPM = $(shell which opm)
endif
endif

# A comma-separated list of bundle images (e.g. make catalog-build BUNDLE_IMGS=example.com/operator-bundle:v0.1.0,example.com/operator-bundle:v0.2.0).
# These images MUST exist in a registry and be pull-able.
BUNDLE_IMGS ?= $(BUNDLE_IMG)

# The image tag given to the resulting catalog image (e.g. make catalog-build CATALOG_IMG=example.com/operator-catalog:v0.2.0).
CATALOG_IMG ?= $(IMAGE_TAG_BASE)-catalog:v$(VERSION)

# Set CATALOG_BASE_IMG to an existing catalog image tag to add $BUNDLE_IMGS to that image.
ifneq ($(origin CATALOG_BASE_IMG), undefined)
FROM_INDEX_OPT := --from-index $(CATALOG_BASE_IMG)
endif

# Build a catalog image by adding bundle images to an empty catalog using the operator package manager tool, 'opm'.
# This recipe invokes 'opm' in 'semver' bundle add mode. For more information on add modes, see:
# https://github.com/operator-framework/community-operators/blob/7f1438c/docs/packaging-operator.md#updating-your-existing-operator
.PHONY: catalog-build
catalog-build: opm ## Build a catalog image.
	$(OPM) index add --container-tool $(CONTAINER_TOOL) --mode semver --tag $(CATALOG_IMG) --bundles $(BUNDLE_IMGS) $(FROM_INDEX_OPT)

# Push the catalog image.
.PHONY: catalog-push
catalog-push: ## Push a catalog image.
	$(MAKE) docker-push IMG=$(CATALOG_IMG)
