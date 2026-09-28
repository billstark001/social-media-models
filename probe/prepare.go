package probe

import (
	"errors"
	"fmt"
	"math"

	"smp/simulation"
)

var errNilRequest = errors.New("nil probe request")

type driftParams struct {
	tolerance float64
	influence float64
}

func gridValues(grid Grid) ([]float64, error) {
	if math.IsNaN(grid.Min) || math.IsNaN(grid.Max) || math.IsNaN(grid.Step) ||
		math.IsInf(grid.Min, 0) || math.IsInf(grid.Max, 0) || math.IsInf(grid.Step, 0) {
		return nil, errors.New("grid values must be finite")
	}
	if grid.Step <= 0 {
		return nil, fmt.Errorf("grid step must be > 0, got %v", grid.Step)
	}
	if grid.Max < grid.Min {
		return nil, fmt.Errorf("grid max %v is less than min %v", grid.Max, grid.Min)
	}
	span := grid.Max - grid.Min
	intervals := int(math.Round(span / grid.Step))
	if math.Abs(float64(intervals)*grid.Step-span) > 1e-9*max(1, math.Abs(span)) {
		return nil, fmt.Errorf("grid step %v does not evenly divide [%v, %v]", grid.Step, grid.Min, grid.Max)
	}
	values := make([]float64, intervals+1)
	for i := range values {
		values[i] = grid.Min + float64(i)*grid.Step
	}
	values[len(values)-1] = grid.Max
	return values, nil
}

func validateRequest(request *Request, needGrid bool) ([]float64, error) {
	if request == nil {
		return nil, errors.New("nil probe request")
	}
	var values []float64
	if needGrid {
		var err error
		values, err = gridValues(request.Grid)
		if err != nil {
			return nil, err
		}
	}
	if request.Replicates <= 0 {
		request.Replicates = 1
	}
	if request.RecsysCount <= 0 {
		return nil, fmt.Errorf("recsys_count must be > 0, got %d", request.RecsysCount)
	}
	if request.PostRetainCount < 0 {
		return nil, fmt.Errorf("post_retain_count must be >= 0, got %d", request.PostRetainCount)
	}
	if len(request.States) == 0 {
		return nil, errors.New("at least one frozen state is required")
	}
	return values, nil
}

func paramsForRequest(request *Request) (driftParams, error) {
	var params driftParams
	switch request.DynamicsType {
	case simulation.DynamicsTypeHK:
		params = driftParams{
			tolerance: request.HKParams.Tolerance,
			influence: request.HKParams.Influence,
		}
	case simulation.DynamicsTypeDeffuant:
		params = driftParams{
			tolerance: request.DeffuantParams.Tolerance,
			influence: request.DeffuantParams.Influence,
		}
	default:
		return params, fmt.Errorf(
			"unsupported dynamics_type %q (supported: HK, Deffuant)",
			request.DynamicsType,
		)
	}
	if math.IsNaN(params.tolerance) || math.IsInf(params.tolerance, 0) || params.tolerance < 0 {
		return params, fmt.Errorf("%s tolerance must be finite and >= 0", request.DynamicsType)
	}
	if math.IsNaN(params.influence) || math.IsInf(params.influence, 0) ||
		params.influence < 0 || params.influence > 1 {
		return params, fmt.Errorf("%s influence must be in [0, 1]", request.DynamicsType)
	}
	return params, nil
}

func prepareEvaluation(request *Request) (map[string]bool, []float64, driftParams, error) {
	if request == nil {
		return nil, nil, driftParams{}, errNilRequest
	}
	if err := loadCheckpointStates(request); err != nil {
		return nil, nil, driftParams{}, err
	}
	selected, err := selectMeasurements(request.Measurements)
	if err != nil {
		return nil, nil, driftParams{}, err
	}
	needGrid := selected["force"] || selected["landscape"] || selected["counterfactual_neighbor_energy"]
	grid, err := validateRequest(request, needGrid)
	if err != nil {
		return nil, nil, driftParams{}, err
	}
	params, err := paramsForRequest(request)
	return selected, grid, params, err
}

func selectMeasurements(names []string) (map[string]bool, error) {
	selected := make(map[string]bool)
	if len(names) == 0 {
		selected["force"] = true
	}
	for _, name := range names {
		switch name {
		case "force", "energy", "landscape", "counterfactual_neighbor_energy":
			selected[name] = true
		default:
			return nil, fmt.Errorf("unknown measurement %q", name)
		}
	}
	return selected, nil
}

func loadCheckpointStates(request *Request) error {
	if len(request.CheckpointPaths) == 0 {
		return nil
	}
	if len(request.States) > 0 {
		return errors.New("provide states or checkpoint_paths, not both")
	}
	var firstMetadata *simulation.ScenarioMetadata
	for _, path := range request.CheckpointPaths {
		snapshot, _, err := simulation.ReadCheckpoint(path)
		if err != nil {
			return err
		}
		if snapshot.Metadata == nil {
			return errors.New("checkpoint lacks measurement metadata")
		}
		if firstMetadata == nil {
			firstMetadata = snapshot.Metadata
		} else if err := simulation.ValidateCheckpointMetadata(firstMetadata, snapshot.Metadata); err != nil {
			return err
		}
		if request.DynamicsType != "" && request.DynamicsType != snapshot.Metadata.DynamicsType {
			return errors.New("checkpoint dynamics differ from request")
		}
		applyCheckpointMetadata(request, snapshot.Metadata)
		view, err := simulation.InspectFloat64Checkpoint(snapshot)
		if err != nil {
			return err
		}
		request.States = append(request.States, FrozenState{Step: view.CompletedStep, Graph: view.Graph, Opinions: view.Opinions, Posts: view.Posts, Precision: "checkpoint_f64"})
	}
	return nil
}

func applyCheckpointMetadata(request *Request, meta *simulation.ScenarioMetadata) {
	request.DynamicsType = meta.DynamicsType
	request.HKParams = meta.HKParams
	request.DeffuantParams = meta.DeffuantParams
	request.RecsysFactoryType = meta.RecsysFactoryType
	request.RecSysParams = meta.RecSysParams
	request.RecsysCount = meta.RecsysCount
	request.PostRetainCount = meta.PostRetainCount
	if request.RNG.IsZero() {
		request.RNG = meta.RNG
	}
}
