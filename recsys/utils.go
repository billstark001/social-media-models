package recsys

import (
	"math"
	"math/rand/v2"
	"smp/model"
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
			selected = append(selected, item)
			selected.up(len(selected) - 1)
		} else if item.key > selected[0].key {
			selected[0] = item
			selected.down(0, len(selected))
		}
	}

	result := make([]int, len(selected))
	// Pop into the slice backwards to return candidates in descending-key order.
	for i := len(result) - 1; i >= 0; i-- {
		selected[0], selected[i] = selected[i], selected[0]
		selected.down(0, i)
		result[i] = population[selected[i].index]
	}
	return result
}

type weightedItem struct {
	key   float64
	index int
}

type weightedMinHeap []weightedItem

// These are the same strict comparisons and swaps as container/heap, including
// its tie order, without boxing each weightedItem through heap.Interface.
func (h weightedMinHeap) up(j int) {
	for j > 0 {
		i := (j - 1) / 2
		if !(h[j].key < h[i].key) {
			break
		}
		h[i], h[j] = h[j], h[i]
		j = i
	}
}

func (h weightedMinHeap) down(i, n int) {
	for {
		j := 2*i + 1
		if j >= n {
			break
		}
		if right := j + 1; right < n && h[right].key < h[j].key {
			j = right
		}
		if !(h[j].key < h[i].key) {
			break
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
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

// postVisibility filters candidates by the holder of the placement rather than
// by the author alone. It leaves the ranking and sampling rules intact.
type postVisibility struct{ hideReposts bool }

func (v *postVisibility) SetHideReposts(hide bool) { v.hideReposts = hide }

func selectPost[O any, P any](
	historicalPostCount int,
	selfAndNeighborIDs map[int64]bool,
	agentPickedID int64,
	m *model.SMPModel[O, P],
	hideReposts bool,
	rng *rand.Rand,
) *model.PostRecord[O] {
	postPickedIndex := -1
	if historicalPostCount > 0 {
		postPickedIndex = rng.IntN(historicalPostCount)
	}
	var el *model.PostRecord[O]
	placementRound := m.CurStep - 1
	visiblePosts := m.Grid.PostMap
	if postPickedIndex != -1 && postPickedIndex < len(visiblePosts[agentPickedID]) {
		el = visiblePosts[agentPickedID][len(visiblePosts[agentPickedID])-postPickedIndex-1]
		placementRound -= postPickedIndex
	} else {
		el = m.Grid.AgentMap[agentPickedID].CurPost
	}
	if el == nil || selfAndNeighborIDs[el.AgentID] || (hideReposts && !el.IsOriginalPlacement(agentPickedID, placementRound)) {
		return nil
	}
	m.RecordRecommendationPlacement(agentPickedID, placementRound)
	return el
}
