// Package probe evaluates counterfactual opinion drift on frozen simulation
// states without advancing or mutating the original simulation.
package probe

import (
	"fmt"
	"smp/dynamics"
	"smp/model"
	"smp/observables"
	smprng "smp/rng"
	"smp/utils"
)

// ProtocolVersion is the current msgpack request and response version.
// Requests that omit the version are legacy version 0.
const ProtocolVersion = 1

// Grid specifies an inclusive, evenly-spaced opinion grid.
type Grid struct {
	Min  float64 `msgpack:"min"`
	Max  float64 `msgpack:"max"`
	Step float64 `msgpack:"step"`
}

// FrozenState contains only the state required to evaluate recommendations
// and continuous-opinion drift at one completed simulation step.
type FrozenState struct {
	Step      int                                   `msgpack:"step"`
	Graph     utils.NetworkXGraph                   `msgpack:"graph"`
	Opinions  []float64                             `msgpack:"opinions"`
	Posts     map[int64][]model.PostRecord[float64] `msgpack:"posts"`
	Precision string                                `msgpack:"precision"`
}

// Request is the stdin protocol consumed by cmd/smp-probe.
type Request struct {
	Version           int                     `msgpack:"version"`
	RNG               smprng.Spec             `msgpack:"rng"`
	DynamicsType      string                  `msgpack:"dynamics_type"`
	HKParams          dynamics.HKParams       `msgpack:"hk_params"`
	DeffuantParams    dynamics.DeffuantParams `msgpack:"deffuant_params"`
	RecsysFactoryType string                  `msgpack:"recsys_factory_type"`
	RecSysParams      map[string]any          `msgpack:"recsys_params"`
	RecsysCount       int                     `msgpack:"recsys_count"`
	PostRetainCount   int                     `msgpack:"post_retain_count"`
	Grid              Grid                    `msgpack:"grid"`
	Replicates        int                     `msgpack:"replicates"`
	AnchorIDs         []int64                 `msgpack:"anchor_ids"`
	PerAnchor         bool                    `msgpack:"per_anchor"`
	States            []FrozenState           `msgpack:"states"`
	CheckpointPaths   []string                `msgpack:"checkpoint_paths"`
	Measurements      []string                `msgpack:"measurements"`
	EnergyScale       float64                 `msgpack:"energy_scale"`
	EnergyEpsilon     *float64                `msgpack:"energy_epsilon"`
}

// Summary reports the distribution across anchor-agent/replicate samples.
// Variance is the population variance and Active is the number of samples with
// at least one concordant post for this drift component.
type Summary struct {
	Mean     float64 `msgpack:"mean"`
	Variance float64 `msgpack:"variance"`
	Samples  int     `msgpack:"samples"`
	Active   int     `msgpack:"active"`
}

// PointResult reports the dynamics' expected drift (FProbe) plus additive
// neighbor and recommendation components. Concordant counts expose the feed
// composition needed by node-only or node-edge analysis.
type PointResult struct {
	X                            float64 `msgpack:"x"`
	FProbe                       Summary `msgpack:"f_probe"`
	FNeighbor                    Summary `msgpack:"f_neighbor"`
	FRecommendation              Summary `msgpack:"f_recommendation"`
	MeanConcordantNeighbor       float64 `msgpack:"mean_concordant_neighbor"`
	MeanConcordantRecommendation float64 `msgpack:"mean_concordant_recommendation"`
	UpdateSecondMoment           Summary `msgpack:"update_second_moment"`
	UpdateVariance               float64 `msgpack:"update_variance"`
	NoUpdateProbability          float64 `msgpack:"no_update_probability"`
}

type StateResult struct {
	Step                         int                     `msgpack:"step"`
	Precision                    string                  `msgpack:"precision"`
	Points                       []PointResult           `msgpack:"points"`
	Energy                       *observables.Energy     `msgpack:"energy,omitempty"`
	Landscape                    *observables.Landscape  `msgpack:"landscape,omitempty"`
	CounterfactualNeighborEnergy map[int64][]float64     `msgpack:"counterfactual_neighbor_energy,omitempty"`
	CounterfactualNeighborValid  map[int64]bool          `msgpack:"counterfactual_neighbor_valid,omitempty"`
	AnchorPoints                 map[int64][]PointResult `msgpack:"anchor_points,omitempty"`
}

type Response struct {
	Version      int           `msgpack:"version"`
	RNG          smprng.Spec   `msgpack:"rng"`
	DynamicsType string        `msgpack:"dynamics_type"`
	Results      []StateResult `msgpack:"results"`
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
	prepared.States = append([]FrozenState(nil), request.States...)
	selected, grid, params, err := prepareEvaluation(&prepared)
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
	return &Response{Version: ProtocolVersion, RNG: resolved, DynamicsType: prepared.DynamicsType, Results: results}, nil
}
