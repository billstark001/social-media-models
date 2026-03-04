package recsys

import (
	"math"
	"math/rand"
	"sort"

	"smp/model"
)

// Opinion implements a recommendation system based on opinion similarity.
// O is fixed to float64; P is the params type.
type Opinion[P any] struct {
	model.BaseRecommendationSystem[float64, P]
	Model               *model.SMPModel[float64, P]
	NoiseStd            float64
	HistoricalPostCount int

	NumNodes     int
	Epsilon      []float64
	PostIndices  []*PostIndex
	AgentMap     map[int64]*model.SMPAgent[float64, P]
	AgentIndices map[int64]int
}

// NewOpinion creates a new opinion-based recommendation system.
func NewOpinion[P any](m *model.SMPModel[float64, P], noiseStd float64, historicalPostCount *int) *Opinion[P] {
	h := m.ModelParams.PostRetainCount
	if historicalPostCount != nil {
		h = *historicalPostCount
	}
	return &Opinion[P]{
		Model:               m,
		NoiseStd:            noiseStd,
		HistoricalPostCount: h,
	}
}

// PostInit implements model.SMPModelRecommendationSystem
func (o *Opinion[P]) PostInit(dumpData []byte) {
	o.NumNodes = o.Model.Graph.Nodes().Len()
	normHistCount := max(o.HistoricalPostCount, 0)
	o.PostIndices = make([]*PostIndex, 0)
	o.AgentMap = make(map[int64]*model.SMPAgent[float64, P], o.NumNodes)
	o.AgentIndices = make(map[int64]int, o.NumNodes)
	o.Epsilon = make([]float64, o.NumNodes)

	for _, a := range o.Model.Schedule.Agents {
		o.AgentMap[a.ID] = a
		o.PostIndices = append(o.PostIndices, &PostIndex{
			AgentID:   a.ID,
			HistoryID: -1,
		})
		for i := range normHistCount {
			tIdx := &PostIndex{
				AgentID:   a.ID,
				HistoryID: i,
			}
			o.PostIndices = append(o.PostIndices, tIdx)
		}
	}
}

// PreStep implements model.SMPModelRecommendationSystem
func (o *Opinion[P]) PreStep() {
	visiblePosts := o.Model.Grid.PostMap
	fetchOpinion := func(pi *PostIndex) float64 {
		tsi := visiblePosts[pi.AgentID]
		if pi.HistoryID == -1 {
			return o.AgentMap[pi.AgentID].CurOpinion
		}
		if len(tsi) <= pi.HistoryID {
			return -2
		}
		post := tsi[pi.HistoryID]
		if post == nil || post.AgentID != pi.AgentID {
			return -2
		}
		return tsi[pi.HistoryID].Opinion
	}

	for _, pi := range o.PostIndices {
		pi.TempOpinion = fetchOpinion(pi)
	}
	sort.Slice(o.PostIndices, func(i, j int) bool {
		return o.PostIndices[i].TempOpinion < o.PostIndices[j].TempOpinion
	})
	for i, a := range o.PostIndices {
		if a.HistoryID == -1 {
			o.AgentIndices[a.AgentID] = i
		}
	}

	for i := range o.Epsilon {
		o.Epsilon[i] = rand.NormFloat64() * o.NoiseStd
	}
}

// Recommend implements model.SMPModelRecommendationSystem
func (o *Opinion[P]) Recommend(agent *model.SMPAgent[float64, P], neighborIDs map[int64]bool, count int) []*model.PostRecord[float64] {
	opinionWithNoise := agent.CurOpinion + o.Epsilon[agent.ID]

	iPre := o.AgentIndices[agent.ID] - 1
	iPost := o.AgentIndices[agent.ID] + 1

	ret := make([]*model.PostRecord[float64], 0, count)

	visiblePosts := o.Model.Grid.PostMap
	for len(ret) < count {
		noPre := iPre < 0 || o.PostIndices[iPre].TempOpinion == -2
		noPost := iPost >= len(o.PostIndices) || o.PostIndices[iPost].TempOpinion == -2
		if noPre && noPost {
			break
		}

		usePre := noPost || (!noPre &&
			math.Abs(opinionWithNoise-o.PostIndices[iPre].TempOpinion) <
				math.Abs(o.PostIndices[iPost].TempOpinion-opinionWithNoise))

		var a *PostIndex
		if usePre {
			a = o.PostIndices[iPre]
			iPre--
		} else {
			a = o.PostIndices[iPost]
			iPost++
		}

		if o.HistoricalPostCount < 1 {
			postToRecommend := o.AgentMap[a.AgentID].CurPost
			cond := postToRecommend != nil &&
				postToRecommend.AgentID != agent.ID &&
				!neighborIDs[postToRecommend.AgentID]
			if cond {
				ret = append(ret, postToRecommend)
				continue
			}
		}
		cond := a.AgentID != agent.ID &&
			a.HistoryID != -1 &&
			!neighborIDs[a.AgentID]
		if cond {
			postToRecommend := visiblePosts[a.AgentID][a.HistoryID]
			if postToRecommend != nil {
				ret = append(ret, postToRecommend)
			}
		}
	}

	return ret
}
