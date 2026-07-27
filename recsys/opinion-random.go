package recsys

import (
	"math"
	"math/rand/v2"

	"smp/model"
	smprng "smp/rng"
)

// OpinionRandom implements a recommendation system with random opinion preferences.
type OpinionRandom[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	Model               *model.SMPModel[O, P]
	HistoricalPostCount int
	AgentCount          int
	Tolerance           float64
	Steepness           float64
	NoiseStd            float64
	RandomRatio         float64

	NumNodes   int
	Agents     []*model.SMPAgent[O, P]
	AllIndices []int
	RateMat    [][]float64
}

// NewOpinionRandom creates a new random opinion-based recommendation system.
func NewOpinionRandom[O any, P any](
	m *model.SMPModel[O, P],
	historicalPostCount *int,
	tolerance, steepness, noiseStd, randomRatio float64,
) *OpinionRandom[O, P] {
	h := m.ModelParams.PostRetainCount
	if historicalPostCount != nil {
		h = *historicalPostCount
	}
	return &OpinionRandom[O, P]{
		Model:               m,
		AgentCount:          m.Graph.Nodes().Len(),
		HistoricalPostCount: h,
		Tolerance:           tolerance,
		Steepness:           steepness,
		NoiseStd:            noiseStd,
		RandomRatio:         randomRatio,
	}
}

// PostInit implements model.SMPModelRecommendationSystem
func (o *OpinionRandom[O, P]) PostInit(dumpData []byte) {
	o.NumNodes = o.Model.Graph.Nodes().Len()
	o.AllIndices = make([]int, o.NumNodes)
	for i := range o.NumNodes {
		o.AllIndices[i] = i
	}
	o.Agents = o.Model.Schedule.Agents
	o.RateMat = makeRawMat[float64](o.NumNodes, o.NumNodes)
}

// PreStep implements model.SMPModelRecommendationSystem
func (o *OpinionRandom[O, P]) PreStep() {
	opinions := make([]float64, o.NumNodes)
	for i, agent := range o.Agents {
		opinions[i] = toFloat64(agent.CurOpinion)
	}

	rawRateMat := makeRawMat[float64](o.NumNodes, o.NumNodes)

	rng := o.Model.RNG.Stream(smprng.StreamRecommendation)
	for i := range o.NumNodes {
		for j := i + 1; j < o.NumNodes; j++ {
			diff := math.Abs(opinions[i] - opinions[j])
			rate := max(1.0-diff/o.Tolerance, 0)

			if o.NoiseStd > 0 {
				noise := rng.NormFloat64() * o.NoiseStd
				rate = max(rate*(1-2*noise)+noise, 0)
			}

			if o.Steepness != 1 {
				rate = math.Pow(rate, o.Steepness)
			}

			rawRateMat[i][j] = rate
			rawRateMat[j][i] = rate
		}
	}

	for i := range o.NumNodes {
		sum := 0.0
		for j := range o.NumNodes {
			sum += rawRateMat[i][j]
		}
		if sum > 0 {
			for j := range o.NumNodes {
				if o.RandomRatio > 0 {
					o.RateMat[i][j] = (1-o.RandomRatio)*rawRateMat[i][j]/sum +
						o.RandomRatio/(float64(o.NumNodes)-1)
				} else {
					o.RateMat[i][j] = rawRateMat[i][j] / sum
				}
			}
		}
		o.RateMat[i][i] = 0
	}
}

// Recommend implements model.SMPModelRecommendationSystem
func (o *OpinionRandom[O, P]) Recommend(
	agent *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
) []*model.PostRecord[O] {
	rng := o.Model.RNG.Stream(smprng.StreamRecommendation)

	rateVec := make([]float64, o.NumNodes)
	copy(rateVec, o.RateMat[agent.ID])
	return o.recommendFromRates(agent, neighborIDs, count, rateVec, rng)
}

// RecommendAt evaluates the weighted opinion recommender for a hypothetical
// opinion without rebuilding the O(N²) real-agent rate matrix.
func (o *OpinionRandom[O, P]) RecommendAt(
	agent *model.SMPAgent[O, P],
	opinion O,
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	query := toFloat64(opinion)
	rawRates := make([]float64, o.NumNodes)
	rawTotal := 0.0
	for i, candidate := range o.Agents {
		if candidate.ID == agent.ID {
			continue
		}
		diff := math.Abs(query - toFloat64(candidate.CurOpinion))
		rate := 0.0
		if o.Tolerance > 0 {
			rate = max(1.0-diff/o.Tolerance, 0)
		} else if diff == 0 {
			rate = 1
		}
		if o.NoiseStd > 0 {
			noise := rng.NormFloat64() * o.NoiseStd
			rate = max(rate*(1-2*noise)+noise, 0)
		}
		if o.Steepness != 1 {
			rate = math.Pow(rate, o.Steepness)
		}
		rawRates[i] = rate
		rawTotal += rate
	}

	rateVec := make([]float64, o.NumNodes)
	for i, rawRate := range rawRates {
		if int64(i) == agent.ID {
			continue
		}
		if rawTotal > 0 {
			rateVec[i] = (1-o.RandomRatio)*rawRate/rawTotal +
				o.RandomRatio/float64(max(o.NumNodes-1, 1))
		} else if o.RandomRatio > 0 {
			rateVec[i] = 1 / float64(max(o.NumNodes-1, 1))
		}
	}
	return o.recommendFromRates(agent, neighborIDs, count, rateVec, rng)
}

func (o *OpinionRandom[O, P]) recommendFromRates(
	agent *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
	rateVec []float64,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	visiblePosts := o.Model.Grid.PostMap
	sum := 0.0
	rateVec[agent.ID] = 0
	for id := range neighborIDs {
		rateVec[id] = 0
	}
	for i := range rateVec {
		sum += rateVec[i]
	}
	if sum > 0 {
		for i := range rateVec {
			rateVec[i] /= sum
		}
	}

	candidates := sampleWithoutReplacement(o.AllIndices, count+4, rateVec, rng)

	ret := make([]*model.PostRecord[O], 0, count)
	for _, idx := range candidates {
		if len(ret) >= count {
			break
		}
		agentPicked := o.Agents[idx]
		post := selectPost(
			o.HistoricalPostCount,
			neighborIDs,
			agentPicked.ID,
			o.Model.Grid.AgentMap,
			visiblePosts,
			rng,
		)
		if post != nil {
			ret = append(ret, post)
		}
	}

	return ret
}
