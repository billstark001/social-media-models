package batch

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"smp/simulation"
)

func TestCheckpointResumeAndBranches(t *testing.T) {
	metadata := testMetadata("parent")
	metadata.CheckpointSteps = []int{0}
	dir := t.TempDir()
	parent := simulation.NewScenarioWithOptions(dir, metadata, simulation.ScenarioOptions{EnableDumps: true, Quiet: true})
	if err := parent.InitError(); err != nil {
		t.Fatal(err)
	}
	defer parent.DB.Close()
	path := filepath.Join(dir, "parent", "checkpoint-000000000.msgpack.lz4")
	parent.Step()
	want := parent.Model.GetOpinions()
	request := Request{SchemaVersion: SchemaVersion, RequestID: "resume", Experiment: &ExperimentSpec{Checkpoint: path, Mode: "resume", Replicates: 1, ObserveAt: []int{1}}, Output: OutputOptions{FullState: true}}
	resume, err := runExperiment(context.Background(), metadata, request)
	if err != nil {
		t.Fatal(err)
	}
	got := resume.Replicates[0].Endpoints[0].Opinions
	if !reflect.DeepEqual(got, want) {
		t.Fatal("resume did not reproduce exact next opinion row")
	}
	request.Experiment.Mode = "branch"
	request.Experiment.Replicates = 3
	first, err := runExperiment(context.Background(), metadata, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runExperiment(context.Background(), metadata, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("branch seeds or outputs are not stable")
	}
	if first.Replicates[0].RNG == first.Replicates[1].RNG {
		t.Fatal("branches reused an RNG specification")
	}
}
