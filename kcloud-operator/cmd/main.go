/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/apiserver"
	"kcloud-operator/internal/controller"
	"kcloud-operator/internal/crdapply"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/uninstall"
	"kcloud-operator/internal/upgrade"
	"kcloud-operator/internal/verification"
	npuwebhook "kcloud-operator/internal/webhook"
	"kcloud-operator/pkg/npuctl"
	// +kubebuilder:scaffold:imports
)

var (
	scheme      = runtime.NewScheme()
	setupLog    = ctrl.Log.WithName("setup")
	buildCommit = "unknown" // ldflags -X main.buildCommit 로 빌드 시 주입
	// releaseVersion 은 이 바이너리의 릴리스 버전이다. 정본은 deploy/helm/Chart.yaml 의
	// appVersion 이고 Makefile 의 docker-build 가 그 값을 빌드 인자로 넘긴다. apply-crds 가
	// 이 값으로 CRD 의 릴리스 표식을 대조한다 — 비면 대조도 기록도 하지 않는다.
	releaseVersion = "" // ldflags -X main.releaseVersion 로 빌드 시 주입
)

// versionLine 은 --version 플래그의 한 줄 출력이다. ldflags 로 박힌 buildCommit 을 그대로 내고,
// 비어 있으면 "unknown" 으로 낸다. test/collect/evidence.sh 가 기동 로그의 "starting manager"
// 줄이 로테이션으로 사라졌을 때 `kubectl exec ... -- /manager --version` 으로 같은 값을 읽는다.
// 서브명령이 아니라 플래그인 이유: 이 플래그를 모르는 구버전 바이너리는 usage 를 내고 종료 코드 2 로
// 끝나 manager 를 한 번 더 기동하지 않는다.
func versionLine() string {
	if buildCommit == "" {
		return "unknown"
	}
	return buildCommit
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	// apiextensionsv1 은 vendoroperator_detect.go 의 CRD 재확인(APIReader.Get 으로
	// CustomResourceDefinition 직접 조회)이 쓴다. client-go 의 기본 scheme 에는 없다.
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))

	utilruntime.Must(npuv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	// === apply-crds 서브명령 분기 (반드시 flag.Parse() 이전) ===
	if len(os.Args) > 1 && os.Args[1] == "apply-crds" {
		ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
		// --force 는 릴리스 표식 대조를 넘겨 하향 적용을 강제한다. 되돌리기를 영구히
		// 막지 않으려고 둔 수단이고, 넘겼다는 사실은 crdapply 가 로그에 남긴다.
		force := slices.Contains(os.Args[2:], "--force")
		setupLog.Info("apply-crds 서브명령 시작", "release", releaseVersion, "force", force)
		if err := crdapply.Run(context.Background(), releaseVersion, force); err != nil {
			setupLog.Error(err, "apply-crds 실패")
			os.Exit(1)
		}
		setupLog.Info("apply-crds 완료")
		return
	}
	// === uninstall-gate 서브명령 분기 (helm pre-delete hook) ===
	// 실패는 exit 1 이고, helm 이 그 실패로 삭제를 멈춘다 — 사용 중인 클러스터에서
	// 아무것도 지우지 않는 것이 이 서브명령의 목적이다.
	if len(os.Args) > 1 && os.Args[1] == "uninstall-gate" {
		ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
		if err := uninstall.Run(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall-gate 실패: %v\n", err)
			os.Exit(1)
		}
		return
	}
	// === 이하 기본 manager 로직 ===
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var apiBindAddress, apiCertPath string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	var webhookInjectScope string
	flag.StringVar(&webhookInjectScope, "webhook-inject-scope", npuwebhook.InjectScopePod,
		"Pod mutating webhook 의 주입 대상 범위: pod(opt-in 라벨 Pod) 또는 namespace(라벨 네임스페이스의 Pod 전부). "+
			"차트 값 webhook.injectScope 와 같은 값이다.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&apiBindAddress, "api-bind-address", "",
		"Address for the management REST API (e.g. :9444). Empty (default) disables it.")
	flag.StringVar(&apiCertPath, "api-cert-path", "",
		"Directory containing the management API TLS cert (tls.crt/tls.key). Empty serves plain HTTP.")
	var genDPImage, genDRAImage string
	flag.StringVar(&genDPImage, "generated-dp-image", "",
		"Image for descriptor-generated device-plugin DaemonSets (helm 이 주입). 비면 device-plugin backend 를 auto-apply 하지 않는다.")
	flag.StringVar(&genDRAImage, "generated-dra-image", "",
		"Image for descriptor-generated DRA driver DaemonSets (helm 이 주입). 비면 DRA backend 를 auto-apply 하지 않는다.")
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "빌드 커밋을 한 줄로 출력하고 종료한다.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if showVersion {
		fmt.Println(versionLine())
		return
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically generate
	// self-signed certificates for the metrics server (not recommended for production).
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	// syncPeriod: 캐시 re-sync 주기. 60s 로 단축하여 stale informer 조기 감지.
	syncPeriod := 60 * time.Second
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		// operator 쓰기의 managedFields 관리자 이름을 바이너리 파일명(manager) 대신 kcloud-operator 로
		// 고정한다. 호출마다 FieldOwner 를 넘기는 쓰기는 그 값이 우선한다.
		NewClient: func(cfg *rest.Config, o client.Options) (client.Client, error) {
			c, err := client.New(cfg, o)
			if err != nil {
				return nil, err
			}
			return client.WithFieldOwner(c, "kcloud-operator"), nil
		},
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: "35b22ec5.ai",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends, speeding up leader transitions — but requires the binary to
		// end immediately when the Manager stops; if any cleanup runs after that, enabling
		// this is unsafe.
		// LeaderElectionReleaseOnCancel: true,
		Cache: cache.Options{SyncPeriod: &syncPeriod},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.NPUClusterPolicyReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("npuclusterpolicy-controller"),
		APIReader: mgr.GetAPIReader(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NPUClusterPolicy")
		os.Exit(1)
	}

	if err := (&controller.DriverDaemonSetReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("driverdaemonset-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DriverDaemonSet")
		os.Exit(1)
	}

	if err := (&controller.ToolkitDaemonSetReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("toolkitdaemonset-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ToolkitDaemonSet")
		os.Exit(1)
	}

	sm := &upgrade.UpgradeStateMachine{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Recorder:  mgr.GetEventRecorderFor("driver-upgrade-statemachine"),
	}
	driverReconciler := &controller.DriverUpgradeReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Recorder:     mgr.GetEventRecorderFor("driverupgrade-controller"),
		StateMachine: sm,
	}
	if err := driverReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DriverUpgrade")
		os.Exit(1)
	}
	// probeLogs 는 벤더 기능 probe 파드의 마지막 로그를 읽는 seam 이다 — controller-runtime client 는
	// log 서브리소스를 못 읽어 clientset 이 필요하다(실패 사유가 근거에 남지 않으면 파드는 이미 지워진 뒤다).
	probeLogs, err := controller.NewPodLogGetter(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to build probe log getter")
		os.Exit(1)
	}
	// acppLiveVerifier 는 apply 검증과 근거 게이트의 allocation probe 가 공유하는 단일 인스턴스다 —
	// 따로 만들면 ACPP_PROBE_IMAGE 설정이 두 곳으로 갈라진다.
	acppLiveVerifier := controller.NewLiveVerifier(mgr.GetClient(), probeLogs)
	acppReconciler := &controller.AcceleratorPartitionPolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("acceleratorpartitionpolicy-controller"),
		Verifier: acppLiveVerifier,
		Verification: &verification.Verifier{
			Client: mgr.GetClient(),
			Prober: controller.NewAllocationProber(acppLiveVerifier),
		},
		// 삭제 경로가 위임 모드에서 노드를 되돌리기 전에 조정자와 같은 Lease 를 잡는다(같은
		// Namespace/Duration — 서로 다른 Lease 로 나뉘면 이 목적 자체가 성립하지 않는다).
		Leases: &operation.LeaseManager{
			Client: mgr.GetClient(), Namespace: naming.OperatorNamespace(),
			Holder: "kcloud-operator", Duration: 60 * time.Second,
		},
	}
	if err := acppReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AcceleratorPartitionPolicy")
		os.Exit(1)
	}
	// AcceleratorOperation 컨트롤러 — ACPP reconciler 인스턴스를 재사용한다. participant 가 그
	// seam(Verifier·Executor·Observer)을 그대로 써야 적용 로직이 갈라지지 않는다.
	if err := (&controller.AcceleratorOperationReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("accelerator-operation"),
		Participants: operation.Registry{
			operation.PartitionReconfigure: controller.NewACPPParticipant(acppReconciler),
			operation.SharingModeChange:    controller.NewACPPParticipant(acppReconciler),
			operation.Revalidate:           controller.NewRevalidateParticipant(acppReconciler),
			operation.DriverInstall:        controller.NewDriverParticipant(driverReconciler),
			operation.DriverUpgrade:        controller.NewDriverParticipant(driverReconciler),
			operation.DriverRollback:       controller.NewDriverParticipant(driverReconciler),
			operation.DevicePluginRestart:  controller.NewDevicePluginParticipant(acppReconciler),
			operation.RecoverDevice:        controller.NewRecoverDeviceParticipant(acppReconciler),
			operation.NodeReboot: controller.NewNodeRebootParticipant(
				mgr.GetClient(), controller.HostExecImage()),
			operation.DeviceReset: controller.NewDeviceResetParticipant(
				mgr.GetClient(), controller.HostExecImage()),
		},
		Leases: &operation.LeaseManager{
			Client: mgr.GetClient(), Namespace: naming.OperatorNamespace(),
			Holder: "kcloud-operator", Duration: 60 * time.Second,
		},
		Verification: &verification.Verifier{
			Client: mgr.GetClient(),
			Prober: controller.NewAllocationProber(controller.NewLiveVerifier(mgr.GetClient(), probeLogs)),
		},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AcceleratorOperation")
		os.Exit(1)
	}

	// Health 감시 컨트롤러 — 장치를 직접 바꾸지 않고 상태 기록·격리 라벨·복구 작업 생성만 한다.
	if err := (&controller.AcceleratorHealthReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("accelerator-health"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AcceleratorHealth")
		os.Exit(1)
	}
	if err := (&controller.AcceleratorDescriptorReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		DPImage:            genDPImage,
		DRAImage:           genDRAImage,
		GeneratedNamespace: naming.OperatorNamespace(),
		Verification: &verification.Verifier{
			Client: mgr.GetClient(),
			Prober: controller.NewAllocationProber(acppLiveVerifier),
		},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AcceleratorDescriptor")
		os.Exit(1)
	}
	if err := (&controller.AcceleratorWorkloadReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("acceleratorworkload-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AcceleratorWorkload")
		os.Exit(1)
	}
	// 정책과 무관한 저빈도 MIG 관측 — 정책이 없어도 보고서의 MIG 필드가 신선해야 한다.
	if err := (&controller.MigObservationReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("migobservation"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MigObservation")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// Admission webhooks(DIP/NCP validating + Pod mutating). webhook-cert-path 가 비면 등록
	// 자체를 건너뛴다 — 핸들러를 등록하는 순간 controller-runtime 이 웹훅 서버를 띄우고,
	// 인증서 경로가 비면 기본 경로(/tmp/k8s-webhook-server/serving-certs)에서 찾다가 매니저가
	// 통째로 죽는다. helm 차트는 webhook.enabled=false 면 --webhook-cert-path 를 주지 않으므로
	// 이 가드가 없으면 차트 기본값 그대로는 operator 가 뜨지 않는다.
	if webhookCertPath != "" {
		if err := npuwebhook.Setup(mgr, webhookInjectScope); err != nil {
			setupLog.Error(err, "unable to set up admission webhooks")
			os.Exit(1)
		}
	} else {
		setupLog.Info("webhook-cert-path 가 비어 admission webhook 미등록 — 웹훅 서버도 띄우지 않는다")
	}

	// Management REST API(opt-in). api-bind-address 가 비면 미기동(무영향).
	// TokenReview/SAR 는 별도 clientset, 상태·CR patch 는 매니저의 캐시 client 를 재사용한다.
	if apiBindAddress != "" {
		authnClient, err := kubernetes.NewForConfig(mgr.GetConfig())
		if err != nil {
			setupLog.Error(err, "unable to build clientset for management API auth")
			os.Exit(1)
		}
		apiSrv := &apiserver.Server{
			Addr:  apiBindAddress,
			Authn: authnClient,
			Ctl:   npuctl.NewFromClient(mgr.GetClient()),
			// 조회 엔드포인트는 매니저 캐시가 아니라 API 서버를 직접 읽는다(캐시에 Event 를
			// 태우지 않기 위해서다 — apiserver.Server.Reader 주석 참고).
			Reader: mgr.GetAPIReader(),
			Log:    ctrl.Log.WithName("apiserver"),
		}
		if len(apiCertPath) > 0 {
			apiSrv.CertFile = filepath.Join(apiCertPath, "tls.crt")
			apiSrv.KeyFile = filepath.Join(apiCertPath, "tls.key")
		}
		if err := mgr.Add(apiSrv); err != nil {
			setupLog.Error(err, "unable to add management API server to manager")
			os.Exit(1)
		}
	}

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	// reconcile-alive: 마지막 reconcile 이후 5분 초과 시 liveness 실패 처리.
	// stale informer / zombie controller 감지를 위한 추가 헬스 체크.
	if err := mgr.AddHealthzCheck("reconcile-alive", func(_ *http.Request) error {
		last := metrics.GetLastReconcileTime()
		if last.IsZero() {
			return nil // 기동 직후 grace period
		}
		if d := time.Since(last); d > 5*time.Minute {
			return fmt.Errorf("no reconcile in %v", d)
		}
		return nil
	}); err != nil {
		setupLog.Error(err, "unable to set up reconcile-alive check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager", "commit", buildCommit)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
