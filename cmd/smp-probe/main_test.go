package main

import (
	"bytes"
	"testing"

	"github.com/vmihailenco/msgpack/v5"

	"smp/dynamics"
	"smp/model"
	"smp/probe"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
)

func TestRunMsgpackProtocol(t *testing.T) {
	graph := utils.NetworkXGraph{
		Nodes: map[int64]map[string]any{
			0: {},
			1: {},
		},
		Adjacency: map[int64]map[int64]any{
			0: {1: map[string]any{}},
			1: {},
		},
		Directed: true,
		Graph:    map[string]any{},
	}
	request := probe.Request{
		RNG:               smprng.FixedSpec(1, 2),
		DynamicsType:      simulation.DynamicsTypeHK,
		HKParams:          dynamics.HKParams{Tolerance: 1, Influence: 1},
		RecsysFactoryType: "Random",
		RecsysCount:       1,
		Grid:              probe.Grid{Min: -1, Max: 1, Step: 1},
		Replicates:        1,
		States: []probe.FrozenState{{
			Graph:    graph,
			Opinions: []float64{-0.5, 0.5},
			Posts: map[int64][]model.PostRecord[float64]{
				0: {{AgentID: 0, Step: -1, Opinion: -0.5}},
				1: {{AgentID: 1, Step: -1, Opinion: 0.5}},
			},
		}},
	}
	encoded, err := msgpack.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(bytes.NewReader(encoded), &output); err != nil {
		t.Fatal(err)
	}
	var response probe.Response
	if err := msgpack.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || len(response.Results[0].Points) != 3 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Version != probe.ProtocolVersion {
		t.Fatalf("protocol version: got %d", response.Version)
	}
}
