package simulation

import (
	"fmt"
	"maps"
	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"sync"

	"github.com/vmihailenco/msgpack/v5"
)

// PreparedCheckpoint owns one immutable decoded microscopic checkpoint.
// It avoids decoding graph/post arrays for every branch. Restore still creates
// independent agents, graphs, posts, recommenders and RNG pools via Dump.Load.
// It is safe for concurrent restores with distinct pools and read-only metadata.
type PreparedCheckpoint struct {
	dynamicsType               string
	metadata                   *ScenarioMetadata
	completedStep, stableSteps int
	load                       func(*ScenarioMetadata, *smprng.Pool, func(*model.EventRecord), bool) IModel
}

func prepareTyped[O any, P any](snapshot *RawSnapshotData,
	loader func(*model.SMPModelDumpData[O, P], *ScenarioMetadata, *smprng.Pool, func(*model.EventRecord), func(model.SMPModelRecommendationSystem[O, P]) model.PreparedRecommendationState) IModel,
) (*PreparedCheckpoint, error) {
	var dump model.SMPModelDumpData[O, P]
	if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
		return nil, err
	}
	var metadata *ScenarioMetadata
	if snapshot.Metadata != nil {
		copy := *snapshot.Metadata
		copy.RecSysParams = maps.Clone(snapshot.Metadata.RecSysParams)
		copy.CheckpointSteps = append([]int(nil), snapshot.Metadata.CheckpointSteps...)
		metadata = &copy
	}
	var prepareOnce sync.Once
	var recommendationState model.PreparedRecommendationState
	prepareRecommendation := func(recommender model.SMPModelRecommendationSystem[O, P]) model.PreparedRecommendationState {
		prepareOnce.Do(func() {
			if len(dump.RecsysDumpData) > 0 {
				if preparer, ok := recommender.(model.RecommendationStatePreparer); ok {
					// Preserve PostInit's fallback behavior for legacy/corrupt cache bytes.
					recommendationState, _ = preparer.PrepareState(dump.RecsysDumpData)
				}
			}
		})
		return recommendationState
	}
	return &PreparedCheckpoint{
		dynamicsType: snapshot.DynamicsType, metadata: metadata,
		completedStep: snapshot.CompletedStep, stableSteps: snapshot.StableSteps,
		load: func(metadata *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), branch bool) IModel {
			copy := dump
			if branch {
				copy.RNGStates = nil
			}
			return loader(&copy, metadata, pool, logger, prepareRecommendation)
		},
	}, nil
}

// PrepareCheckpoint performs no simulation, filesystem I/O or RNG draws.
func PrepareCheckpoint(snapshot *RawSnapshotData) (*PreparedCheckpoint, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("nil checkpoint")
	}
	switch snapshot.DynamicsType {
	case DynamicsTypeHK:
		return prepareTyped(snapshot, func(d *model.SMPModelDumpData[float64, dynamics.HKParams], m *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), prepare func(model.SMPModelRecommendationSystem[float64, dynamics.HKParams]) model.PreparedRecommendationState) IModel {
			factory := GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](m.RecSysParams)[m.RecsysFactoryType]
			params := model.SMPModelParams[float64, dynamics.HKParams]{SMPModelPureParams: m.SMPModelPureParams, RecsysFactory: factory}
			return &Float64ModelWrapper[dynamics.HKParams]{M: d.LoadWithRecommenderState(&params, &m.HKParams, &dynamics.HK{}, &m.CollectItemOptions, logger, pool, prepare)}
		})
	case DynamicsTypeDeffuant:
		return prepareTyped(snapshot, func(d *model.SMPModelDumpData[float64, dynamics.DeffuantParams], m *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), prepare func(model.SMPModelRecommendationSystem[float64, dynamics.DeffuantParams]) model.PreparedRecommendationState) IModel {
			factory := GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](m.RecSysParams)[m.RecsysFactoryType]
			params := model.SMPModelParams[float64, dynamics.DeffuantParams]{SMPModelPureParams: m.SMPModelPureParams, RecsysFactory: factory}
			return &Float64ModelWrapper[dynamics.DeffuantParams]{M: d.LoadWithRecommenderState(&params, &m.DeffuantParams, &dynamics.Deffuant{}, &m.CollectItemOptions, logger, pool, prepare)}
		})
	case DynamicsTypeGalam:
		return prepareTyped(snapshot, func(d *model.SMPModelDumpData[bool, dynamics.GalamParams], m *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), prepare func(model.SMPModelRecommendationSystem[bool, dynamics.GalamParams]) model.PreparedRecommendationState) IModel {
			factory := GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](m.RecSysParams)[m.RecsysFactoryType]
			params := model.SMPModelParams[bool, dynamics.GalamParams]{SMPModelPureParams: m.SMPModelPureParams, RecsysFactory: factory}
			return &BoolModelWrapper[dynamics.GalamParams]{M: d.LoadWithRecommenderState(&params, &m.GalamParams, &dynamics.Galam{}, &m.CollectItemOptions, logger, pool, prepare)}
		})
	case DynamicsTypeVoter:
		return prepareTyped(snapshot, func(d *model.SMPModelDumpData[bool, dynamics.VoterParams], m *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), prepare func(model.SMPModelRecommendationSystem[bool, dynamics.VoterParams]) model.PreparedRecommendationState) IModel {
			factory := GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](m.RecSysParams)[m.RecsysFactoryType]
			params := model.SMPModelParams[bool, dynamics.VoterParams]{SMPModelPureParams: m.SMPModelPureParams, RecsysFactory: factory}
			return &BoolModelWrapper[dynamics.VoterParams]{M: d.LoadWithRecommenderState(&params, &m.VoterParams, &dynamics.Voter{}, &m.CollectItemOptions, logger, pool, prepare)}
		})
	default:
		return nil, fmt.Errorf("unknown checkpoint dynamics %q", snapshot.DynamicsType)
	}
}

func (p *PreparedCheckpoint) restore(metadata *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), branch bool) (IModel, error) {
	if metadata == nil || pool == nil {
		return nil, fmt.Errorf("restore requires metadata and RNG pool")
	}
	if p.dynamicsType != metadata.DynamicsType {
		return nil, fmt.Errorf("checkpoint dynamics differs from request")
	}
	var initialStates map[string][]byte
	if branch {
		var err error
		initialStates, err = pool.Snapshot()
		if err != nil {
			return nil, err
		}
	}
	m := p.load(metadata, pool, logger, branch)
	if branch {
		if err := pool.Restore(initialStates); err != nil {
			return nil, err
		}
	}
	return m, nil
}
