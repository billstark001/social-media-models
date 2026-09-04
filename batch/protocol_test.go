package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
)

func testMetadata(name string) *simulation.ScenarioMetadata {
	return &simulation.ScenarioMetadata{
		DataVersion:       simulation.CurrentDataVersion,
		RNG:               smprng.FixedSpec(1, 2),
		UniqueName:        name,
		DynamicsType:      simulation.DynamicsTypeHK,
		HKParams:          *dynamics.DefaultHKParams(),
		MaxSimulationStep: 4,
		RecsysFactoryType: "Random",
		NetworkType:       "Random",
		NodeCount:         20,
		NodeFollowCount:   5,
		SMPModelPureParams: model.SMPModelPureParams{
			RecsysCount:     10,
			PostRetainCount: 3,
		},
	}
}

func requestLineWithOutput(t *testing.T, requestID string, output OutputOptions) string {
	t.Helper()
	metadata, err := json.Marshal(testMetadata(requestID))
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(Request{
		SchemaVersion: SchemaVersion,
		RequestID:     requestID,
		Metadata:      metadata,
		Output:        output,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(request)
}

func requestLine(t *testing.T, requestID string, includeOpinions bool) string {
	t.Helper()
	return requestLineWithOutput(
		t,
		requestID,
		OutputOptions{FinalOpinions: includeOpinions},
	)
}

func decodeResponses(t *testing.T, output string) []Response {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	responses := make([]Response, 0, len(lines))
	for _, line := range lines {
		var response Response
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode response %q: %v", line, err)
		}
		responses = append(responses, response)
	}
	return responses
}

func TestRunReturnsCompactSummary(t *testing.T) {
	input := strings.NewReader(requestLine(t, "compact", false) + "\n")
	var output bytes.Buffer
	if err := Run(context.Background(), input, &output, Options{}); err != nil {
		t.Fatal(err)
	}
	responses := decodeResponses(t, output.String())
	if len(responses) != 1 || responses[0].Status != "ok" || responses[0].Result == nil {
		t.Fatalf("unexpected response: %+v", responses)
	}
	result := responses[0].Result
	if result.Opinions.Count != 20 || len(result.FinalOpinions) != 0 {
		t.Fatalf("unexpected compact result: %+v", result)
	}
	if len(result.ProtocolSHA256) != 64 || result.RNG.IsZero() {
		t.Fatalf("missing provenance: %+v", result)
	}
}

func TestRunCanReturnFinalOpinions(t *testing.T) {
	input := strings.NewReader(requestLine(t, "opinions", true) + "\n")
	var output bytes.Buffer
	if err := Run(context.Background(), input, &output, Options{}); err != nil {
		t.Fatal(err)
	}
	response := decodeResponses(t, output.String())[0]
	if response.Result == nil || len(response.Result.FinalOpinions) != 20 {
		t.Fatalf("final opinions missing: %+v", response)
	}
}

func TestRunCanClassifyTerminalMeasure(t *testing.T) {
	input := strings.NewReader(requestLineWithOutput(t, "terminal", OutputOptions{
		Terminal: &TerminalOptions{MajorMass: 0.02},
	}) + "\n")
	var output bytes.Buffer
	if err := Run(context.Background(), input, &output, Options{}); err != nil {
		t.Fatal(err)
	}
	response := decodeResponses(t, output.String())[0]
	if response.Result == nil || response.Result.Terminal == nil {
		t.Fatalf("terminal classification missing: %+v", response)
	}
	if response.Result.Terminal.Category == "" || response.Result.Terminal.Status == "" {
		t.Fatalf("incomplete terminal classification: %+v", response.Result.Terminal)
	}
}

func TestRunRecoversAfterInvalidRequest(t *testing.T) {
	input := strings.NewReader("{not-json}\n" + requestLine(t, "valid", false) + "\n")
	var output bytes.Buffer
	if err := Run(context.Background(), input, &output, Options{}); err != nil {
		t.Fatal(err)
	}
	responses := decodeResponses(t, output.String())
	if len(responses) != 2 || responses[0].Status != "error" || responses[1].Status != "ok" {
		t.Fatalf("batch did not recover: %+v", responses)
	}
}

func TestRunRejectsMissingOrUnsupportedSchemaVersion(t *testing.T) {
	valid := requestLine(t, "valid", false)
	missing := strings.Replace(valid, `"schema_version":1,`, "", 1)
	unsupported := strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1)
	input := strings.NewReader(missing + "\n" + unsupported + "\n" + valid + "\n")
	var output bytes.Buffer
	if err := Run(context.Background(), input, &output, Options{}); err != nil {
		t.Fatal(err)
	}
	responses := decodeResponses(t, output.String())
	if len(responses) != 3 || responses[0].Status != "error" ||
		responses[1].Status != "error" || responses[2].Status != "ok" {
		t.Fatalf("unexpected version responses: %+v", responses)
	}
}

func TestRunEmitsJSONLProgress(t *testing.T) {
	input := strings.NewReader(requestLine(t, "progress", false) + "\n")
	var output bytes.Buffer
	var progress bytes.Buffer
	if err := Run(context.Background(), input, &output, Options{
		Progress:             &progress,
		ProgressMode:         "jsonl",
		ProgressStepInterval: 2,
	}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(progress.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected start/progress/done events, got %q", progress.String())
	}
	for _, line := range lines {
		var event ProgressEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid progress event %q: %v", line, err)
		}
		if event.Version != 1 || event.RequestID != "progress" {
			t.Fatalf("wrong progress request id: %+v", event)
		}
	}
}
