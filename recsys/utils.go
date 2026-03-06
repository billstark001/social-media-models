package recsys

import (
	"math"
	"math/rand/v2"
	"smp/model"
	"sort"

	"gonum.org/v1/gonum/graph"
)

func makeRawMat[T any](h int, w int) [][]T {
	m := make([][]T, h)
	for i := range m {
		m[i] = make([]T, w)
	}
	return m
}

// sampleWithoutReplacement samples n items from population without replacement
// using the given probabilities via the Efraimidis-Spirakis A-ES algorithm.
// Each item i gets key = log(U) / p[i]; the n items with the largest keys are returned.
func sampleWithoutReplacement(population []int, n int, probabilities []float64) []int {
	if n >= len(population) {
		result := make([]int, len(population))
		copy(result, population)
		return result
	}

	type keyed struct {
		key float64
		i   int
	}
	items := make([]keyed, len(population))
	for i, p := range probabilities {
		items[i] = keyed{math.Log(rand.Float64()) / p, i}
	}
	sort.Slice(items, func(a, b int) bool {
		return items[a].key > items[b].key
	})
	result := make([]int, n)
	for i := range n {
		result[i] = population[items[i].i]
	}
	return result
}

// PostIndex is used by the opinion-based recsys to index and sort posts.
type PostIndex struct {
	AgentID     int64
	HistoryID   int // -1: current opinion marker
	TempOpinion float64
}

func selectPost[O any, P any](
	historicalPostCount int,
	selfAndNeighborIDs map[int64]bool,
	agentPickedID int64,
	agentMap map[int64]*model.SMPAgent[O, P],
	visiblePosts map[int64][]*model.PostRecord[O],
) *model.PostRecord[O] {
	postPickedIndex := -1
	if historicalPostCount > 0 {
		postPickedIndex = rand.IntN(historicalPostCount)
	}
	var el *model.PostRecord[O]
	if postPickedIndex != -1 && postPickedIndex < len(visiblePosts[agentPickedID]) {
		el = visiblePosts[agentPickedID][len(visiblePosts[agentPickedID])-postPickedIndex-1]
	} else {
		el = agentMap[agentPickedID].CurPost
	}
	if el == nil || selfAndNeighborIDs[el.AgentID] {
		return nil
	}
	return el
}

// commonNeighborsCount calculates the number of common neighbors between two nodes
func commonNeighborsCount(g graph.Directed, u, v int) int {
	uPred := nodesSet(g.To(int64(u)))
	uSucc := nodesSet(g.From(int64(u)))
	vPred := nodesSet(g.To(int64(v)))
	vSucc := nodesSet(g.From(int64(v)))

	count := 0

	for w := range uPred {
		if vPred[w] || vSucc[w] {
			count++
		}
	}

	for w := range uSucc {
		if vPred[w] || vSucc[w] {
			count++
		}
	}

	return count
}

// nodesSet converts a nodes iterator to a set (map)
func nodesSet(iter graph.Nodes) map[int64]bool {
	result := make(map[int64]bool)
	for iter.Next() {
		result[iter.Node().ID()] = true
	}
	return result
}
