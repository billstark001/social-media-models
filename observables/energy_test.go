package observables

import (
	"math"
	"testing"

	"gonum.org/v1/gonum/graph/simple"
)

func TestEnergyLandscapeIdentitiesWithVariableDegree(t *testing.T) {
	x := []float64{-0.5, 0, 0.4, 0.9}
	g := simple.NewDirectedGraph()
	for i := range x {
		g.AddNode(simple.Node(i))
	}
	for _, edge := range [][2]int64{{0, 1}, {0, 2}, {1, 2}, {2, 0}, {2, 3}} {
		g.SetEdge(g.NewEdge(g.Node(edge[0]), g.Node(edge[1])))
	}
	e, err := MeasureEnergy(x, g, 0.5, 1)
	if err != nil {
		t.Fatal(err)
	}
	land, err := MeasureLandscape(x, g, 0.5, 1, x)
	if err != nil {
		t.Fatal(err)
	}
	meanPhi := 0.0
	for _, v := range land.Population {
		meanPhi += v / float64(len(x))
	}
	if math.Abs(meanPhi-2*e.Population) > 1e-14 {
		t.Fatalf("population identity failed: %v vs %v", meanPhi, 2*e.Population)
	}
	edgePhi := 0.0
	for i, v := range land.EdgeBySource {
		edgePhi += v * float64(land.EdgeCounts[i]) / float64(e.Edges)
	}
	if math.Abs(edgePhi-e.Edge) > 1e-14 {
		t.Fatalf("edge identity failed: %v vs %v", edgePhi, e.Edge)
	}
	b, err := MeasureBinnedState(x, g, []float64{-1, 0, 0.5, 1})
	if err != nil {
		t.Fatal(err)
	}
	rhoSum, edgeSum := 0.0, 0.0
	for _, v := range b.Population {
		rhoSum += v
	}
	for _, v := range b.Edge {
		edgeSum += v
	}
	if math.Abs(rhoSum-1) > 1e-14 || math.Abs(edgeSum-float64(e.Edges)/float64(len(x))) > 1e-14 {
		t.Fatal("binned normalization failed")
	}
}
