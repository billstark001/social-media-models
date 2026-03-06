package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"smp/dynamics"
	"smp/model"
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

func TestParsableProgressOutput(t *testing.T) {
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

	scenario := simulation.NewScenario(basePath, metadata, true)
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		t.Fatalf("Expected stdout output, got nothing")
	}

	taskPrefix := fmt.Sprintf("TASK:%s;", metadata.UniqueName)

	var hasInit, hasDone bool
	for _, line := range lines {
		if !strings.HasPrefix(line, taskPrefix) {
			t.Errorf("Line missing task prefix %q: %s", taskPrefix, line)
		}
		if strings.Contains(line, "TYPE:INIT") {
			hasInit = true
			// Verify format: TASK:<name>;TYPE:INIT;STEP:<n>;
			expected := fmt.Sprintf("%sTYPE:INIT;STEP:", taskPrefix)
			if !strings.HasPrefix(line, expected) {
				t.Errorf("INIT line has unexpected format: %s", line)
			}
		}
		if strings.Contains(line, "TYPE:DONE") {
			hasDone = true
			// Verify DONE line contains DONE_TYPE
			if !strings.Contains(line, "DONE_TYPE:") {
				t.Errorf("DONE line missing DONE_TYPE field: %s", line)
			}
			// DONE_TYPE must be one of SIG, ITER, HALT
			validDoneType := strings.Contains(line, "DONE_TYPE:SIG") ||
				strings.Contains(line, "DONE_TYPE:ITER") ||
				strings.Contains(line, "DONE_TYPE:HALT")
			if !validDoneType {
				t.Errorf("DONE line has unrecognised DONE_TYPE: %s", line)
			}
		}
		if strings.Contains(line, "TYPE:PROGRESS") {
			expected := fmt.Sprintf("%sTYPE:PROGRESS;STEP:", taskPrefix)
			if !strings.HasPrefix(line, expected) {
				t.Errorf("PROGRESS line has unexpected format: %s", line)
			}
		}
	}

	if !hasInit {
		t.Errorf("Expected an INIT line in stdout output; got:\n%s", output)
	}
	if !hasDone {
		t.Errorf("Expected a DONE line in stdout output; got:\n%s", output)
	}
}

func TestParsableProgressOutputDeffuant(t *testing.T) {
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

	scenario := simulation.NewScenario(basePath, metadata, true)
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	if !strings.Contains(output, "TYPE:INIT") {
		t.Errorf("Deffuant: expected INIT line; got:\n%s", output)
	}
	if !strings.Contains(output, "TYPE:DONE") {
		t.Errorf("Deffuant: expected DONE line; got:\n%s", output)
	}
}

func TestParsableProgressOutputGalam(t *testing.T) {
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

	scenario := simulation.NewScenario(basePath, metadata, true)
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	if !strings.Contains(output, "TYPE:INIT") {
		t.Errorf("Galam: expected INIT line; got:\n%s", output)
	}
	if !strings.Contains(output, "TYPE:DONE") {
		t.Errorf("Galam: expected DONE line; got:\n%s", output)
	}
}

func TestParsableProgressOutputVoter(t *testing.T) {
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

	scenario := simulation.NewScenario(basePath, metadata, true)
	scenario.Init()

	output := captureStdout(func() {
		scenario.StepTillEnd(context.Background())
	})

	if !strings.Contains(output, "TYPE:INIT") {
		t.Errorf("Voter: expected INIT line; got:\n%s", output)
	}
	if !strings.Contains(output, "TYPE:DONE") {
		t.Errorf("Voter: expected DONE line; got:\n%s", output)
	}
}
