package recsys

import (
	"maps"
	"math/rand"
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

	generated := make(map[int64]bool)
	maps.Copy(generated, neighborIDs)

	visiblePosts := r.Model.Grid.PostMap

	result := make([]*model.PostRecord[O], 0, count)
	i := 0
	for len(result) < count {
		if i > count*10 {
			break
		}
		agentPickedID := int64(rand.Intn(r.AgentCount))
		if !generated[agentPickedID] {
			generated[agentPickedID] = true
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
		i++
	}

	return result
}
