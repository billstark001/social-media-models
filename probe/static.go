package probe

import (
	"smp/observables"
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
	options := staticOptions(request, selected, grid, params)
	if options.Energy == nil && options.Pathway == nil && options.Bins == nil && options.Landscape == nil && options.Terminal == nil && !selected["counterfactual_neighbor_energy"] {
		return nil
	}
	var measurer observables.StateMeasurer
	for i := range request.States {
		state := &request.States[i]
		var graph *simple.DirectedGraph
		if requiresGraph(selected) {
			graph = utils.DeserializeGraph(&state.Graph)
		}
		value, err := measurer.Measure(state.Opinions, graph, options)
		if err != nil {
			return err
		}
		result := &results[i]
		result.Energy, result.Pathway, result.Binned = value.Energy, value.Pathway, value.Binned
		result.Landscape, result.Terminal = value.Landscape, value.Terminal
		if selected["counterfactual_neighbor_energy"] {
			energy := energyOptions(request, params.tolerance)
			values := make(map[int64][]float64, len(request.AnchorIDs))
			valid := make(map[int64]bool, len(request.AnchorIDs))
			for _, id := range request.AnchorIDs {
				curve, isValid, err := observables.CounterfactualNeighborEnergy(state.Opinions, graph, id, energy.Epsilon, energy.Scale, grid)
				if err != nil {
					return err
				}
				values[id], valid[id] = curve, isValid
			}
			result.CounterfactualNeighborEnergy, result.CounterfactualNeighborValid = values, valid
		}
	}
	return nil
}
