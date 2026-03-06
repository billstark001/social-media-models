package recsys

import (
	"math"
	"smp/model"
)

// StructureRandom implements a weighted random recommendation based on network structure.
type StructureRandom[O any, P any] struct {
	Structure[O, P]
	Steepness   float64
	RandomRatio float64
}

// NewStructureRandom creates a new structure-based random recommendation system.
func NewStructureRandom[O any, P any](
	m *model.SMPModel[O, P],
	historicalPostCount *int,
	steepness, noiseStd, randomRatio float64,
	matrixInit bool,
	logFunc func(string),
) *StructureRandom[O, P] {
	return &StructureRandom[O, P]{
		Structure:   *NewStructure(m, noiseStd, historicalPostCount, matrixInit, logFunc),
		Steepness:   steepness,
		RandomRatio: randomRatio,
	}
}

// PreStep implements model.SMPModelRecommendationSystem
func (s *StructureRandom[O, P]) PreStep() {
	s.Structure.PreStep()

	if s.Steepness != 1 {
		for i := range s.NumNodes {
			for j := range s.NumNodes {
				s.RateMat[i][j] = math.Pow(s.RateMat[i][j], s.Steepness)
			}
		}
	}

	for i := range s.NumNodes {
		sum := 0.0
		for j := range s.NumNodes {
			sum += s.RateMat[i][j]
		}
		if sum > 0 {
			for j := range s.NumNodes {
				if s.RandomRatio > 0 {
					s.RateMat[i][j] = (1-s.RandomRatio)*s.RateMat[i][j]/sum +
						s.RandomRatio/(float64(s.NumNodes)-1)
				} else {
					s.RateMat[i][j] = s.RateMat[i][j] / sum
				}
			}
		}
		s.RateMat[i][i] = 0
	}
}

// Recommend implements model.SMPModelRecommendationSystem
func (s *StructureRandom[O, P]) Recommend(
	agent *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
) []*model.PostRecord[O] {

	visiblePosts := s.Model.Grid.PostMap

	rateVec := make([]float64, s.NumNodes)
	copy(rateVec, s.RateMat[agent.ID])

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

	candidates := sampleWithoutReplacement(s.AllIndices, count+4, rateVec)

	ret := make([]*model.PostRecord[O], 0, count)
	for _, idx := range candidates {
		if len(ret) >= count {
			break
		}
		agentPicked := s.AgentMap[int64(idx)]
		post := selectPost(
			s.HistoricalPostCount,
			neighborIDs,
			agentPicked.ID,
			s.Model.Grid.AgentMap,
			visiblePosts,
		)
		if post != nil {
			ret = append(ret, post)
		}
	}

	return ret
}
