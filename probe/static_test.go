package probe_test

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"smp/observables"
	"smp/probe"
	smprng "smp/rng"
	"smp/simulation"
	"smp/terminal"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

func TestInvalidBinsFailBeforeLoadingSource(t *testing.T) {
	_, err := probe.Evaluate(&probe.Request{
		RunDirectory: t.TempDir(), Measurements: []string{"bins"}, Bins: []float64{0, 0},
	})
	if err == nil || !strings.Contains(err.Error(), "boundaries") {
		t.Fatalf("read a source before validating known-invalid bins: %v", err)
	}
}

func TestStaticMeasurementsShareOnlineKernelWithoutForceInputs(t *testing.T) {
	state := frozenState()
	state.Posts = nil
	epsilon, energyEpsilon := 0.6, 0.9
	request := &probe.Request{
		Version:       probe.ProtocolVersion,
		States:        []probe.FrozenState{state},
		Measurements:  []string{"energy", "landscape", "pathway", "bins", "terminal"},
		Epsilon:       &epsilon,
		EnergyEpsilon: &energyEpsilon,
		EnergyScale:   2,
		Bins:          []float64{-1, 0, 1},
		Grid:          probe.Grid{Min: -1, Max: 1, Step: 1},
		// Static observables impose no requirements on drift configuration.
		HKParams: requestFor(simulation.DynamicsTypeHK).HKParams,
		RNG:      smprng.Spec{Algorithm: "invalid-for-force"},
	}
	request.HKParams.Influence = math.NaN()
	got, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	var canonical observables.StateMeasurer
	want, err := canonical.Measure(state.Opinions, utils.DeserializeGraph(&state.Graph), observables.StateOptions{
		Energy:    &observables.EnergyOptions{Epsilon: energyEpsilon, Scale: 2},
		Pathway:   &observables.PathwayOptions{Epsilon: epsilon},
		Bins:      request.Bins,
		Landscape: &observables.LandscapeOptions{Epsilon: energyEpsilon, Scale: 2, Queries: []float64{-1, 0, 1}},
		Terminal:  &terminal.Options{Epsilon: epsilon, MajorMass: .02},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := got.Results[0]
	if !reflect.DeepEqual(result.Pathway, want.Pathway) || !reflect.DeepEqual(result.Binned, want.Binned) ||
		!reflect.DeepEqual(result.Terminal, want.Terminal) {
		t.Fatalf("probe and canonical state measurements differ: %+v", result)
	}
	if math.Abs(result.Energy.Edge-want.Energy.Edge) > 1e-12 || !reflect.DeepEqual(result.Landscape, want.Landscape) {
		t.Fatal("energy override did not stay within energy measurements")
	}
	if !got.RNG.IsZero() || len(result.Points) != 0 {
		t.Fatal("static measurements used the force path")
	}
}

func TestTerminalOnlyNeedsOpinionsAndHonorsExplicitOptions(t *testing.T) {
	request := &probe.Request{
		States:       []probe.FrozenState{{Step: 4, Opinions: []float64{0, 0, .2, .2}}},
		Measurements: []string{"terminal"},
		Terminal:     &terminal.Options{Epsilon: 0, MajorMass: .25},
	}
	got, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Results[0].Terminal.KMajor != 2 {
		t.Fatalf("explicit zero epsilon was replaced: %+v", got.Results[0].Terminal)
	}
	request.States[0].Graph = utils.NetworkXGraph{
		Directed:  true,
		Nodes:     map[int64]map[string]any{0: {}},
		Adjacency: map[int64]map[int64]any{0: {0: nil}},
	}
	if _, err := probe.Evaluate(request); err != nil {
		t.Fatalf("terminal-only evaluated unused graph: %v", err)
	}
}

func TestForceRejectsMissingPostsAndStaticRejectsMalformedTopology(t *testing.T) {
	request := requestFor(simulation.DynamicsTypeHK)
	request.States[0].Posts = nil
	if _, err := probe.Evaluate(request); err == nil {
		t.Fatal("force synthesized missing post histories")
	}
	request.Measurements = []string{"pathway"}
	request.States[0].Graph.Adjacency[0][0] = nil
	if _, err := probe.Evaluate(request); err == nil {
		t.Fatal("a self edge was accepted")
	}
	request.States = []probe.FrozenState{frozenState()}
	request.Measurements = []string{"bins"}
	if _, err := probe.Evaluate(request); err == nil {
		t.Fatal("bins without boundaries were silently omitted")
	}
}

func TestStaticSelectionDoesNotResetForceSampling(t *testing.T) {
	request := requestFor(simulation.DynamicsTypeHK)
	request.RecsysFactoryType = "Random"
	second := frozenState()
	second.Step++
	request.States = append(request.States, second)
	plain, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Measurements = []string{"force", "energy", "pathway", "terminal"}
	mixed, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if mixed.RNG != plain.RNG {
		t.Fatal("static measurement changed the force root")
	}
	for i := range plain.Results {
		if !reflect.DeepEqual(plain.Results[i].Points, mixed.Results[i].Points) {
			t.Fatalf("force sampling changed in selected state %d", i)
		}
	}
}

func TestProbeJSONAndConfiguredMsgpackUseSameFieldNames(t *testing.T) {
	request := &probe.Request{
		States:       []probe.FrozenState{{Step: 0, Opinions: []float64{0, 0}}},
		Measurements: []string{"terminal"},
		Terminal:     &terminal.Options{Epsilon: .45, MajorMass: .02},
	}
	jsonBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded probe.Request
	if err = json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatal(err)
	}
	response, err := probe.Evaluate(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	encoder := msgpack.NewEncoder(&encoded)
	encoder.SetCustomStructTag("json")
	if err := encoder.Encode(response); err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = msgpack.Unmarshal(encoded.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	results := wire["results"].([]any)
	terminalWire := results[0].(map[string]any)["terminal"].(map[string]any)
	if _, ok := terminalWire["k_major"]; !ok {
		t.Fatalf("terminal wire field names differ: %+v", terminalWire)
	}
}
