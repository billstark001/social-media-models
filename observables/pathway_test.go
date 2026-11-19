package observables

import (
	"math"
	"testing"

	"github.com/vmihailenco/msgpack/v5"

	"gonum.org/v1/gonum/graph/simple"
)

func BenchmarkPathwayAt500Nodes(b *testing.B) {
	const n = 500
	g := simple.NewDirectedGraph()
	opinions := make([]float64, n)
	for i := range opinions {
		g.AddNode(simple.Node(i))
		opinions[i] = float64((i*31)%n)*2/n - 1
	}
	for i := range opinions {
		for d := 1; d <= 15; d++ {
			g.SetEdge(g.NewEdge(g.Node(int64(i)), g.Node(int64((i+d)%n))))
		}
	}
	b.ReportAllocs()

	for b.Loop() {
		if _, err := MeasurePathway(opinions, g, 0.45); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPathwayThresholdMatchesReportedExcess(t *testing.T) {
	g := simple.NewDirectedGraph()
	for i := range 5 {
		g.AddNode(simple.Node(i))
	}
	opinions := []float64{-0.61, -0.60, 0.63, 0.64, 0.65}
	var measure PathwayMeasurer
	state, err := measure.Measure(opinions, g, 0.45)
	if err != nil {
		t.Fatal(err)
	}
	if state.EdgeConcordance != nil || state.ExcessConcordance != nil {
		t.Fatal("edge concordance is undefined without edges")
	}
	if hit, err := measure.ExcessAtLeast(opinions, g, 0.45, -1); err != nil || hit {
		t.Fatal("an empty graph cannot cross the threshold")
	}
	g.SetEdge(g.NewEdge(g.Node(0), g.Node(1)))
	g.SetEdge(g.NewEdge(g.Node(3), g.Node(2)))
	g.SetEdge(g.NewEdge(g.Node(0), g.Node(4)))
	state, err = measure.Measure(opinions, g, 0.45)
	if err != nil {
		t.Fatal(err)
	}
	if state.Separation != 0.8 || state.PairConcordance != 0.4 || *state.EdgeConcordance != 2.0/3.0 {
		t.Fatalf("unexpected pathway state: %+v", state)
	}
	hit, err := measure.ExcessAtLeast(opinions, g, 0.45, *state.ExcessConcordance)
	if err != nil {
		t.Fatal(err)
	}
	miss, err := measure.ExcessAtLeast(opinions, g, 0.45, *state.ExcessConcordance+1e-9)
	if err != nil || !hit || miss {
		t.Fatal("threshold must use the same signed excess as observation")
	}
}

func TestPathwayInvalidInputsReturnErrors(t *testing.T) {
	graph := simple.NewDirectedGraph()
	graph.AddNode(simple.Node(7))
	for _, test := range []struct {
		opinions []float64
		graph    *simple.DirectedGraph
		epsilon  float64
	}{
		{[]float64{0}, nil, .45},
		{[]float64{0, 0}, nil, -1},
		{[]float64{0, math.NaN()}, nil, .45},
		{[]float64{0, 0}, graph, .45},
	} {
		if _, err := MeasurePathway(test.opinions, test.graph, test.epsilon); err == nil {
			t.Fatal("expected invalid measurement input to return an error")
		}
	}
}

func TestPathwayMsgpackUsesCanonicalNames(t *testing.T) {
	state, err := MeasurePathway([]float64{-.6, .6}, nil, .45)
	if err != nil {
		t.Fatal(err)
	}
	data, err := msgpack.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := msgpack.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"separation", "pair_concordance", "edge_concordance", "excess_concordance"} {
		if _, ok := values[name]; !ok {
			t.Fatalf("missing canonical key %s", name)
		}
	}
}
