package batch

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"

	"github.com/vmihailenco/msgpack/v5"
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

func TestPairedPolicyChangeRestoresCheckpointAndMeasuresDisplayedFeed(t *testing.T) {
	ctx := context.Background()
	metadata := testMetadata("staged-fixture")
	metadata.RecsysFactoryType = "OpinionM9"
	metadata.PostRetainCount = 0
	metadata.RecSysParams = map[string]any{"LogRecommendations": false}
	path := filepath.Join(t.TempDir(), "state.msgpack.lz4")
	output := OutputOptions{FullState: true, Pathway: true, FinalOpinions: true}
	reference, err := runExperiment(ctx, metadata, Request{
		Experiment: &ExperimentSpec{Mode: "fresh", Replicates: 1, ObserveAt: []int{0, 1, 2, 3}, CheckpointAt: map[int]string{1: path}},
		Output:     output,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := ExperimentSpec{Mode: "resume", Replicates: 1, Checkpoint: path, ObserveAt: []int{0, 1, 2}}
	hold, err := runExperiment(ctx, metadata, Request{Experiment: &spec, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	start := 0
	spec.PolicyChange = &PolicyChange{Recommender: "StructureM9", At: &start}
	switched, err := runExperiment(ctx, metadata, Request{Experiment: &spec, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	origin, control, treatment := reference.Replicates[0], hold.Replicates[0], switched.Replicates[0]
	if !reflect.DeepEqual(control.FinalOpinions, origin.FinalOpinions) ||
		!reflect.DeepEqual(control.Endpoints[2].Exposure, origin.Endpoints[3].Exposure) {
		t.Fatal("hold arm diverged from the reference after restoring the checkpoint")
	}
	if hold.SourceSHA256 != switched.SourceSHA256 || !reflect.DeepEqual(control.Endpoints[0], treatment.Endpoints[0]) {
		t.Fatal("paired arms did not start from the same physical state")
	}
	if treatment.SwitchStep == nil || *treatment.SwitchStep != 1 {
		t.Fatal("policy change was not applied immediately after the requested checkpoint")
	}
	for _, point := range treatment.Endpoints[1:] {
		got := point.Exposure
		if got.Updates != metadata.NodeCount || got.Recommended > metadata.NodeCount*metadata.RecsysCount ||
			got.CrossRewire > got.CrossConfident || got.CrossConfident > got.CrossCamp ||
			got.Policy.CrossCamp > got.CrossCamp || got.Policy.Shown+got.Bridge.Shown+got.Exploration.Shown != got.Recommended {
			t.Fatalf("inconsistent exposure counts: %+v", got)
		}
	}

	threshold := 1.0
	spec.PolicyChange = &PolicyChange{Recommender: "StructureM9", ExcessAtLeast: &threshold, Latest: 1, Duration: 1}
	timed, err := runExperiment(ctx, metadata, Request{Experiment: &spec, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	rep := timed.Replicates[0]
	if !rep.SwitchFallback || rep.SwitchStep == nil || *rep.SwitchStep != 2 {
		t.Fatal("state trigger did not use its budgeted latest step")
	}
}

func TestPathwayObservationPreservesTrajectoryAndRNG(t *testing.T) {
	for _, policy := range []string{"Opinion", "OpinionM9", "OpinionRandom", "Structure", "StructureM9", "StructureRandom"} {
		t.Run(policy, func(t *testing.T) {
			metadata := testMetadata("observation-fixture")
			metadata.RecsysFactoryType = policy
			metadata.RecSysParams = map[string]any{"LogRecommendations": false}
			dir := t.TempDir()

			run := func(name string, pathway bool) model.SMPModelDumpData[float64, dynamics.HKParams] {
				t.Helper()
				path := filepath.Join(dir, name+".msgpack.lz4")
				_, err := runExperiment(context.Background(), metadata, Request{
					Experiment: &ExperimentSpec{
						Mode: "fresh", Replicates: 1,
						ObserveAt: []int{0, 2, 4}, CheckpointAt: map[int]string{4: path},
					},
					Output: OutputOptions{Pathway: pathway},
				})
				if err != nil {
					t.Fatal(err)
				}
				snapshot, _, err := simulation.ReadCheckpoint(path)
				if err != nil {
					t.Fatal(err)
				}
				var dump model.SMPModelDumpData[float64, dynamics.HKParams]
				if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
					t.Fatal(err)
				}
				// Serialized recommender maps need not have identical byte order.
				dump.RecsysDumpData = nil
				return dump
			}
			if plain, observed := run("plain", false), run("observed", true); !reflect.DeepEqual(plain, observed) {
				t.Fatal("observation changed the physical state or a simulation RNG stream")
			}
		})
	}
}

func TestResumeKeepsStructureCacheOnSamePolicyAndUsesSavedRoot(t *testing.T) {
	metadata := testMetadata("structure-fixture")
	metadata.RecsysFactoryType = "StructureM9"
	metadata.RecSysParams = map[string]any{"LogRecommendations": false}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "warm.msgpack.lz4")
	_, err := runExperiment(ctx, metadata, Request{
		Experiment: &ExperimentSpec{Mode: "fresh", Replicates: 1, ObserveAt: []int{2}, CheckpointAt: map[int]string{2: path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	requested := *metadata
	requested.RNG = smprng.Spec{}
	spec := ExperimentSpec{Mode: "resume", Checkpoint: path, Replicates: 1, ObserveAt: []int{0, 1, 2, 3}}
	request := Request{Experiment: &spec, Output: OutputOptions{Pathway: true, FullState: true}}
	plain, err := runExperiment(ctx, &requested, request)
	if err != nil {
		t.Fatal(err)
	}
	if plain.RNG != metadata.RNG || plain.Replicates[0].RNG != metadata.RNG {
		t.Fatal("resume did not report the checkpoint RNG root")
	}
	at := 0
	spec.PolicyChange = &PolicyChange{Recommender: "StructureM9", At: &at, Duration: 1}
	same, err := runExperiment(ctx, &requested, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain.Replicates[0].Endpoints, same.Replicates[0].Endpoints) {
		t.Fatal("a pulse using the already-active policy changed the trajectory")
	}
	requested.RNG = smprng.FixedSpec(3, 4)
	if _, err := runExperiment(ctx, &requested, request); err == nil {
		t.Fatal("resume accepted a different seed")
	}
}
