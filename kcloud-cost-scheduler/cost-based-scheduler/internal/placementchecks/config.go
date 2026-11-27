package placementchecks

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the validated required-checks configuration (loaded once at start).
type Config struct {
	Version string                 `yaml:"version" json:"version"`
	Checks  map[CheckID]CheckConfig `yaml:"checks" json:"checks"`
}

// CheckConfig is per-check settings. Pointer thresholds mean "unset" → UNEVALUATED when needed.
type CheckConfig struct {
	Mode Mode `yaml:"mode" json:"mode"`

	// Memory (check 2)
	MemorySafetyMarginMiB *int64 `yaml:"memory_safety_margin_mib" json:"memory_safety_margin_mib"`

	// Freshness (check 6)
	MaxAgeSeconds *int64 `yaml:"max_age_seconds" json:"max_age_seconds"`
	// EarliestValidUnix rejects source clocks at/before this (default: reject pre-2000 / 1970 NETIO).
	EarliestValidUnix *int64 `yaml:"earliest_valid_unix" json:"earliest_valid_unix"`
	// MaxFutureSkewSeconds: source/receive time ahead of now beyond this → invalid.
	MaxFutureSkewSeconds *int64 `yaml:"max_future_skew_seconds" json:"max_future_skew_seconds"`

	// Thermal/power (check 5) — reserved for P5
	MaxGPUTempC    *float64 `yaml:"max_gpu_temp_c" json:"max_gpu_temp_c"`
	MaxGPUPowerW   *float64 `yaml:"max_gpu_power_w" json:"max_gpu_power_w"`
	MaxServerPowerW *float64 `yaml:"max_server_power_w" json:"max_server_power_w"`
}

// DefaultConfig returns record mode for all checks with conservative freshness defaults
// and unset memory safety margin (null → memory check UNEVALUATED until configured).
func DefaultConfig() Config {
	earliest := defaultEarliestValidUnix // same constant as freshness.go
	skew := int64(300)
	maxAge := int64(60)
	cfg := Config{
		Version: "required-checks-v1",
		Checks:  map[CheckID]CheckConfig{},
	}
	for _, id := range AllCheckIDs {
		c := CheckConfig{Mode: ModeRecord}
		if id == CheckFreshness {
			c.MaxAgeSeconds = &maxAge
			c.EarliestValidUnix = &earliest
			c.MaxFutureSkewSeconds = &skew
		}
		// MemorySafetyMarginMiB left nil on purpose.
		cfg.Checks[id] = c
	}
	return cfg
}

// LoadConfigFile reads YAML from path. Empty path uses defaults then still
// applies SCHED_REQUIRED_CHECK_*_MODE env overrides and validates.
func LoadConfigFile(path string) (Config, error) {
	if path == "" {
		cfg := ApplyEnvOverrides(DefaultConfig())
		if err := cfg.Validate(); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("required-checks config: %w", err)
	}
	return ParseConfigYAML(raw)
}

// ParseConfigYAML merges YAML over defaults then validates.
func ParseConfigYAML(raw []byte) (Config, error) {
	cfg := DefaultConfig()
	var overlay struct {
		Version string                       `yaml:"version"`
		Checks  map[string]map[string]any    `yaml:"checks"`
	}
	if err := yaml.Unmarshal(raw, &overlay); err != nil {
		return Config{}, fmt.Errorf("required-checks yaml: %w", err)
	}
	if overlay.Version != "" {
		cfg.Version = overlay.Version
	}
	for key, fields := range overlay.Checks {
		id := resolveCheckKey(key)
		if id == "" {
			return Config{}, fmt.Errorf("unknown check key %q", key)
		}
		cur := cfg.Checks[id]
		if err := applyCheckFields(&cur, fields); err != nil {
			return Config{}, fmt.Errorf("check %s: %w", id, err)
		}
		cfg.Checks[id] = cur
	}
	cfg = ApplyEnvOverrides(cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func resolveCheckKey(key string) CheckID {
	k := strings.ToLower(strings.TrimSpace(key))
	aliases := map[string]CheckID{
		"1": CheckAccelerator, "accelerator": CheckAccelerator, "device": CheckAccelerator,
		"2": CheckMemory, "memory": CheckMemory, "memory-headroom": CheckMemory,
		"3": CheckRuntime, "runtime": CheckRuntime,
		"4": CheckTopology, "topology": CheckTopology,
		"5": CheckThermalPower, "thermal-power": CheckThermalPower, "thermal": CheckThermalPower,
		"6": CheckFreshness, "data-freshness": CheckFreshness, "freshness": CheckFreshness,
		"7": CheckPolicy, "policy": CheckPolicy,
	}
	return aliases[k]
}

var knownCheckFieldKeys = map[string]struct{}{
	"mode": {},
	"memory_safety_margin_mib": {},
	"max_age_seconds": {},
	"earliest_valid_unix": {},
	"max_future_skew_seconds": {},
	"max_gpu_temp_c": {},
	"max_gpu_power_w": {},
	"max_server_power_w": {},
}

func applyCheckFields(cur *CheckConfig, fields map[string]any) error {
	for k := range fields {
		if _, ok := knownCheckFieldKeys[k]; !ok {
			return fmt.Errorf("unknown config key %q", k)
		}
	}
	if v, ok := fields["mode"]; ok {
		m, err := parseMode(fmt.Sprint(v))
		if err != nil {
			return err
		}
		cur.Mode = m
	}
	if err := optionalInt64(fields, "memory_safety_margin_mib", &cur.MemorySafetyMarginMiB, true); err != nil {
		return err
	}
	if err := optionalInt64(fields, "max_age_seconds", &cur.MaxAgeSeconds, true); err != nil {
		return err
	}
	if err := optionalInt64(fields, "earliest_valid_unix", &cur.EarliestValidUnix, false); err != nil {
		return err
	}
	if err := optionalInt64(fields, "max_future_skew_seconds", &cur.MaxFutureSkewSeconds, true); err != nil {
		return err
	}
	if err := optionalFloat64(fields, "max_gpu_temp_c", &cur.MaxGPUTempC); err != nil {
		return err
	}
	if err := optionalFloat64(fields, "max_gpu_power_w", &cur.MaxGPUPowerW); err != nil {
		return err
	}
	if err := optionalFloat64(fields, "max_server_power_w", &cur.MaxServerPowerW); err != nil {
		return err
	}
	return nil
}

// optionalInt64: key absent → leave dst unchanged (keep default).
// key present with null → clear dst to nil. key present with value → set.
func optionalInt64(fields map[string]any, key string, dst **int64, nonNeg bool) error {
	v, ok := fields[key]
	if !ok {
		return nil
	}
	if v == nil {
		*dst = nil
		return nil
	}
	n, err := toInt64(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	if nonNeg && n < 0 {
		return fmt.Errorf("%s must be >= 0", key)
	}
	dstVal := n
	*dst = &dstVal
	return nil
}

func optionalFloat64(fields map[string]any, key string, dst **float64) error {
	v, ok := fields[key]
	if !ok {
		return nil
	}
	if v == nil {
		*dst = nil
		return nil
	}
	f, err := toFloat64(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	if !floatIsFinite(f) {
		return fmt.Errorf("%s must be finite (not NaN/Inf)", key)
	}
	if f < 0 {
		return fmt.Errorf("%s must be >= 0", key)
	}
	dstVal := f
	*dst = &dstVal
	return nil
}

func toInt64(v any) (int64, error) {
	switch t := v.(type) {
	case int:
		return int64(t), nil
	case int64:
		return t, nil
	case uint64:
		return int64(t), nil
	case float64:
		if t != float64(int64(t)) {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(t), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

func toFloat64(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		if !floatIsFinite(t) {
			return 0, fmt.Errorf("non-finite float")
		}
		return t, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case string:
		s := strings.TrimSpace(t)
		lower := strings.ToLower(s)
		if lower == ".nan" || lower == "nan" || lower == "+.inf" || lower == "-.inf" ||
			lower == ".inf" || lower == "+inf" || lower == "-inf" || lower == "inf" {
			return 0, fmt.Errorf("non-finite float literal %q", s)
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		if !floatIsFinite(f) {
			return 0, fmt.Errorf("non-finite float")
		}
		return f, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

func parseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeOff, ModeRecord, ModeEnforce:
		return Mode(strings.ToLower(strings.TrimSpace(s))), nil
	default:
		return "", fmt.Errorf("invalid mode %q (want off|record|enforce)", s)
	}
}

// ApplyEnvOverrides applies SCHED_REQUIRED_CHECK_{1-7}_MODE and SCHED_REQUIRED_CHECKS_CONFIG is path-only.
func ApplyEnvOverrides(cfg Config) Config {
	for _, id := range AllCheckIDs {
		n := CheckNumber(id)
		key := fmt.Sprintf("SCHED_REQUIRED_CHECK_%d_MODE", n)
		if raw := os.Getenv(key); raw != "" {
			if m, err := parseMode(raw); err == nil {
				c := cfg.Checks[id]
				c.Mode = m
				cfg.Checks[id] = c
			}
			// Invalid env is caught in Validate via re-read below — apply only valid;
			// stash invalid by checking again in ValidateEnv.
		}
	}
	return cfg
}

// ValidateEnvModes fails if an override env is set to an invalid mode.
func ValidateEnvModes() error {
	for _, id := range AllCheckIDs {
		n := CheckNumber(id)
		key := fmt.Sprintf("SCHED_REQUIRED_CHECK_%d_MODE", n)
		raw := os.Getenv(key)
		if raw == "" {
			continue
		}
		if _, err := parseMode(raw); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

// Validate checks modes and threshold signs. Does not invent defaults for null thresholds.
func (c Config) Validate() error {
	if err := ValidateEnvModes(); err != nil {
		return err
	}
	if c.Version == "" {
		return fmt.Errorf("config version required")
	}
	for _, id := range AllCheckIDs {
		cc, ok := c.Checks[id]
		if !ok {
			return fmt.Errorf("missing check config for %s", id)
		}
		switch cc.Mode {
		case ModeOff, ModeRecord, ModeEnforce:
		default:
			return fmt.Errorf("check %s: invalid mode %q", id, cc.Mode)
		}
		if cc.MemorySafetyMarginMiB != nil && *cc.MemorySafetyMarginMiB < 0 {
			return fmt.Errorf("check %s: memory_safety_margin_mib < 0", id)
		}
		if cc.MaxAgeSeconds != nil && *cc.MaxAgeSeconds < 0 {
			return fmt.Errorf("check %s: max_age_seconds < 0", id)
		}
		if cc.MaxFutureSkewSeconds != nil && *cc.MaxFutureSkewSeconds < 0 {
			return fmt.Errorf("check %s: max_future_skew_seconds < 0", id)
		}
		for _, pair := range []struct {
			name string
			p    *float64
		}{
			{"max_gpu_temp_c", cc.MaxGPUTempC},
			{"max_gpu_power_w", cc.MaxGPUPowerW},
			{"max_server_power_w", cc.MaxServerPowerW},
		} {
			if pair.p == nil {
				continue
			}
			if !floatIsFinite(*pair.p) {
				return fmt.Errorf("check %s: %s must be finite", id, pair.name)
			}
			if *pair.p < 0 {
				return fmt.Errorf("check %s: %s must be >= 0", id, pair.name)
			}
		}
	}
	return nil
}

// ModeOf returns the mode for a check (default record if somehow missing).
func (c Config) ModeOf(id CheckID) Mode {
	if cc, ok := c.Checks[id]; ok {
		return cc.Mode
	}
	return ModeRecord
}

// MaxAge returns freshness TTL; nil means unset.
func (c Config) MaxAge(id CheckID) *time.Duration {
	cc := c.Checks[id]
	if cc.MaxAgeSeconds == nil {
		return nil
	}
	d := time.Duration(*cc.MaxAgeSeconds) * time.Second
	return &d
}
