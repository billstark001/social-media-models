package utils

import (
	"gonum.org/v1/gonum/graph/simple"
)

// Helper function to generate a unique key for an edge
func edgeKey(from, to int64) string {
	return string(rune(from)) + "->" + string(rune(to))
}

// Helper function to compare two graphs for equality
func CompareGraphs(g1, g2 *simple.DirectedGraph) bool {
	// Check that both graphs have the same nodes
	nodes1 := g1.Nodes()
	nodes2 := g2.Nodes()

	nodeCount1 := 0
	nodeCount2 := 0
	nodeMap1 := make(map[int64]bool)
	nodeMap2 := make(map[int64]bool)

	for nodes1.Next() {
		nodeCount1++
		nodeMap1[nodes1.Node().ID()] = true
	}

	for nodes2.Next() {
		nodeCount2++
		nodeMap2[nodes2.Node().ID()] = true
	}

	if nodeCount1 != nodeCount2 || len(nodeMap1) != len(nodeMap2) {
		return false
	}

	for id := range nodeMap1 {
		if !nodeMap2[id] {
			return false
		}
	}

	// Check that both graphs have the same edges
	edges1 := g1.Edges()
	edges2 := g2.Edges()

	edgeCount1 := 0
	edgeCount2 := 0
	edgeMap1 := make(map[string]float64)
	edgeMap2 := make(map[string]float64)

	for edges1.Next() {
		edgeCount1++
		edge := edges1.Edge()
		key := edgeKey(edge.From().ID(), edge.To().ID())
		if weighted, ok := edge.(simple.WeightedEdge); ok {
			edgeMap1[key] = weighted.W
		} else {
			edgeMap1[key] = 0
		}
	}

	for edges2.Next() {
		edgeCount2++
		edge := edges2.Edge()
		key := edgeKey(edge.From().ID(), edge.To().ID())
		if weighted, ok := edge.(simple.WeightedEdge); ok {
			edgeMap2[key] = weighted.W
		} else {
			edgeMap2[key] = 0
		}
	}

	if edgeCount1 != edgeCount2 || len(edgeMap1) != len(edgeMap2) {
		return false
	}

	for key, weight := range edgeMap1 {
		if w2, exists := edgeMap2[key]; !exists || w2 != weight {
			return false
		}
	}

	return true
}
