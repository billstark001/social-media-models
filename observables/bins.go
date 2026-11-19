package observables

import (
	"fmt"
	"math"
	"sort"

	"gonum.org/v1/gonum/graph/simple"
)

// BinnedState uses population mass 1/N and directed edge mass 1/N. Thus the
// edge matrix sums to the observed mean out-degree, matching the mesoscopic E.
type BinnedState struct {
	Population []float64 `json:"population" msgpack:"population"`
	Edge       []float64 `json:"edge" msgpack:"edge"`
	Nodes      int       `json:"nodes" msgpack:"nodes"`
	Edges      int       `json:"edges" msgpack:"edges"`
}

// ValidateBinBoundaries checks a binning request before state or file loading.
func ValidateBinBoundaries(boundaries []float64) error {
	if len(boundaries) < 2 {
		return fmt.Errorf("at least two bin boundaries are required")
	}
	bins := len(boundaries) - 1
	if bins > int(^uint(0)>>1)/8/bins {
		return fmt.Errorf("binned edge matrix exceeds addressable memory")
	}
	for i, x := range boundaries {
		if math.IsNaN(x) || math.IsInf(x, 0) || (i > 0 && x <= boundaries[i-1]) {
			return fmt.Errorf("bin boundaries must be finite and increasing")
		}
	}
	return nil
}

func MeasureBinnedState(opinions []float64, graph *simple.DirectedGraph, boundaries []float64) (BinnedState, error) {
	var out BinnedState
	if err := ValidateBinBoundaries(boundaries); err != nil {
		return out, err
	}
	n := len(opinions)
	if n == 0 {
		return out, fmt.Errorf("empty opinion state")
	}
	bins := len(boundaries) - 1
	out.Population = make([]float64, bins)
	out.Edge = make([]float64, bins*bins)
	populationCounts := make([]int, bins)
	edgeCounts := make([]int, bins*bins)
	out.Nodes = n
	indices := make([]int, n)
	for i, x := range opinions {
		if math.IsNaN(x) || x < boundaries[0] || x > boundaries[len(boundaries)-1] {
			return BinnedState{}, fmt.Errorf("opinion %d outside bins", i)
		}
		j := sort.Search(bins, func(k int) bool { return x < boundaries[k+1] || k == bins-1 })
		indices[i] = j
		populationCounts[j]++
	}
	if graph != nil {
		nodes := graph.Nodes()
		for nodes.Next() {
			i := nodes.Node().ID()
			if i < 0 || int(i) >= n {
				return BinnedState{}, fmt.Errorf("graph node out of range")
			}
			neighbors := graph.From(i)
			for neighbors.Next() {
				j := neighbors.Node().ID()
				if j < 0 || int(j) >= n {
					return BinnedState{}, fmt.Errorf("graph target out of range")
				}
				edgeCounts[indices[i]*bins+indices[j]]++
				out.Edges++
			}
		}
	}
	// Normalize once per bin. Repeated additions of 1/N accumulate rounding
	// error when a concentrated state puts thousands of edges in one bin.
	for i, count := range populationCounts {
		out.Population[i] = float64(count) / float64(n)
	}
	for i, count := range edgeCounts {
		out.Edge[i] = float64(count) / float64(n)
	}
	return out, nil
}
