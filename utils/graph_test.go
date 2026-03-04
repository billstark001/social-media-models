package utils

import (
	"os"
	"path"
	"testing"

	"gonum.org/v1/gonum/graph/simple"
)

// Test case for SerializeGraph and DeserializeGraph
func TestSerializeAndDeserializeGraph(t *testing.T) {
	// Create a random graph
	nodeCount := 100
	edgeProbability := 0.3
	g := CreateRandomNetwork(nodeCount, edgeProbability)

	// Serialize the graph
	nxGraph := SerializeGraph(g)

	// Deserialize the graph
	deserializedGraph := DeserializeGraph(nxGraph)

	// Compare the original and deserialized graphs
	if !CompareGraphs(g, deserializedGraph) {
		t.Errorf("Original graph and deserialized graph are not equal")
	}
}

// Test case for SaveGraphToFile and LoadGraphFromFile
func TestSaveAndLoadGraphToFile(t *testing.T) {
	// Create a random graph
	nodeCount := 100
	edgeProbability := 0.3
	g := CreateRandomNetwork(nodeCount, edgeProbability)

	// Save the graph to a file
	tempDir := os.TempDir()
	filename := path.Join(tempDir, "test_graph.msgpack")
	err := SaveGraphToFile(g, filename)
	if err != nil {
		t.Fatalf("Failed to save graph to file: %v", err)
	}

	// Load the graph from the file
	loadedGraph, err := LoadGraphFromFile(filename)
	if err != nil {
		t.Fatalf("Failed to load graph from file: %v", err)
	}

	// Compare the original and loaded graphs
	if !CompareGraphs(g, loadedGraph) {
		t.Errorf("Original graph and loaded graph are not equal")
	}
}

// Test case for CreateSmallWorldNetwork with serialization and deserialization
func TestSmallWorldNetworkSerialization(t *testing.T) {
	// Create a small-world network
	nodeCount := 100
	k := 4
	rewireProbability := 0.1
	g := CreateSmallWorldNetwork(nodeCount, k, rewireProbability)

	// Serialize the graph
	nxGraph := SerializeGraph(g)

	// Deserialize the graph
	deserializedGraph := DeserializeGraph(nxGraph)

	// Compare the original and deserialized graphs
	if !CompareGraphs(g, deserializedGraph) {
		t.Errorf("Small-world network and deserialized graph are not equal")
	}
}

// Test case for weighted edges
func TestWeightedEdgesSerialization(t *testing.T) {
	// Create a graph with weighted edges
	g := simple.NewDirectedGraph()
	edge1 := simple.WeightedEdge{F: simple.Node(1), T: simple.Node(2), W: 5.5}
	edge2 := simple.WeightedEdge{F: simple.Node(2), T: simple.Node(3), W: 2.3}

	g.SetEdge(edge1)
	g.SetEdge(edge2)

	// Serialize the graph
	nxGraph := SerializeGraph(g)

	// Deserialize the graph
	deserializedGraph := DeserializeGraph(nxGraph)

	// Compare the original and deserialized graphs
	if !CompareGraphs(g, deserializedGraph) {
		t.Errorf("Graph with weighted edges and deserialized graph are not equal")
	}
}
