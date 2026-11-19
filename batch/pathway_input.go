package batch

import (
	"errors"
	"fmt"

	"smp/observables"

	"gonum.org/v1/gonum/graph/simple"
)

// PathwayInput observes a frozen graph without initializing a simulator.
type PathwayInput struct {
	Opinions []float64  `json:"opinions"`
	Edges    []EdgePair `json:"edges"`
	Epsilon  *float64   `json:"epsilon"`
}

func (input PathwayInput) measure() (observables.PathwayState, error) {
	if input.Epsilon == nil || !isFinite(*input.Epsilon) || *input.Epsilon < 0 {
		return observables.PathwayState{}, errors.New("epsilon must be finite and nonnegative")
	}
	n := int64(len(input.Opinions))
	if n < 2 {
		return observables.PathwayState{}, errors.New("expected at least two opinions")
	}
	g := simple.NewDirectedGraph()
	for i, opinion := range input.Opinions {
		if !isFinite(opinion) || opinion < -1 || opinion > 1 {
			return observables.PathwayState{}, fmt.Errorf("opinion %d must be in [-1, 1]", i)
		}
		g.AddNode(simple.Node(i))
	}
	for _, edge := range input.Edges {
		i, j := edge[0], edge[1]
		if i < 0 || i >= n || j < 0 || j >= n || i == j || g.HasEdgeFromTo(i, j) {
			return observables.PathwayState{}, fmt.Errorf("invalid or repeated directed edge %v", edge)
		}
		g.SetEdge(simple.Edge{F: g.Node(i), T: g.Node(j)})
	}
	return observables.MeasurePathway(input.Opinions, g, *input.Epsilon)
}
