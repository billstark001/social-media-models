package model

import "math/rand/v2"

// SMPModelRecommendationSystem defines the interface for recommendation systems.
type SMPModelRecommendationSystem[O any, P any] interface {
	PostInit(dumpData []byte)
	PreStep()
	PreCommit()
	PostStep(changed []*RewiringEventBody)
	Recommend(agent *SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*PostRecord[O]
	Dump() []byte
}

// PreparedRecommendationState is an immutable decoded recommender snapshot.
// Restore initializes a fresh recommender with independently mutable cache indexes.
type PreparedRecommendationState interface {
	Restore(recommender any) error
}

// RecommendationStatePreparer is optional; existing recommendation systems keep
// their PostInit/Dump contract and use serialized restoration when absent.
type RecommendationStatePreparer interface {
	PrepareState(data []byte) (PreparedRecommendationState, error)
}

// SMPModelRecommendationAtSystem is the optional counterfactual recommendation
// interface. It evaluates the same recommender for an existing anchor agent at
// a hypothetical opinion without mutating model state. The caller owns rng so
// probe sampling cannot perturb a continued simulation.
type SMPModelRecommendationAtSystem[O any, P any] interface {
	RecommendAt(
		agent *SMPAgent[O, P],
		opinion O,
		neighborIDs map[int64]bool,
		count int,
		rng *rand.Rand,
	) []*PostRecord[O]
}

// SMPModelProbePreparer prepares deterministic indexes needed by RecommendAt
// without constructing simulation-only random matrices or epsilon vectors.
type SMPModelProbePreparer interface {
	PrepareProbe()
}

// BaseRecommendationSystem provides default empty implementations.
type BaseRecommendationSystem[O any, P any] struct{}

func (rs *BaseRecommendationSystem[O, P]) PostInit(dumpData []byte) {}
func (rs *BaseRecommendationSystem[O, P]) PreStep()                 {}
func (rs *BaseRecommendationSystem[O, P]) PrepareProbe()            {}
func (rs *BaseRecommendationSystem[O, P]) PreCommit()               {}
func (rs *BaseRecommendationSystem[O, P]) PostStep(changed []*RewiringEventBody) {
}
func (rs *BaseRecommendationSystem[O, P]) Recommend(agent *SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*PostRecord[O] {
	return []*PostRecord[O]{}
}
func (rs *BaseRecommendationSystem[O, P]) RecommendAt(
	agent *SMPAgent[O, P],
	opinion O,
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*PostRecord[O] {
	return []*PostRecord[O]{}
}
func (rs *BaseRecommendationSystem[O, P]) Dump() []byte { return nil }
