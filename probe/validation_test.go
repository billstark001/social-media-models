package probe_test

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"smp/probe"
	"smp/simulation"
)

func TestRejectInvalidGridsAndAnchors(t *testing.T) {
	for _, grid := range []probe.Grid{
		{Min: 0, Max: 1e-10, Step: 1},
		{Min: 1e16, Max: 1e16 + 4, Step: 1},
		{Min: 0, Max: 1, Step: math.SmallestNonzeroFloat64},
	} {
		request := requestFor(simulation.DynamicsTypeHK)
		request.Grid = grid
		if _, err := probe.Evaluate(request); err == nil {
			t.Fatalf("accepted invalid grid %+v", grid)
		}
	}
	for _, selection := range [][]string{{"force"}, {"counterfactual_neighbor_energy"}} {
		for _, ids := range [][]int64{{0, 0}, {-1}, {4}} {
			request := requestFor(simulation.DynamicsTypeHK)
			request.Measurements = selection
			request.AnchorIDs = ids
			if _, err := probe.Evaluate(request); err == nil {
				t.Fatalf("accepted anchors %v for %v", ids, selection)
			}
		}
	}
	request := requestFor(simulation.DynamicsTypeHK)
	request.Measurements = []string{"counterfactual_neighbor_energy"}
	request.AnchorIDs = nil
	if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "anchor_ids") {
		t.Fatalf("expected anchor selection error, got %v", err)
	}
}

func TestForceUsesCanonicalRecsysParametersAndPreservesRequest(t *testing.T) {
	request := requestFor(simulation.DynamicsTypeHK)
	request.AnchorIDs = nil
	request.Grid = probe.Grid{Min: -1, Max: 1, Step: .2}
	request.RecSysParams = map[string]any{"NoiseStd": 0.0}
	expected, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	request.RecSysParams = map[string]any{"NoiseStd": int(0)}
	actual, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("int and float parameters changed canonical force behavior")
	}
	if _, ok := request.RecSysParams["NoiseStd"].(int); !ok {
		t.Fatal("evaluation modified caller metadata")
	}
	for _, params := range []map[string]any{
		{"NoiseStd": "0"}, {"MixRate": 2.0}, {"UseCache": "true"}, {"Steepness": math.Inf(1)},
	} {
		request.RecSysParams = params
		if _, err := probe.Evaluate(request); err == nil {
			t.Fatalf("accepted invalid recommender params: %v", params)
		}
	}
	request.RecSysParams = nil
	request.States[0].Step = int(^uint(0) >> 1)
	if _, err := probe.Evaluate(request); err == nil {
		t.Fatal("accepted overflowing completed step")
	}
}

func TestInvalidGridDoesNotReadDiskSource(t *testing.T) {
	_, err := probe.Evaluate(&probe.Request{RunDirectory: "missing-run", Measurements: []string{"force"}, Grid: probe.Grid{Step: 0}})
	if err == nil || !strings.Contains(err.Error(), "grid step") {
		t.Fatalf("grid validation should precede history I/O, got %v", err)
	}
}
