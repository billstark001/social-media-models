package batch

import (
	"context"
	"math"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/observables"
	"smp/probe"
	"smp/simulation"
	"smp/terminal"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

func TestFrozenStateMeasurementsAgreeAcrossBatchEntrypoints(t *testing.T) {
	metadata := testMetadata("static-consistency")
	scenario := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{EnableDumps: false, Quiet: true})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		scenario.Step()
	}
	dump := func() model.SMPModelDumpData[float64, dynamics.HKParams] {
		t.Helper()
		data, err := scenario.Model.RawDump()
		if err != nil {
			t.Fatal(err)
		}
		var result model.SMPModelDumpData[float64, dynamics.HKParams]
		if err := msgpack.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		// Recommender maps may serialize in a different order.
		result.RecsysDumpData = nil
		return result
	}
	before := dump()
	opinions := scenario.Model.GetOpinions()
	graph := scenario.Model.GetGraph()
	epsilon := metadata.HKParams.Tolerance
	bins := []float64{-1, -0.5, 0, 0.5, 1}
	var measure observables.StateMeasurer
	want, err := measure.Measure(opinions, graph, observables.StateOptions{
		Energy: &observables.EnergyOptions{Epsilon: epsilon, Scale: 1}, Pathway: &observables.PathwayOptions{Epsilon: epsilon},
		Bins: bins, Terminal: &terminal.Options{Epsilon: epsilon, MajorMass: 0.02},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Edge iteration order can change the final few summation bits.
	sameEnergy := func(a, b *observables.Energy) bool {
		return a != nil && b != nil && a.Nodes == b.Nodes && a.Edges == b.Edges &&
			math.Abs(a.Population-b.Population) < 1e-14 && math.Abs(a.Edge-b.Edge) < 1e-14 && math.Abs(a.TopologyExcess-b.TopologyExcess) < 1e-14
	}
	output := OutputOptions{Energy: true, Bins: bins, Terminal: &TerminalOptions{MajorMass: 0.02}, FinalOpinions: true}
	var normal Result
	if kind, err := measureNormalResult(metadata, output, scenario, &normal); err != nil {
		t.Fatalf("normal measurement failed (%s): %v", kind, err)
	}
	if !sameEnergy(normal.Energy, want.Energy) || !reflect.DeepEqual(normal.Binned, want.Binned) || !reflect.DeepEqual(normal.Terminal, want.Terminal) || !reflect.DeepEqual(normal.FinalOpinions, opinions) {
		t.Fatalf("normal result differs from canonical state measurement: energy=%+v want=%+v binned=%v terminal=%v opinions=%v", normal.Energy, want.Energy, reflect.DeepEqual(normal.Binned, want.Binned), reflect.DeepEqual(normal.Terminal, want.Terminal), reflect.DeepEqual(normal.FinalOpinions, opinions))
	}
	pointOutput := output
	pointOutput.Pathway, pointOutput.FullState = true, true
	observer, err := newEndpointObserver(metadata, pointOutput)
	if err != nil {
		t.Fatal(err)
	}
	point, err := observer.observe(scenario, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !sameEnergy(point.Energy, want.Energy) || !reflect.DeepEqual(point.Binned, want.Binned) || !reflect.DeepEqual(point.Pathway, want.Pathway) {
		t.Fatal("experiment endpoint differs from canonical state measurement")
	}
	frozen, err := probe.Evaluate(&probe.Request{
		Version: probe.ProtocolVersion, Epsilon: &epsilon, Measurements: []string{"pathway"},
		States: []probe.FrozenState{{Step: 2, Opinions: opinions, Graph: *utils.SerializeGraph(graph)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(frozen.Results[0].Pathway, want.Pathway) {
		t.Fatal("offline probe differs from canonical state measurement")
	}
	if after := dump(); !reflect.DeepEqual(before, after) {
		t.Fatal("static observation changed model state or simulation RNG streams")
	}
	replicate, err := runReplicate(context.Background(), metadata, nil, "", &ExperimentSpec{Mode: "fresh", Replicates: 1, ObserveAt: []int{2}}, pointOutput, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replicate.Terminal, want.Terminal) || !reflect.DeepEqual(replicate.FinalOpinions, opinions) {
		t.Fatal("final experiment classification differs from the same frozen state")
	}
}

func TestNormalMeasurementErrorKinds(t *testing.T) {
	metadata := testMetadata("static-errors")
	scenario := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{EnableDumps: false, Quiet: true})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		output OutputOptions
		kind   string
	}{
		{"terminal", OutputOptions{Terminal: &TerminalOptions{MajorMass: math.NaN()}}, "terminal_classification"},
		{"bins_before_terminal", OutputOptions{Bins: []float64{2, 3}, Terminal: &TerminalOptions{MajorMass: math.NaN()}}, "measurement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if kind, err := measureNormalResult(metadata, tc.output, scenario, &Result{}); err == nil || kind != tc.kind {
				t.Fatalf("measurement error changed kind: %q, %v", kind, err)
			}
		})
	}
	metadata.DynamicsType = "Boolean"
	if kind, err := measureNormalResult(metadata, OutputOptions{Bins: []float64{-1, 1}}, scenario, &Result{}); err != nil {
		t.Fatalf("bins required unused continuous dynamics parameters (%s): %v", kind, err)
	}
}
