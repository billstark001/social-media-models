package recsys

import (
	"math/rand/v2"
	"smp/model"
	smprng "smp/rng"
)

type Random[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	postVisibility
	Model               *model.SMPModel[O, P]
	HistoricalPostCount int
	AgentCount          int
}

func NewRandom[O any, P any](
	m *model.SMPModel[O, P],
	historicalPostCount *int,
) *Random[O, P] {
	h := m.ModelParams.PostRetainCount
	if historicalPostCount != nil {
		h = *historicalPostCount
	}
	return &Random[O, P]{
		Model:               m,
		AgentCount:          m.Graph.Nodes().Len(),
		HistoricalPostCount: h,
	}
}

func (r *Random[O, P]) Recommend(
	agent *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
) []*model.PostRecord[O] {
	rng := r.Model.RNG.Stream(smprng.StreamRecommendation)
	return r.recommend(agent, neighborIDs, count, rng)
}

// RecommendWithLayout marks all uniform draws as exploration.
func (r *Random[O, P]) RecommendWithLayout(agent *model.SMPAgent[O, P], neighbors map[int64]bool, count int) ([]*model.PostRecord[O], model.RecommendationLayout) {
	posts := r.Recommend(agent, neighbors, count)
	return posts, model.RecommendationLayout{Exploration: len(posts)}
}

// RecommendAt implements counterfactual recommendation. Random recommendations
// do not depend on opinion, but use the probe-owned RNG.
func (r *Random[O, P]) RecommendAt(
	agent *model.SMPAgent[O, P],
	_ O,
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	return r.recommend(agent, neighborIDs, count, rng)
}

func (r *Random[O, P]) recommend(
	_ *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	result := make([]*model.PostRecord[O], 0, count)
	forEachRandomIndex(r.AgentCount, rng, func(idx int) bool {
		agentPickedID := int64(idx)
		if neighborIDs[agentPickedID] {
			return true
		}
		post := selectPost(
			r.HistoricalPostCount,
			neighborIDs,
			agentPickedID,
			r.Model,
			r.hideReposts,
			rng,
		)
		if post != nil {
			result = append(result, post)
		}
		return len(result) < count
	})
	return result
}
