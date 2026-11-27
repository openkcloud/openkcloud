package placementchecks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultConfigValidate(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.ModeOf(CheckMemory) != ModeRecord {
		t.Fatalf("mode=%s", cfg.ModeOf(CheckMemory))
	}
	if cfg.Checks[CheckMemory].MemorySafetyMarginMiB != nil {
		t.Fatal("default memory margin must be null")
	}
}

func TestParseConfigYAMLExample(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "config", "required-checks.yaml")
	raw, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfigYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Checks[CheckMemory].MemorySafetyMarginMiB
	if m == nil || *m != 1024 {
		t.Fatalf("margin=%v", m)
	}
}

func TestConfigRejectsNegativeMargin(t *testing.T) {
	_, err := ParseConfigYAML([]byte(`
version: t
checks:
  memory-headroom:
    mode: record
    memory_safety_margin_mib: -1
`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConfigRejectsBadMode(t *testing.T) {
	_, err := ParseConfigYAML([]byte(`
version: t
checks:
  memory-headroom:
    mode: maybe
`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEnvModeOverride(t *testing.T) {
	t.Setenv("SCHED_REQUIRED_CHECK_2_MODE", "enforce")
	cfg, err := ParseConfigYAML([]byte("version: t\nchecks: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModeOf(CheckMemory) != ModeEnforce {
		t.Fatalf("got %s", cfg.ModeOf(CheckMemory))
	}
}
