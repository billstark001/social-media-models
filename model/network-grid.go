package model

import (
	"sync"

	"gonum.org/v1/gonum/graph/simple"
)

// NetworkGrid represents a network structure for placing agents.
type NetworkGrid[O any, P any] struct {
	Graph    *simple.DirectedGraph
	AgentMap map[int64]*SMPAgent[O, P]
	PostMap  map[int64][]*PostRecord[O] // { [agent id]: latest posts (left -> right: newer) }
	mu       sync.RWMutex
}

// NewNetworkGrid creates a new network grid.
func NewNetworkGrid[O any, P any](g *simple.DirectedGraph) *NetworkGrid[O, P] {
	return &NetworkGrid[O, P]{
		Graph:    g,
		AgentMap: make(map[int64]*SMPAgent[O, P]),
		PostMap:  make(map[int64][]*PostRecord[O]),
	}
}

// PlaceAgent places an agent on the grid.
func (ng *NetworkGrid[O, P]) PlaceAgent(agent *SMPAgent[O, P], nodeID int64) {
	ng.mu.Lock()
	defer ng.mu.Unlock()
	ng.AgentMap[nodeID] = agent
}

// GetAgent returns the agent at the specified node.
func (ng *NetworkGrid[O, P]) GetAgent(nodeID int64) *SMPAgent[O, P] {
	ng.mu.RLock()
	defer ng.mu.RUnlock()
	return ng.AgentMap[nodeID]
}

// AddPost adds a post to the specified node.
func (ng *NetworkGrid[O, P]) AddPost(nodeID int64, post *PostRecord[O], maxPosts int) {
	ng.mu.Lock()
	defer ng.mu.Unlock()
	posts := ng.PostMap[nodeID]
	posts = append(posts, post)
	if len(posts) > maxPosts {
		posts = posts[len(posts)-maxPosts:]
	}
	ng.PostMap[nodeID] = posts
}

// GetNeighbors returns the agents from the neighbors of a node.
func (ng *NetworkGrid[O, P]) GetNeighbors(nodeID int64, includeCenter bool) []*SMPAgent[O, P] {
	ng.mu.RLock()
	defer ng.mu.RUnlock()

	var result []*SMPAgent[O, P]
	neighbors := ng.Graph.From(nodeID)
	for neighbors.Next() {
		neighborID := neighbors.Node().ID()
		result = append(result, ng.AgentMap[neighborID])
	}
	if includeCenter {
		result = append(result, ng.AgentMap[nodeID])
	}
	return result
}
