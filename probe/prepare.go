package probe

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"smp/observables"
	"smp/record"
	"smp/simulation"
)

var errNilRequest = errors.New("nil probe request")

const maxGridPoints = 1_000_001

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
	rounded := math.Round(span / grid.Step)
	if !isFinite(span) || !isFinite(rounded) || rounded >= float64(int(^uint(0)>>1)) {
		return nil, errors.New("grid point count exceeds supported integer range")
	}
	if rounded >= maxGridPoints {
		return nil, fmt.Errorf("grid may contain at most %d points", maxGridPoints)
	}
	intervals := int(rounded)
	if intervals == 0 && span != 0 || math.Abs(float64(intervals)*grid.Step-span) > 1e-9*max(1, math.Abs(span)) {
		return nil, fmt.Errorf("grid step %v does not evenly divide [%v, %v]", grid.Step, grid.Min, grid.Max)
	}
	values := make([]float64, intervals+1)
	for i := range values {
		values[i] = grid.Min + float64(i)*grid.Step
	}
	values[len(values)-1] = grid.Max
	for i := 1; i < len(values); i++ {
		if values[i] <= values[i-1] {
			return nil, errors.New("grid coordinates must increase at float64 precision")
		}
	}
	return values, nil
}

func requiresGraph(selected map[string]bool) bool {
	return selected["force"] || requiresStaticGraph(selected) || selected["bridges"]
}

func requiresStaticGraph(selected map[string]bool) bool {
	return selected["pathway"] || selected["energy"] || selected["landscape"] || selected["counterfactual_neighbor_energy"] || selected["bins"]
}

func sourceRequirements(request *Request, selected map[string]bool) record.Requirements {
	return record.Requirements{Graph: requiresGraph(selected), Posts: selected["force"] || selected["bridges"], PostsOptional: selected["bridges"] && !selected["force"], LegacyHeaderActor: request.SourceCompatibility == "legacy_header_actor"}
}

func validateRequest(request *Request, selected map[string]bool) error {
	if len(request.States) == 0 {
		return errors.New("at least one frozen state is required")
	}
	if selected["counterfactual_neighbor_energy"] && len(request.AnchorIDs) == 0 {
		return errors.New("counterfactual_neighbor_energy requires anchor_ids")
	}
	if selected["force"] || selected["counterfactual_neighbor_energy"] {
		seen := make(map[int64]bool, len(request.AnchorIDs))
		for _, id := range request.AnchorIDs {
			if id < 0 || seen[id] {
				return fmt.Errorf("anchor_ids must contain distinct nonnegative IDs; got %d", id)
			}
			seen[id] = true
		}
	}
	needGraph := requiresGraph(selected)
	for _, state := range request.States {
		if state.Step < 0 {
			return errors.New("frozen state step must be nonnegative")
		}
		if needGraph {
			if err := validateFrozenGraph(&state); err != nil {
				return err
			}
		}
		if selected["bridges"] && request.BridgeCores != nil {
			if err := validateBridgeCores(request.BridgeCores, len(state.Opinions)); err != nil {
				return fmt.Errorf("state %d: %w", state.Step, err)
			}
		}
		if selected["force"] || selected["counterfactual_neighbor_energy"] {
			for _, id := range request.AnchorIDs {
				if id >= int64(len(state.Opinions)) {
					return fmt.Errorf("state %d has no anchor agent %d", state.Step, id)
				}
			}
		}
	}
	if selected["force"] {
		if err := simulation.ValidateRecsysParams(request.RecSysParams); err != nil {
			return err
		}
		if request.Replicates <= 0 {
			request.Replicates = 1
		}
		if request.RecsysCount <= 0 {
			return fmt.Errorf("recsys_count must be > 0, got %d", request.RecsysCount)
		}
		if request.PostRetainCount < 0 {
			return fmt.Errorf("post_retain_count must be >= 0, got %d", request.PostRetainCount)
		}
		for _, state := range request.States {
			if err := validateForceState(&state); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateForceState(state *FrozenState) error {
	if state.Step < 0 || len(state.Opinions) == 0 {
		return errors.New("force requires a nonempty completed state")
	}
	if state.Step == int(^uint(0)>>1) {
		return errors.New("force step cannot be advanced to a simulator step without integer overflow")
	}
	n := len(state.Opinions)
	for i, opinion := range state.Opinions {
		if !isFinite(opinion) {
			return fmt.Errorf("state %d opinion %d must be finite", state.Step, i)
		}
		posts, ok := state.Posts[int64(i)]
		if !ok || len(posts) == 0 {
			return fmt.Errorf("state %d has no post history for agent %d", state.Step, i)
		}
		for _, post := range posts {
			if post.AgentID < 0 || post.AgentID >= int64(n) || post.Step < -1 || post.Step > state.Step || !isFinite(post.Opinion) {
				return fmt.Errorf("state %d has invalid post history for agent %d", state.Step, i)
			}
		}
	}
	if len(state.Posts) != n {
		return fmt.Errorf("state %d posts include unknown agents", state.Step)
	}
	return nil
}

func validateFrozenGraph(state *FrozenState) error {
	n := len(state.Opinions)
	if !state.Graph.Directed || len(state.Graph.Nodes) != n {
		return fmt.Errorf("state %d requires a directed graph with one node per opinion", state.Step)
	}
	for i := range state.Opinions {
		if _, ok := state.Graph.Nodes[int64(i)]; !ok {
			return fmt.Errorf("state %d graph node IDs must be dense", state.Step)
		}
	}
	for source, targets := range state.Graph.Adjacency {
		if source < 0 || source >= int64(n) {
			return fmt.Errorf("state %d has an out-of-range edge source", state.Step)
		}
		for target := range targets {
			if target < 0 || target >= int64(n) || target == source {
				return fmt.Errorf("state %d has an invalid edge", state.Step)
			}
		}
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
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

func prepareMeasurements(request *Request) (map[string]bool, []float64, error) {
	if request == nil {
		return nil, nil, errNilRequest
	}
	selected, err := selectMeasurements(request.Measurements)
	if err != nil {
		return nil, nil, err
	}
	if selected["groups"] && request.Groups != nil {
		if err := request.Groups.Validate(); err != nil {
			return nil, nil, fmt.Errorf("groups: %w", err)
		}
	}
	if selected["bridges"] {
		if request.Bridges != nil {
			if err := request.Bridges.Validate(); err != nil {
				return nil, nil, err
			}
		}
		if request.BridgeCores != nil {
			if err := validateBridgeCores(request.BridgeCores, -1); err != nil {
				return nil, nil, err
			}
		}
	}
	if selected["bins"] {
		if err := observables.ValidateBinBoundaries(request.Bins); err != nil {
			return nil, nil, err
		}
	}
	var grid []float64
	if selected["force"] || selected["landscape"] || selected["counterfactual_neighbor_energy"] {
		grid, err = gridValues(request.Grid)
		if err != nil {
			return nil, nil, err
		}
	}
	return selected, grid, nil
}

func prepareStates(request *Request, selected map[string]bool) (driftParams, error) {
	if err := loadSources(request, selected); err != nil {
		return driftParams{}, err
	}
	if err := validateRequest(request, selected); err != nil {
		return driftParams{}, err
	}
	var params driftParams
	var err error
	if selected["force"] {
		params, err = paramsForRequest(request)
	} else {
		params.tolerance, err = staticEpsilon(request, selected)
	}
	return params, err
}

func staticEpsilon(request *Request, selected map[string]bool) (float64, error) {
	if !usesCommonEpsilon(request, selected) {
		return 0, nil
	}
	var epsilon float64
	switch {
	case request.Epsilon != nil:
		epsilon = *request.Epsilon
	case request.DynamicsType == simulation.DynamicsTypeHK:
		epsilon = request.HKParams.Tolerance
	case request.DynamicsType == simulation.DynamicsTypeDeffuant:
		epsilon = request.DeffuantParams.Tolerance
	default:
		return 0, errors.New("selected measurements require epsilon or continuous-dynamics metadata")
	}
	if !isFinite(epsilon) || epsilon < 0 {
		return 0, errors.New("epsilon must be finite and nonnegative")
	}
	return epsilon, nil
}

func usesCommonEpsilon(request *Request, selected map[string]bool) bool {
	needCommon := selected["pathway"] || (selected["terminal"] && request.Terminal == nil)
	needCommon = needCommon || (selected["groups"] && request.Groups == nil && (!selected["bridges"] || request.Bridges == nil))
	needEnergy := selected["energy"] || selected["landscape"] || selected["counterfactual_neighbor_energy"]
	return needCommon || (needEnergy && request.EnergyEpsilon == nil)
}

// Fixed reference IDs are a separate namespace from dynamic group lineage IDs.
// Explicit cohorts can be smaller than detector MinCore, but cannot overlap.
func validateBridgeCores(cores [][]int, agents int) error {
	if len(cores) < 2 {
		return errors.New("bridge_cores requires at least two nonempty disjoint cohorts")
	}
	seen := make(map[int]bool)
	for i, members := range cores {
		if len(members) == 0 {
			return fmt.Errorf("bridge_cores cohort %d is empty", i)
		}
		for _, id := range members {
			if id < 0 || agents >= 0 && id >= agents || seen[id] {
				return fmt.Errorf("bridge_cores requires distinct in-range agent IDs; got %d in cohort %d", id, i)
			}
			seen[id] = true
		}
	}
	return nil
}

func validateRunDefaults(request *Request, selected map[string]bool, fields map[string]json.RawMessage) error {
	if request.DynamicsType != simulation.DynamicsTypeHK && request.DynamicsType != simulation.DynamicsTypeDeffuant {
		return nil
	}
	var required []string
	if selected["force"] {
		required = []string{"Tolerance", "Influence"}
	} else if request.Epsilon == nil && usesCommonEpsilon(request, selected) {
		required = []string{"Tolerance"}
	}
	if len(required) == 0 {
		return nil
	}
	paramsName := request.DynamicsType + "Params"
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal(fields[paramsName], &parameters); err != nil {
		return fmt.Errorf("run metadata lacks explicit %s required by selected measurements", paramsName)
	}
	for _, name := range required {
		var value *float64
		if err := json.Unmarshal(parameters[name], &value); err != nil || value == nil {
			return fmt.Errorf("run metadata lacks explicit %s.%s required by selected measurements", paramsName, name)
		}
	}
	return nil
}

func validateSourceSelection(request *Request) error {
	if request.SourceCompatibility != "" && request.SourceCompatibility != "legacy_header_actor" {
		return errors.New("unknown source_compatibility; supported historical exception: legacy_header_actor")
	}
	if request.Scan != nil && (request.RunDirectory == "" || len(request.Steps) != 0) {
		return errors.New("scan requires run_directory and cannot be combined with steps")
	}
	if request.SourceCompatibility != "" && request.RunDirectory == "" {
		return errors.New("source_compatibility requires run_directory")
	}
	sources := 0
	for _, present := range []bool{len(request.States) > 0, len(request.CheckpointPaths) > 0, request.RunDirectory != ""} {
		if present {
			sources++
		}
	}
	if sources != 1 {
		return errors.New("exactly one of states, checkpoint_paths or run_directory is required")
	}
	if request.RunDirectory == "" {
		if request.RunMetadata != nil {
			return errors.New("run_metadata is supported only with run_directory")
		}
		if len(request.Steps) > 0 {
			return errors.New("steps are supported only with run_directory")
		}
	}
	return nil
}

func loadSources(request *Request, selected map[string]bool) error {
	if err := validateSourceSelection(request); err != nil {
		return err
	}
	if request.RunDirectory == "" {
		return loadCheckpointStates(request, selected)
	}
	run, err := openRunSource(request, selected)
	if err != nil {
		return err
	}
	defer run.Close()
	return run.ReadStates(runSteps(request, run), sourceRequirements(request, selected), func(state record.State) error {
		request.States = append(request.States, frozenRunState(state))
		return nil
	})
}

func openRunSource(request *Request, selected map[string]bool) (*record.Reader, error) {
	run, err := record.OpenRun(request.RunDirectory, request.RunMetadata)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			run.Close()
		}
	}()
	if selected["force"] && run.Metadata.DataVersion >= 3 {
		return nil, errors.New("force reconstruction from research runs requires explicit frozen states and an explicitly chosen recommender; static measurements remain available")
	}
	if request.DynamicsType != "" && request.DynamicsType != run.Metadata.DynamicsType {
		return nil, errors.New("run dynamics differ from request")
	}
	applySourceMetadata(request, run.Metadata)
	if err := validateRunDefaults(request, selected, run.MetadataFields); err != nil {
		return nil, err
	}
	maxStep := run.MaxStep
	request.sourceMaxStep = &maxStep
	if err := validateScan(request.Scan, maxStep); err != nil {
		return nil, err
	}
	ok = true
	return run, nil
}

func runSteps(request *Request, run *record.Reader) []int {
	if request.Scan != nil {
		return scanSteps(request.Scan, run.MaxStep)
	}
	if len(request.Steps) == 0 {
		return []int{run.MaxStep}
	}
	return request.Steps
}

func frozenRunState(state record.State) FrozenState {
	return FrozenState{Step: state.Step, Graph: state.Graph, Opinions: state.Opinions, Posts: state.Posts, Precision: state.Precision, PostsUnavailableReason: state.PostsUnavailableReason}
}

func selectMeasurements(names []string) (map[string]bool, error) {
	selected := make(map[string]bool)
	if len(names) == 0 {
		selected["force"] = true
	}
	for _, name := range names {
		switch name {
		case "force", "energy", "landscape", "counterfactual_neighbor_energy", "pathway", "bins", "terminal", "groups", "bridges":
			selected[name] = true
			if name == "bridges" {
				selected["groups"] = true
			}
		default:
			return nil, fmt.Errorf("unknown measurement %q", name)
		}
	}
	return selected, nil
}

func loadCheckpointStates(request *Request, selected map[string]bool) error {
	if len(request.CheckpointPaths) == 0 {
		return nil
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
		if snapshot.Metadata.DataVersion < 0 || snapshot.Metadata.DataVersion > simulation.CurrentDataVersion {
			return fmt.Errorf("unsupported checkpoint DataVersion %d", snapshot.Metadata.DataVersion)
		}
		if selected["force"] && snapshot.Research != nil && snapshot.Research.Options.PolicyChange != nil {
			return errors.New("force reconstruction from intervention checkpoints requires explicit frozen states and an explicitly chosen recommender; static measurements remain available")
		}
		if snapshot.DynamicsType != snapshot.Metadata.DynamicsType {
			return errors.New("checkpoint dynamics differ from its metadata")
		}
		if firstMetadata == nil {
			firstMetadata = snapshot.Metadata
		} else if err := simulation.ValidateCheckpointMetadata(firstMetadata, snapshot.Metadata); err != nil {
			return err
		}
		if request.DynamicsType != "" && request.DynamicsType != snapshot.Metadata.DynamicsType {
			return errors.New("checkpoint dynamics differ from request")
		}
		applySourceMetadata(request, snapshot.Metadata)
		view, err := simulation.InspectFloat64Checkpoint(snapshot)
		if err != nil {
			return err
		}
		request.States = append(request.States, FrozenState{Step: view.CompletedStep, Graph: view.Graph, Opinions: view.Opinions, Posts: view.Posts, Precision: "checkpoint_f64"})
	}
	return nil
}

func applySourceMetadata(request *Request, meta *simulation.ScenarioMetadata) {
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
