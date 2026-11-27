package workloadcontext

import "testing"

func TestParseOK(t *testing.T) {
	ctx, err := Parse(Input{
		ExperimentID:  "exp1",
		WorkloadID:    "wl1",
		TraceID:       "tr1",
		Model:         "meta-llama/Llama-3.1-8B-Instruct",
		Runtime:       "vllm",
		Accelerator:   "gpu",
		Precision:     "fp16",
		InputTokens:   256,
		OutputTokens:  128,
		ContextLength: 4096,
		Concurrency:   5,
		SloP99Ms:      2115,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.ProfileKey == "" || ctx.Confidence != ConfidenceSPEC {
		t.Fatalf("unexpected ctx: %+v", ctx)
	}
}

func TestParseInvalidSLO(t *testing.T) {
	_, err := Parse(Input{
		ExperimentID: "e", WorkloadID: "w", TraceID: "t",
		Model: "m", Runtime: "vllm", Concurrency: 1, SloP99Ms: -1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseAnnotationOverride(t *testing.T) {
	ctx, err := Parse(Input{
		ExperimentID: "e", WorkloadID: "w", TraceID: "t",
		Model: "m", Runtime: "vllm", Concurrency: 1, SloP99Ms: 1000,
		Annotations: map[string]string{
			"profile.ai/slo-p99-ms": "2115",
			"profile.ai/model":      "furiosa-ai/Llama-3.1-8B-Instruct",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.SloP99Ms != 2115 || ctx.Model != "furiosa-ai/Llama-3.1-8B-Instruct" {
		t.Fatalf("override failed: %+v", ctx)
	}
}

func TestParseMissingModelFallback(t *testing.T) {
	ctx, err := Parse(Input{
		ExperimentID: "e", WorkloadID: "w", TraceID: "t",
		Runtime: "vllm", Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Confidence != ConfidenceFALLBACK {
		t.Fatalf("want FALLBACK got %s", ctx.Confidence)
	}
}

func TestParseBadAnnotationNumber(t *testing.T) {
	_, err := Parse(Input{
		ExperimentID: "e", WorkloadID: "w", TraceID: "t",
		Model: "m", Runtime: "vllm", Concurrency: 1,
		Annotations: map[string]string{"profile.ai/slo-p99-ms": "abc"},
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
}
