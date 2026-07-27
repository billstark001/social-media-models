package recsys

import (
	"math"
	"math/rand/v2"
	"sort"

	"smp/model"
	smprng "smp/rng"
)

// Opinion implements a recommendation system based on opinion similarity.
type Opinion[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	Model               *model.SMPModel[O, P]
	NoiseStd            float64
	HistoricalPostCount int

	NumNodes     int
	Epsilon      []float64
	PostIndices  []*PostIndex
	AgentMap     map[int64]*model.SMPAgent[O, P]
	AgentIndices map[int64]int
}

// NewOpinion creates a new opinion-based recommendation system.
func NewOpinion[O any, P any](m *model.SMPModel[O, P], noiseStd float64, historicalPostCount *int) *Opinion[O, P] {
	h := m.ModelParams.PostRetainCount
	if historicalPostCount != nil {
		h = *historicalPostCount
	}
	return &Opinion[O, P]{
		Model:               m,
		NoiseStd:            noiseStd,
		HistoricalPostCount: h,
	}
}

// PostInit implements model.SMPModelRecommendationSystem
func (o *Opinion[O, P]) PostInit(dumpData []byte) {
	o.NumNodes = o.Model.Graph.Nodes().Len()
	normHistCount := max(o.HistoricalPostCount, 0)
	o.PostIndices = make([]*PostIndex, 0)
	o.AgentMap = make(map[int64]*model.SMPAgent[O, P], o.NumNodes)
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
func (o *Opinion[O, P]) PreStep() {
	o.prepareIndex()

	rng := o.Model.RNG.Stream(smprng.StreamRecommendation)
	for i := range o.Epsilon {
		o.Epsilon[i] = rng.NormFloat64() * o.NoiseStd
	}
}

// PrepareProbe builds the searchable post index without consuming simulation
// recommendation noise.
func (o *Opinion[O, P]) PrepareProbe() {
	o.prepareIndex()
}

func (o *Opinion[O, P]) prepareIndex() {
	visiblePosts := o.Model.Grid.PostMap
	fetchOpinion := func(pi *PostIndex) float64 {
		tsi := visiblePosts[pi.AgentID]
		if pi.HistoryID == -1 {
			return toFloat64(o.AgentMap[pi.AgentID].CurOpinion)
		}
		if len(tsi) <= pi.HistoryID {
			return -2
		}
		post := tsi[pi.HistoryID]
		if post == nil || post.AgentID != pi.AgentID {
			return -2
		}
		return toFloat64(tsi[pi.HistoryID].Opinion)
	}

	for _, pi := range o.PostIndices {
		pi.TempOpinion = fetchOpinion(pi)
	}
	sort.Slice(o.PostIndices, func(i, j int) bool {
		left, right := o.PostIndices[i], o.PostIndices[j]
		if left.TempOpinion != right.TempOpinion {
			return left.TempOpinion < right.TempOpinion
		}
		if left.AgentID != right.AgentID {
			return left.AgentID < right.AgentID
		}
		return left.HistoryID < right.HistoryID
	})
	for i, a := range o.PostIndices {
		if a.HistoryID == -1 {
			o.AgentIndices[a.AgentID] = i
		}
	}
}

// Recommend implements model.SMPModelRecommendationSystem
func (o *Opinion[O, P]) Recommend(agent *model.SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*model.PostRecord[O] {
	opinionWithNoise := toFloat64(agent.CurOpinion) + o.Epsilon[agent.ID]

	iPre := o.AgentIndices[agent.ID] - 1
	iPost := o.AgentIndices[agent.ID] + 1
	return o.recommendAround(agent.ID, opinionWithNoise, iPre, iPost, neighborIDs, count)
}

// RecommendAt evaluates opinion-nearest recommendation around a hypothetical
// opinion. Binary search replaces the real agent marker used by Recommend.
func (o *Opinion[O, P]) RecommendAt(
	agent *model.SMPAgent[O, P],
	opinion O,
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	opinionWithNoise := toFloat64(opinion)
	if o.NoiseStd > 0 {
		opinionWithNoise += rng.NormFloat64() * o.NoiseStd
	}
	cursor := sort.Search(len(o.PostIndices), func(i int) bool {
		return o.PostIndices[i].TempOpinion >= opinionWithNoise
	})
	return o.recommendAround(agent.ID, opinionWithNoise, cursor-1, cursor, neighborIDs, count)
}

func (o *Opinion[O, P]) recommendAround(
	agentID int64,
	opinionWithNoise float64,
	iPre int,
	iPost int,
	neighborIDs map[int64]bool,
	count int,
) []*model.PostRecord[O] {
	ret := make([]*model.PostRecord[O], 0, count)

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
				postToRecommend.AgentID != agentID &&
				!neighborIDs[postToRecommend.AgentID]
			if cond {
				ret = append(ret, postToRecommend)
				continue
			}
		}
		cond := a.AgentID != agentID &&
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
