package observables

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"smp/terminal"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
)

func TestStateMeasurerMatchesCanonicalKernels(t *testing.T) {
	opinions := []float64{0.875, -0.5, 0.375, 0}
	graph := simple.NewDirectedGraph()
	for i := range opinions {
		graph.AddNode(simple.Node(i))
	}
	for _, edge := range [][2]int64{{0, 1}, {0, 2}, {1, 2}, {2, 0}, {2, 3}} {
		graph.SetEdge(graph.NewEdge(graph.Node(edge[0]), graph.Node(edge[1])))
	}
	options := StateOptions{
		Energy:    &EnergyOptions{Epsilon: 0.5, Scale: 2},
		Pathway:   &PathwayOptions{Epsilon: 0.75},
		Bins:      []float64{-1, 0, 0.5, 1},
		Landscape: &LandscapeOptions{Epsilon: 0.25, Scale: 4, Queries: []float64{-1, 0, 1}},
		Terminal:  &terminal.Options{Epsilon: 0.125, MajorMass: 0.02},
	}
	beforeOpinions := slices.Clone(opinions)
	beforeBins := slices.Clone(options.Bins)
	beforeQueries := slices.Clone(options.Landscape.Queries)
	beforeEdges := graph.Edges().Len()

	var measure StateMeasurer
	got, err := measure.Measure(opinions, graph, options)
	if err != nil {
		t.Fatal(err)
	}
	energy, err := MeasureEnergy(opinions, graph, 0.5, 2)
	if err != nil {
		t.Fatal(err)
	}
	pathway, err := MeasurePathway(opinions, graph, 0.75)
	if err != nil {
		t.Fatal(err)
	}
	binned, err := MeasureBinnedState(opinions, graph, beforeBins)
	if err != nil {
		t.Fatal(err)
	}
	landscape, err := MeasureLandscape(opinions, graph, 0.25, 4, beforeQueries)
	if err != nil {
		t.Fatal(err)
	}
	classified, err := terminal.ClassifyOpinions(opinions, 0.125, 0.02, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := StateResult{Energy: &energy, Pathway: &pathway, Binned: &binned, Landscape: &landscape, Terminal: &classified}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shared measurement differs from canonical kernels:\n got %+v\nwant %+v", got, want)
	}
	if !slices.Equal(opinions, beforeOpinions) || !slices.Equal(options.Bins, beforeBins) || !slices.Equal(options.Landscape.Queries, beforeQueries) || graph.Edges().Len() != beforeEdges {
		t.Fatal("measurement modified its inputs")
	}
	if _, err := measure.Measure([]float64{-1, 1}, nil, options); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("later workspace use changed an earlier result")
	}
}

func TestStateMeasurerValidatesOnlySelectedInputs(t *testing.T) {
	var measure StateMeasurer
	if got, err := measure.Measure(nil, nil, StateOptions{}); err != nil || !reflect.DeepEqual(got, StateResult{}) {
		t.Fatalf("empty selection imposed state requirements: %+v, %v", got, err)
	}
	invalidGraph := simple.NewDirectedGraph()
	invalidGraph.AddNode(simple.Node(100))
	if _, err := measure.Measure([]float64{0}, invalidGraph, StateOptions{Terminal: &terminal.Options{Epsilon: 0, MajorMass: 0.02}}); err != nil {
		t.Fatalf("terminal classification required an unused graph: %v", err)
	}
	for _, tc := range []struct {
		name     string
		opinions []float64
		graph    *simple.DirectedGraph
		options  StateOptions
	}{
		{"energy_scale_has_no_default", []float64{0}, nil, StateOptions{Energy: &EnergyOptions{}}},
		{"energy_graph", []float64{0}, invalidGraph, StateOptions{Energy: &EnergyOptions{Scale: 1}}},
		{"pathway_population", []float64{0}, nil, StateOptions{Pathway: &PathwayOptions{}}},
		{"bins", []float64{0}, nil, StateOptions{Bins: []float64{0}}},
		{"landscape_queries", []float64{0}, nil, StateOptions{Landscape: &LandscapeOptions{Scale: 1, Queries: []float64{math.NaN()}}}},
		{"terminal_epsilon", []float64{0}, nil, StateOptions{Terminal: &terminal.Options{Epsilon: math.NaN(), MajorMass: 0.02}}},
		{"terminal_occupied_mass", []float64{0}, nil, StateOptions{Terminal: &terminal.Options{OccupiedMass: 0.1, MajorMass: 0.02}}},
		{"terminal_nonfinite_occupied_mass", []float64{0}, nil, StateOptions{Terminal: &terminal.Options{OccupiedMass: math.NaN(), MajorMass: 0.02}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := measure.Measure(tc.opinions, tc.graph, tc.options); err == nil {
				t.Fatal("accepted invalid selected measurement inputs")
			}
		})
	}
}

func TestStateResultWireNamesAndOmission(t *testing.T) {
	var measure StateMeasurer
	result, err := measure.Measure([]float64{-0.5, 0.5}, nil, StateOptions{Pathway: &PathwayOptions{Epsilon: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []struct {
		name      string
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{
		{"json", json.Marshal, json.Unmarshal},
		{"msgpack", msgpack.Marshal, msgpack.Unmarshal},
	} {
		t.Run(codec.name, func(t *testing.T) {
			data, err := codec.marshal(StateResult{})
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := codec.unmarshal(data, &values); err != nil || len(values) != 0 {
				t.Fatalf("empty results must omit all measurements: %v, %v", values, err)
			}
			data, err = codec.marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := codec.unmarshal(data, &values); err != nil {
				t.Fatal(err)
			}
			pathway, ok := values["pathway"].(map[string]any)
			if !ok || len(values) != 1 || pathway["pair_concordance"] == nil {
				t.Fatalf("noncanonical measurement keys: %v", values)
			}
		})
	}
}
