// ============================================================
// state_machine_install_secret_test.go: 설치 Secret 부재 시 업그레이드 슬롯 미점유 시험
// 상세: Warboy 설치 파드가 마운트할 furiosa-apt-auth 가 없을 때 Warboy DUS 가 슬롯을 잡지 않고 Idle
//       에서 기다리는지, 같은 노드의 NVIDIA DUS 가 슬롯을 받는지, 기다리는 동안 바뀐 정책이 반영되는지,
//       Secret 을 만든 뒤 대기 사유가 슬롯을 쥔 DUS 로 바뀌는지, 확인 경로(APIReader)·권한 없음·API 오류·
//       목록 순서를 fake client 로 본다.
// 생성일: 2026-10-22 | 수정일: 2026-10-22
// ============================================================

package upgrade

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driveradapter"
	"kcloud-operator/internal/naming"
)

// makeRequiredDUSFor 는 handleIdle 이 설치 필요로 판정한 뒤의 DUS 다(이름 <노드>-<벤더>).
func makeRequiredDUSFor(node, vendor, model string) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: node + "-" + vendor},
		Spec:       v1alpha1.DriverUpgradeStateSpec{NodeName: node, Vendor: vendor, Model: model},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:          v1alpha1.UpgradeStateRequired,
			DesiredVersion: "1",
		},
	}
}

// makeSlotDIP 는 슬롯 slots 개짜리 Job 모드 정책이다(차트 기본값은 1).
func makeSlotDIP(vendor, model string, slots int32) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: vendor + "-" + model},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor:        vendor,
			Model:         model,
			Driver:        v1alpha1.DriverSpec{Mode: "job", Version: "1"},
			UpgradePolicy: &v1alpha1.UpgradePolicy{MaxParallelUpgrades: slots},
		},
	}
}

func aptAuthSecret() *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: driveradapter.AptAuthSecretName, Namespace: naming.OperatorNamespace()}}
}

// newFakeClient 는 이 시험의 fake 클라이언트다. funcs 가 있으면 호출을 가로챈다.
func newFakeClient(funcs *interceptor.Funcs, objs ...client.Object) client.WithWatch {
	b := fake.NewClientBuilder().
		WithScheme(newUpgradeTestScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.DriverUpgradeState{})
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	return b.Build()
}

// newSecretGateSM 은 캐시 Client 와 APIReader 를 따로 넣은 상태 기계다. Secret 을 reader 에만 두면
// 확인이 캐시 Client 가 아니라 APIReader 로 가는지 가려진다 — 운영에서 캐시로 읽으면 클러스터 전체
// Secret informer 가 뜨고 그 권한이 없어 Get 이 멈춘다.
func newSecretGateSM(reader client.Reader, c client.Client) *UpgradeStateMachine {
	return &UpgradeStateMachine{Client: c, APIReader: reader, Recorder: record.NewFakeRecorder(16)}
}

// step 은 컨트롤러 Reconcile 처럼 한 pass 를 돌리고 status 를 저장한 뒤 재확인 간격을 돌려준다 — 슬롯
// 계산은 저장된 DUS 를 센다.
func step(t *testing.T, m *UpgradeStateMachine, dus *v1alpha1.DriverUpgradeState,
	pol *v1alpha1.DriverInstallPolicy) time.Duration {
	t.Helper()
	_, after, err := m.TransitionState(context.Background(), dus, pol)
	if err != nil {
		t.Fatalf("%s TransitionState: %v", dus.Name, err)
	}
	if err := m.Status().Update(context.Background(), dus); err != nil {
		t.Fatalf("%s status 저장: %v", dus.Name, err)
	}
	return after
}

// TestMissingInstallSecretKeepsSlotFree 는 보고된 장애 그대로다 — Warboy·NVIDIA 가 같은 노드에 있고
// furiosa-apt-auth 가 없다. Warboy 가 슬롯을 잡으면 설치 파드가 볼륨 마운트에서 멈춘 채 Job 시한까지
// 슬롯과 cordon 을 쥐어 NVIDIA 설치가 시작되지 못한다. Warboy 는 Idle 로 돌아가 기다리고, Secret 이
// 생기면 사이클을 다시 연다.
// 깨는 뮤테이션: handleUpgradeRequired 의 확인을 빼면 Warboy 가 PreFlight 로 가고 NVIDIA 가 막힌다.
// handleIdle 의 확인을 빼면 Idle 에서 곧바로 UpgradeRequired 로 다시 나간다.
func TestMissingInstallSecretKeepsSlotFree(t *testing.T) {
	warboy, wPol := makeRequiredDUSFor("w1", "furiosa", "warboy"), makeSlotDIP("furiosa", "warboy", 1)
	nvidia, nPol := makeRequiredDUSFor("w1", "nvidia", "generic"), makeSlotDIP("nvidia", "generic", 1)
	reader := newFakeClient(nil)
	m := newSecretGateSM(reader, newFakeClient(nil, warboy, nvidia, wPol, nPol))

	step(t, m, warboy, wPol)
	if warboy.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("Secret 없이 Warboy 가 %s 로 갔다 — Idle 에서 기다려야 한다", warboy.Status.State)
	}
	if !strings.Contains(warboy.Status.Message, driveradapter.AptAuthSecretName) {
		t.Errorf("대기 사유에 Secret 이름이 없다: %q", warboy.Status.Message)
	}
	if ev := drainEvents(m); len(ev) != 1 || !strings.Contains(ev[0], "InstallSecretMissing") {
		t.Errorf("InstallSecretMissing 경고 1건을 기대했다: %v", ev)
	}

	step(t, m, nvidia, nPol)
	if nvidia.Status.State != v1alpha1.UpgradeStatePreFlight {
		t.Fatalf("NVIDIA 가 %s 에 머물렀다 — Warboy 대기가 슬롯을 막았다", nvidia.Status.State)
	}

	// Idle 에서 같은 사유로 기다린다 — 사이클을 다시 열지 않고 경고도 되풀이하지 않는다.
	step(t, m, warboy, wPol)
	if ev := drainEvents(m); warboy.Status.State != v1alpha1.UpgradeStateIdle || len(ev) != 0 {
		t.Errorf("Secret 없이 Idle 에서 나갔거나 경고가 다시 나왔다: %s %v", warboy.Status.State, ev)
	}

	// Secret 을 만들면 사이클을 다시 열고, 슬롯을 쥔 NVIDIA 를 대기 사유로 남긴다.
	if err := reader.Create(context.Background(), aptAuthSecret()); err != nil {
		t.Fatalf("Secret 생성: %v", err)
	}
	step(t, m, warboy, wPol)
	step(t, m, warboy, wPol)
	if warboy.Status.State != v1alpha1.UpgradeStateRequired ||
		!strings.Contains(warboy.Status.Message, nvidia.Name) ||
		strings.Contains(warboy.Status.Message, driveradapter.AptAuthSecretName) {
		t.Errorf("슬롯 대기 사유에 슬롯을 쥔 %s 가 없거나 Secret 사유가 남았다: %s %q",
			nvidia.Name, warboy.Status.State, warboy.Status.Message)
	}
}

// TestInstallSecretWaitHonorsPolicyChange 는 Secret 을 기다리는 동안 바뀐 정책이 반영되는지 본다.
// UpgradeRequired 에서 기다리면 정책을 다시 보지 않아, 허용 목록에서 뺀 버전도 Secret 이 생기는 순간
// 설치된다. Idle 에서 기다리면 매 pass 정책 게이트를 다시 거친다.
// 깨는 뮤테이션: handleIdle 의 Job 모드 설치 확인을 허용 목록 판정 앞으로 옮기면 UnverifiedVersion 대신 대기한다.
func TestInstallSecretWaitHonorsPolicyChange(t *testing.T) {
	warboy, wPol := makeRequiredDUSFor("w1", "furiosa", "warboy"), makeSlotDIP("furiosa", "warboy", 1)
	warboy.Status.State = v1alpha1.UpgradeStateIdle
	m := newSecretGateSM(newFakeClient(nil), newFakeClient(nil, warboy, wPol))

	step(t, m, warboy, wPol)
	if warboy.Status.State != v1alpha1.UpgradeStateIdle ||
		!strings.Contains(warboy.Status.Message, driveradapter.AptAuthSecretName) {
		t.Fatalf("Secret 없이 Idle 에서 기다리지 않았다: %s %q", warboy.Status.State, warboy.Status.Message)
	}

	wPol.Spec.VerifiedVersions = []string{"2"}
	step(t, m, warboy, wPol)
	if warboy.Status.State != v1alpha1.UpgradeStateUnverifiedVersion {
		t.Errorf("대기 중 허용 목록에서 뺀 버전이 반영되지 않았다: %s %q", warboy.Status.State, warboy.Status.Message)
	}
}

// TestIdleHoldsEveryCycleEntry 는 handleIdle 이 사이클을 여는 세 경로(Job 모드 처음 설치, 버전 변경
// 업그레이드, 롤백 보류 중 복원 설치) 모두 Secret 이 없으면 Idle 에서 기다리는지 본다. 생성 때 status 가
// 버려진 새 DUS(빈 상태)는 Idle 로 맞추고, autoUpgrade 가 꺼진 버전 변경은 Secret 보다 그 사유를 먼저 낸다.
// 깨는 뮤테이션: openCycle 의 확인을 빼면 세 경로가 UpgradeRequired 로 나간다. 빈 상태 정규화를 빼면
// 빈 상태로 남는다. 재확인 간격을 0 으로 바꾸면 간격 확인이 실패한다.
func TestIdleHoldsEveryCycleEntry(t *testing.T) {
	unloaded := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "w1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "furiosa", Model: "warboy", DriverVersion: "0.9"}}},
	}
	upgradeFrom := func(auto bool) func(*v1alpha1.DriverUpgradeState, *v1alpha1.DriverInstallPolicy) []client.Object {
		return func(d *v1alpha1.DriverUpgradeState, p *v1alpha1.DriverInstallPolicy) []client.Object {
			d.Status.CurrentVersion = "0.9"
			p.Spec.UpgradePolicy.AutoUpgrade = auto
			return nil
		}
	}
	for _, tc := range []struct {
		name     string
		setup    func(*v1alpha1.DriverUpgradeState, *v1alpha1.DriverInstallPolicy) []client.Object
		wantHold bool
	}{
		{"처음 설치", func(*v1alpha1.DriverUpgradeState, *v1alpha1.DriverInstallPolicy) []client.Object { return nil }, true},
		{"새 DUS(빈 상태)", func(d *v1alpha1.DriverUpgradeState, _ *v1alpha1.DriverInstallPolicy) []client.Object {
			d.Status = v1alpha1.DriverUpgradeStateStatus{}
			return nil
		}, true},
		{"버전 변경", upgradeFrom(true), true},
		{"버전 변경, autoUpgrade 꺼짐", upgradeFrom(false), false},
		{"보류 중 복원", func(d *v1alpha1.DriverUpgradeState, _ *v1alpha1.DriverInstallPolicy) []client.Object {
			d.Status.CurrentVersion = "0.9"
			d.Status.RolledBackVersion = "1"
			return []client.Object{unloaded.DeepCopy()}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warboy, wPol := makeRequiredDUSFor("w1", "furiosa", "warboy"), makeSlotDIP("furiosa", "warboy", 1)
			warboy.Status.State = v1alpha1.UpgradeStateIdle
			objs := append(tc.setup(warboy, wPol), warboy, wPol)
			m := newSecretGateSM(newFakeClient(nil), newFakeClient(nil, objs...))
			after := step(t, m, warboy, wPol)
			waiting := strings.Contains(warboy.Status.Message, driveradapter.AptAuthSecretName)
			if warboy.Status.State != v1alpha1.UpgradeStateIdle || waiting != tc.wantHold {
				t.Fatalf("Idle·Secret 대기(%v)를 기대했다: %s %q", tc.wantHold, warboy.Status.State, warboy.Status.Message)
			}
			if tc.wantHold && after < 10*time.Second {
				t.Errorf("재확인 간격 %s 가 너무 짧다 — Secret 을 기다리는 동안 재조정이 몰린다", after)
			}
		})
	}
}

// TestInstallSecretCheck 는 확인 경로별 결과를 본다. Secret 이 APIReader 쪽에만 있거나, APIReader 가
// 없거나, 확인 권한이 없으면 종전대로 슬롯을 받는다 — 권한이 없을 때 막으면 Role 이 없는 배포에서
// Warboy 설치가 영영 시작되지 않는다. 그 밖의 API 오류는 전이 없이 오류로 올린다.
func TestInstallSecretCheck(t *testing.T) {
	failWith := func(err error) *interceptor.Funcs {
		return &interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey,
			client.Object, ...client.GetOption) error {
			return err
		}}
	}
	for _, tc := range []struct {
		name   string
		reader func() client.Reader
		want   string // 기대 상태. 빈 값이면 오류와 UpgradeRequired 유지를 기대한다.
	}{
		{"Secret 은 APIReader 쪽에만", func() client.Reader { return newFakeClient(nil, aptAuthSecret()) },
			v1alpha1.UpgradeStatePreFlight},
		{"APIReader 미주입", func() client.Reader { return nil }, v1alpha1.UpgradeStatePreFlight},
		{"확인 권한 없음", func() client.Reader {
			return newFakeClient(failWith(apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "x", nil)))
		}, v1alpha1.UpgradeStatePreFlight},
		{"API 오류", func() client.Reader {
			return newFakeClient(failWith(apierrors.NewInternalError(errors.New("apiserver 응답 없음"))))
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warboy, wPol := makeRequiredDUSFor("w1", "furiosa", "warboy"), makeSlotDIP("furiosa", "warboy", 1)
			m := newSecretGateSM(tc.reader(), newFakeClient(nil, warboy, wPol))
			_, _, err := m.TransitionState(context.Background(), warboy, wPol)
			if tc.want == "" {
				if err == nil || warboy.Status.State != v1alpha1.UpgradeStateRequired {
					t.Fatalf("API 오류에 오류 없이 %s 로 갔다", warboy.Status.State)
				}
				return
			}
			if err != nil || warboy.Status.State != tc.want {
				t.Fatalf("Warboy 가 %s(err %v)다, %s 를 기대했다(%q)", warboy.Status.State, err, tc.want, warboy.Status.Message)
			}
		})
	}
}

// TestSlotWaitMessageIgnoresListOrder 는 슬롯 대기 사유가 목록 순서와 무관한지 본다. 운영 캐시 List 는
// 순서가 정해지지 않는데(informer 인덱서가 map 을 순회), 받은 순서대로 이름을 이으면 pass 마다 사유가
// 바뀌어 이벤트·status 쓰기·재조정이 반복된다. fake client 는 이름순이라 가로채서 순서를 뒤집는다.
// 깨는 뮤테이션: activeUpgrades 의 정렬을 빼면 두 번째 pass 에서 사유가 바뀌고 이벤트가 다시 나온다.
func TestSlotWaitMessageIgnoresListOrder(t *testing.T) {
	waiting, pol := makeRequiredDUSFor("w1", "nvidia", "generic"), makeSlotDIP("nvidia", "generic", 2)
	busy2, busy3 := makeRequiredDUSFor("w2", "nvidia", "generic"), makeRequiredDUSFor("w3", "nvidia", "generic")
	busy2.Status.State, busy3.Status.State = v1alpha1.UpgradeStatePreFlight, v1alpha1.UpgradeStatePreFlight
	lists := 0
	reverseEveryOther := &interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch,
		list client.ObjectList, opts ...client.ListOption) error {
		if err := c.List(ctx, list, opts...); err != nil {
			return err
		}
		if l, ok := list.(*v1alpha1.DriverUpgradeStateList); ok {
			if lists++; lists%2 == 0 {
				slices.Reverse(l.Items)
			}
		}
		return nil
	}}
	m := newSecretGateSM(nil, newFakeClient(reverseEveryOther, waiting, busy2, busy3, pol))

	step(t, m, waiting, pol)
	first := waiting.Status.Message
	drainEvents(m)
	step(t, m, waiting, pol)
	if waiting.Status.State != v1alpha1.UpgradeStateRequired || !strings.Contains(first, busy2.Name) {
		t.Fatalf("슬롯 대기를 기대했다: %s %q", waiting.Status.State, first)
	}
	if ev := drainEvents(m); len(ev) != 0 || waiting.Status.Message != first {
		t.Errorf("목록 순서만 바뀌었는데 사유가 바뀌었다: %q → %q, 이벤트 %v", first, waiting.Status.Message, ev)
	}
}
