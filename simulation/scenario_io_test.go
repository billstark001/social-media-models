package simulation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/progress"
	"smp/simulation"
)

// captureStdout redirects os.Stdout while fn runs and returns the captured output.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func decodeProgressEvents(t *testing.T, output string) []progress.Event {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	events := make([]progress.Event, 0, len(lines))
	for _, line := range lines {
		var event progress.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSON progress line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func TestJSONProgressOutput(t *testing.T) {
	metadata := &simulation.ScenarioMetadata{
		DynamicsType: simulation.DynamicsTypeHK,
		HKParams: dynamics.HKParams{
			Influence:    0.01,
			Tolerance:    0.45,
			RewiringRate: 0.05,
			RepostRate:   0.3,
		},
		SMPModelPureParams: model.SMPModelPureParams{
			PostRetainCount: 3,
			RecsysCount:     5,
		},
		CollectItemOptions: model.CollectItemOptions{},
		RecsysFactoryType:  "Random",
		NetworkType:        "Random",
		NodeCount:          50,
		NodeFollowCount:    5,
		MaxSimulationStep:  200,
		UniqueName:         "io-test",
	}

	basePath := path.Join(os.TempDir(), "test_smp_io")
	os.RemoveAll(basePath)
	if err := os.MkdirAll(basePath, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	scenario := simulation.NewScenarioWithOptions(basePath, metadata, simulation.ScenarioOptions{
		OutputJSONProgress: true,
		EnableDumps:        false,
	})
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	events := decodeProgressEvents(t, output)
	if len(events) == 0 {
		t.Fatalf("Expected stdout output, got nothing")
	}

	var hasStart, hasDone, hasRNG bool
	for _, event := range events {
		if event.Version != progress.Version || event.RequestID != metadata.UniqueName {
			t.Errorf("unexpected progress envelope: %+v", event)
		}
		switch event.Type {
		case progress.TypeRNG:
			hasRNG = event.Algorithm != "" && event.Seed1 != "" && event.Seed2 != ""
		case progress.TypeStart:
			hasStart = true
			if event.Step != 1 || event.MaxStep != metadata.MaxSimulationStep {
				t.Errorf("unexpected start event: %+v", event)
			}
		case progress.TypeDone:
			hasDone = true
			if event.StopReason != "horizon" && event.StopReason != "halt" {
				t.Errorf("unrecognized stop reason: %+v", event)
			}
		case progress.TypeProgress:
			if event.MaxStep != metadata.MaxSimulationStep {
				t.Errorf("unexpected progress event: %+v", event)
			}
		}
	}

	if !hasRNG || !hasStart {
		t.Errorf("Expected RNG and start lines in stdout output; got:\n%s", output)
	}
	if !hasDone {
		t.Errorf("Expected a DONE line in stdout output; got:\n%s", output)
	}
}

func TestJSONProgressOutputDeffuant(t *testing.T) {
	metadata := &simulation.ScenarioMetadata{
		DynamicsType: simulation.DynamicsTypeDeffuant,
		DeffuantParams: dynamics.DeffuantParams{
			Influence:    0.5,
			Tolerance:    0.3,
			RewiringRate: 0.05,
			RepostRate:   0.3,
		},
		SMPModelPureParams: model.SMPModelPureParams{
			PostRetainCount: 3,
			RecsysCount:     5,
		},
		RecsysFactoryType: "Random",
		NetworkType:       "Random",
		NodeCount:         50,
		NodeFollowCount:   5,
		MaxSimulationStep: 200,
		UniqueName:        "io-test-deffuant",
	}

	basePath := path.Join(os.TempDir(), "test_smp_io_deffuant")
	os.RemoveAll(basePath)
	if err := os.MkdirAll(basePath, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	scenario := simulation.NewScenarioWithOptions(basePath, metadata, simulation.ScenarioOptions{
		OutputJSONProgress: true,
		EnableDumps:        false,
	})
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	events := decodeProgressEvents(t, output)
	if events[1].Type != progress.TypeStart || events[len(events)-1].Type != progress.TypeDone {
		t.Errorf("Deffuant: expected start/done events; got:\n%s", output)
	}
}

func TestJSONProgressOutputGalam(t *testing.T) {
	metadata := &simulation.ScenarioMetadata{
		DynamicsType: simulation.DynamicsTypeGalam,
		GalamParams: dynamics.GalamParams{
			RewiringRate: 0.05,
			RepostRate:   0.3,
		},
		SMPModelPureParams: model.SMPModelPureParams{
			PostRetainCount: 3,
			RecsysCount:     5,
		},
		RecsysFactoryType: "Random",
		NetworkType:       "Random",
		NodeCount:         50,
		NodeFollowCount:   5,
		MaxSimulationStep: 200,
		UniqueName:        "io-test-galam",
	}

	basePath := path.Join(os.TempDir(), "test_smp_io_galam")
	os.RemoveAll(basePath)
	if err := os.MkdirAll(basePath, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	scenario := simulation.NewScenarioWithOptions(basePath, metadata, simulation.ScenarioOptions{
		OutputJSONProgress: true,
		EnableDumps:        false,
	})
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	events := decodeProgressEvents(t, output)
	if events[1].Type != progress.TypeStart || events[len(events)-1].Type != progress.TypeDone {
		t.Errorf("Galam: expected start/done events; got:\n%s", output)
	}
}

func TestJSONProgressOutputVoter(t *testing.T) {
	metadata := &simulation.ScenarioMetadata{
		DynamicsType: simulation.DynamicsTypeVoter,
		VoterParams: dynamics.VoterParams{
			RewiringRate: 0.05,
			RepostRate:   0.3,
		},
		SMPModelPureParams: model.SMPModelPureParams{
			PostRetainCount: 3,
			RecsysCount:     5,
		},
		RecsysFactoryType: "Random",
		NetworkType:       "Random",
		NodeCount:         50,
		NodeFollowCount:   5,
		MaxSimulationStep: 200,
		UniqueName:        "io-test-voter",
	}

	basePath := path.Join(os.TempDir(), "test_smp_io_voter")
	os.RemoveAll(basePath)
	if err := os.MkdirAll(basePath, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	scenario := simulation.NewScenarioWithOptions(basePath, metadata, simulation.ScenarioOptions{
		OutputJSONProgress: true,
		EnableDumps:        false,
	})
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	events := decodeProgressEvents(t, output)
	if events[1].Type != progress.TypeStart || events[len(events)-1].Type != progress.TypeDone {
		t.Errorf("Voter: expected start/done events; got:\n%s", output)
	}
}

func TestNoDumpScenarioDoesNoIOOrHistory(t *testing.T) {
	basePath := path.Join(t.TempDir(), "must-not-exist")
	metadata := makeValidMetadata()
	metadata.UniqueName = "no-io"
	metadata.MaxSimulationStep = 5

	scenario := simulation.NewScenarioWithOptions(basePath, metadata, simulation.ScenarioOptions{
		EnableDumps: false,
		Quiet:       true,
	})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	result := scenario.StepTillEndResult(context.Background())

	if !result.Completed {
		t.Fatalf("run did not complete: %+v", result)
	}
	if scenario.AccState != nil || scenario.Serializer != nil || scenario.DB != nil {
		t.Fatalf(
			"no-I/O state allocated persistence: acc=%v serializer=%v db=%v",
			scenario.AccState != nil,
			scenario.Serializer != nil,
			scenario.DB != nil,
		)
	}
	if _, err := os.Stat(basePath); !os.IsNotExist(err) {
		t.Fatalf("no-I/O run touched %q: %v", basePath, err)
	}
}

func TestNoDumpScenarioMatchesPersistentRun(t *testing.T) {
	fullMetadata := makeValidMetadata()
	fullMetadata.UniqueName = "full-equivalence"
	fullMetadata.MaxSimulationStep = 20
	noIOMetadata := *fullMetadata
	noIOMetadata.UniqueName = "no-io-equivalence"

	full := simulation.NewScenarioWithOptions(t.TempDir(), fullMetadata, simulation.ScenarioOptions{
		EnableDumps: true,
		Quiet:       true,
	})
	if err := full.InitError(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if full.DB != nil {
			_ = full.DB.Close()
		}
	})
	noIO := simulation.NewScenarioWithOptions(
		path.Join(t.TempDir(), "must-not-exist"),
		&noIOMetadata,
		simulation.ScenarioOptions{EnableDumps: false, Quiet: true},
	)
	if err := noIO.InitError(); err != nil {
		t.Fatal(err)
	}

	fullResult := full.StepTillEndResult(context.Background())
	noIOResult := noIO.StepTillEndResult(context.Background())
	if fullResult != noIOResult {
		t.Fatalf("run summaries differ: full=%+v no_io=%+v", fullResult, noIOResult)
	}
	if got, want := noIO.Model.GetOpinions(), full.Model.GetOpinions(); !reflect.DeepEqual(got, want) {
		t.Fatal("final opinions differ between persistent and no-I/O runs")
	}
}
