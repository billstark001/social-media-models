package probe_test

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"smp/model"
	"smp/probe"
	"smp/record"
	"smp/simulation"
	"smp/trajectory"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

func staticHistory(t *testing.T, format string) string {
	t.Helper()
	dir := t.TempDir()
	metadata := map[string]any{
		"DynamicsType": "HK", "NodeCount": 3, "RewiringEvent": true, "PostEvent": false,
		"HKParams": map[string]any{"Tolerance": .5, "RewiringRate": .5, "RepostRate": .5},
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	opinions := [][]float64{{-.5, 0, .5}, {-.25, 0, .5}, {-.25, .25, .5}, {-.125, .25, .5}, {0, .25, .5}}
	if format == "legacy" {
		state := &simulation.AccumulativeModelState{Opinions: opinions}
		for range opinions {
			state.AgentNumbers = append(state.AgentNumbers, make([][4]int16, 3))
			state.AgentOpinionSums = append(state.AgentOpinionSums, make([][4]float64, 3))
		}
		if err := simulation.SaveAccumulativeModelState(filepath.Join(dir, "acc-state-2026-11-25T000000Z.lz4"), state); err != nil {
			t.Fatal(err)
		}
	} else {
		precision := trajectory.Precision{Opinions: format, OpinionSums: format}
		writer, err := trajectory.Open(dir, 3, precision)
		if err != nil {
			t.Fatal(err)
		}
		buffer := trajectory.NewBuffer(3, precision)
		for step, row := range opinions {
			if err := buffer.Append(row, make([]model.AgentNumberRecord, 3), make([]model.AgentOpinionSumRecord, 3)); err != nil {
				t.Fatal(err)
			}
			if step == 1 || step == 4 {
				if err := writer.Flush(buffer); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	graph := utils.NetworkXGraph{Directed: true, Nodes: map[int64]map[string]any{0: {}, 1: {}, 2: {}},
		Adjacency: map[int64]map[int64]any{0: {1: map[string]any{}}, 1: {2: map[string]any{}}, 2: {0: map[string]any{}}}}
	data, err = msgpack.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph-0.msgpack"), data, 0644); err != nil {
		t.Fatal(err)
	}
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []*model.EventRecord{
		{Type: "Rewiring", AgentID: 0, Step: 1, Body: model.RewiringEventBody{AgentID: 0, Unfollow: 1, Follow: 2}},
		{Type: "Rewiring", AgentID: 2, Step: 3, Body: model.RewiringEventBody{AgentID: 2, Unfollow: 0, Follow: 1}},
	} {
		if err := db.StoreEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func staticHistoryHashes(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	result := make(map[string][32]byte)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// SQLite read locks may maintain sidecars, not persisted history.
		if entry.Name() == "events.db-shm" || entry.Name() == "events.db-wal" && len(data) == 0 {
			continue
		}
		result[entry.Name()] = sha256.Sum256(data)
	}
	return result
}

func TestStreamedStaticRunMatchesInlineAcrossHistoryFormats(t *testing.T) {
	for _, format := range []string{"float64", "float16", "legacy"} {
		t.Run(format, func(t *testing.T) {
			dir := staticHistory(t, format)
			before := staticHistoryHashes(t, dir)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			steps := []int{0, 2, 4}
			var states []probe.FrozenState
			if err := reader.ReadStates(steps, record.Requirements{Graph: true}, func(state record.State) error {
				states = append(states, probe.FrozenState{Step: state.Step, Graph: state.Graph, Opinions: state.Opinions, Precision: state.Precision})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			epsilon := .5
			request := &probe.Request{Version: probe.ProtocolVersion, DynamicsType: "HK", States: states,
				Measurements: []string{"pathway", "energy", "bins", "landscape", "terminal", "counterfactual_neighbor_energy"},
				Epsilon:      &epsilon, EnergyScale: 2, Bins: []float64{-1, 0, 1}, Grid: probe.Grid{Min: -1, Max: 1, Step: 1}, AnchorIDs: []int64{0, 2}}
			inline, err := probe.Evaluate(request)
			if err != nil {
				t.Fatal(err)
			}
			request.States, request.RunDirectory, request.Steps = nil, dir, steps
			streamed, err := probe.Evaluate(request)
			if err != nil {
				t.Fatal(err)
			}
			if streamed.SourceMaxStep == nil || *streamed.SourceMaxStep != 4 || streamed.Version != probe.ProtocolVersion || !streamed.RNG.IsZero() {
				t.Fatalf("lost source horizon/protocol identity or used force RNG: %+v", streamed)
			}
			streamed.SourceMaxStep = nil
			if !reflect.DeepEqual(inline, streamed) {
				t.Fatalf("streamed static measurements differ from inline states: inline=%+v streamed=%+v", inline, streamed)
			}
			if len(request.States) != 0 || !reflect.DeepEqual(request.Steps, steps) {
				t.Fatal("streamed evaluation materialized or mutated the caller request")
			}
			if after := staticHistoryHashes(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatal("streamed static evaluation changed saved history")
			}
		})
	}
}
