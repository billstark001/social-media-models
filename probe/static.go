package probe

import (
	"smp/observables"
	"smp/record"
	"smp/terminal"
	"smp/utils"

	"gonum.org/v1/gonum/graph/simple"
)

func energyOptions(request *Request, epsilon float64) observables.EnergyOptions {
	if request.Epsilon != nil {
		epsilon = *request.Epsilon
	}
	if request.EnergyEpsilon != nil {
		epsilon = *request.EnergyEpsilon
	}
	scale := request.EnergyScale
	if scale == 0 {
		scale = 1
	}
	return observables.EnergyOptions{Epsilon: epsilon, Scale: scale}
}

func staticOptions(request *Request, selected map[string]bool, grid []float64, params driftParams) observables.StateOptions {
	epsilon := params.tolerance
	if request.Epsilon != nil {
		epsilon = *request.Epsilon
	}
	energy := energyOptions(request, params.tolerance)
	options := observables.StateOptions{}
	if selected["energy"] {
		options.Energy = &energy
	}
	if selected["pathway"] {
		options.Pathway = &observables.PathwayOptions{Epsilon: epsilon}
	}
	if selected["bins"] {
		options.Bins = request.Bins
	}
	if selected["landscape"] {
		options.Landscape = &observables.LandscapeOptions{Epsilon: energy.Epsilon, Scale: energy.Scale, Queries: grid}
	}
	if selected["terminal"] {
		options.Terminal = &terminal.Options{Epsilon: epsilon, MajorMass: 0.02}
		if request.Terminal != nil {
			*options.Terminal = *request.Terminal
		}
	}
	return options
}

func measureStatic(request *Request, selected map[string]bool, grid []float64, params driftParams, results []StateResult) error {
	evaluator := newStaticEvaluator(request, selected, grid, params)
	for i := range request.States {
		if err := evaluator.measure(&request.States[i], &results[i]); err != nil {
			return err
		}
	}
	return nil
}

type staticEvaluator struct {
	options        observables.StateOptions
	needGraph      bool
	counterfactual bool
	energy         observables.EnergyOptions
	anchors        []int64
	grid           []float64
	measurer       observables.StateMeasurer
}

func newStaticEvaluator(request *Request, selected map[string]bool, grid []float64, params driftParams) staticEvaluator {
	return staticEvaluator{
		options: staticOptions(request, selected, grid, params), needGraph: requiresGraph(selected),
		counterfactual: selected["counterfactual_neighbor_energy"], energy: energyOptions(request, params.tolerance),
		anchors: request.AnchorIDs, grid: grid,
	}
}

func (e *staticEvaluator) measure(state *FrozenState, result *StateResult) error {
	if e.options.Energy == nil && e.options.Pathway == nil && e.options.Bins == nil && e.options.Landscape == nil && e.options.Terminal == nil && !e.counterfactual {
		return nil
	}
	var graph *simple.DirectedGraph
	if e.needGraph {
		graph = utils.DeserializeGraph(&state.Graph)
	}
	value, err := e.measurer.Measure(state.Opinions, graph, e.options)
	if err != nil {
		return err
	}
	result.Energy, result.Pathway, result.Binned = value.Energy, value.Pathway, value.Binned
	result.Landscape, result.Terminal = value.Landscape, value.Terminal
	if e.counterfactual {
		values := make(map[int64][]float64, len(e.anchors))
		valid := make(map[int64]bool, len(e.anchors))
		for _, id := range e.anchors {
			curve, isValid, err := observables.CounterfactualNeighborEnergy(state.Opinions, graph, id, e.energy.Epsilon, e.energy.Scale, e.grid)
			if err != nil {
				return err
			}
			values[id], valid[id] = curve, isValid
		}
		result.CounterfactualNeighborEnergy, result.CounterfactualNeighborValid = values, valid
	}
	return nil
}

type staticRunReader interface {
	ReadStates([]int, record.Requirements, func(record.State) error) error
}

// measureStaticRun consumes each microscopic state inside the reader callback.
// Only independent measurement results outlive a callback; request.States stays
// empty and the canonical measurer reuses one stream's workspace.
func measureStaticRun(request *Request, selected map[string]bool, grid []float64, params driftParams, run staticRunReader, steps []int) ([]StateResult, error) {
	evaluator := newStaticEvaluator(request, selected, grid, params)
	results := make([]StateResult, 0, len(steps))
	err := run.ReadStates(steps, record.Requirements{Graph: requiresGraph(selected)}, func(state record.State) error {
		frozen := frozenRunState(state)
		validation := *request
		validation.States = []FrozenState{frozen}
		if err := validateRequest(&validation, selected); err != nil {
			return err
		}
		result := StateResult{Step: frozen.Step, Precision: frozen.Precision}
		if err := evaluator.measure(&frozen, &result); err != nil {
			return err
		}
		results = append(results, result)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func evaluateStaticRun(request *Request, selected map[string]bool, grid []float64) (*Response, error) {
	if err := validateSourceSelection(request); err != nil {
		return nil, err
	}
	run, err := openRunSource(request, selected)
	if err != nil {
		return nil, err
	}
	defer run.Close()
	epsilon, err := staticEpsilon(request, selected)
	if err != nil {
		return nil, err
	}
	results, err := measureStaticRun(request, selected, grid, driftParams{tolerance: epsilon}, run, runSteps(request, run))
	if err != nil {
		return nil, err
	}
	return &Response{Version: ProtocolVersion, DynamicsType: request.DynamicsType, Results: results, SourceMaxStep: request.sourceMaxStep}, nil
}
