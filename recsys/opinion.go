package recsys

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"sort"

	"smp/model"
	smprng "smp/rng"
)

// Opinion implements a recommendation system based on opinion similarity.
type Opinion[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	postVisibility
	Model               *model.SMPModel[O, P]
	NoiseStd            float64
	HistoricalPostCount int

	NumNodes      int
	Epsilon       []float64
	PostIndices   []*PostIndex
	AgentMap      map[int64]*model.SMPAgent[O, P]
	AgentIndices  map[int64]int
	authorIndices []opinionAuthorIndices
	groupStarts   []int
	groupEnds     []int
	ties          tieSampler
}

type opinionPlacement[O any] struct {
	post   *model.PostRecord[O]
	holder int64
	round  int
}

// The sorted search index and the per-author preparation index share these
// slots. Preparing a round reads each author's history once, while sorting only
// rearranges pointers in PostIndices.
type opinionAuthorIndices struct {
	marker  *PostIndex
	history []PostIndex
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
	o.PostIndices = make([]*PostIndex, 0, o.NumNodes*(normHistCount+1))
	o.AgentMap = make(map[int64]*model.SMPAgent[O, P], o.NumNodes)
	o.AgentIndices = make(map[int64]int, o.NumNodes)
	o.authorIndices = make([]opinionAuthorIndices, 0, o.NumNodes)
	o.Epsilon = make([]float64, o.NumNodes)

	for _, a := range o.Model.Schedule.Agents {
		o.AgentMap[a.ID] = a
		author := opinionAuthorIndices{
			marker:  &PostIndex{AgentID: a.ID, HistoryID: -1},
			history: make([]PostIndex, normHistCount),
		}
		o.PostIndices = append(o.PostIndices, author.marker)
		for i := range normHistCount {
			tIdx := &author.history[i]
			tIdx.AgentID, tIdx.HistoryID = a.ID, i
			o.PostIndices = append(o.PostIndices, tIdx)
		}
		o.authorIndices = append(o.authorIndices, author)
	}
	o.groupStarts = make([]int, len(o.PostIndices))
	o.groupEnds = make([]int, len(o.PostIndices))
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
	for _, author := range o.authorIndices {
		id := author.marker.AgentID
		author.marker.TempOpinion = toFloat64(o.AgentMap[id].CurOpinion)
		if len(author.history) == 0 {
			continue
		}
		history := visiblePosts[id]
		for index := range author.history {
			entry := &author.history[index]
			entry.TempOpinion = -2
			if index < len(history) {
				if post := history[index]; post != nil && post.AgentID == id {
					entry.TempOpinion = toFloat64(post.Opinion)
				}
			}
		}
	}
	slices.SortFunc(o.PostIndices, func(left, right *PostIndex) int {
		if left.TempOpinion != right.TempOpinion {
			return cmp.Compare(left.TempOpinion, right.TempOpinion)
		}
		// Stable enumeration for RNG replay only. recommendAround samples the
		// complete equal-distance frontier, independently of this ID order.
		if left.AgentID != right.AgentID {
			return cmp.Compare(left.AgentID, right.AgentID)
		}
		return cmp.Compare(left.HistoryID, right.HistoryID)
	})
	for start := 0; start < len(o.PostIndices); {
		end := start + 1
		for end < len(o.PostIndices) && o.PostIndices[end].TempOpinion == o.PostIndices[start].TempOpinion {
			end++
		}
		for index := start; index < end; index++ {
			if entry := o.PostIndices[index]; entry.HistoryID == -1 {
				o.AgentIndices[entry.AgentID] = index
			}
			o.groupStarts[index], o.groupEnds[index] = start, end
		}
		start = end
	}
}

// Recommend implements model.SMPModelRecommendationSystem
func (o *Opinion[O, P]) Recommend(agent *model.SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*model.PostRecord[O] {
	opinionWithNoise := toFloat64(agent.CurOpinion) + o.Epsilon[agent.ID]

	marker := o.AgentIndices[agent.ID]
	iPre := marker - 1
	iPost := marker + 1
	return o.recommendAround(agent.ID, opinionWithNoise, iPre, iPost, neighborIDs, count,
		o.Model.RNG.Stream(smprng.StreamRecommendation))
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
	return o.recommendAround(agent.ID, opinionWithNoise, cursor-1, cursor, neighborIDs, count, rng)
}

func (o *Opinion[O, P]) recommendAround(
	agentID int64,
	opinionWithNoise float64,
	iPre int,
	iPost int,
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	ret := make([]*model.PostRecord[O], 0, count)

	for len(ret) < count {
		noPre := iPre < 0 || o.PostIndices[iPre].TempOpinion == -2
		noPost := iPost >= len(o.PostIndices) || o.PostIndices[iPost].TempOpinion == -2
		if noPre && noPost {
			break
		}

		// Keep the original marker-based traversal. Only the complete equal-
		// distance frontier is unordered, including ties across both sides.
		distance := math.Inf(1)
		if !noPre {
			distance = math.Abs(opinionWithNoise - o.PostIndices[iPre].TempOpinion)
		}
		if !noPost {
			distance = min(distance, math.Abs(o.PostIndices[iPost].TempOpinion-opinionWithNoise))
		}
		preEnd, postStart := iPre+1, iPost
		for iPre >= 0 && o.PostIndices[iPre].TempOpinion != -2 && math.Abs(opinionWithNoise-o.PostIndices[iPre].TempOpinion) == distance {
			iPre = o.groupStarts[iPre] - 1
		}
		for iPost < len(o.PostIndices) && o.PostIndices[iPost].TempOpinion != -2 && math.Abs(o.PostIndices[iPost].TempOpinion-opinionWithNoise) == distance {
			iPost = o.groupEnds[iPost]
		}
		preStart, preCount := iPre+1, preEnd-(iPre+1)
		size := preCount + iPost - postStart
		if size == 1 {
			index := postStart
			if preCount == 1 {
				index = preStart
			}
			if placement, ok := o.placement(o.PostIndices[index], agentID, neighborIDs); ok {
				o.Model.RecordRecommendationPlacement(placement.holder, placement.round)
				ret = append(ret, placement.post)
			}
			continue
		}
		indexAt := func(offset int) *PostIndex {
			if offset < preCount {
				return o.PostIndices[preStart+offset]
			}
			return o.PostIndices[postStart+offset-preCount]
		}
		// Detect a unique valid slot before using RNG. Usually only two slots
		// need checking; large equal-opinion groups are never copied or shuffled.
		valid := 0
		var only opinionPlacement[O]
		for offset := 0; offset < size && valid < 2; offset++ {
			if placement, ok := o.placement(indexAt(offset), agentID, neighborIDs); ok {
				only = placement
				valid++
			}
		}
		if valid == 1 {
			o.Model.RecordRecommendationPlacement(only.holder, only.round)
			ret = append(ret, only.post)
			continue
		}
		if valid == 0 {
			continue
		}
		o.ties.reset(size)
		for o.ties.remaining > 0 && len(ret) < count {
			placement, ok := o.placement(indexAt(o.ties.draw(rng)), agentID, neighborIDs)
			if !ok {
				continue
			}
			o.Model.RecordRecommendationPlacement(placement.holder, placement.round)
			ret = append(ret, placement.post)
		}
	}

	return ret
}

// placement checks the existing visibility/history rules without logging an
// exposure. Only selected placements are recorded by recommendAround.
func (o *Opinion[O, P]) placement(a *PostIndex, agentID int64, neighborIDs map[int64]bool) (opinionPlacement[O], bool) {
	if o.HistoricalPostCount < 1 {
		post := o.AgentMap[a.AgentID].CurPost
		if post != nil && post.AgentID != agentID && !neighborIDs[post.AgentID] &&
			(!o.hideReposts || post.IsOriginalPlacement(a.AgentID, o.Model.CurStep-1)) {
			return opinionPlacement[O]{post, a.AgentID, o.Model.CurStep - 1}, true
		}
		return opinionPlacement[O]{}, false
	}
	if a.AgentID == agentID || a.HistoryID == -1 || neighborIDs[a.AgentID] {
		return opinionPlacement[O]{}, false
	}
	history := o.Model.Grid.PostMap[a.AgentID]
	post := history[a.HistoryID]
	round := o.Model.CurStep - (len(history) - a.HistoryID)
	if post != nil && (!o.hideReposts || post.IsOriginalPlacement(a.AgentID, round)) {
		return opinionPlacement[O]{post, a.AgentID, round}, true
	}
	return opinionPlacement[O]{}, false
}
