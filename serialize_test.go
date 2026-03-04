package main

import (
	"context"
	"os"
	"path"
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
// T1 is the key type and T2 is the element type of the slice.
// Both T1 and T2 must be comparable.
func CompareMaps[T1 comparable, T2 comparable](map1, map2 map[T1][]T2) bool {
	// If the maps have different lengths, they are not equal
	if len(map1) != len(map2) {
		return false
	}

	// Iterate over the first map
	for key, slice1 := range map1 {
		slice2, exists := map2[key]
		// If the key doesn't exist in the second map, they are not equal
		if !exists {
			return false
		}

		// If the slices are of different lengths, they are not equal
		if !CompareSlices(slice1, slice2) {
			return false
		}
	}

	// If all keys and values match, the maps are equal
	return true
}

func TestSerializeAndDeserializeScenario(t *testing.T) {
	metadata := &simulation.ScenarioMetadata{

		SMPAgentParams: model.SMPAgentParams{

			Decay:        0.005,
			Tolerance:    0.45,
			RewiringRate: 0.5,
			RetweetRate:  0.25,
		},

		SMPModelPureParams: model.SMPModelPureParams{

			TweetRetainCount: 3,
			RecsysCount:      10,
		},

		CollectItemOptions: model.CollectItemOptions{

			AgentNumber:   true,
			OpinionSum:    true,
			RewiringEvent: true,
			TweetEvent:    true,
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

	// compare everything
	model1 := scenario1.Model
	model2 := scenario2.Model

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
	if !CompareMaps(model1.CollectTweets(), model2.CollectTweets()) {
		t.Errorf("Original and loaded are not equal: model.CollectTweets()")
	}

	if model1.CurStep != model2.CurStep {
		t.Errorf("Original and loaded are not equal: model.CurStep")
	}

}
