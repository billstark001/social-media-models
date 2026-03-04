package recsys

import (
	"maps"
	"math/rand"
	"smp/model"
)

type Random struct {
	model.BaseRecommendationSystem
	Model                *model.SMPModel
	HistoricalTweetCount int
	AgentCount           int
}

func NewRandom(
	model *model.SMPModel,
	historicalTweetCount *int,
) *Random {
	h := model.ModelParams.TweetRetainCount
	if historicalTweetCount != nil {
		h = *historicalTweetCount
	}
	return &Random{
		Model:                model,
		AgentCount:           model.Graph.Nodes().Len(),
		HistoricalTweetCount: h,
	}
}

func (r *Random) Recommend(
	agent *model.SMPAgent,
	neighborIDs map[int64]bool,
	count int,
) []*model.TweetRecord {

	generated := make(map[int64]bool)
	maps.Copy(generated, neighborIDs)

	visibleTweets := r.Model.Grid.TweetMap

	// collect results that are not in neighbors
	result := make([]*model.TweetRecord, 0, count)
	i := 0
	for len(result) < count {
		// avoid dead loop
		if i > count*10 {
			break
		}
		agentPickedID := int64(rand.Intn(r.AgentCount))
		if !generated[agentPickedID] {
			// do not replace
			generated[agentPickedID] = true
			tweet := selectTweet(
				r.HistoricalTweetCount,
				neighborIDs,
				agentPickedID,
				r.Model.Grid.AgentMap,
				visibleTweets,
			)
			if tweet != nil {
				result = append(result, tweet)
			}
		}
		i++
	}

	return result
}
