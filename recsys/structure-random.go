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
// useCache enables row-level lazy caching: raw 2-hop scores are reused until
// the agent's 1-hop or 2-hop neighborhood changes (see PostStep).
func NewStructureRandom[O any, P any](
	m *model.SMPModel[O, P],
	historicalPostCount *int,
	steepness, noiseStd, randomRatio float64,
	useCache bool,
	logFunc func(string),
) *StructureRandom[O, P] {
	return &StructureRandom[O, P]{
		Structure:   *NewStructure(m, noiseStd, historicalPostCount, useCache, logFunc),
		Steepness:   steepness,
		RandomRatio: randomRatio,
	}
}

// Recommend implements model.SMPModelRecommendationSystem.
// It computes common-neighbor scores on demand (or from cache), applies the
// steepness transform, normalises, mixes with a uniform random component,
// then samples without replacement.
//
// Cache enabled (useCache=true):
//   - Cache hit:  O(D² + N) – skips graph traversal, rebuilds probability
//     vector from cached raw scores and re-samples.
//   - Cache miss: full O(D²) graph traversal then as above.
func (s *StructureRandom[O, P]) Recommend(
	agent *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
) []*model.PostRecord[O] {
	visiblePosts := s.Model.Grid.PostMap
	g := s.Model.Graph

	// Attempt to read raw scores from cache.
	var rawCounts map[int64]float64
	if s.useCache && s.cacheValid[agent.ID] {
		rawCounts = s.rawScoreCache[agent.ID]
	}

	if rawCounts == nil {
		// Collect undirected 1-hop neighborhood of agent.
		agentNeighborSet := make(map[int64]struct{})
		it := g.From(agent.ID)
		for it.Next() {
			agentNeighborSet[it.Node().ID()] = struct{}{}
		}
		it = g.To(agent.ID)
		for it.Next() {
			agentNeighborSet[it.Node().ID()] = struct{}{}
		}
		delete(agentNeighborSet, agent.ID)

		// Count 2-hop neighbors.
		rawCounts = make(map[int64]float64, len(agentNeighborSet)*8)
		for w := range agentNeighborSet {
			wNeighborSet := make(map[int64]struct{})
			it := g.From(w)
			for it.Next() {
				wNeighborSet[it.Node().ID()] = struct{}{}
			}
			it = g.To(w)
			for it.Next() {
				wNeighborSet[it.Node().ID()] = struct{}{}
			}
			for v := range wNeighborSet {
				if v != agent.ID {
					rawCounts[v]++
				}
			}
		}

		if s.useCache {
			s.rawScoreCache[agent.ID] = rawCounts
			s.cacheValid[agent.ID] = true
		}
	}

	// Apply steepness and compute total.
	protoScores := make(map[int64]float64, len(rawCounts))
	total := 0.0
	for v, sc := range rawCounts {
		var transformed float64
		if s.Steepness != 1 {
			transformed = math.Pow(sc, s.Steepness)
		} else {
			transformed = sc
		}
		protoScores[v] = transformed
		total += transformed
	}

	// Build full probability vector: base random component for every node,
	// plus the normalized structure component for 2-hop neighbors.
	rateVec := make([]float64, s.NumNodes)
	if s.RandomRatio > 0 && s.NumNodes > 1 {
		baseProb := s.RandomRatio / float64(s.NumNodes-1)
		for i := range rateVec {
			rateVec[i] = baseProb
		}
	}
	if total > 0 {
		structWeight := 1 - s.RandomRatio
		for v, sc := range protoScores {
			rateVec[v] += structWeight * sc / total
		}
	}

	// Zero out self and already-known neighbors.
	rateVec[agent.ID] = 0
	for id := range neighborIDs {
		rateVec[id] = 0
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
