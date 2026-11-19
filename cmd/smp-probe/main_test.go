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
	if err := runMsgpack(bytes.NewReader(encoded), &output); err != nil {
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

func TestMsgpackTerminalUsesBoundaryFieldNames(t *testing.T) {
	request := map[string]any{
		"version": 1, "measurements": []string{"terminal"},
		"states":   []map[string]any{{"step": 0, "opinions": []float64{0, 0.2}}},
		"terminal": map[string]any{"epsilon": 0.0, "major_mass": 0.02},
	}
	encoded, err := msgpack.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runMsgpack(bytes.NewReader(encoded), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := msgpack.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response["results"].([]any)[0].(map[string]any)["terminal"].(map[string]any)
	if _, ok := result["k_major"]; !ok || result["category"] != "k2" {
		t.Fatalf("terminal boundary codec lost its explicit zero or canonical keys: %v", result)
	}
	encoded = append(encoded, 0xc0)
	if err := runMsgpack(bytes.NewReader(encoded), &bytes.Buffer{}); err == nil {
		t.Fatal("accepted trailing MsgPack object")
	}
}
