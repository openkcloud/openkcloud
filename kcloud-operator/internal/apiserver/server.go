// ============================================================
// server.go: 관리 REST API 서버
// 상세: operator pod 내 별도 HTTPS listener(:9444). pkg/npuctl 를 감싸 read·control 을 낸다 —
//       control 은 CR patch 로 귀결한다(1단계 계약 유지). 인증=TokenReview, 인가=SAR.
// 생성일: 2026-07-20 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/pkg/npuctl"
)

// apiGroup: SubjectAccessReview 인가에 사용하는 리소스 그룹(CRD group).
const apiGroup = "npu.ai"

// events 는 core 그룹이라 operator 의 기존 npu.ai/apps 권한으로는 읽을 수 없다 — handleEvents
// (events.go) 가 실제로 list 하므로 여기서 읽기 권한을 선언한다. 이 마커만으로는 배포되지
// 않는다 — config/rbac/role.yaml 재생성 + deploy/helm/templates/rbac.yaml 수기 반영까지
// 3계층을 모두 맞춰야 한다.
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch

// knownVendors: control 요청에서 허용하는 vendor 집합(npuctl 상수와 1:1).
var knownVendors = map[string]bool{
	npuctl.VendorNvidia:      true,
	npuctl.VendorFuriosa:     true,
	npuctl.VendorRngd:        true,
	npuctl.VendorRebellions:  true,
	npuctl.VendorTenstorrent: true,
}

// Server 는 관리 REST API 를 서빙합니다. Authn 은 TokenReview/SAR 용 clientset,
// Ctl 은 상태 집약·CR patch 를 수행하는 npuctl.Client 입니다.
type Server struct {
	Addr     string // 예: ":9444"
	CertFile string // TLS 인증서(파일). 비면 평문 HTTP(테스트/개발 전용).
	KeyFile  string // TLS 키(파일).
	Authn    kubernetes.Interface
	Ctl      *npuctl.Client
	// Reader 는 신규 조회 엔드포인트(accelerators/capabilities/classes/policies/workloads/events)가
	// 쓰는 직접 읽기 경로다. 매니저 캐시가 아니라 API 서버를 직접 읽는다 — 사람이 5초 주기로
	// 새로고침하는 관리 화면이라 캐시 이득이 없고, Event 를 캐시에 태우면 클러스터 전체 이벤트를
	// watch 하게 되어 operator 메모리만 늘어난다.
	Reader client.Reader
	Log    logr.Logger
}

// NeedLeaderElection 은 false 를 반환하여 API 서버가 리더 여부와 무관하게 서빙되게 합니다
// (읽기·CR patch 는 어느 replica 에서 수행해도 동일 계약).
func (s *Server) NeedLeaderElection() bool { return false }

// Start 는 manager.Runnable 진입점입니다. ctx 취소 시 graceful shutdown 합니다.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if s.CertFile != "" {
			s.Log.Info("management API server listening (HTTPS)", "addr", s.Addr)
			errCh <- srv.ListenAndServeTLS(s.CertFile, s.KeyFile)
			return
		}
		s.Log.Info("management API server listening (HTTP, no cert)", "addr", s.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Handler 는 라우팅된 http.Handler 를 반환합니다(TLS 없이 테스트에서 직접 소비 가능).
// Go 1.22+ 메서드/와일드카드 패턴 라우팅 사용.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	nodeGate := authzPair{Group: "", Verb: "list", Resource: "nodes"}
	acppGate := npuPair("list", "acceleratorpartitionpolicies")
	ndrGate := npuPair("list", "nodedevicereports")
	classGate := npuPair("list", "acceleratorclasses")
	podsGate := authzPair{Group: "", Verb: "list", Resource: "pods"}
	// 인벤토리 라우트 셋은 건강 판정(acceleratorhealths)·할당량(pods)·DRA 집계(resourceclaims)까지
	// 읽는다(loadInventory 의 attachDRAAllocation). capabilities 는 그것들을 읽지 않으므로
	// 게이트를 나눈다 — 게이트는 라우트가 실제로 읽는 것만 요구한다.
	// capabilities·accelerators 는 둘 다 intent.Load 를 거치고, 그 안의 LoadDRACapability 가
	// DeviceClass·ResourceSlice 를 읽으므로 그 둘의 list 인가를 capGates 에 싣는다 — invGates 가
	// capGates 를 포함해 가속기 라우트에도 그대로 전파된다.
	capGates := []authzPair{nodeGate, acppGate, ndrGate,
		{Group: "resource.k8s.io", Verb: "list", Resource: "resourceslices"},
		{Group: "resource.k8s.io", Verb: "list", Resource: "deviceclasses"},
	}
	invGates := append(append([]authzPair{}, capGates...), npuPair("list", "acceleratorhealths"), podsGate,
		authzPair{Group: "resource.k8s.io", Verb: "list", Resource: "resourceclaims"})
	// read: npu.ai 리소스 list 권한 + core 그룹 nodes 권한. CollectStatus 가 corev1.NodeList 를
	// 읽어 node.status.allocatable 전량을 응답에 싣기 때문이다(pkg/npuctl/types.go 의 Allocatable
	// 주석 — 벤더 리소스명이 설정마다 달라 의도적으로 필터하지 않는다). 필터링이 아니라 인가로
	// 닫는다: CLI(kubectl-npu)는 kubeconfig RBAC 로 이미 인가받고, --json 출력의 byte-identical
	// 성질을 깨지 않아야 한다.
	mux.HandleFunc("GET /api/v1/status", s.authzAll([]authzPair{nodeGate, npuPair("list", "driverinstallpolicies")}, s.handleStatus))
	mux.HandleFunc("GET /api/v1/nodes/{node}", s.authzAll([]authzPair{nodeGate, npuPair("list", "driverinstallpolicies")}, s.handleNode))
	// control: upgrade/rollback 은 DIP patch, toggle 은 NCP patch 권한 요구.
	mux.HandleFunc("POST /api/v1/vendors/{vendor}/upgrade", s.authz("patch", "driverinstallpolicies", s.handleUpgrade))
	mux.HandleFunc("POST /api/v1/vendors/{vendor}/rollback", s.authz("patch", "driverinstallpolicies", s.handleUpgrade))
	mux.HandleFunc("POST /api/v1/vendors/{vendor}/toggle", s.authz("patch", "npuclusterpolicies", s.handleToggle))
	// 인벤토리 조회: 미관리 노드는 NodeDeviceReport 만으로 채워지므로 acceleratorpartitionpolicies
	// 인가만으론 부족하다 — nodedevicereports 인가도 함께 요구한다. 응답은 node 이름·cordon
	// 상태·advertised 수량도 함께 내므로(corev1.Node 직접 읽음) core 그룹 nodes list 인가도
	// 요구한다 — capGates 를 공유하는 accelerators 3개·capabilities 까지 4라우트의 공통 요구다
	// (vendors/capabilities·preview 는 별도 게이트로 같은 인가를 요구한다). loadInventory 가
	// attachDRAAllocation 용 ResourceClaim 도 읽으므로 resourceclaims 인가도 invGates 에 포함한다.
	mux.HandleFunc("GET /api/v1/accelerators", s.authzAll(invGates, s.handleAccelerators))
	mux.HandleFunc("GET /api/v1/accelerators/{uid...}", s.authzAll(invGates, s.handleAcceleratorByUID))
	mux.HandleFunc("GET /api/v1/nodes/{node}/accelerators", s.authzAll(invGates, s.handleNodeAccelerators))
	// capability 조회: memoryMiB/sharingReplicas 는 NDR 로 되짚은 값이라(intent.applyObservedSharing)
	// accelerators 와 같은 이유로 nodedevicereports 인가도, node 파생 필드(schedulable/advertised) 때문에
	// nodes 인가도 함께 요구한다.
	mux.HandleFunc("GET /api/v1/capabilities", s.authzAll(capGates, s.handleCapabilities))
	// 벤더×능력 선언·검증 뷰: evidence·ACPP·NDR·검증 정책·nodes 를 읽으므로 다섯 다 인가한다
	// (검증 정책은 evidence 신선도 판정의 감시축을 정한다 — CollectVendorCapabilitiesWith).
	mux.HandleFunc("GET /api/v1/vendors/capabilities", s.authzAll([]authzPair{
		npuPair("list", "acceleratorevidences"),
		npuPair("list", "acceleratorpartitionpolicies"),
		npuPair("list", "nodedevicereports"),
		npuPair("list", "acceleratorverificationpolicies"),
		nodeGate,
	}, s.handleVendorCapabilities))
	// CR 투영: 각 라우트는 자기 응답이 실제로 읽는 CR 종류의 list 권한만 요구한다
	// (조인이 없으므로 accelerators/capabilities 처럼 이중 게이트가 필요 없다).
	mux.HandleFunc("GET /api/v1/classes", s.authz("list", "acceleratorclasses", s.handleClasses))
	mux.HandleFunc("GET /api/v1/policies", s.authz("list", "acceleratorpartitionpolicies", s.handlePolicies))
	mux.HandleFunc("GET /api/v1/workloads", s.authz("list", "acceleratorworkloads", s.handleWorkloads))
	// 소비자 목록은 Pod·ResourceClaim·NodeDeviceReport 세 리소스를 읽으므로 세 게이트를 모두 요구한다.
	// resourceclaims 는 클러스터에 없을 수도 있지만(1.28 라인) SAR 은 RBAC 만 보므로 게이트는 선다.
	mux.HandleFunc("GET /api/v1/consumers", s.authzAll([]authzPair{
		podsGate,
		{Group: "resource.k8s.io", Verb: "list", Resource: "resourceclaims"},
		ndrGate,
	}, s.handleConsumers))
	// core 그룹 리소스이므로 그룹을 명시한다 — npu.ai 로 물으면 SAR 이 항상 거부한다.
	mux.HandleFunc("GET /api/v1/events", s.authzGroup("", "list", "events", s.handleEvents))
	// preview 는 POST 지만 아무것도 쓰지 않는다 — 그래서 읽기 등급(list) 권한을 요구한다.
	// intent.Load(ACPP+NDR+Node+DeviceClass+ResourceSlice)와 클래스 조회를 그대로 수행하므로,
	// 응답이 실제로 읽는 여섯 리소스 전부를 인가한다("읽는 리소스는 전부 인가한다" 규칙 —
	// acceleratorworkloads 자체는 읽지 않으므로 요구하지 않는다. nodes·resourceslices·
	// deviceclasses 는 core/resource.k8s.io 그룹이라 별도 게이트가 필요하다).
	mux.HandleFunc("POST /api/v1/preview", s.authzAll([]authzPair{nodeGate, acppGate, ndrGate, classGate,
		{Group: "resource.k8s.io", Verb: "list", Resource: "resourceslices"},
		{Group: "resource.k8s.io", Verb: "list", Resource: "deviceclasses"},
	}, s.handlePreview))
	// 정책 쓰기: 생성은 create, 삭제는 delete 인가를 요구한다. 수정(PUT)은 만들지 않는다 —
	// 상태기계가 중간 단계에 있는 채로 spec 이 바뀌면 되돌릴 기준이 흐려진다.
	// dryRun=true 면 같은 핸들러가 client.DryRunAll 로 webhook 만 돌린다(인가는 동일).
	mux.HandleFunc("POST /api/v1/policies",
		s.authz("create", "acceleratorpartitionpolicies", s.handleCreatePolicy))
	mux.HandleFunc("DELETE /api/v1/policies/{name}",
		s.authz("delete", "acceleratorpartitionpolicies", s.handleDeletePolicy))
	// 배포 화면: AcceleratorWorkload 생성·삭제. 거절은 정책 라우트와 같은 200+rejection 계약.
	mux.HandleFunc("POST /api/v1/workloads", s.authzNamespaced("create", "acceleratorworkloads", postBodyNamespace, s.handleCreateWorkload))
	mux.HandleFunc("DELETE /api/v1/workloads/{namespace}/{name}", s.authzNamespaced("delete", "acceleratorworkloads", deleteWorkloadNamespace, s.handleDeleteWorkload))
	// 정적 대시보드. "{$}" 는 정확히 그 경로만 매칭하므로 /api/* 를 가리지 않는다.
	mux.HandleFunc("GET /ui/{$}", s.handleUI)
	mux.HandleFunc("GET /{$}", s.handleUI)
	return mux
}

// ctxUserKey: 인증된 주체(username)를 request context 로 전달하는 키.
type ctxUserKey struct{}

// authz 는 npu.ai 그룹 리소스에 대한 인가다(기존 계약 그대로).
func (s *Server) authz(verb, resource string, next http.HandlerFunc) http.HandlerFunc {
	return s.authzGroup(apiGroup, verb, resource, next)
}

// authzPair 는 한 요청이 통과해야 하는 인가 한 건이다.
type authzPair struct {
	Group    string // CRD 그룹(npu.ai) 또는 core 그룹("")
	Verb     string
	Resource string
}

// npuPair 는 npu.ai 그룹 인가 한 건을 만드는 축약이다. verb 는 오늘 모든 호출에서 "list" 다 —
// 정본 helper 라 향후 patch 게이트를 authzAll 로 옮길 때 그대로 재사용하도록
// verb 를 고정하지 않는다.
//
//nolint:unparam
func npuPair(verb, resource string) authzPair {
	return authzPair{Group: apiGroup, Verb: verb, Resource: resource}
}

// authzAll 은 TokenReview 를 한 번만 돌고 pairs 를 순서대로 SAR 한다. 중첩 authzGroup 과
// 동작이 같지만(첫 실패가 403 을 결정) 토큰 검증 왕복이 게이트 수에 비례하지 않는다.
// pairs 순서는 403 메시지에 이름이 나오는 리소스를 결정하므로 기존 중첩 순서를 유지한다.
func (s *Server) authzAll(pairs []authzPair, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		tr, err := s.Authn.AuthenticationV1().TokenReviews().Create(r.Context(),
			&authnv1.TokenReview{Spec: authnv1.TokenReviewSpec{Token: token}}, metav1.CreateOptions{})
		if err != nil {
			s.Log.Error(err, "TokenReview 실패")
			writeError(w, http.StatusInternalServerError, "token review failed")
			return
		}
		if !tr.Status.Authenticated {
			writeError(w, http.StatusUnauthorized, "token not authenticated")
			return
		}
		user := tr.Status.User
		for _, p := range pairs {
			sar, err := s.Authn.AuthorizationV1().SubjectAccessReviews().Create(r.Context(),
				&authzv1.SubjectAccessReview{Spec: authzv1.SubjectAccessReviewSpec{
					User:   user.Username,
					UID:    user.UID,
					Groups: user.Groups,
					ResourceAttributes: &authzv1.ResourceAttributes{
						Group: p.Group, Resource: p.Resource, Verb: p.Verb,
					},
				}}, metav1.CreateOptions{})
			if err != nil {
				s.Log.Error(err, "SubjectAccessReview 실패")
				writeError(w, http.StatusInternalServerError, "authorization review failed")
				return
			}
			if !sar.Status.Allowed {
				writeError(w, http.StatusForbidden,
					fmt.Sprintf("user %q not allowed to %s %s", user.Username, p.Verb, qualifiedResource(p.Group, p.Resource)))
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, user.Username)))
	}
}

// authzGroup 은 Bearer 토큰을 TokenReview 로 인증하고 SubjectAccessReview 로 인가한 뒤 next 를
// 호출합니다. group 은 CRD 그룹(npu.ai) 또는 core 그룹("") 입니다.
// 무토큰 401 / 인증 실패 401 / 무권한 403 / 리뷰 API 오류 500.
func (s *Server) authzGroup(group, verb, resource string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}

		tr, err := s.Authn.AuthenticationV1().TokenReviews().Create(r.Context(),
			&authnv1.TokenReview{Spec: authnv1.TokenReviewSpec{Token: token}}, metav1.CreateOptions{})
		if err != nil {
			s.Log.Error(err, "TokenReview 실패")
			writeError(w, http.StatusInternalServerError, "token review failed")
			return
		}
		if !tr.Status.Authenticated {
			writeError(w, http.StatusUnauthorized, "token not authenticated")
			return
		}
		user := tr.Status.User

		sar, err := s.Authn.AuthorizationV1().SubjectAccessReviews().Create(r.Context(),
			&authzv1.SubjectAccessReview{Spec: authzv1.SubjectAccessReviewSpec{
				User:   user.Username,
				UID:    user.UID,
				Groups: user.Groups,
				ResourceAttributes: &authzv1.ResourceAttributes{
					Group: group, Resource: resource, Verb: verb,
				},
			}}, metav1.CreateOptions{})
		if err != nil {
			s.Log.Error(err, "SubjectAccessReview 실패")
			writeError(w, http.StatusInternalServerError, "authorization review failed")
			return
		}
		if !sar.Status.Allowed {
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("user %q not allowed to %s %s", user.Username, verb, qualifiedResource(group, resource)))
			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, user.Username)))
	}
}

// authzNamespaced 는 namespaced 리소스의 쓰기 인가다 — SubjectAccessReview 에 요청 네임스페이스를 실어
// RoleBinding 으로 한 네임스페이스만 받은 호출자도 허용되고, 그 밖 네임스페이스는 거부된다.
// nsOf 가 빈 문자열을 돌려주면 400 으로 끝낸다(네임스페이스 없는 SAR 은 "모든 네임스페이스" 라 쓰지 않는다).
func (s *Server) authzNamespaced(verb, resource string, nsOf func(*http.Request) (string, error), next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		tr, err := s.Authn.AuthenticationV1().TokenReviews().Create(r.Context(),
			&authnv1.TokenReview{Spec: authnv1.TokenReviewSpec{Token: token}}, metav1.CreateOptions{})
		if err != nil {
			s.Log.Error(err, "TokenReview 실패")
			writeError(w, http.StatusInternalServerError, "token review failed")
			return
		}
		if !tr.Status.Authenticated {
			writeError(w, http.StatusUnauthorized, "token not authenticated")
			return
		}
		user := tr.Status.User

		ns, err := nsOf(r)
		if err != nil || ns == "" {
			msg := "namespace is required"
			if err != nil {
				msg = err.Error()
			}
			writeJSON(w, http.StatusBadRequest, CreateWorkloadResponse{Rejection: &RejectionView{Reason: reasonBadRequest, Message: msg}})
			return
		}

		sar, err := s.Authn.AuthorizationV1().SubjectAccessReviews().Create(r.Context(),
			&authzv1.SubjectAccessReview{Spec: authzv1.SubjectAccessReviewSpec{
				User:   user.Username,
				UID:    user.UID,
				Groups: user.Groups,
				ResourceAttributes: &authzv1.ResourceAttributes{
					Group: apiGroup, Resource: resource, Verb: verb, Namespace: ns,
				},
			}}, metav1.CreateOptions{})
		if err != nil {
			s.Log.Error(err, "SubjectAccessReview 실패")
			writeError(w, http.StatusInternalServerError, "authorization review failed")
			return
		}
		if !sar.Status.Allowed {
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("user %q not allowed to %s %s in namespace %q", user.Username, verb, qualifiedResource(apiGroup, resource), ns))
			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, user.Username)))
	}
}

// postBodyNamespace 는 workloads 생성 요청 본문에서 namespace 만 먼저 디코드해 인가에 쓴다.
// 본문을 1MiB 상한으로 전부 읽은 뒤 되돌려 놓아(io.NopCloser) handleCreateWorkload 가 다시
// 전체를 디코드할 수 있게 한다.
func postBodyNamespace(r *http.Request) (string, error) {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return "", err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	var body struct {
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return "", err
	}
	if body.Namespace == "" {
		return "", errors.New("namespace is required")
	}
	return body.Namespace, nil
}

// deleteWorkloadNamespace 는 삭제 라우트의 경로 세그먼트에서 namespace 를 읽는다.
func deleteWorkloadNamespace(r *http.Request) (string, error) {
	return r.PathValue("namespace"), nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.Ctl.CollectStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONETag(w, r, http.StatusOK, status)
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("node")
	status, err := s.Ctl.CollectStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range status.Nodes {
		if status.Nodes[i].Name == name {
			writeJSON(w, http.StatusOK, status.Nodes[i])
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", name))
}

// controlRequest 는 upgrade/rollback/toggle 요청 본문입니다.
type controlRequest struct {
	Version string `json:"version,omitempty"`
	Model   string `json:"model,omitempty"`
	Auto    bool   `json:"auto,omitempty"`
	Force   bool   `json:"force,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// handleUpgrade 는 upgrade/rollback 을 처리합니다(둘 다 DIP.spec.driver.version patch 로 귀결).
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	vendor := r.PathValue("vendor")
	if !knownVendors[vendor] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown vendor %q", vendor))
		return
	}
	req, ok := decodeBody(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Version) == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}

	result, err := s.Ctl.UpgradeVendor(r.Context(), vendor, req.Version,
		npuctl.UpgradeOptions{Model: req.Model, Auto: req.Auto, Force: req.Force})
	if err != nil {
		// UpgradeVendor 오류는 대부분 입력 오류(미매칭 vendor/model, 빈 version)이므로 400.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// action = 마지막 경로 세그먼트("upgrade" | "rollback").
	s.audit(r, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:],
		vendor, fmt.Sprintf("version=%s->%s noChange=%t", result.PreviousVersion, result.NewVersion, result.NoChange))
	writeJSON(w, http.StatusOK, result)
}

// handleToggle 은 NPUClusterPolicy.spec.<vendor>.enabled 를 patch 합니다.
func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request) {
	vendor := r.PathValue("vendor")
	if !knownVendors[vendor] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown vendor %q", vendor))
		return
	}
	req, ok := decodeBody(w, r)
	if !ok {
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled (bool) is required")
		return
	}

	result, err := s.Ctl.SetVendorEnabled(r.Context(), vendor, *req.Enabled)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "toggle", vendor,
		fmt.Sprintf("enabled=%t->%t noChange=%t", result.PreviousEnabled, result.NewEnabled, result.NoChange))
	writeJSON(w, http.StatusOK, result)
}

// audit 은 모든 control 요청을 구조화 로그(주체·행위·대상)로 남깁니다.
func (s *Server) audit(r *http.Request, action, target, detail string) {
	subject, _ := r.Context().Value(ctxUserKey{}).(string)
	s.Log.Info("management API audit",
		"subject", subject, "action", action, "target", target, "detail", detail,
		"remoteAddr", r.RemoteAddr)
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

// decodeBody 는 본문을 controlRequest 로 파싱합니다. 빈 본문(EOF)은 허용하고
// (필드 검증에서 재차 거름), 그 외 파싱 오류는 400 을 씁니다.
func decodeBody(w http.ResponseWriter, r *http.Request) (controlRequest, bool) {
	var req controlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return req, false
	}
	return req, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// writeJSONETag 는 writeJSON 과 같되 본문 해시를 ETag 로 붙이고, If-None-Match 가 같으면 304 와
// 빈 본문을 낸다. 화면이 5초마다 전체를 다시 받는 대신 왕복 하나로 "안 바뀌었다" 를 확인한다.
// watch 스트림이 아닌 이유: Rancher 안의 화면은 장수명 연결을 유지하기 어렵고 응답은 수백 KB 를
// 넘지 않아 전송량보다 왕복 수가 문제다.
func writeJSONETag(w http.ResponseWriter, r *http.Request, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sum := sha256.Sum256(body)
	tag := `"sha256-` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", tag)
	if code == http.StatusOK && r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
