package recsys

import (
	"math"
	"math/rand"

	"smp/model"
)

// OpinionRandom implements a recommendation system with random opinion preferences.
// O is fixed to float64; P is the params type.
type OpinionRandom[P any] struct {
	model.BaseRecommendationSystem[float64, P]
	Model               *model.SMPModel[float64, P]
	HistoricalPostCount int
	AgentCount          int
	Tolerance           float64
	Steepness           float64
	NoiseStd            float64
	RandomRatio         float64

	NumNodes   int
	Agents     []*model.SMPAgent[float64, P]
	AllIndices []int
	RateMat    [][]float64
}

// NewOpinionRandom creates a new random opinion-based recommendation system.
func NewOpinionRandom[P any](
	m *model.SMPModel[float64, P],
	historicalPostCount *int,
	tolerance, steepness, noiseStd, randomRatio float64,
) *OpinionRandom[P] {
	h := m.ModelParams.PostRetainCount
	if historicalPostCount != nil {
		h = *historicalPostCount
	}
	return &OpinionRandom[P]{
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
func (o *OpinionRandom[P]) PostInit(dumpData []byte) {
	o.NumNodes = o.Model.Graph.Nodes().Len()
	o.AllIndices = make([]int, o.NumNodes)
	for i := range o.NumNodes {
		o.AllIndices[i] = i
	}
	o.Agents = o.Model.Schedule.Agents
	o.RateMat = makeRawMat[float64](o.NumNodes, o.NumNodes)
}

// PreStep implements model.SMPModelRecommendationSystem
func (o *OpinionRandom[P]) PreStep() {
	opinions := make([]float64, o.NumNodes)
	for i, agent := range o.Agents {
		opinions[i] = agent.CurOpinion
	}

	rawRateMat := makeRawMat[float64](o.NumNodes, o.NumNodes)

	for i := range o.NumNodes {
		for j := i + 1; j < o.NumNodes; j++ {
			diff := math.Abs(opinions[i] - opinions[j])
			rate := max(1.0-diff/o.Tolerance, 0)

			if o.NoiseStd > 0 {
				noise := rand.NormFloat64() * o.NoiseStd
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
func (o *OpinionRandom[P]) Recommend(
	agent *model.SMPAgent[float64, P],
	neighborIDs map[int64]bool,
	count int,
) []*model.PostRecord[float64] {

	visiblePosts := o.Model.Grid.PostMap

	rateVec := make([]float64, o.NumNodes)
	copy(rateVec, o.RateMat[agent.ID])

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

	candidates := sampleWithoutReplacement(o.AllIndices, count+4, rateVec)

	ret := make([]*model.PostRecord[float64], 0, len(candidates))
	for _, idx := range candidates {
		if len(ret) >= len(candidates) {
			break
		}
		agentPicked := o.Agents[idx]
		post := selectPost(
			o.HistoricalPostCount,
			neighborIDs,
			agentPicked.ID,
			o.Model.Grid.AgentMap,
			visiblePosts,
		)
		if post != nil {
			ret = append(ret, post)
		}
	}

	return ret
}
