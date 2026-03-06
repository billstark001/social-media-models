package simulation_test

import (
	"os"
	"path/filepath"
	"testing"

	"smp/simulation"
)

// ---- AccumulativeModelState serialization tests ----

func makeTestAccState(steps, agents int) *simulation.AccumulativeModelState {
	s := simulation.NewAccumulativeModelState()
	for step := range steps {
		opRow := make([]float32, agents)
		numRow := make([][4]int16, agents)
		sumRow := make([][4]float32, agents)
		for a := range agents {
			opRow[a] = float32(step)*0.1 + float32(a)*0.01
			numRow[a] = [4]int16{int16(step), int16(a), int16(step + a), 0}
			sumRow[a] = [4]float32{float32(step), float32(a), 0, float32(step + a)}
		}
		s.Opinions = append(s.Opinions, opRow)
		s.AgentNumbers = append(s.AgentNumbers, numRow)
		s.AgentOpinionSums = append(s.AgentOpinionSums, sumRow)
	}
	return s
}

func TestSaveLoadAccumulativeModelState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acc_state_test.lz4")

	original := makeTestAccState(10, 20)
	if err := simulation.SaveAccumulativeModelState(path, original); err != nil {
		t.Fatalf("SaveAccumulativeModelState: %v", err)
	}

	loaded, err := simulation.LoadAccumulativeModelState(path)
	if err != nil {
		t.Fatalf("LoadAccumulativeModelState: %v", err)
	}

	if len(loaded.Opinions) != len(original.Opinions) {
		t.Fatalf("Opinions steps: got %d, want %d", len(loaded.Opinions), len(original.Opinions))
	}
	for step := range original.Opinions {
		for a := range original.Opinions[step] {
			if loaded.Opinions[step][a] != original.Opinions[step][a] {
				t.Errorf("Opinions[%d][%d]: got %v, want %v",
					step, a, loaded.Opinions[step][a], original.Opinions[step][a])
			}
			if loaded.AgentNumbers[step][a] != original.AgentNumbers[step][a] {
				t.Errorf("AgentNumbers[%d][%d]: got %v, want %v",
					step, a, loaded.AgentNumbers[step][a], original.AgentNumbers[step][a])
			}
			for k := range 4 {
				if loaded.AgentOpinionSums[step][a][k] != original.AgentOpinionSums[step][a][k] {
					t.Errorf("AgentOpinionSums[%d][%d][%d]: got %v, want %v",
						step, a, k,
						loaded.AgentOpinionSums[step][a][k],
						original.AgentOpinionSums[step][a][k])
				}
			}
		}
	}
}

func TestSaveAccumulativeModelStateEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.lz4")

	empty := simulation.NewAccumulativeModelState()
	err := simulation.SaveAccumulativeModelState(path, empty)
	if err == nil {
		t.Error("expected error for empty state, got nil")
	}
}

// ---- SimulationSerializer tests ----

func TestSimulationSerializerExists(t *testing.T) {
	dir := t.TempDir()
	ser := simulation.NewSimulationSerializer(dir, "test-sim", 3)

	if ser.Exists() {
		t.Error("serializer should not exist before any data is written")
	}

	meta := &simulation.ScenarioMetadata{UniqueName: "test-sim"}
	if err := ser.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	if !ser.Exists() {
		t.Error("serializer should exist after SaveMetadata")
	}
}

func TestSimulationSerializerMetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ser := simulation.NewSimulationSerializer(dir, "meta-test", 5)

	original := &simulation.ScenarioMetadata{
		UniqueName:        "meta-test",
		MaxSimulationStep: 200,
		NodeCount:         50,
		NodeFollowCount:   5,
		RecsysFactoryType: "Random",
		NetworkType:       "Random",
	}

	if err := ser.SaveMetadata(original); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	loaded, err := ser.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.UniqueName != original.UniqueName {
		t.Errorf("UniqueName: got %q, want %q", loaded.UniqueName, original.UniqueName)
	}
	if loaded.MaxSimulationStep != original.MaxSimulationStep {
		t.Errorf("MaxSimulationStep: got %d, want %d", loaded.MaxSimulationStep, original.MaxSimulationStep)
	}
	if loaded.NodeCount != original.NodeCount {
		t.Errorf("NodeCount: got %d, want %d", loaded.NodeCount, original.NodeCount)
	}
}

func TestSimulationSerializerFinishMark(t *testing.T) {
	dir := t.TempDir()
	ser := simulation.NewSimulationSerializer(dir, "finish-test", 5)

	// Create dir by saving metadata
	meta := &simulation.ScenarioMetadata{UniqueName: "finish-test"}
	_ = ser.SaveMetadata(meta)

	finished, err := ser.IsFinished()
	if err != nil {
		t.Fatalf("IsFinished (before mark): %v", err)
	}
	if finished {
		t.Error("should not be finished before marking")
	}

	if err := ser.MarkFinished(); err != nil {
		t.Fatalf("MarkFinished: %v", err)
	}

	finished, err = ser.IsFinished()
	if err != nil {
		t.Fatalf("IsFinished (after mark): %v", err)
	}
	if !finished {
		t.Error("should be finished after marking")
	}
}

func TestSimulationSerializerAccStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ser := simulation.NewSimulationSerializer(dir, "acc-test", 5)
	meta := &simulation.ScenarioMetadata{UniqueName: "acc-test"}
	_ = ser.SaveMetadata(meta)

	acc := makeTestAccState(5, 10)
	if err := ser.SaveAccumulativeState(acc); err != nil {
		t.Fatalf("SaveAccumulativeState: %v", err)
	}

	loaded, err := ser.GetLatestAccumulativeState()
	if err != nil {
		t.Fatalf("GetLatestAccumulativeState: %v", err)
	}
	if len(loaded.Opinions) != 5 {
		t.Errorf("expected 5 steps, got %d", len(loaded.Opinions))
	}
}

func TestSimulationSerializerCleanOldFiles(t *testing.T) {
	dir := t.TempDir()
	ser := simulation.NewSimulationSerializer(dir, "clean-test", 2)
	meta := &simulation.ScenarioMetadata{UniqueName: "clean-test"}
	_ = ser.SaveMetadata(meta)

	// Save 4 acc states; only 2 should be retained
	for range 4 {
		acc := makeTestAccState(3, 5)
		if err := ser.SaveAccumulativeState(acc); err != nil {
			t.Fatalf("SaveAccumulativeState: %v", err)
		}
	}

	// Count .lz4 files in the simulation directory
	simDir := filepath.Join(dir, "clean-test")
	entries, _ := os.ReadDir(simDir)
	lz4Count := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".lz4" {
			lz4Count++
		}
	}
	if lz4Count > 2 {
		t.Errorf("expected at most 2 acc-state files, found %d", lz4Count)
	}
}
