package simulation_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/record"
	smprng "smp/rng"
	"smp/simulation"
	"smp/trajectory"
)

func TestNativeRecordReadsCompletedWALEventHistory(t *testing.T) {
	dir := t.TempDir()
	if root := os.Getenv("SMP_EVENT_DB_TEST_DIR"); root != "" {
		var err error
		dir, err = os.MkdirTemp(root, "smp-wal-record-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Error(err)
			}
		})
	}
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "wal-native-reader"
	metadata.DynamicsType = simulation.DynamicsTypeHK
	metadata.HKParams = dynamics.HKParams{Tolerance: .45, Influence: .1, RewiringRate: .1, RepostRate: .1}
	metadata.NodeCount, metadata.NodeFollowCount = 12, 3
	metadata.MaxSimulationStep = 8
	metadata.RNG = smprng.FixedSpec(10, 20)
	metadata.RecSysParams = map[string]any{"LogRecommendations": false}
	metadata.TrajectoryPrecision = trajectory.Precision{Opinions: "float64", OpinionSums: "float64"}
	scenario := simulation.NewScenarioWithOptions(dir, metadata, simulation.ScenarioOptions{EnableDumps: true, Quiet: true, FixedHorizon: true})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	result := scenario.StepTillEndResult(context.Background())
	if !result.Completed || result.Step != 8 {
		t.Fatalf("fixture incomplete: %+v", result)
	}
	final := scenario.Model.GetOpinions()
	if err := scenario.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := record.OpenRun(filepath.Join(dir, metadata.UniqueName), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	states := []record.State{}
	if err := reader.ReadStates([]int{0, 4, 8}, record.Requirements{Graph: true, Posts: true}, func(state record.State) error {
		states = append(states, state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(states) != 3 || !reflect.DeepEqual(states[2].Opinions, final) || len(states[2].Posts) != metadata.NodeCount {
		t.Fatal("native reader lost completed opinions or post history")
	}
}
