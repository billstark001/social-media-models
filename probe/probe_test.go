package probe_test

import (
	"math"
	"reflect"
	"testing"

	"gonum.org/v1/gonum/graph/simple"

	"smp/dynamics"
	"smp/model"
	"smp/probe"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
)

func frozenState() probe.FrozenState {
	graph := simple.NewDirectedGraph()
	for i := range 4 {
		graph.AddNode(simple.Node(i))
	}
	for _, edge := range [][2]int64{{0, 1}, {1, 2}, {2, 3}, {3, 0}} {
		graph.SetEdge(graph.NewEdge(graph.Node(edge[0]), graph.Node(edge[1])))
	}
	opinions := []float64{0, 0.5, -0.5, 1}
	posts := make(map[int64][]model.PostRecord[float64], len(opinions))
	for id, opinion := range opinions {
		posts[int64(id)] = []model.PostRecord[float64]{{
			AgentID: int64(id),
			Step:    5,
			Opinion: opinion,
		}}
	}
	return probe.FrozenState{
		Step:     5,
		Graph:    *utils.SerializeGraph(graph),
		Opinions: opinions,
		Posts:    posts,
	}
}

func requestFor(dynamicsType string) *probe.Request {
	return &probe.Request{
		RNG:               smprng.FixedSpec(11, 22),
		DynamicsType:      dynamicsType,
		HKParams:          dynamics.HKParams{Tolerance: 0.75, Influence: 0.5},
		DeffuantParams:    dynamics.DeffuantParams{Tolerance: 0.75, Influence: 0.5},
		RecsysFactoryType: "Opinion",
		RecSysParams:      map[string]any{"NoiseStd": 0.0},
		RecsysCount:       1,
		PostRetainCount:   0,
		Grid:              probe.Grid{Min: -1, Max: 1, Step: 1},
		Replicates:        2,
		AnchorIDs:         []int64{0},
		States:            []probe.FrozenState{frozenState()},
	}
}

func TestEvaluateSupportsHKAndDeffuant(t *testing.T) {
	for _, dynamicsType := range []string{
		simulation.DynamicsTypeHK,
		simulation.DynamicsTypeDeffuant,
	} {
		t.Run(dynamicsType, func(t *testing.T) {
			request := requestFor(dynamicsType)
			response, err := probe.Evaluate(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.DynamicsType != dynamicsType {
				t.Fatalf("dynamics type: got %q, want %q", response.DynamicsType, dynamicsType)
			}
			if response.Version != probe.ProtocolVersion {
				t.Fatalf("protocol version: got %d, want %d", response.Version, probe.ProtocolVersion)
			}
			if len(response.Results) != 1 || len(response.Results[0].Points) != 3 {
				t.Fatalf("unexpected result shape: %+v", response.Results)
			}
			points := response.Results[0].Points
			if math.Abs(points[0].FProbe.Mean-0.25) > 1e-12 {
				t.Fatalf("FProbe(-1): got %v, want 0.25", points[0].FProbe.Mean)
			}
			if math.Abs(points[1].FProbe.Mean) > 1e-12 {
				t.Fatalf("FProbe(0): got %v, want 0", points[1].FProbe.Mean)
			}
			if math.Abs(points[2].FProbe.Mean+0.125) > 1e-12 {
				t.Fatalf("FProbe(1): got %v, want -0.125", points[2].FProbe.Mean)
			}
			for _, point := range points {
				if point.FProbe.Samples != 2 {
					t.Fatalf("samples: got %d, want 2", point.FProbe.Samples)
				}
				sum := point.FNeighbor.Mean + point.FRecommendation.Mean
				if math.Abs(point.FProbe.Mean-sum) > 1e-12 {
					t.Fatalf("force components do not add at x=%v", point.X)
				}
			}
		})
	}
}

func TestEvaluateProtocolVersions(t *testing.T) {
	for _, version := range []int{0, probe.ProtocolVersion} {
		request := requestFor(simulation.DynamicsTypeHK)
		request.Version = version
		response, err := probe.Evaluate(request)
		if err != nil {
			t.Fatalf("version %d: %v", version, err)
		}
		if response.Version != probe.ProtocolVersion {
			t.Fatalf("version %d returned %d", version, response.Version)
		}
	}
	request := requestFor(simulation.DynamicsTypeHK)
	request.Version = probe.ProtocolVersion + 1
	if _, err := probe.Evaluate(request); err == nil {
		t.Fatal("future protocol version was accepted")
	}
}

func TestEvaluateIsReproducibleWithFixedRNG(t *testing.T) {
	request1 := requestFor(simulation.DynamicsTypeHK)
	request1.RecSysParams = map[string]any{"NoiseStd": 0.2}
	response1, err := probe.Evaluate(request1)
	if err != nil {
		t.Fatal(err)
	}
	request2 := requestFor(simulation.DynamicsTypeHK)
	request2.RecSysParams = map[string]any{"NoiseStd": 0.2}
	response2, err := probe.Evaluate(request2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response1, response2) {
		t.Fatal("fixed probe RNG did not reproduce the same response")
	}
}

func TestEvaluateRejectsUnsupportedDynamics(t *testing.T) {
	request := requestFor(simulation.DynamicsTypeGalam)
	if _, err := probe.Evaluate(request); err == nil {
		t.Fatal("expected unsupported dynamics error")
	}
}

func TestEvaluateGeneratesRNGWhenOmitted(t *testing.T) {
	request := requestFor(simulation.DynamicsTypeHK)
	request.RNG = smprng.Spec{}
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.RNG.IsZero() {
		t.Fatal("probe did not return its generated RNG")
	}
}
