package utils

import (
	"math/rand"

	"gonum.org/v1/gonum/graph/simple"
)

// n, p graph
//
// p = m / (n - 1),
func CreateRandomNetwork(nodeCount int, edgeProbability float64) *simple.DirectedGraph {
	g := simple.NewDirectedGraph()

	for i := range nodeCount {
		g.AddNode(simple.Node(i))
	}

	for i := range nodeCount {
		for j := range nodeCount {
			if i != j && rand.Float64() < edgeProbability {
				g.SetEdge(g.NewEdge(simple.Node(i), simple.Node(j)))
			}
		}
	}

	return g
}

func CreateSmallWorldNetwork(nodeCount int, k int, rewireProbability float64) *simple.DirectedGraph {
	g := simple.NewDirectedGraph()

	for i := range nodeCount {
		g.AddNode(simple.Node(i))
	}

	for i := range nodeCount {
		for j := 1; j <= k/2; j++ {
			rightNeighbor := (i + j) % nodeCount
			leftNeighbor := (i - j + nodeCount) % nodeCount

			g.SetEdge(g.NewEdge(simple.Node(i), simple.Node(rightNeighbor)))
			g.SetEdge(g.NewEdge(simple.Node(i), simple.Node(leftNeighbor)))
		}
	}

	// random reconnect
	for i := 0; i < nodeCount; i++ {
		for j := 1; j <= k/2; j++ {
			if rand.Float64() < rewireProbability {
				// current target
				oldTarget := (i + j) % nodeCount

				// find new target
				var newTarget int
				for {
					newTarget = rand.Intn(nodeCount)
					if newTarget != i && !g.HasEdgeBetween(int64(i), int64(newTarget)) {
						break
					}
				}

				// rewire
				g.RemoveEdge(int64(i), int64(oldTarget))
				g.SetEdge(g.NewEdge(simple.Node(i), simple.Node(newTarget)))
			}
		}
	}

	return g
}
