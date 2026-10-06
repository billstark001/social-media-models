package simulation

import (
	"fmt"
	"math"
	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/utils"

	"gonum.org/v1/gonum/graph/simple"
)

// InitialState supplies an optional explicit microscopic starting state.
// The model takes ownership of Graph; callers must not mutate it afterwards.
// Opinions, when non-nil, are copied into HK/Deffuant agents without sampling.
// Nil fields retain the standard ER graph and uniform-opinion initialization.
type InitialState struct {
	Graph    *simple.DirectedGraph
	Opinions []float64
}

// NewModel is the common microscopic model constructor used by Scenario and
// in-memory callers. It initializes posts/recommenders but leaves CurStep at 0.
// Metadata and the supplied RNG pool must already describe the desired run.
func NewModel(metadata *ScenarioMetadata, initial *InitialState, pool *smprng.Pool, eventLogger func(*model.EventRecord)) (IModel, error) {
	if err := metadata.Validate(); err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("model requires an RNG pool")
	}
	var graph *simple.DirectedGraph
	var opinions *[]float64
	if initial != nil {
		graph = initial.Graph
		if initial.Opinions != nil {
			if metadata.DynamicsType != DynamicsTypeHK && metadata.DynamicsType != DynamicsTypeDeffuant {
				return nil, fmt.Errorf("explicit float64 opinions require HK or Deffuant")
			}
			if len(initial.Opinions) != metadata.NodeCount {
				return nil, fmt.Errorf("opinion count differs from NodeCount")
			}
			for _, x := range initial.Opinions {
				if math.IsNaN(x) || math.IsInf(x, 0) {
					return nil, fmt.Errorf("opinions must be finite")
				}
			}
			opinions = &initial.Opinions
		}
	}
	if graph == nil {
		if metadata.NetworkType == "Explicit" {
			return nil, fmt.Errorf("Explicit network requires an initial graph")
		}
		graph = utils.CreateRandomNetwork(metadata.NodeCount,
			float64(metadata.NodeFollowCount)/float64(metadata.NodeCount-1),
			pool.Stream(smprng.StreamNetwork))
	} else {
		if graph.Nodes().Len() != metadata.NodeCount {
			return nil, fmt.Errorf("graph node count differs from NodeCount")
		}
		for i := 0; i < metadata.NodeCount; i++ {
			if graph.Node(int64(i)) == nil {
				return nil, fmt.Errorf("graph requires dense node IDs 0..NodeCount-1")
			}
		}
	}
	var result IModel
	switch metadata.DynamicsType {
	case "", DynamicsTypeHK:
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.HKParams]{
			SMPModelPureParams: metadata.SMPModelPureParams,
			RecsysFactory:      factories[metadata.RecsysFactoryType],
		}
		m := model.NewSMPModelFloat64(graph, opinions, &params, &metadata.HKParams, &dynamics.HK{}, &metadata.CollectItemOptions, eventLogger, pool)
		result = &Float64ModelWrapper[dynamics.HKParams]{M: m}
	case DynamicsTypeDeffuant:
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.DeffuantParams]{
			SMPModelPureParams: metadata.SMPModelPureParams,
			RecsysFactory:      factories[metadata.RecsysFactoryType],
		}
		m := model.NewSMPModelFloat64(graph, opinions, &params, &metadata.DeffuantParams, &dynamics.Deffuant{}, &metadata.CollectItemOptions, eventLogger, pool)
		result = &Float64ModelWrapper[dynamics.DeffuantParams]{M: m}
	case DynamicsTypeGalam:
		factories := GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.GalamParams]{
			SMPModelPureParams: metadata.SMPModelPureParams,
			RecsysFactory:      factories[metadata.RecsysFactoryType],
		}
		n := graph.Nodes().Len()
		ops := make([]bool, n)
		opinionRNG := pool.Stream(smprng.StreamOpinion)
		for i := range ops {
			ops[i] = opinionRNG.IntN(2) == 1
		}
		m := model.NewSMPModel(graph, &ops, &params, &metadata.GalamParams, &dynamics.Galam{}, &metadata.CollectItemOptions, eventLogger, pool)
		result = &BoolModelWrapper[dynamics.GalamParams]{M: m}
	case DynamicsTypeVoter:
		factories := GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.VoterParams]{
			SMPModelPureParams: metadata.SMPModelPureParams,
			RecsysFactory:      factories[metadata.RecsysFactoryType],
		}
		n := graph.Nodes().Len()
		ops := make([]bool, n)
		opinionRNG := pool.Stream(smprng.StreamOpinion)
		for i := range ops {
			ops[i] = opinionRNG.IntN(2) == 1
		}
		m := model.NewSMPModel(graph, &ops, &params, &metadata.VoterParams, &dynamics.Voter{}, &metadata.CollectItemOptions, eventLogger, pool)
		result = &BoolModelWrapper[dynamics.VoterParams]{M: m}
	default:
		return nil, fmt.Errorf("unknown DynamicsType: %q", metadata.DynamicsType)
	}

	result.InitPosts()
	return result, nil
}
