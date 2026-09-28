package probe

import (
	"errors"

	"smp/observables"
	"smp/utils"
)

func measureStatic(request *Request, selected map[string]bool, grid []float64, params driftParams, results []StateResult) error {
	if !selected["energy"] && !selected["landscape"] && !selected["counterfactual_neighbor_energy"] {
		return nil
	}
	epsilon := params.tolerance
	if request.EnergyEpsilon != nil {
		epsilon = *request.EnergyEpsilon
	}
	scale := request.EnergyScale
	if scale == 0 {
		scale = 1
	}
	for i := range request.States {
		if err := measureStaticState(&request.States[i], request.AnchorIDs, selected, grid, epsilon, scale, &results[i]); err != nil {
			return err
		}
	}
	return nil
}

func measureStaticState(state *FrozenState, anchorIDs []int64, selected map[string]bool, grid []float64, epsilon, scale float64, result *StateResult) error {
	graph := utils.DeserializeGraph(&state.Graph)
	if selected["energy"] {
		value, err := observables.MeasureEnergy(state.Opinions, graph, epsilon, scale)
		if err != nil {
			return err
		}
		result.Energy = &value
	}
	if selected["landscape"] {
		value, err := observables.MeasureLandscape(state.Opinions, graph, epsilon, scale, grid)
		if err != nil {
			return err
		}
		result.Landscape = &value
	}
	if selected["counterfactual_neighbor_energy"] {
		if len(anchorIDs) == 0 {
			return errors.New("counterfactual_neighbor_energy requires anchor_ids")
		}
		values := make(map[int64][]float64, len(anchorIDs))
		valid := make(map[int64]bool, len(anchorIDs))
		for _, id := range anchorIDs {
			curve, isValid, err := observables.CounterfactualNeighborEnergy(state.Opinions, graph, id, epsilon, scale, grid)
			if err != nil {
				return err
			}
			values[id] = curve
			valid[id] = isValid
		}
		result.CounterfactualNeighborEnergy = values
		result.CounterfactualNeighborValid = valid
	}
	return nil
}
