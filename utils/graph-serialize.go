package utils

import (
	"os"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
)

type NetworkXGraph struct {
	Adjacency map[int64]map[int64]any  `json:"adjacency" msgpack:"adjacency"`
	Directed  bool                     `json:"directed" msgpack:"directed"`
	Nodes     map[int64]map[string]any `json:"nodes" msgpack:"nodes"`
	Graph     map[string]any           `json:"graph" msgpack:"graph"`
}

func SerializeGraph(g *simple.DirectedGraph) *NetworkXGraph {
	nxGraph := &NetworkXGraph{
		Adjacency: make(map[int64]map[int64]any),
		Directed:  true,
		Nodes:     make(map[int64]map[string]any),
		Graph:     make(map[string]any),
	}

	// An edge-only traversal loses isolated agents in model checkpoints.
	nodes := g.Nodes()
	for nodes.Next() {
		id := nodes.Node().ID()
		nxGraph.Nodes[id] = make(map[string]any)
		nxGraph.Adjacency[id] = make(map[int64]any)
	}
	edges := g.Edges()
	for edges.Next() {
		edge := edges.Edge()
		nodeID := edge.From().ID()
		targetID := edge.To().ID()

		if weighted, ok := edge.(simple.WeightedEdge); ok {
			nxGraph.Adjacency[nodeID][targetID] = map[string]any{
				"weight": weighted.W,
			}
		} else {
			nxGraph.Adjacency[nodeID][targetID] = map[string]any{}
		}
	}

	nxGraph.Graph["name"] = "Generated from Gonum DirectedGraph"

	return nxGraph
}

func DeserializeGraph(nxGraph *NetworkXGraph) *simple.DirectedGraph {
	g := simple.NewDirectedGraph()

	for nodeID := range nxGraph.Nodes {
		g.AddNode(simple.Node(nodeID))
	}

	// Adjacency can identify nodes without an attribute entry.
	for nodeID := range nxGraph.Adjacency {
		if _, exists := nxGraph.Nodes[nodeID]; !exists {
			g.AddNode(simple.Node(nodeID))
		}
	}

	for fromID, targets := range nxGraph.Adjacency {
		for toID, edgeAttr := range targets {
			if g.Node(toID) == nil {
				g.AddNode(simple.Node(toID))
			}

			var weight *float64
			if attrs, ok := edgeAttr.(map[string]any); ok {
				if w, exists := attrs["weight"]; exists {
					switch v := w.(type) {
					case float64:
						weight = &v
					case float32:
						value := float64(v)
						weight = &value
					case int:
						value := float64(v)
						weight = &value
					case int64:
						value := float64(v)
						weight = &value
					}
				}
			}

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
