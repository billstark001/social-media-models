package simulation

import (
	"path/filepath"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
)

func TestTrajectoryResumeTruncatesOnlyUncheckpointedBlocks(t *testing.T) {
	metadata := DefaultScenarioMetadata()
	metadata.UniqueName = "resume-chunks"
	metadata.DynamicsType = DynamicsTypeHK
	metadata.HKParams = dynamics.HKParams{Tolerance: 0.4, Influence: 0.1, RewiringRate: 0.1, RepostRate: 0.1}
	metadata.NodeCount = 20
	metadata.NodeFollowCount = 5
	metadata.MaxSimulationStep = 300
	metadata.RNG = smprng.FixedSpec(3, 4)
	metadata.CollectItemOptions = model.CollectItemOptions{RewiringEvent: true, PostEvent: true}
	dir := t.TempDir()
	first := NewScenarioWithOptions(dir, metadata, ScenarioOptions{EnableDumps: true, Quiet: true})
	if err := first.InitError(); err != nil {
		t.Fatal(err)
	}
	first.Step()
	first.Step()
	first.Dump()
	if first.Model.GetCurStep() != 3 {
		t.Fatal("wrong checkpoint step")
	}
	for range 260 {
		first.Step()
	}
	if first.Trajectory.Manifest.NextStep <= 3 {
		t.Fatal("test did not create an uncheckpointed block")
	}
	if err := first.DB.Close(); err != nil {
		t.Fatal(err)
	}
	resumed := NewScenarioWithOptions(dir, metadata, ScenarioOptions{EnableDumps: true, Quiet: true})
	if !resumed.Load() {
		t.Fatal("failed to resume chunked trajectory")
	}
	defer resumed.DB.Close()
	if resumed.Model.GetCurStep() != 3 || resumed.Trajectory.Manifest.NextStep != 3 {
		t.Fatalf("resume alignment: model %d, trajectory %d", resumed.Model.GetCurStep(), resumed.Trajectory.Manifest.NextStep)
	}
	chunk, err := LoadTrajectoryChunk(filepath.Join(dir, metadata.UniqueName, resumed.Trajectory.Manifest.Chunks[len(resumed.Trajectory.Manifest.Chunks)-1].File))
	if err != nil || len(chunk.Opinions) == 0 {
		t.Fatalf("retained checkpoint block invalid: %v", err)
	}
	resumed.Step()
	resumed.Dump()
	if err := resumed.Trajectory.Verify(); err != nil {
		t.Fatal(err)
	}
}
