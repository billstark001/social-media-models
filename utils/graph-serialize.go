package utils

import (
	"os"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
)

type NetworkXGraph struct {
	Adjacency map[int64]map[int64]any  `msgpack:"adjacency"`
	Directed  bool                     `msgpack:"directed"`
	Nodes     map[int64]map[string]any `msgpack:"nodes"`
	Graph     map[string]any           `msgpack:"graph"`
}

func SerializeGraph(g *simple.DirectedGraph) *NetworkXGraph {
	nxGraph := &NetworkXGraph{
		Adjacency: make(map[int64]map[int64]any),
		Directed:  true,
		Nodes:     make(map[int64]map[string]any),
		Graph:     make(map[string]any),
	}

	edges := g.Edges()
	for edges.Next() {
		edge := edges.Edge()
		nodeID := edge.From().ID()
		targetID := edge.To().ID()

		// init table
		if _, ok := nxGraph.Nodes[nodeID]; !ok {
			nxGraph.Nodes[nodeID] = make(map[string]any)
		}
		if _, ok := nxGraph.Adjacency[nodeID]; !ok {
			nxGraph.Adjacency[nodeID] = make(map[int64]any)
		}
		if _, ok := nxGraph.Nodes[targetID]; !ok {
			nxGraph.Nodes[targetID] = make(map[string]any)
		}
		if _, ok := nxGraph.Adjacency[targetID]; !ok {
			nxGraph.Adjacency[targetID] = make(map[int64]any)
		}

		// add edge
		if weighted, ok := edge.(simple.WeightedEdge); ok {
			nxGraph.Adjacency[nodeID][targetID] = map[string]any{
				"weight": weighted.W,
			}
		} else {
			nxGraph.Adjacency[nodeID][targetID] = map[string]any{}
		}
	}

	// add graph metadata
	nxGraph.Graph["name"] = "Generated from Gonum DirectedGraph"

	return nxGraph
}

func DeserializeGraph(nxGraph *NetworkXGraph) *simple.DirectedGraph {
	g := simple.NewDirectedGraph()

	// add nodes
	for nodeID := range nxGraph.Nodes {
		g.AddNode(simple.Node(nodeID))
	}

	// add nodes not in the list
	for nodeID := range nxGraph.Adjacency {
		if _, exists := nxGraph.Nodes[nodeID]; !exists {
			g.AddNode(simple.Node(nodeID))
		}
	}

	// add edges
	for fromID, targets := range nxGraph.Adjacency {
		for toID, edgeAttr := range targets {
			// ensure node
			if g.Node(toID) == nil {
				g.AddNode(simple.Node(toID))
			}

			// handle weight
			var weight *float64
			if attrs, ok := edgeAttr.(map[string]any); ok {
				if w, exists := attrs["weight"]; exists {
					switch v := w.(type) {
					case float64:
						weight = &v
					case float32:
					case int:
					case int64:
						_v := float64(v)
						weight = &_v
					}
				}
			}

			// add edge
			if weight != nil {
				g.SetEdge(simple.WeightedEdge{
					F: simple.Node(fromID),
					T: simple.Node(toID),
					W: *weight,
				})
			} else {
				g.SetEdge(simple.Edge{
					F: simple.Node(fromID),
					T: simple.Node(toID),
				})
			}
		}
	}

	return g
}

func SaveGraphToFile(g *simple.DirectedGraph, filename string) error {
	nxGraph := SerializeGraph(g)

	data, err := msgpack.Marshal(nxGraph)
	if err != nil {
		return err
	}

	return os.WriteFile(filename, data, 0644)
}

func LoadGraphFromFile(filename string) (*simple.DirectedGraph, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var nxGraph NetworkXGraph
	err = msgpack.Unmarshal(data, &nxGraph)
	if err != nil {
		return nil, err
	}

	return DeserializeGraph(&nxGraph), nil
}
