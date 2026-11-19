package utils

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestGraphJSONUsesCanonicalFieldNames(t *testing.T) {
	data, err := json.Marshal(NetworkXGraph{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"adjacency", "directed", "nodes", "graph"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("canonical graph field %q missing: %s", name, data)
		}
	}
}

func TestGraphNumericWeightsSurviveDecoding(t *testing.T) {
	for _, weight := range []any{float32(3), float64(3), int(3), int64(3)} {
		t.Run(fmt.Sprintf("%T", weight), func(t *testing.T) {
			graph := DeserializeGraph(&NetworkXGraph{
				Nodes:     map[int64]map[string]any{0: {}, 1: {}},
				Adjacency: map[int64]map[int64]any{0: {1: map[string]any{"weight": weight}}},
				Directed:  true,
			})
			edge, ok := graph.Edge(0, 1).(interface{ Weight() float64 })
			if !ok || edge.Weight() != 3 {
				t.Fatalf("lost numeric weight %v (%T)", weight, weight)
			}
		})
	}
}
