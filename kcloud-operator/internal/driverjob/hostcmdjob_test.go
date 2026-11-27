// ============================================================
// hostcmdjob_test.go: 호스트 명령 Job 렌더러 시험
// 생성일: 2026-09-30 | 수정일: 2026-10-11
// ============================================================
package driverjob

import (
	"strings"
	"testing"
)

func TestRenderDeviceResetJobFor(t *testing.T) {
	job := RenderDeviceResetJobFor(nil, "n1", "host-exec:test", "kcloud-device-reset-abc", []string{"nvidia-smi", "-r"})
	c := job.Spec.Template.Spec.Containers[0]
	if job.Name != "kcloud-device-reset-abc" || job.Namespace != Namespace || job.Spec.Template.Spec.NodeName != "n1" {
		t.Fatalf("job = %+v", job.ObjectMeta)
	}
	if !job.Spec.Template.Spec.HostPID || c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
		t.Fatal("hostPID·privileged 가 아니면 nsenter 가 동작하지 않는다")
	}
	if cmd := strings.Join(c.Command, " "); !strings.HasPrefix(cmd, "nsenter --target 1") || !strings.HasSuffix(cmd, "-- nvidia-smi -r") {
		t.Fatalf("command = %q", cmd)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatal("리셋 명령은 자동 재시도하지 않는다(backoffLimit 0)")
	}
	if job.Spec.Template.Spec.HostNetwork {
		t.Fatal("nsenter --net 이 이미 호스트 네트워크로 들어간다 — hostNetwork 는 불필요한 권한이다")
	}
}
