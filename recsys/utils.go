package recsys

import (
	"container/heap"
	"math"
	"math/rand/v2"
	"smp/model"

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
func sampleWithoutReplacement(population []int, n int, probabilities []float64, rng *rand.Rand) []int {
	if n <= 0 {
		return nil
	}

	selected := make(weightedMinHeap, 0, min(n, len(population)))
	for i, p := range probabilities {
		if p <= 0 || math.IsNaN(p) {
			continue
		}
		item := weightedItem{
			key:   math.Log1p(-rng.Float64()) / p,
			index: i,
		}
		if len(selected) < n {
			heap.Push(&selected, item)
		} else if item.key > selected[0].key {
			selected[0] = item
			heap.Fix(&selected, 0)
		}
	}

	result := make([]int, len(selected))
	// Pop from smallest to largest into the slice backwards, yielding the same
	// descending-key order the previous full sort used.
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = population[heap.Pop(&selected).(weightedItem).index]
	}
	return result
}

type weightedItem struct {
	key   float64
	index int
}

type weightedMinHeap []weightedItem

func (h weightedMinHeap) Len() int           { return len(h) }
func (h weightedMinHeap) Less(i, j int) bool { return h[i].key < h[j].key }
func (h weightedMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *weightedMinHeap) Push(x any)        { *h = append(*h, x.(weightedItem)) }
func (h *weightedMinHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// forEachRandomIndex visits a uniformly random permutation lazily, stopping
// when visit returns false. It avoids allocating and shuffling an O(N) slice
// when a recommender normally needs only a small prefix.
func forEachRandomIndex(n int, rng *rand.Rand, visit func(int) bool) {
	swaps := make(map[int]int)
	valueAt := func(i int) int {
		if value, ok := swaps[i]; ok {
			return value
		}
		return i
	}
	for i := 0; i < n; i++ {
		j := i + rng.IntN(n-i)
		chosen := valueAt(j)
		swaps[j] = valueAt(i)
		delete(swaps, i)
		if !visit(chosen) {
			return
		}
	}
}

// PostIndex is used by the opinion-based recsys to index and sort posts.
type PostIndex struct {
	AgentID     int64
	HistoryID   int // -1: current opinion marker
	TempOpinion float64
}

// toFloat64 converts any opinion type to float64 for similarity computation.
func toFloat64(val any) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case bool:
		if v {
			return 1.0
		}
		return 0.0
	default:
		return 0.0
	}
}

func selectPost[O any, P any](
	historicalPostCount int,
	selfAndNeighborIDs map[int64]bool,
	agentPickedID int64,
	agentMap map[int64]*model.SMPAgent[O, P],
	visiblePosts map[int64][]*model.PostRecord[O],
	rng *rand.Rand,
) *model.PostRecord[O] {
	postPickedIndex := -1
	if historicalPostCount > 0 {
		postPickedIndex = rng.IntN(historicalPostCount)
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
