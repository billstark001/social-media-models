package utils

import (
	"math/rand/v2"
	"os"
	"path"
	"testing"

	"gonum.org/v1/gonum/graph/simple"
)

func testRNG() *rand.Rand {
	return rand.New(rand.NewPCG(1, 2))
}

func TestGraphRoundTripPreservesIsolatedNodes(t *testing.T) {
	for _, withEdge := range []bool{false, true} {
		g := simple.NewDirectedGraph()
		for _, id := range []int64{0, 1, 4, 39} {
			g.AddNode(simple.Node(id))
		}
		if withEdge {
			g.SetEdge(g.NewEdge(g.Node(0), g.Node(1)))
		}
		file := path.Join(t.TempDir(), "isolates.msgpack")
		if err := SaveGraphToFile(g, file); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadGraphFromFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !CompareGraphs(g, loaded) || loaded.Node(39) == nil {
			t.Fatalf("isolated nodes lost (withEdge=%v)", withEdge)
		}
	}
}

// Test case for SerializeGraph and DeserializeGraph
func TestSerializeAndDeserializeGraph(t *testing.T) {
	// Create a random graph
	nodeCount := 100
	edgeProbability := 0.3
	g := CreateRandomNetwork(nodeCount, edgeProbability, testRNG())

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
	g := CreateRandomNetwork(nodeCount, edgeProbability, testRNG())

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
	g := CreateSmallWorldNetwork(nodeCount, k, rewireProbability, testRNG())

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
