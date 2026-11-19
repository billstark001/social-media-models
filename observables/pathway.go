package observables

import (
	"fmt"
	"math"
	"slices"

	"gonum.org/v1/gonum/graph/simple"
)

// PathwayState measures the same post-commit opinions and directed follow graph.
type PathwayState struct {
	Separation        float64  `json:"separation" msgpack:"separation"`
	PairConcordance   float64  `json:"pair_concordance" msgpack:"pair_concordance"`
	EdgeConcordance   *float64 `json:"edge_concordance" msgpack:"edge_concordance"`
	ExcessConcordance *float64 `json:"excess_concordance" msgpack:"excess_concordance"`
}

// PathwayMeasurer reuses opinion workspace within a trajectory. Use one per
// running model. Its zero value is ready to use.
type PathwayMeasurer struct {
	sorted []float64
}

// MeasurePathway counts distinct random pairs within epsilon. Separation is
// twice the maximum smaller mass of two width-epsilon/2 opinion intervals
// with an intervening gap greater than epsilon.
func MeasurePathway(opinions []float64, graph *simple.DirectedGraph, epsilon float64) (PathwayState, error) {
	var workspace PathwayMeasurer
	return workspace.Measure(opinions, graph, epsilon)
}

func (w *PathwayMeasurer) Measure(opinions []float64, graph *simple.DirectedGraph, epsilon float64) (PathwayState, error) {
	if err := validatePathway(opinions, epsilon); err != nil {
		return PathwayState{}, err
	}
	pair := w.pairConcordance(opinions, epsilon)
	state := PathwayState{
		Separation:      opinionSeparation(w.sorted, epsilon),
		PairConcordance: pair,
	}
	raw, ok, err := edgeConcordance(opinions, graph, epsilon)
	if err != nil {
		return PathwayState{}, err
	}
	if ok {
		excess := raw - pair
		state.EdgeConcordance, state.ExcessConcordance = &raw, &excess
	}
	return state, nil
}

// ExcessAtLeast evaluates the same concordance difference as Measure, without
// computing opinion separation or materializing an observation.
func (w *PathwayMeasurer) ExcessAtLeast(opinions []float64, graph *simple.DirectedGraph, epsilon, threshold float64) (bool, error) {
	if err := validatePathway(opinions, epsilon); err != nil {
		return false, err
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < -1 || threshold > 1 {
		return false, fmt.Errorf("excess threshold must be in [-1, 1]")
	}
	pair := w.pairConcordance(opinions, epsilon)
	if 1-pair < threshold {
		return false, nil
	}
	raw, ok, err := edgeConcordance(opinions, graph, epsilon)
	return ok && raw-pair >= threshold, err
}

func validatePathway(opinions []float64, epsilon float64) error {
	if len(opinions) < 2 {
		return fmt.Errorf("pathway requires at least two opinions")
	}
	return Validate(opinions, epsilon, 1)
}

func (w *PathwayMeasurer) pairConcordance(opinions []float64, epsilon float64) float64 {
	w.sorted = append(w.sorted[:0], opinions...)
	slices.Sort(w.sorted)
	agree, left := 0, 0
	for right, op := range w.sorted {
		for left < right && op-w.sorted[left] > epsilon {
			left++
		}
		agree += right - left
	}
	if n := len(w.sorted); n > 1 {
		return 2 * float64(agree) / (float64(n) * float64(n-1))
	}
	return 0
}

func opinionSeparation(sorted []float64, epsilon float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	left, end, eligible, bestLeft, best := 0, 0, 0, 0, 0
	for right, op := range sorted {
		for eligible < right && op-sorted[eligible] > epsilon {
			for left < eligible && sorted[eligible]-sorted[left] > epsilon/2 {
				left++
			}
			bestLeft = max(bestLeft, eligible-left+1)
			eligible++
		}
		for end < len(sorted) && sorted[end]-op <= epsilon/2 {
			end++
		}
		best = max(best, min(bestLeft, end-right))
	}
	return 2 * float64(best) / float64(len(sorted))
}

func edgeConcordance(opinions []float64, graph *simple.DirectedGraph, epsilon float64) (float64, bool, error) {
	if graph == nil {
		return 0, false, nil
	}
	concordant, count := 0, 0
	nodes := graph.Nodes()
	for nodes.Next() {
		source := nodes.Node().ID()
		if source < 0 || source >= int64(len(opinions)) {
			return 0, false, fmt.Errorf("graph node %d outside opinions", source)
		}
		neighbors := graph.From(source)
		count += neighbors.Len()
		for neighbors.Next() {
			target := neighbors.Node().ID()
			if target < 0 || target >= int64(len(opinions)) || target == source {
				return 0, false, fmt.Errorf("invalid directed edge %d to %d", source, target)
			}
			if math.Abs(opinions[source]-opinions[target]) <= epsilon {
				concordant++
			}
		}
	}
	if count == 0 {
		return 0, false, nil
	}
	return float64(concordant) / float64(count), true, nil
}
