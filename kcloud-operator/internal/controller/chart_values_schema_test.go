// ============================================================
// chart_values_schema_test.go: values.schema.json 이 모르는 키를 막는지 고정한다.
// 상세: helm 은 스키마가 없으면 오타 키를 받고 조용히 버린다. 닫은 블록은 거절하고, 사이트마다
// 키가 달라 열어 둔 블록은 통과해야 한다 — 둘을 같이 고정해야 엄격함이 의도대로 걸렸음이 남는다.
// 생성일: 2026-10-04 | 수정일: 2026-10-27
// ============================================================
package controller

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// helmRenderWithSet 은 --set 한 쌍으로 차트 렌더를 시도하고 성공 여부와 출력을 돌려준다.
// helmTemplate 과 달리 실패를 t.Fatalf 로 끝내지 않는다 — 거절 자체가 기대값인 시험이 있다.
// os/exec 로 helm 을 부르므로 손으로 돌릴 때는 -count=1 을 붙일 것(make test 는 -coverprofile 로 캐시를 끈다).
func helmRenderWithSet(t *testing.T, set string) (bool, string) {
	t.Helper()
	bin := helmBinary(t)
	out, err := exec.Command(bin, "template", "t", "../../deploy/helm", "--set", set).CombinedOutput()
	return err == nil, string(out)
}

// 스키마 파일이 실재하고 파싱되어야 한다. 깨진 JSON 이면 helm 이 모든 렌더를 거절한다.
func TestChartValuesSchemaParses(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/helm/values.schema.json")
	if err != nil {
		t.Fatalf("values.schema.json 읽기 실패: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("values.schema.json 파싱 실패: %v", err)
	}
	if doc["additionalProperties"] != false {
		t.Errorf("최상위가 닫혀 있지 않다: additionalProperties=%v", doc["additionalProperties"])
	}
}

// 닫은 블록의 오타 키는 거절돼야 한다. 세 자리를 본다 — 최상위, 중첩 블록,
// 그리고 $defs 를 $ref 로 공유하는 블록(참조가 풀리지 않으면 여기만 조용히 통과한다).
func TestChartValuesSchemaRejectsUnknownKeyInClosedBlock(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  string
		want string
	}{
		{"최상위", "relbellions.enabled=false", "'relbellions' not allowed"},
		{"중첩 블록", "detector.imagee=x", "'imagee' not allowed"},
		{"$ref 공유 블록", "driverInstallPolicies.nvidia.upgradePolicy.maxUnavailble=2", "'maxUnavailble' not allowed"},
		{"한 단 닫은 resources", "resources.limitss.cpu=1", "'limitss' not allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, out := helmRenderWithSet(t, tc.set)
			if ok {
				t.Fatalf("%s 오타 키가 거절되지 않았다 — 스키마가 그 블록을 닫지 않았다", tc.set)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("거절 이유가 스키마 위반이 아니다: %q 를 찾지 못함\n%s", tc.want, out)
			}
		})
	}
}

// 열어 둔 블록은 통과해야 한다. 키가 커널 릴리스 문자열·노드 라벨이라 차트가 미리 적을 수
// 없는 자리다 — 닫으면 사전 컴파일 이미지를 커널별로 줄 수 없게 된다.
func TestChartValuesSchemaKeepsSiteKeyedBlocksOpen(t *testing.T) {
	for _, tc := range []struct{ name, set string }{
		{"kernelImages", `driverInstallPolicies.nvidia.driver.kernelImages.5\.15\.0-999-generic=repo/img:tag`},
		{"nodeSelector", `nodeSelector.example\.com/anything=yes`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, out := helmRenderWithSet(t, tc.set)
			if !ok {
				t.Fatalf("열어 둔 블록의 키가 거절됐다: %s\n%s", tc.set, out)
			}
		})
	}
}

// 벤더 항목은 그 벤더 템플릿이 읽는 키만 받아야 한다. 공유 정의 한 벌을 다섯 벤더가 쓰므로
// 합집합으로 두면 읽히지 않는 키가 통과하고 렌더에서 조용히 사라진다. 거절과 통과를 쌍으로 고정한다.
func TestChartValuesSchemaScopesVendorOnlyKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   string
		reads bool // 그 벤더 템플릿이 이 키를 읽는가
	}{
		{"toolkit 은 nvidia 만", "driverInstallPolicies.nvidia.toolkit.enabled=true", true},
		{"toolkit, furiosa", "driverInstallPolicies.furiosa.toolkit.enabled=true", false},
		{"toolkit, rngd", "driverInstallPolicies.rngd.toolkit.enabled=true", false},
		{"toolkit, tenstorrent", "driverInstallPolicies.tenstorrent.toolkit.enabled=true", false},
		{"trackOnly 는 tenstorrent·rebellions 만", "driverInstallPolicies.tenstorrent.driver.trackOnly=true", true},
		// rebellions 는 설치기가 없는 추적 전용이라 trackOnly 는 true 로만 받고 설치 경로 전용 키는 막는다.
		{"trackOnly, rebellions(고정 true)", "driverInstallPolicies.rebellions.driver.trackOnly=true", true},
		{"toolkit, rebellions", "driverInstallPolicies.rebellions.toolkit.enabled=true", false},
		{"설치 이미지 키, rebellions", "driverInstallPolicies.rebellions.driver.repository=x", false},
		{"kernelImageBuildRevision, rebellions", "driverInstallPolicies.rebellions.driver.kernelImageBuildRevision=v4", false},
		{"trackOnly, furiosa", "driverInstallPolicies.furiosa.driver.trackOnly=true", false},
		{"trackOnly, nvidia", "driverInstallPolicies.nvidia.driver.trackOnly=true", false},
		{"trackOnly, rngd", "driverInstallPolicies.rngd.driver.trackOnly=true", false},
		{"imageVersionBaked 는 tenstorrent 만", "driverInstallPolicies.tenstorrent.driver.imageVersionBaked=true", true},
		{"imageVersionBaked, furiosa", "driverInstallPolicies.furiosa.driver.imageVersionBaked=true", false},
		{"imageVersionBaked, nvidia", "driverInstallPolicies.nvidia.driver.imageVersionBaked=true", false},
		{"imageVersionBaked, rngd", "driverInstallPolicies.rngd.driver.imageVersionBaked=true", false},
		// kernelImageBuildRevision 은 네 벤더 템플릿이 모두 읽는다 — 어느 벤더에서도 막히지 않아야
		// 한다. 벤더 한 곳에서만 막히면 그 벤더는 조립 경로를 켤 수 없다.
		{"kernelImageBuildRevision, furiosa", "driverInstallPolicies.furiosa.driver.kernelImageBuildRevision=v4", true},
		{"kernelImageBuildRevision, nvidia", "driverInstallPolicies.nvidia.driver.kernelImageBuildRevision=v4", true},
		{"kernelImageBuildRevision, rngd", "driverInstallPolicies.rngd.driver.kernelImageBuildRevision=v4", true},
		{"kernelImageBuildRevision, tenstorrent", "driverInstallPolicies.tenstorrent.driver.kernelImageBuildRevision=v4", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, out := helmRenderWithSet(t, tc.set)
			if tc.reads {
				if !ok {
					t.Fatalf("그 벤더가 읽는 키가 거절됐다: %s\n%s", tc.set, out)
				}
				return
			}
			if ok {
				t.Fatalf("읽히지 않는 키가 통과했다: %s — 렌더에서 조용히 사라진다", tc.set)
			}
			if !strings.Contains(out, "false schema") {
				t.Errorf("거절 이유가 벤더 제약이 아니다: %q 를 찾지 못함\n%s", "false schema", out)
			}
		})
	}
}

// 구조를 받는 자리에 스칼라가 오면 거절해야 한다. 통과시키면 템플릿의 toYaml 이
// 엉뚱한 YAML 을 뱉거나 렌더 중에 터진다.
func TestChartValuesSchemaRejectsScalarForStructuredBlocks(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{"resources", "resources=notamap", "want object"},
		{"tolerations", "tolerations=notalist", "want array"},
		{"affinity", "affinity=notamap", "want object"},
		{"driver.env", "driverInstallPolicies.nvidia.driver.env=notalist", "want array"},
		{"toolkit.env", "driverInstallPolicies.nvidia.toolkit.env=notalist", "want array"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, out := helmRenderWithSet(t, tc.set)
			if ok {
				t.Fatalf("스칼라가 통과했다: %s", tc.set)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("거절 이유가 타입 위반이 아니다: %q 를 찾지 못함\n%s", tc.want, out)
			}
		})
	}
}

// TestChartRendersKernelImageAssemblyKeys 는 네 벤더 템플릿이 조립 값을 실제로 CR 에 싣는지 본다.
// 스키마가 키를 받는 것만으로는 부족하다 — 템플릿 블록이 한 벤더에서 빠지면 값이 조용히 버려지고
// 그 벤더는 조립 경로를 켤 수 없다. 벤더마다 다른 값을 줘서 어느 블록이 빠졌는지 출력으로 드러나게 한다.
func TestChartRendersKernelImageAssemblyKeys(t *testing.T) {
	bin := helmBinary(t)
	revisions := map[string]string{
		"furiosa": "v11", "nvidia": "v12", "rngd": "v13", "tenstorrent": "v14",
	}
	args := []string{"template", "t", "../../deploy/helm"}
	for vendor, rev := range revisions {
		args = append(args, "--set", "driverInstallPolicies."+vendor+".driver.kernelImageBuildRevision="+rev)
	}
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("렌더 실패: %v\n%s", err, out)
	}
	rendered := string(out)
	for vendor, rev := range revisions {
		if want := `kernelImageBuildRevision: "` + rev + `"`; !strings.Contains(rendered, want) {
			t.Errorf("%s 템플릿이 빌드 개정을 싣지 않는다: %q 를 찾지 못함", vendor, want)
		}
	}
	// variant 는 발행 규약에 그 토막이 있는 벤더에서만 실린다. 차트 기본값이 그 둘을 채운다.
	for _, want := range []string{`kernelImageVariant: "warboy"`, `kernelImageVariant: "rngd"`} {
		if !strings.Contains(rendered, want) {
			t.Errorf("Furiosa 계열 variant 가 실리지 않는다: %q 를 찾지 못함", want)
		}
	}
	// variant 는 빌드 개정 없이는 아무 효과가 없다 — 기본 렌더에 혼자 실려서는 안 된다.
	// 실리면 mode=daemonset 으로 되돌린 사이트에서 CEL 이 그 DIP 를 거절한다.
	base, err := exec.Command(bin, "template", "t", "../../deploy/helm").CombinedOutput()
	if err != nil {
		t.Fatalf("기본 렌더 실패: %v\n%s", err, base)
	}
	if strings.Contains(string(base), "kernelImageVariant") {
		t.Error("기본 렌더에 kernelImageVariant 가 혼자 실렸다 — 빌드 개정과 함께만 실려야 한다")
	}
}

// rebellions.devicePluginGeneration 은 빈 값·v0.3·v0.4 만 받고, 값은 정책에 그대로 실린다.
// 빈 값이면 정책에 필드를 싣지 않는다(operator 가 태그로 판별).
func TestChartRebellionsDevicePluginGeneration(t *testing.T) {
	if ok, out := helmRenderWithSet(t, "rebellions.devicePluginGeneration=v0.9"); ok {
		t.Fatalf("허용값 밖의 판이 거절되지 않았다\n%s", out)
	}
	ok, out := helmRenderWithSet(t, "rebellions.devicePluginGeneration=v0.4")
	if !ok {
		t.Fatalf("v0.4 렌더 실패\n%s", out)
	}
	if !strings.Contains(out, `devicePluginGeneration: "v0.4"`) {
		t.Error("정책에 devicePluginGeneration 이 실리지 않았다")
	}
	if strings.Contains(helmTemplate(t), "devicePluginGeneration:") {
		t.Error("빈 값인데 정책에 devicePluginGeneration 이 실렸다")
	}
}
