// Package observables contains measurements shared by batch and probe.
// Measurements never advance a model or consume its random streams.
package observables

import (
	"fmt"
	"math"
	"sort"

	"gonum.org/v1/gonum/graph/simple"
)

type Energy struct {
	Population     float64 `json:"population" msgpack:"population"`
	Edge           float64 `json:"edge" msgpack:"edge"`
	TopologyExcess float64 `json:"topology_excess" msgpack:"topology_excess"`
	Nodes          int     `json:"nodes" msgpack:"nodes"`
	Edges          int     `json:"edges" msgpack:"edges"`
}

type Landscape struct {
	X                          []float64 `json:"x" msgpack:"x"`
	Population                 []float64 `json:"population" msgpack:"population"`
	PopulationNegativeGradient []float64 `json:"population_negative_gradient" msgpack:"population_negative_gradient"`
	PopulationConcordantMass   []float64 `json:"population_concordant_mass" msgpack:"population_concordant_mass"`
	EdgeBySource               []float64 `json:"edge_by_source" msgpack:"edge_by_source"`
	SourceCounts               []int     `json:"source_counts" msgpack:"source_counts"`
	EdgeCounts                 []int     `json:"edge_counts" msgpack:"edge_counts"`
	EdgeBySourceValid          []bool    `json:"edge_by_source_valid" msgpack:"edge_by_source_valid"`
}

func kernel(distance, epsilon, scale float64) float64 {
	d := math.Abs(distance)
	if d > epsilon {
		d = epsilon
	}
	return scale * d * d / 2
}

func Validate(opinions []float64, epsilon, scale float64) error {
	if len(opinions) == 0 {
		return fmt.Errorf("empty opinion state")
	}
	if math.IsNaN(epsilon) || math.IsInf(epsilon, 0) || epsilon < 0 {
		return fmt.Errorf("epsilon must be finite and non-negative")
	}
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		return fmt.Errorf("energy scale must be finite and positive")
	}
	for i, x := range opinions {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("opinion %d is not finite", i)
		}
	}
	return nil
}

// MeasureEnergy uses the empirical population law and the actual number of
// directed edges. It does not assume fixed out-degree.
func MeasureEnergy(opinions []float64, graph *simple.DirectedGraph, epsilon, scale float64) (Energy, error) {
	var result Energy
	if err := Validate(opinions, epsilon, scale); err != nil {
		return result, err
	}
	n := len(opinions)
	result.Nodes = n
	values := append([]float64(nil), opinions...)
	sort.Float64s(values)
	prefix := make([]float64, n+1)
	prefixSq := make([]float64, n+1)
	for i, x := range values {
		prefix[i+1] = prefix[i] + x
		prefixSq[i+1] = prefixSq[i] + x*x
	}
	left := 0
	pairEnergy := 0.0
	for i, x := range values {
		for left < i && x-values[left] > epsilon {
			left++
		}
		// Values [left,i) lie inside the confidence interval.
		nearSum := prefix[i] - prefix[left]
		nearSq := prefixSq[i] - prefixSq[left]
		near := float64(i - left)
		pairEnergy += scale * (near*x*x - 2*x*nearSum + nearSq + float64(left)*epsilon*epsilon) / 2
	}
	result.Population = pairEnergy / float64(n*n)
	if graph != nil {
		nodes := graph.Nodes()
		for nodes.Next() {
			id := nodes.Node().ID()
			if id < 0 || int(id) >= n {
				return Energy{}, fmt.Errorf("graph node %d outside opinions", id)
			}
			neighbors := graph.From(id)
			for neighbors.Next() {
				j := neighbors.Node().ID()
				if j < 0 || int(j) >= n {
					return Energy{}, fmt.Errorf("graph target %d outside opinions", j)
				}
				result.Edge += kernel(opinions[id]-opinions[j], epsilon, scale)
				result.Edges++
			}
		}
		if result.Edges > 0 {
			result.Edge /= float64(result.Edges)
		}
	}
	result.TopologyExcess = result.Edge - 2*result.Population
	return result, nil
}

// MeasureLandscape returns population values at arbitrary query coordinates.
// EdgeBySource conditions on current source opinions binned at the midpoints
// between query coordinates; empty bins are marked invalid.
func MeasureLandscape(opinions []float64, graph *simple.DirectedGraph, epsilon, scale float64, queries []float64) (Landscape, error) {
	var out Landscape
	if err := Validate(opinions, epsilon, scale); err != nil {
		return out, err
	}
	for i, x := range queries {
		if math.IsNaN(x) || math.IsInf(x, 0) || (i > 0 && x <= queries[i-1]) {
			return out, fmt.Errorf("query coordinates must be finite and strictly increasing")
		}
	}
	n := len(opinions)
	out.X = append([]float64(nil), queries...)
	m := len(queries)
	out.Population = make([]float64, m)
	out.PopulationNegativeGradient = make([]float64, m)
	out.PopulationConcordantMass = make([]float64, m)
	out.EdgeBySource = make([]float64, m)
	out.SourceCounts = make([]int, m)
	out.EdgeCounts = make([]int, m)
	out.EdgeBySourceValid = make([]bool, m)
	for q, x := range queries {
		for _, y := range opinions {
			out.Population[q] += kernel(x-y, epsilon, scale)
			if math.Abs(x-y) < epsilon {
				out.PopulationNegativeGradient[q] += scale * (y - x)
				out.PopulationConcordantMass[q]++
			}
		}
		out.Population[q] /= float64(n)
		out.PopulationNegativeGradient[q] /= float64(n)
		out.PopulationConcordantMass[q] /= float64(n)
	}
	if m == 0 || graph == nil {
		return out, nil
	}
	bin := func(x float64) int {
		return sort.Search(m-1, func(i int) bool { return x < (queries[i]+queries[i+1])/2 })
	}
	nodes := graph.Nodes()
	for nodes.Next() {
		id := nodes.Node().ID()
		if id < 0 || int(id) >= n {
			return Landscape{}, fmt.Errorf("graph node %d outside opinions", id)
		}
		b := bin(opinions[id])
		out.SourceCounts[b]++
		neighbors := graph.From(id)
		for neighbors.Next() {
			j := neighbors.Node().ID()
			if j < 0 || int(j) >= n {
				return Landscape{}, fmt.Errorf("graph target %d outside opinions", j)
			}
			out.EdgeBySource[b] += kernel(opinions[id]-opinions[j], epsilon, scale)
			out.EdgeCounts[b]++
		}
	}
	for i, count := range out.EdgeCounts {
		if count > 0 {
			out.EdgeBySource[i] /= float64(count)
			out.EdgeBySourceValid[i] = true
		}
	}
	return out, nil
}

func CounterfactualNeighborEnergy(opinions []float64, graph *simple.DirectedGraph, anchor int64, epsilon, scale float64, queries []float64) ([]float64, bool, error) {
	if err := Validate(opinions, epsilon, scale); err != nil {
		return nil, false, err
	}
	if graph == nil || anchor < 0 || int(anchor) >= len(opinions) || graph.Node(anchor) == nil {
		return nil, false, fmt.Errorf("invalid anchor %d", anchor)
	}
	neighbors := graph.From(anchor)
	ids := make([]int64, 0, neighbors.Len())
	for neighbors.Next() {
		ids = append(ids, neighbors.Node().ID())
	}
	values := make([]float64, len(queries))
	if len(ids) == 0 {
		return values, false, nil
	}
	for i, x := range queries {
		for _, id := range ids {
			values[i] += kernel(x-opinions[id], epsilon, scale)
		}
		values[i] /= float64(len(ids))
	}
	return values, true, nil
}
