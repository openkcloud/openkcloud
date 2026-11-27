// ============================================================
// rollbackjob_test.go: 롤백 Job 이 다운그레이드를 허용받는지 고정
// 상세: 롤백 Job 은 정책의 allowDowngrade 값을 그대로 물려받지 않는다 — 물려받으면 installer
// 자신의 다운그레이드 가드가 롤백을 거부해 구조적으로 무동작이 된다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package driverjob

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

const (
	envTrue        = "true"
	envFalse       = "false"
	sourceHost     = "Host"
	rollbackTarget = "2026.1.0"
)

// jobEnv 는 Job 컨테이너의 env 를 이름으로 찾는다.
func jobEnv(t *testing.T, envs []corev1.EnvVar, name string) string {
	t.Helper()
	for _, e := range envs {
		if e.Name == name {
			return e.Value
		}
	}
	t.Fatalf("env %s 가 없다", name)
	return ""
}

// noDowngradePolicy 는 다운그레이드를 금지한 job 모드 정책이다(라이브와 같은 설정).
func noDowngradePolicy() *npuv1alpha1.DriverInstallPolicy {
	return &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-rngd-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa", Model: "rngd",
			Driver: npuv1alpha1.DriverSpec{
				Installer: "apt", Mode: "job",
				AllowDowngrade: false,
				// Host 로 둔다 — 롤백이 이것을 Policy 로 덮지 않으면 installer 가 호스트에 남은
				// *실패한* 버전을 desired 로 채택해 롤백이 무동작이 된다. 빈 값으로 두면
				// versionSourceOrDefault 가 이미 "Policy" 를 돌려줘 단언이 공허해진다.
				VersionSource: sourceHost,
				Version:       "2026.2.0",
				Image:         "reg.local/kcloud/furiosa-rngd-driver-installer:2026.2.0-v1",
			},
		},
	}
}

// TestRollbackJobIsAllowedToDowngrade 는 롤백 Job 이 ALLOW_DOWNGRADE=true 로 렌더링되는지 본다.
// 롤백은 정의상 내려가는 일이다. 정책의 allowDowngrade 는 정책 변경으로 버전을 낮추는 것만
// 막는 장치이지 롤백까지 막는 장치가 아니다 — 같은 값으로 묶으면 되돌릴 수 없는 업그레이드가
// 되어 노드가 실패한 버전에 갇힌다. 깨는 뮤테이션: RenderRollbackJob 이 RenderInstallJob 을
// 그대로 부르면(정책 값 사용) ALLOW_DOWNGRADE 가 false 로 나와 실패한다.
func TestRollbackJobIsAllowedToDowngrade(t *testing.T) {
	pol := noDowngradePolicy()

	job := RenderRollbackJob(pol, "rngd-1", "reg.local/kcloud/furiosa-rngd-driver-installer:2026.2.0-v1", rollbackTarget)
	envs := job.Spec.Template.Spec.Containers[0].Env

	if got := jobEnv(t, envs, "ALLOW_DOWNGRADE"); got != envTrue {
		t.Errorf("롤백 Job 의 ALLOW_DOWNGRADE = %q, want \"true\" — 되돌릴 수 없는 업그레이드가 된다", got)
	}
	if got := jobEnv(t, envs, "DRIVER_VERSION"); got != rollbackTarget {
		t.Errorf("롤백 Job 의 DRIVER_VERSION = %q, want \"2026.1.0\"", got)
	}
	// 롤백은 정책이 선언한 버전 출처를 존중하면 안 된다 — 존중하면 되돌릴 대상이 사라진다.
	// `!= Host` 가 아니라 `== Policy` 로 못박는다: 전자는 어떤 값이든 통과한다.
	if got := jobEnv(t, envs, "VERSION_SOURCE"); got != "Policy" {
		t.Errorf("롤백 Job 의 VERSION_SOURCE = %q, want \"Policy\" — 호스트의 실패한 버전을 채택해 롤백이 무동작이 된다", got)
	}
}

// TestInstallJobKeepsPolicyDowngradeSetting 는 일반 설치 Job 은 종전대로 정책 값을 쓰는지 본다.
// 롤백 예외가 정상 경로의 가드까지 풀어 버리면 안 된다.
//
// 깨는 뮤테이션: RenderInstallJob 이 ALLOW_DOWNGRADE 를 무조건 true 로 넣게 하면 실패한다.
func TestInstallJobKeepsPolicyDowngradeSetting(t *testing.T) {
	pol := noDowngradePolicy()

	job := RenderInstallJob(pol, "rngd-1", pol.Spec.Driver.Image, pol.Spec.Driver.Version)
	if got := jobEnv(t, job.Spec.Template.Spec.Containers[0].Env, "ALLOW_DOWNGRADE"); got != envFalse {
		t.Errorf("일반 설치 Job 의 ALLOW_DOWNGRADE = %q, want \"false\" — 정책 가드가 풀렸다", got)
	}

	pol.Spec.Driver.AllowDowngrade = true
	job = RenderInstallJob(pol, "rngd-1", pol.Spec.Driver.Image, pol.Spec.Driver.Version)
	if got := jobEnv(t, job.Spec.Template.Spec.Containers[0].Env, "ALLOW_DOWNGRADE"); got != envTrue {
		t.Errorf("정책이 허용했는데 설치 Job 의 ALLOW_DOWNGRADE = %q, want \"true\"", got)
	}
}
