package simulation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	smprng "smp/rng"
	"smp/simulation"
)

func TestLegacyRecoverySelectsNumericAccumulatedGeneration(t *testing.T) {
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName, metadata.DynamicsType = "legacy-numeric", simulation.DynamicsTypeHK
	metadata.NodeCount, metadata.NodeFollowCount = 18, 3
	metadata.RecsysFactoryType = "OpinionRandom"
	metadata.RNG = smprng.FixedSpec(731, 997)
	original := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{Quiet: true})
	if err := original.InitError(); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	serializer := simulation.NewSimulationSerializer(base, metadata.UniqueName, 2)
	if err := serializer.SaveMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	history := simulation.NewAccumulativeModelState()
	original.Model.Accumulate(history)
	for step := 1; step <= 10; step++ {
		original.Step()
		original.Model.Accumulate(history)
		if step == 2 || step == 10 {
			name := "acc-state-2.lz4"
			if step == 10 {
				name = "acc-state-10.lz4"
			}
			if err := simulation.SaveAccumulativeModelState(filepath.Join(base, metadata.UniqueName, name), history); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot, err := original.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := serializer.SaveRawSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	db, err := simulation.OpenEventDB(filepath.Join(base, metadata.UniqueName, "events.db"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, metadata.UniqueName, "trajectory.json")); !os.IsNotExist(err) {
		t.Fatal("fixture must exercise the legacy history path")
	}
	resumed := simulation.NewScenarioWithOptions(base, metadata, simulation.ScenarioOptions{EnableDumps: true, Quiet: true})
	t.Cleanup(func() { resumed.Close() })
	if !resumed.Load() {
		t.Fatal("legacy recovery selected step 2 rather than the complete step-10 history")
	}
	if resumed.AccState.Len() != 11 {
		t.Fatalf("legacy history has %d rows, want 11", resumed.AccState.Len())
	}
	for range 4 {
		original.Step()
		resumed.Step()
		if string(persistentReviewState(t, original)) != string(persistentReviewState(t, resumed)) {
			t.Fatal("legacy recovery changed physical state or RNG continuation")
		}
	}
}

func TestRecoveryRejectsAmbiguousAccumulatedGenerationsBeforeDecoding(t *testing.T) {
	for _, names := range [][]string{
		{"acc-state-2.lz4", "acc-state-02.lz4"},
		{"acc-state-2.lz4", "acc-state-2026-11-27T010000Z.lz4"},
		{"acc-state-2026-02-30T010000Z.lz4"},
	} {
		t.Run(strings.Join(names, "+"), func(t *testing.T) {
			base := t.TempDir()
			metadata := simulation.DefaultScenarioMetadata()
			metadata.UniqueName = "ambiguous-history"
			serializer := simulation.NewSimulationSerializer(base, metadata.UniqueName, 2)
			if err := serializer.SaveMetadata(metadata); err != nil {
				t.Fatal(err)
			}
			for _, name := range names {
				if err := os.WriteFile(filepath.Join(base, metadata.UniqueName, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := serializer.GetLatestAccumulativeState(); err == nil || !strings.Contains(err.Error(), "accumulated-state") {
				t.Fatalf("ambiguous generation reached history decoding: %v", err)
			}
		})
	}
}
