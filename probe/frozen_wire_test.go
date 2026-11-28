package probe_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"smp/probe"

	"github.com/vmihailenco/msgpack/v5"
)

func decodeInlineRequest(t *testing.T, format string, posts map[int64][]map[string]any) (*probe.Request, error) {
	t.Helper()
	state := map[string]any{"step": 0, "opinions": []float64{-.6, .6, 0}, "posts": posts,
		"graph": map[string]any{"directed": true, "nodes": map[int64]any{0: map[string]any{}, 1: map[string]any{}, 2: map[string]any{}},
			"adjacency": map[int64]any{0: map[int64]any{2: map[string]any{}}, 1: map[int64]any{2: map[string]any{}}, 2: map[int64]any{}}}}
	request := map[string]any{"version": 1, "states": []any{state}, "measurements": []string{"bridges"}, "epsilon": .65,
		"bridge_cores": [][]int{{0}, {1}}, "bridges": map[string]any{"epsilon": .65, "detail_output": true}}
	var decoded probe.Request
	if format == "json" {
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		return &decoded, decoder.Decode(&decoded)
	}
	data, err := msgpack.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoder := msgpack.NewDecoder(bytes.NewReader(data))
	decoder.SetCustomStructTag("json")
	decoder.DisallowUnknownFields(true)
	return &decoded, decoder.Decode(&decoded)
}

func TestInlinePostWireRejectsMissingNullOrUnknownFields(t *testing.T) {
	for _, format := range []string{"json", "msgpack"} {
		for _, field := range []string{"AgentID", "Step", "Opinion"} {
			for _, null := range []bool{false, true} {
				t.Run(format+"/"+field+map[bool]string{false: "/missing", true: "/null"}[null], func(t *testing.T) {
					post := map[string]any{"AgentID": int64(0), "Step": -1, "Opinion": 0.0}
					if null {
						post[field] = nil
					} else {
						delete(post, field)
					}
					if _, err := decodeInlineRequest(t, format, map[int64][]map[string]any{2: {post}}); err == nil || !strings.Contains(err.Error(), "explicit non-null") {
						t.Fatalf("incomplete provenance/value became a real zero: %v", err)
					}
				})
			}
		}
		t.Run(format+"/unknown_field", func(t *testing.T) {
			post := map[string]any{"AgentID": int64(0), "Step": -1, "Opinion": 0.0, "unexpected": true}
			if _, err := decodeInlineRequest(t, format, map[int64][]map[string]any{2: {post}}); err == nil || !strings.Contains(err.Error(), "unexpected") {
				t.Fatalf("strict nested post validation was bypassed: %v", err)
			}
		})
		t.Run(format+"/null_record", func(t *testing.T) {
			if _, err := decodeInlineRequest(t, format, map[int64][]map[string]any{2: {nil}}); err == nil {
				t.Fatal("null post record became a real zero-valued post")
			}
		})
	}
}

func TestInlineExplicitZeroUnknownAuthorAndUnavailableValueRemainDistinct(t *testing.T) {
	for _, format := range []string{"json", "msgpack"} {
		t.Run(format, func(t *testing.T) {
			request, err := decodeInlineRequest(t, format, map[int64][]map[string]any{2: {{"AgentID": int64(0), "Step": -1, "Opinion": 0.0}}})
			if err != nil {
				t.Fatal(err)
			}
			if request.States[0].Posts[2][0].AgentID != 0 || request.States[0].Posts[2][0].Opinion != 0 {
				t.Fatal("valid explicit zeros were lost")
			}
			request, err = decodeInlineRequest(t, format, map[int64][]map[string]any{
				0: {{"AgentID": int64(0), "Step": -1, "Opinion": -.6}},
				1: {{"AgentID": int64(1), "Step": -1, "Opinion": .6}},
				2: {{"AgentID": int64(-1), "Step": -1, "Opinion": 0.0}},
			})
			if err != nil {
				t.Fatal(err)
			}
			response, err := probe.Evaluate(request)
			if err != nil {
				t.Fatal(err)
			}
			holder := response.Results[0].Bridges.Pairs[0].Holders[2]
			if holder.P[0].SourceKnown || !holder.P[0].Status.Known || !holder.P[0].Status.Active {
				t.Fatalf("known middle value or unknown author was invented/lost: %+v", holder)
			}
			request.Measurements = []string{"force"}
			request.AnchorIDs = []int64{2}
			request.DynamicsType = "HK"
			request.HKParams.Tolerance, request.HKParams.Influence = .65, .1
			request.RecsysFactoryType, request.RecsysCount, request.PostRetainCount = "Random", 1, 1
			request.Grid = probe.Grid{Min: 0, Max: 0, Step: 1}
			if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "invalid post history") {
				t.Fatalf("force accepted unavailable provenance or failed for the wrong reason: %v", err)
			}
			request, err = decodeInlineRequest(t, format, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err = probe.Evaluate(request)
			if err != nil {
				t.Fatal(err)
			}
			holder = response.Results[0].Bridges.Pairs[0].Holders[2]
			if holder.P[0].SourceKnown || holder.P[0].Status.Known || !holder.O[0].Status.Active {
				t.Fatalf("missing post was filled with a compatible zero value: %+v", holder)
			}
		})
	}
}

func TestInlineStateWireRemainsStrictAndFailedDecodeDoesNotMutateReceiver(t *testing.T) {
	state := probe.FrozenState{Step: 42, Opinions: []float64{.4}}
	for _, invalid := range []string{`{"step":0,"unknown":true}`, `null`, `{"posts":{"0":[{"AgentID":0,"Step":-1}]}}`, `{"opinions":[0]} {}`} {
		if err := json.Unmarshal([]byte(invalid), &state); err == nil {
			t.Fatalf("invalid state decoded: %s", invalid)
		}
		if state.Step != 42 || state.Opinions[0] != .4 {
			t.Fatal("failed decode partially mutated the receiver")
		}
	}
}
