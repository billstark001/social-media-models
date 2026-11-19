package probe_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/probe"
	"smp/record"
	smprng "smp/rng"
	"smp/simulation"
	"smp/terminal"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

func TestRunDirectoryMatchesExplicitStatesWithoutResettingForce(t *testing.T) {
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "probe-source"
	metadata.DynamicsType = simulation.DynamicsTypeHK
	metadata.HKParams = dynamics.HKParams{Tolerance: .45, Influence: .1, RewiringRate: .1, RepostRate: .1}
	metadata.RNG = smprng.FixedSpec(5, 7)
	metadata.NodeCount, metadata.NodeFollowCount, metadata.MaxSimulationStep = 12, 3, 3
	directory := t.TempDir()
	parent := simulation.NewScenarioWithOptions(directory, metadata, simulation.ScenarioOptions{Quiet: true, EnableDumps: true})
	if err := parent.InitError(); err != nil {
		t.Fatal(err)
	}
	parent.StepTillEnd(context.Background())
	if err := parent.DB.Close(); err != nil {
		t.Fatal(err)
	}
	runDirectory := directory + "/" + metadata.UniqueName
	run, err := record.OpenRun(runDirectory, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	steps := []int{0, 1, run.MaxStep}
	states := []probe.FrozenState{}
	if err := run.ReadStates(steps, record.Requirements{Graph: true, Posts: true}, func(state record.State) error {
		states = append(states, probe.FrozenState{Step: state.Step, Opinions: state.Opinions, Graph: state.Graph, Posts: state.Posts, Precision: state.Precision})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request := &probe.Request{
		RNG:               smprng.FixedSpec(11, 22),
		DynamicsType:      metadata.DynamicsType,
		HKParams:          metadata.HKParams,
		RecsysFactoryType: metadata.RecsysFactoryType,
		RecSysParams:      metadata.RecSysParams,
		RecsysCount:       metadata.RecsysCount,
		PostRetainCount:   metadata.PostRetainCount,
		States:            states,
		Grid:              probe.Grid{Min: -1, Max: 1, Step: 1},
		Replicates:        2,
		Measurements:      []string{"force", "pathway"},
	}
	explicit, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	request.States, request.RunDirectory, request.Steps = nil, runDirectory, steps
	fromRun, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if fromRun.SourceMaxStep == nil || *fromRun.SourceMaxStep != run.MaxStep || explicit.SourceMaxStep != nil {
		t.Fatal("source_max_step must report the run horizon only for directory sources")
	}
	fromRun.SourceMaxStep = nil
	if !reflect.DeepEqual(fromRun, explicit) {
		t.Fatal("source reader altered measurement order, precision, or probe RNG stream")
	}
	request.Measurements = []string{"terminal"}
	request.Steps = nil
	last, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Results) != 1 || last.Results[0].Step != run.MaxStep {
		t.Fatal("default source observation must select only the last recorded step")
	}
}

func TestExplicitRunMetadataAndSourceValidation(t *testing.T) {
	directory := t.TempDir()
	state := &simulation.AccumulativeModelState{
		Opinions:         [][]float64{{-.5, .5}, {-.25, .25}},
		AgentNumbers:     [][][4]int16{make([][4]int16, 2), make([][4]int16, 2)},
		AgentOpinionSums: [][][4]float64{make([][4]float64, 2), make([][4]float64, 2)},
	}
	if err := simulation.SaveAccumulativeModelState(filepath.Join(directory, "acc-state-2026-11-19T010000Z.lz4"), state); err != nil {
		t.Fatal(err)
	}
	request := &probe.Request{
		RunDirectory: directory, RunMetadata: map[string]any{"DynamicsType": "HK", "NodeCount": 2}, Steps: []int{0},
		Measurements: []string{"terminal"}, Terminal: &terminal.Options{Epsilon: .45, MajorMass: .02},
	}
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.SourceMaxStep == nil || *response.SourceMaxStep != 1 || response.Results[0].Step != 0 {
		t.Fatalf("lost run horizon: %+v", response)
	}
	if _, err := os.Stat(filepath.Join(directory, "metadata.json")); !os.IsNotExist(err) {
		t.Fatal("explicit source metadata was persisted")
	}
	request.Terminal = nil
	if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "HKParams") {
		t.Fatalf("invented an unknown source epsilon: %v", err)
	}
	epsilon := .45
	request.Epsilon = &epsilon
	if _, err := probe.Evaluate(request); err != nil {
		t.Fatalf("explicit epsilon should avoid unused metadata defaults: %v", err)
	}
	request.Measurements = []string{"force"}
	request.Grid = probe.Grid{Min: 0, Max: 0, Step: 1}
	request.RunMetadata["HKParams"] = map[string]any{"Tolerance": 0.0}
	if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "Influence") {
		t.Fatalf("invented an unknown source influence: %v", err)
	}
	request.Measurements = []string{"terminal"}
	request.Terminal = &terminal.Options{Epsilon: .45, MajorMass: .02}
	request.DynamicsType = "Voter"
	if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "run dynamics") {
		t.Fatalf("expected dynamics conflict, got %v", err)
	}
	request.DynamicsType = ""
	metadata, err := json.Marshal(request.RunMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "metadata.json"), metadata, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Evaluate(request); err == nil {
		t.Fatal("ignored explicit metadata when a persisted file exists")
	}
	request.RunDirectory = ""
	request.Steps = nil
	request.States = []probe.FrozenState{{Step: 0, Opinions: []float64{0}}}
	if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "run_metadata") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestCheckpointRejectsInvalidMetadataAndBoolState(t *testing.T) {
	for _, kind := range []string{"contradictory", "bool", "future_version"} {
		t.Run(kind, func(t *testing.T) {
			snapshot := simulation.RawSnapshotData{DynamicsType: "HK", Metadata: &simulation.ScenarioMetadata{DynamicsType: "Voter"}, CompletedStep: 0}
			var data []byte
			var err error
			if kind == "bool" {
				snapshot.DynamicsType = "Voter"
				data, err = msgpack.Marshal(model.SMPModelDumpData[bool, dynamics.VoterParams]{CurStep: 1})
			} else {
				data, err = msgpack.Marshal(model.SMPModelDumpData[float64, dynamics.HKParams]{CurStep: 1, Opinions: []float64{0}, Graph: utils.NetworkXGraph{Directed: true, Nodes: map[int64]map[string]any{0: {}}}})
			}
			if kind == "future_version" {
				snapshot.Metadata.DynamicsType = "HK"
				snapshot.Metadata.DataVersion = simulation.CurrentDataVersion + 1
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Data = data
			encoded, err := msgpack.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "checkpoint.msgpack")
			if err := os.WriteFile(path, encoded, 0644); err != nil {
				t.Fatal(err)
			}
			_, err = probe.Evaluate(&probe.Request{CheckpointPaths: []string{path}, Measurements: []string{"terminal"}, Terminal: &terminal.Options{Epsilon: .4, MajorMass: .02}})
			if err == nil {
				t.Fatal("accepted invalid float64 checkpoint source")
			}
			if kind == "contradictory" && !strings.Contains(err.Error(), "metadata") || kind == "bool" && !strings.Contains(err.Error(), "float64") {
				t.Fatalf("unexpected error: %v", err)
			}
			if kind == "future_version" && !strings.Contains(err.Error(), "DataVersion") {
				t.Fatalf("accepted an unsupported checkpoint format: %v", err)
			}
		})
	}
}

func TestInterventionSourcesKeepStaticMeasurementsAndRejectImplicitForce(t *testing.T) {
	directory := t.TempDir()
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "intervention"
	metadata.DynamicsType = "HK"
	metadata.HKParams = dynamics.HKParams{Tolerance: .45, Influence: .1, RewiringRate: .1}
	metadata.RNG = smprng.FixedSpec(3, 7)
	metadata.NodeCount, metadata.NodeFollowCount, metadata.MaxSimulationStep = 12, 3, 3
	at := 1
	scenario := simulation.NewScenarioWithOptions(directory, metadata, simulation.ScenarioOptions{
		EnableDumps: true, Quiet: true, FixedHorizon: true,
		Research: &simulation.ResearchOptions{ObserveAt: []int{0, 3}, PolicyChange: &simulation.PolicyChange{Recommender: "OpinionM9", At: &at}},
	})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	scenario.StepTillEnd(context.Background())
	checkpoint := filepath.Join(directory, "checkpoint.msgpack.lz4")
	if err := scenario.SaveCheckpointTo(checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := scenario.Close(); err != nil {
		t.Fatal(err)
	}
	for _, request := range []*probe.Request{
		{RunDirectory: filepath.Join(directory, metadata.UniqueName)},
		{CheckpointPaths: []string{checkpoint}},
	} {
		request.Measurements = []string{"pathway", "terminal"}
		if _, err := probe.Evaluate(request); err != nil {
			t.Fatalf("static intervention measurement failed: %v", err)
		}
		request.Measurements = []string{"force"}
		request.Grid = probe.Grid{Min: 0, Max: 0, Step: 1}
		if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "explicit frozen states") {
			t.Fatalf("implicitly used original policy for intervention force: %v", err)
		}
	}
}
