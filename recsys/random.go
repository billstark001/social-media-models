package recsys

import (
	"smp/model"
	smprng "smp/rng"
)

type Random[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
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
	candidates := make([]int, r.AgentCount)
	for i := range candidates {
		candidates[i] = i
	}
	rng := r.Model.RNG.Stream(smprng.StreamRecommendation)
	rng.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	visiblePosts := r.Model.Grid.PostMap
	result := make([]*model.PostRecord[O], 0, count)
	for _, idx := range candidates {
		if len(result) >= count {
			break
		}
		agentPickedID := int64(idx)
		if neighborIDs[agentPickedID] {
			continue
		}
		post := selectPost(
			r.HistoricalPostCount,
			neighborIDs,
			agentPickedID,
			r.Model.Grid.AgentMap,
			visiblePosts,
			rng,
		)
		if post != nil {
			result = append(result, post)
		}
	}
	return result
}
