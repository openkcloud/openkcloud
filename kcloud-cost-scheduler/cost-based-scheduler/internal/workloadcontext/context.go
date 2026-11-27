package workloadcontext

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	ConfidenceSPEC      = "SPEC"
	ConfidenceMEASURED  = "MEASURED"
	ConfidenceREFERENCE = "REFERENCE"
	ConfidenceFALLBACK  = "FALLBACK"
)

// Context is the shared WorkloadContext contract (Python/Go).
type Context struct {
	ProfileKey      string         `json:"profile_key"`
	WorkloadID      string         `json:"workload_id"`
	TraceID         string         `json:"trace_id"`
	ExperimentID    string         `json:"experiment_id"`
	Task            string         `json:"task"`
	Model           string         `json:"model"`
	Runtime         string         `json:"runtime"`
	Precision       string         `json:"precision"`
	InputTokens     int            `json:"input_tokens"`
	OutputTokens    int            `json:"output_tokens"`
	ContextLength   int            `json:"context_length"`
	Batch           int            `json:"batch"`
	Concurrency     int            `json:"concurrency"`
	SloP99Ms        float64        `json:"slo_p99_ms"`
	Accelerator     string         `json:"accelerator"`
	Confidence      string         `json:"confidence"`
	RoutingPolicy   string         `json:"routing_policy"`
	DecisionReason  string         `json:"decision_reason"`
	AnnotationsHash string         `json:"annotations_hash"`
	CreatedAt       string         `json:"created_at"`
	Extras          map[string]any `json:"extras"`
	EnvironmentKey  string         `json:"environment_key,omitempty"`
}

type Input struct {
	ExperimentID   string
	WorkloadID     string
	TraceID        string
	Model          string
	Runtime        string
	Accelerator    string
	Precision      string
	InputTokens    int
	OutputTokens   int
	ContextLength  int
	Concurrency    int
	SloP99Ms       float64
	RoutingPolicy  string
	DecisionReason string
	Confidence     string
	Annotations    map[string]string
}

func BuildProfileKey(model, runtime, accelerator, precision string, inTok, outTok, ctxLen, concurrency int) string {
	raw := strings.Join([]string{
		nz(model, "unknown"),
		nz(runtime, "unknown"),
		nz(accelerator, "unknown"),
		nz(precision, "unknown"),
		strconv.Itoa(inTok),
		strconv.Itoa(outTok),
		strconv.Itoa(ctxLen),
		strconv.Itoa(concurrency),
	}, "|")
	sum := sha1.Sum([]byte(raw))
	digest := hex.EncodeToString(sum[:])[:12]
	safe := strings.ReplaceAll(nz(model, "unknown"), "/", "_")
	return fmt.Sprintf("%s:%s:%s:c%d:%s", safe, nz(runtime, "unknown"), nz(accelerator, "unknown"), concurrency, digest)
}

func Parse(in Input) (*Context, error) {
	if in.SloP99Ms < 0 {
		return nil, fmt.Errorf("slo_p99_ms must be >= 0")
	}
	if in.Concurrency < 1 {
		return nil, fmt.Errorf("concurrency must be >= 1")
	}
	if in.InputTokens < 0 || in.OutputTokens < 0 {
		return nil, fmt.Errorf("token counts must be >= 0")
	}

	model := in.Model
	precision := nz(in.Precision, "fp16")
	slo := in.SloP99Ms
	ann := in.Annotations
	if ann == nil {
		ann = map[string]string{}
	}
	if v, ok := ann["profile.ai/slo-p99-ms"]; ok && v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid profile.ai/slo-p99-ms: %w", err)
		}
		slo = f
	}
	if v, ok := ann["profile.ai/model"]; ok && v != "" {
		model = v
	}
	if v, ok := ann["profile.ai/precision"]; ok && v != "" {
		precision = v
	}

	conf := nz(in.Confidence, ConfidenceSPEC)
	if model == "" {
		conf = ConfidenceFALLBACK
	}
	annHash := ""
	if len(ann) > 0 {
		b, _ := json.Marshal(ann)
		sum := sha1.Sum(b)
		annHash = hex.EncodeToString(sum[:])[:16]
	}

	accel := nz(in.Accelerator, "gpu")
	runtime := nz(in.Runtime, "unknown")
	return &Context{
		ProfileKey:      BuildProfileKey(model, runtime, accel, precision, in.InputTokens, in.OutputTokens, in.ContextLength, in.Concurrency),
		WorkloadID:      in.WorkloadID,
		TraceID:         in.TraceID,
		ExperimentID:    in.ExperimentID,
		Task:            "serving",
		Model:           model,
		Runtime:         runtime,
		Precision:       precision,
		InputTokens:     in.InputTokens,
		OutputTokens:    in.OutputTokens,
		ContextLength:   in.ContextLength,
		Batch:           1,
		Concurrency:     in.Concurrency,
		SloP99Ms:        slo,
		Accelerator:     accel,
		Confidence:      conf,
		RoutingPolicy:   in.RoutingPolicy,
		DecisionReason:  nz(in.DecisionReason, "experimental_assignment"),
		AnnotationsHash: annHash,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Extras:          map[string]any{},
	}, nil
}

func nz(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
