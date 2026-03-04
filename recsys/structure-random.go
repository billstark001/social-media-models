package recsys

import (
	"math"
	"smp/model"
)

// StructureRandom implements a weighted random recommendation based on network structure
type StructureRandom struct {
	Structure
	Steepness   float64
	RandomRatio float64
}

// NewStructureRandom creates a new structure-based random recommendation system
func NewStructureRandom(
	model *model.SMPModel,
	historicalTweetCount *int,
	steepness, noiseStd, randomRatio float64,
	matrixInit bool,
	logFunc func(string),
) *StructureRandom {
	return &StructureRandom{
		Structure:   *NewStructure(model, noiseStd, historicalTweetCount, matrixInit, logFunc),
		Steepness:   steepness,
		RandomRatio: randomRatio,
	}
}

// PreStep implements model.SMPModelRecommendationSystem
func (s *StructureRandom) PreStep() {
	// Call parent PreStep to create raw rate matrix
	s.Structure.PreStep()

	// Apply steepness
	if s.Steepness != 1 {
		for i := range s.NumNodes {
			for j := range s.NumNodes {
				s.RateMat[i][j] = math.Pow(s.RateMat[i][j], s.Steepness)
			}
		}
	}

	// Normalize rate matrix
	for i := range s.NumNodes {
		sum := 0.0
		for j := range s.NumNodes {
			sum += s.RateMat[i][j]
		}

		if sum > 0 {
			for j := range s.NumNodes {
				// Normalize and add random component if needed
				if s.RandomRatio > 0 {
					s.RateMat[i][j] = (1-s.RandomRatio)*s.RateMat[i][j]/sum +
						s.RandomRatio/(float64(s.NumNodes)-1)
				} else {
					s.RateMat[i][j] = s.RateMat[i][j] / sum
				}
			}
		}
		s.RateMat[i][i] = 0 // No self-recommendation
	}
}

// TODO this one is essentially identical to the `opinion-random`'s one, replace them with a common function
// Recommend implements model.SMPModelRecommendationSystem
func (s *StructureRandom) Recommend(
	agent *model.SMPAgent,
	neighborIDs map[int64]bool,
	count int,
) []*model.TweetRecord {

	visibleTweets := s.Model.Grid.TweetMap

	// Create a copy of the rate vector
	rateVec := make([]float64, s.NumNodes)
	copy(rateVec, s.RateMat[agent.ID])

	// Set rate to 0 for neighbors
	sum := 0.0
	rateVec[agent.ID] = 0
	for id := range neighborIDs {
		rateVec[id] = 0
	}

	// Renormalize
	for i := range rateVec {
		sum += rateVec[i]
	}
	if sum > 0 {
		for i := range rateVec {
			rateVec[i] /= sum
		}
	}

	// Sample agents based on probability
	candidates := sampleWithoutReplacement(s.AllIndices, count+4, rateVec)

	// Collect tweets from selected agents
	ret := make([]*model.TweetRecord, 0, len(candidates))
	for _, idx := range candidates {
		if len(ret) >= len(candidates) {
			break
		}
		agentPicked := s.AgentMap[int64(idx)]
		tweet := selectTweet(
			s.HistoricalTweetCount,
			neighborIDs,
			agentPicked.ID,
			s.Model.Grid.AgentMap,
			visibleTweets,
		)
		if tweet != nil {
			ret = append(ret, tweet)
		}
	}

	return ret
}
