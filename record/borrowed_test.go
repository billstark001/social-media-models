package record_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"smp/record"
	"smp/trajectory"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

func TestBorrowedCallbackMatchesOwnedStates(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		directory, _ := writeRun(t, legacy, trajectory.Precision{Opinions: "float64", OpinionSums: "float32"})
		reader, err := record.OpenRun(directory, nil)
		if err != nil {
			t.Fatal(err)
		}
		steps := []int{0, 1, 2, 3, 4}
		owned := readStates(t, reader, steps, record.Requirements{Posts: true})
		before, _ := json.Marshal(owned)
		index := 0
		err = reader.ReadStatesBorrowed(steps, record.Requirements{Posts: true}, func(state record.State) error {
			got, _ := json.Marshal(state)
			want, _ := json.Marshal(owned[index])
			if string(got) != string(want) {
				t.Fatalf("borrowed snapshot %d differs from owned", index)
			}
			index++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		after, _ := json.Marshal(owned)
		if string(before) != string(after) {
			t.Fatal("borrowed reader mutated retained owned snapshots")
		}
		stop := errors.New("stop")
		if err := reader.ReadStatesBorrowed(steps, record.Requirements{Graph: true}, func(record.State) error { return stop }); !errors.Is(err, stop) {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkReadStatesOwnership(b *testing.B) {
	directory := writeSparseRun(b, 31, 500, 32, 1)
	graph := utils.NetworkXGraph{Directed: true, Nodes: make(map[int64]map[string]any), Adjacency: make(map[int64]map[int64]any)}
	for i := int64(0); i < 500; i++ {
		graph.Nodes[i] = map[string]any{}
		graph.Adjacency[i] = make(map[int64]any)
		for k := int64(1); k <= 15; k++ {
			graph.Adjacency[i][(i+k)%500] = map[string]any{}
		}
	}
	data, err := msgpack.Marshal(graph)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "graph-0.msgpack"), data, 0644); err != nil {
		b.Fatal(err)
	}
	steps := make([]int, 32)
	for i := range steps {
		steps[i] = i
	}
	for _, borrowed := range []bool{false, true} {
		name := "owned"
		if borrowed {
			name = "borrowed"
		}
		b.Run(name, func(b *testing.B) {
			reader, err := record.OpenRun(directory, nil)
			if err != nil {
				b.Fatal(err)
			}
			defer reader.Close()
			read := reader.ReadStates
			if borrowed {
				read = reader.ReadStatesBorrowed
			}
			var first []float64
			callback := func(state record.State) error {
				if state.Step == 0 {
					first = append(first[:0], state.Opinions...)
				}
				return nil
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := read(steps, record.Requirements{Posts: true}, callback); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if !reflect.DeepEqual(first, makeOpinionsAtZero(500)) {
				b.Fatal("unexpected measured input")
			}
		})
	}
}

func makeOpinionsAtZero(n int) []float64 {
	values := make([]float64, n)
	for i := range values {
		values[i] = float64(i%64) / 64
	}
	return values
}
