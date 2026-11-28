// Package probe evaluates counterfactual opinion drift on frozen simulation
// states without advancing or mutating the original simulation.
package probe

import (
	"fmt"
	"smp/bridges"
	"smp/dynamics"
	"smp/groups"
	"smp/model"
	"smp/observables"
	smprng "smp/rng"
	"smp/terminal"
	"smp/utils"
)

// ProtocolVersion is the current msgpack request and response version.
// Requests that omit the version are legacy version 0.
const ProtocolVersion = 1

// Grid specifies an inclusive, evenly-spaced opinion grid.
type Grid struct {
	Min  float64 `msgpack:"min" json:"min"`
	Max  float64 `msgpack:"max" json:"max"`
	Step float64 `msgpack:"step" json:"step"`
}

// FrozenState contains only the state required to evaluate recommendations
// and continuous-opinion drift at one completed simulation step.
type FrozenState struct {
	Step                   int                                   `msgpack:"step" json:"step"`
	Graph                  utils.NetworkXGraph                   `msgpack:"graph" json:"graph"`
	Opinions               []float64                             `msgpack:"opinions" json:"opinions"`
	Posts                  map[int64][]model.PostRecord[float64] `msgpack:"posts" json:"posts"`
	Precision              string                                `msgpack:"precision" json:"precision"`
	PostsUnavailableReason string                                `msgpack:"posts_unavailable_reason,omitempty" json:"posts_unavailable_reason,omitempty"`
}

// Request is the stdin protocol consumed by cmd/smp-probe.
type Request struct {
	Version             int                     `msgpack:"version" json:"version"`
	RNG                 smprng.Spec             `msgpack:"rng" json:"rng"`
	DynamicsType        string                  `msgpack:"dynamics_type" json:"dynamics_type"`
	HKParams            dynamics.HKParams       `msgpack:"hk_params" json:"hk_params"`
	DeffuantParams      dynamics.DeffuantParams `msgpack:"deffuant_params" json:"deffuant_params"`
	RecsysFactoryType   string                  `msgpack:"recsys_factory_type" json:"recsys_factory_type"`
	RecSysParams        map[string]any          `msgpack:"recsys_params" json:"recsys_params"`
	RecsysCount         int                     `msgpack:"recsys_count" json:"recsys_count"`
	PostRetainCount     int                     `msgpack:"post_retain_count" json:"post_retain_count"`
	Grid                Grid                    `msgpack:"grid" json:"grid"`
	Replicates          int                     `msgpack:"replicates" json:"replicates"`
	AnchorIDs           []int64                 `msgpack:"anchor_ids" json:"anchor_ids"`
	PerAnchor           bool                    `msgpack:"per_anchor" json:"per_anchor"`
	States              []FrozenState           `msgpack:"states" json:"states"`
	CheckpointPaths     []string                `msgpack:"checkpoint_paths" json:"checkpoint_paths"`
	Measurements        []string                `msgpack:"measurements" json:"measurements"`
	EnergyScale         float64                 `msgpack:"energy_scale" json:"energy_scale"`
	EnergyEpsilon       *float64                `msgpack:"energy_epsilon" json:"energy_epsilon"`
	Epsilon             *float64                `msgpack:"epsilon,omitempty" json:"epsilon,omitempty"`
	Bins                []float64               `msgpack:"bins,omitempty" json:"bins,omitempty"`
	Terminal            *terminal.Options       `msgpack:"terminal,omitempty" json:"terminal,omitempty"`
	RunDirectory        string                  `msgpack:"run_directory,omitempty" json:"run_directory,omitempty"`
	RunMetadata         map[string]any          `msgpack:"run_metadata,omitempty" json:"run_metadata,omitempty"`
	Steps               []int                   `msgpack:"steps,omitempty" json:"steps,omitempty"`
	Groups              *groups.Options         `msgpack:"groups,omitempty" json:"groups,omitempty"`
	Bridges             *bridges.Options        `msgpack:"bridges,omitempty" json:"bridges,omitempty"`
	BridgeCores         [][]int                 `msgpack:"bridge_cores,omitempty" json:"bridge_cores,omitempty"`
	Scan                *ScanOptions            `msgpack:"scan,omitempty" json:"scan,omitempty"`
	SourceCompatibility string                  `msgpack:"source_compatibility,omitempty" json:"source_compatibility,omitempty"`
	sourceMaxStep       *int
	analysis            *AnalysisResult
}

// Summary reports the distribution across anchor-agent/replicate samples.
// Variance is the population variance and Active is the number of samples with
// at least one concordant post for this drift component.
type Summary struct {
	Mean     float64 `msgpack:"mean" json:"mean"`
	Variance float64 `msgpack:"variance" json:"variance"`
	Samples  int     `msgpack:"samples" json:"samples"`
	Active   int     `msgpack:"active" json:"active"`
}

// PointResult reports the dynamics' expected drift (FProbe) plus additive
// neighbor and recommendation components. Concordant counts expose the feed
// composition needed by node-only or node-edge analysis.
type PointResult struct {
	X                            float64 `msgpack:"x" json:"x"`
	FProbe                       Summary `msgpack:"f_probe" json:"f_probe"`
	FNeighbor                    Summary `msgpack:"f_neighbor" json:"f_neighbor"`
	FRecommendation              Summary `msgpack:"f_recommendation" json:"f_recommendation"`
	MeanConcordantNeighbor       float64 `msgpack:"mean_concordant_neighbor" json:"mean_concordant_neighbor"`
	MeanConcordantRecommendation float64 `msgpack:"mean_concordant_recommendation" json:"mean_concordant_recommendation"`
	UpdateSecondMoment           Summary `msgpack:"update_second_moment" json:"update_second_moment"`
	UpdateVariance               float64 `msgpack:"update_variance" json:"update_variance"`
	NoUpdateProbability          float64 `msgpack:"no_update_probability" json:"no_update_probability"`
}

type StateResult struct {
	Step                         int                       `msgpack:"step" json:"step"`
	Precision                    string                    `msgpack:"precision" json:"precision"`
	Points                       []PointResult             `msgpack:"points" json:"points"`
	Energy                       *observables.Energy       `msgpack:"energy,omitempty" json:"energy,omitempty"`
	Pathway                      *observables.PathwayState `msgpack:"pathway,omitempty" json:"pathway,omitempty"`
	Binned                       *observables.BinnedState  `msgpack:"binned,omitempty" json:"binned,omitempty"`
	Terminal                     *terminal.Result          `msgpack:"terminal,omitempty" json:"terminal,omitempty"`
	Landscape                    *observables.Landscape    `msgpack:"landscape,omitempty" json:"landscape,omitempty"`
	CounterfactualNeighborEnergy map[int64][]float64       `msgpack:"counterfactual_neighbor_energy,omitempty" json:"counterfactual_neighbor_energy,omitempty"`
	CounterfactualNeighborValid  map[int64]bool            `msgpack:"counterfactual_neighbor_valid,omitempty" json:"counterfactual_neighbor_valid,omitempty"`
	AnchorPoints                 map[int64][]PointResult   `msgpack:"anchor_points,omitempty" json:"anchor_points,omitempty"`
	Groups                       *groups.Frame             `msgpack:"groups,omitempty" json:"groups,omitempty"`
	Bridges                      *bridges.Frame            `msgpack:"bridges,omitempty" json:"bridges,omitempty"`
}

type Response struct {
	Version             int             `msgpack:"version" json:"version"`
	RNG                 smprng.Spec     `msgpack:"rng" json:"rng"`
	DynamicsType        string          `msgpack:"dynamics_type" json:"dynamics_type"`
	Results             []StateResult   `msgpack:"results" json:"results"`
	SourceMaxStep       *int            `msgpack:"source_max_step,omitempty" json:"source_max_step,omitempty"`
	Analysis            *AnalysisResult `msgpack:"analysis,omitempty" json:"analysis,omitempty"`
	SourceCompatibility string          `msgpack:"source_compatibility,omitempty" json:"source_compatibility,omitempty"`
}

// Evaluate measures opinion drift and selected observables on frozen states.
// For Deffuant, FProbe is the conditional expected drift of its uniformly
// selected concordant post; this is the exact expectation of the implemented
// stochastic update rule.
func Evaluate(request *Request) (*Response, error) {
	if request == nil {
		return nil, errNilRequest
	}
	if request.Version < 0 || request.Version > ProtocolVersion {
		return nil, fmt.Errorf("unsupported probe protocol version %d", request.Version)
	}
	prepared := *request
	selected, grid, err := prepareMeasurements(&prepared)
	if err != nil {
		return nil, err
	}
	// Static run measurements consume each reconstructed state before reading
	// the next one. Force jobs retain their shared RNG and state lifecycle.
	if prepared.RunDirectory != "" && !selected["force"] {
		return evaluateStaticRun(&prepared, selected, grid)
	}
	prepared.States = append([]FrozenState(nil), request.States...)
	params, err := prepareStates(&prepared, selected)
	if err != nil {
		return nil, err
	}
	results, resolved, err := measureDrift(&prepared, selected, grid, params)
	if err != nil {
		return nil, err
	}
	if err := measureStatic(&prepared, selected, grid, params, results); err != nil {
		return nil, err
	}
	for i := range results {
		results[i].Precision = prepared.States[i].Precision
	}
	results = thinResults(&prepared, results)
	return &Response{Version: ProtocolVersion, RNG: resolved, DynamicsType: prepared.DynamicsType, Results: results, SourceMaxStep: prepared.sourceMaxStep, Analysis: prepared.analysis, SourceCompatibility: prepared.SourceCompatibility}, nil
}
