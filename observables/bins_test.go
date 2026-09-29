package observables

import (
	"testing"

	"gonum.org/v1/gonum/graph/simple"
)

func TestBinnedStateConcentratedCounts(t *testing.T) {
	const n, edgeCount = 250, 3768
	x := make([]float64, n)
	g := simple.NewDirectedGraph()
	for i := range x {
		g.AddNode(simple.Node(i))
	}
	remaining := edgeCount
	for i := 0; i < n && remaining > 0; i++ {
		for j := 0; j < n && remaining > 0; j++ {
			if i != j {
				g.SetEdge(g.NewEdge(g.Node(int64(i)), g.Node(int64(j))))
				remaining--
			}
		}
	}
	b, err := MeasureBinnedState(x, g, []float64{-1, -0.1, 0.1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if b.Nodes != n || b.Edges != edgeCount {
		t.Fatalf("wrong counts: nodes=%d edges=%d", b.Nodes, b.Edges)
	}
	for i, got := range b.Population {
		want := 0.0
		if i == 1 {
			want = 1
		}
		if got != want {
			t.Fatalf("population[%d]=%.17g, want %.17g", i, got, want)
		}
	}
	for i, got := range b.Edge {
		want := 0.0
		if i == 4 {
			want = float64(edgeCount) / float64(n)
		}
		if got != want {
			t.Fatalf("edge[%d]=%.17g, want %.17g", i, got, want)
		}
	}
}

func TestBinnedStateBoundaryAndDirectedCounts(t *testing.T) {
	x := []float64{-1, 0, 0, 1}
	g := simple.NewDirectedGraph()
	for i := range x {
		g.AddNode(simple.Node(i))
	}
	for _, edge := range [][2]int64{{0, 1}, {0, 2}, {2, 3}} {
		g.SetEdge(g.NewEdge(g.Node(edge[0]), g.Node(edge[1])))
	}
	b, err := MeasureBinnedState(x, g, []float64{-1, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{0.25, 0.75} {
		if b.Population[i] != want {
			t.Fatalf("population bin %d: got %v, want %v", i, b.Population[i], want)
		}
	}
	for i, want := range []float64{0, 0.5, 0, 0.25} {
		if b.Edge[i] != want {
			t.Fatalf("directed edge bin %d: got %v, want %v", i, b.Edge[i], want)
		}
	}
}
