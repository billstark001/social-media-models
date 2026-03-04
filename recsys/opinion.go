package recsys

import (
	"math"
	"math/rand"
	"sort"

	"smp/model"
)

// Opinion implements a recommendation system based on opinion similarity
type Opinion struct {
	model.BaseRecommendationSystem
	Model                *model.SMPModel
	NoiseStd             float64
	HistoricalTweetCount int

	NumNodes     int
	Epsilon      []float64
	TweetIndices []*TweetIndex
	AgentMap     map[int64]*model.SMPAgent
	AgentIndices map[int64]int
}

type TweetIndex struct {
	AgentID     int64
	HistoryID   int // -1: current opinion
	TempOpinion float64
}

// NewOpinion creates a new opinion-based recommendation system
func NewOpinion(model *model.SMPModel, noiseStd float64, historicalTweetCount *int) *Opinion {
	h := model.ModelParams.TweetRetainCount
	if historicalTweetCount != nil {
		h = *historicalTweetCount
	}
	return &Opinion{
		Model:                model,
		NoiseStd:             noiseStd,
		HistoricalTweetCount: h,
	}
}

// PostInit implements model.SMPModelRecommendationSystem
func (o *Opinion) PostInit(dumpData []byte) {
	o.NumNodes = o.Model.Graph.Nodes().Len()
	normHistCount := max(o.HistoricalTweetCount, 0)
	o.TweetIndices = make([]*TweetIndex, 0)
	o.AgentMap = make(map[int64]*model.SMPAgent, o.NumNodes)
	o.AgentIndices = make(map[int64]int, o.NumNodes)
	o.Epsilon = make([]float64, o.NumNodes)

	// initiate tweet index sorter
	for _, a := range o.Model.Schedule.Agents {
		o.AgentMap[a.ID] = a
		o.TweetIndices = append(o.TweetIndices, &TweetIndex{
			AgentID:   a.ID,
			HistoryID: -1,
		})
		for i := range normHistCount {
			tIdx := &TweetIndex{
				AgentID:   a.ID,
				HistoryID: i,
			}
			o.TweetIndices = append(o.TweetIndices, tIdx)
		}
	}
}

// PreStep implements model.SMPModelRecommendationSystem
func (o *Opinion) PreStep() {
	// Sort agents by current opinion

	visibleTweets := o.Model.Grid.TweetMap
	fetchOpinion := func(ti *TweetIndex) float64 {
		tsi := visibleTweets[ti.AgentID]
		if ti.HistoryID == -1 {
			return o.AgentMap[ti.AgentID].CurOpinion // only used for marker
		}
		if len(tsi) <= ti.HistoryID {
			return -2 // not applicable
		}
		tweet := tsi[ti.HistoryID]
		if tweet == nil || tweet.AgentID != ti.AgentID {
			return -2 // retweeted tweet, discard
		}
		return tsi[ti.HistoryID].Opinion // sent tweet
	}

	// get opinion
	for _, ti := range o.TweetIndices {
		ti.TempOpinion = fetchOpinion(ti)
	}
	// sort
	sort.Slice(o.TweetIndices, func(i, j int) bool {
		return o.TweetIndices[i].TempOpinion < o.TweetIndices[j].TempOpinion
	})
	// Update agent indices map (for initial search pos)
	for i, a := range o.TweetIndices {
		if a.HistoryID == -1 {
			o.AgentIndices[a.AgentID] = i
		}
	}

	// Generate random noise
	for i := range o.Epsilon {
		o.Epsilon[i] = rand.NormFloat64() * o.NoiseStd
	}
}

// Recommend implements model.SMPModelRecommendationSystem
func (o *Opinion) Recommend(agent *model.SMPAgent, neighborIDs map[int64]bool, count int) []*model.TweetRecord {
	// Get adjusted opinion with noise
	opinionWithNoise := agent.CurOpinion + o.Epsilon[agent.ID]

	// Start indices for searching closest agents by opinion
	iPre := o.AgentIndices[agent.ID] - 1
	iPost := o.AgentIndices[agent.ID] + 1

	ret := make([]*model.TweetRecord, 0, count)

	// Find closest agents by opinion difference
	visibleTweets := o.Model.Grid.TweetMap
	for len(ret) < count {
		noPre := iPre < 0 || o.TweetIndices[iPre].TempOpinion == -2
		noPost := iPost >= len(o.TweetIndices) || o.TweetIndices[iPost].TempOpinion == -2
		if noPre && noPost {
			break
		}

		// Determine whether to use predecessor or successor
		usePre := noPost || (!noPre &&
			math.Abs(opinionWithNoise-o.TweetIndices[iPre].TempOpinion) <
				math.Abs(o.TweetIndices[iPost].TempOpinion-opinionWithNoise))

		var a *TweetIndex
		if usePre {
			a = o.TweetIndices[iPre]
			iPre--
		} else {
			a = o.TweetIndices[iPost]
			iPost++
		}

		// use the current agent's tweet at HistoricalTweetCount == 0
		if o.HistoricalTweetCount < 1 {
			tweetToRecommend := o.AgentMap[a.AgentID].CurTweet
			// skip:
			cond := tweetToRecommend != nil && // nil
				tweetToRecommend.AgentID != agent.ID && // itself's tweet
				!neighborIDs[tweetToRecommend.AgentID] // its neighbor's tweet
			if cond {
				ret = append(ret, tweetToRecommend)
				continue
			}
		}
		// else, use tweets inside
		// skip:
		cond := a.AgentID != agent.ID && // itself
			a.HistoryID != -1 && // marker indices
			!neighborIDs[a.AgentID] // its neighbor
		if cond {
			// Get the tweet from the historical record
			tweetToRecommend := visibleTweets[a.AgentID][a.HistoryID]
			if tweetToRecommend != nil { // nil check
				ret = append(ret, tweetToRecommend)
			}
		}
	}

	return ret
}
