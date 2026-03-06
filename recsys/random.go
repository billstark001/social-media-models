package recsys

import (
	"math/rand/v2"
	"smp/model"
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
	rand.Shuffle(len(candidates), func(i, j int) {
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
		)
		if post != nil {
			result = append(result, post)
		}
	}
	return result
}
