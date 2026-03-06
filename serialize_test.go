package main

import (
	"context"
	"os"
	"path"
	"smp/dynamics"
	"smp/model"
	"smp/simulation"
	"smp/utils"
	"testing"
)

func CompareSlices[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// CompareMaps compares two maps of type map[T1][]T2 for equality.
func CompareMaps[T1 comparable, T2 comparable](map1, map2 map[T1][]T2) bool {
	if len(map1) != len(map2) {
		return false
	}

	for key, slice1 := range map1 {
		slice2, exists := map2[key]
		if !exists {
			return false
		}

		if !CompareSlices(slice1, slice2) {
			return false
		}
	}

	return true
}

func TestSerializeAndDeserializeScenario(t *testing.T) {
	metadata := &simulation.ScenarioMetadata{

		DynamicsType: simulation.DynamicsTypeHK,

		HKParams: dynamics.HKParams{

			Influence:    0.005,
			Tolerance:    0.45,
			RewiringRate: 0.5,
			RepostRate:   0.25,
		},

		SMPModelPureParams: model.SMPModelPureParams{

			PostRetainCount: 3,
			RecsysCount:     10,
		},

		CollectItemOptions: model.CollectItemOptions{

			AgentNumber:   true,
			OpinionSum:    true,
			RewiringEvent: true,
			PostEvent:     true,
		},

		RecsysFactoryType: "OpinionM9",
		NetworkType:       "Random",
		NodeCount:         500,
		NodeFollowCount:   15,

		MaxSimulationStep: 100,

		UniqueName: "test",
	}

	// ensure base path
	basePath := path.Join(os.TempDir(), "test_smp_serialize")
	_, err := os.Stat(basePath)
	if os.IsNotExist(err) {
		err := os.Mkdir(basePath, 0755)
		if err != nil {
			t.Fatalf("Error making test directory: %v", err)
		}
	} else if err == nil {
		// exists
		err := os.RemoveAll(basePath)
		if err != nil {
			t.Fatalf("Error removing test directory: %v", err)
		}
		err = os.Mkdir(basePath, 0755)
		if err != nil {
			t.Fatalf("Error making test directory: %v", err)
		}
	} else {
		t.Fatalf("Error stating test directory: %v", err)
	}

	// sim 1000 steps and end
	scenario1 := simulation.NewScenario(basePath, metadata, false)
	scenario1.Init()
	ctx := context.Background()
	scenario1.StepTillEnd(ctx)

	// load it to a new scenario
	scenario2 := simulation.NewScenario(basePath, metadata, false)
	if !scenario2.Load() {
		t.Errorf("Failed to load scenario")
	}

	// type-assert to the concrete HK wrapper to access the underlying model fields
	w1, ok1 := scenario1.Model.(*simulation.Float64ModelWrapper[dynamics.HKParams])
	w2, ok2 := scenario2.Model.(*simulation.Float64ModelWrapper[dynamics.HKParams])
	if !ok1 || !ok2 {
		t.Fatalf("Unexpected model wrapper type")
	}
	model1 := w1.M
	model2 := w2.M

	if !utils.CompareGraphs(model1.Graph, model2.Graph) {
		t.Errorf("Original and loaded graphs are not equal")
	}

	op1 := model1.CollectOpinions()
	op2 := model2.CollectOpinions()

	if !CompareSlices(op1, op2) {
		t.Errorf("Original and loaded are not equal: model.CollectOpinions()")
	}
	if !CompareSlices(model1.CollectAgentNumbers(), model2.CollectAgentNumbers()) {
		t.Errorf("Original and loaded are not equal: model.CollectAgentNumbers()")
	}
	if !CompareSlices(model1.CollectAgentOpinions(), model2.CollectAgentOpinions()) {
		t.Errorf("Original and loaded are not equal: model.CollectAgentOpinions()")
	}
	if !CompareMaps(model1.CollectPosts(), model2.CollectPosts()) {
		t.Errorf("Original and loaded are not equal: model.CollectPosts()")
	}

	if model1.CurStep != model2.CurStep {
		t.Errorf("Original and loaded are not equal: model.CurStep")
	}

}
