package model

import (
	"sync"

	"gonum.org/v1/gonum/graph/simple"
)

// NetworkGrid represents a network structure for placing agents
type NetworkGrid struct {
	Graph    *simple.DirectedGraph
	AgentMap map[int64]*SMPAgent
	TweetMap map[int64][]*TweetRecord // { [agent id]: latest tweets (left -> right: newer) }
	mu       sync.RWMutex
}

// NewNetworkGrid creates a new network grid
func NewNetworkGrid(g *simple.DirectedGraph) *NetworkGrid {
	return &NetworkGrid{
		Graph:    g,
		AgentMap: make(map[int64]*SMPAgent),
		TweetMap: make(map[int64][]*TweetRecord),
	}
}

// PlaceAgent places an agent on the grid
func (ng *NetworkGrid) PlaceAgent(agent *SMPAgent, nodeID int64) {
	ng.mu.Lock()
	defer ng.mu.Unlock()
	ng.AgentMap[nodeID] = agent
}

// GetAgent returns the agent at the specified node
func (ng *NetworkGrid) GetAgent(nodeID int64) *SMPAgent {
	ng.mu.RLock()
	defer ng.mu.RUnlock()
	return ng.AgentMap[nodeID]
}

// AddTweet adds a tweet to the specified node
func (ng *NetworkGrid) AddTweet(nodeID int64, tweet *TweetRecord, maxTweets int) {
	ng.mu.Lock()
	defer ng.mu.Unlock()
	tweets := ng.TweetMap[nodeID]

	// Add the new tweet
	tweets = append(tweets, tweet)

	// Limit the number of tweets per node
	if len(tweets) > maxTweets {
		tweets = tweets[len(tweets)-maxTweets:]
	}

	ng.TweetMap[nodeID] = tweets
}

// GetNeighbors returns the agents from the neighbors of a node
func (ng *NetworkGrid) GetNeighbors(nodeID int64, includeCenter bool) []*SMPAgent {
	ng.mu.RLock()
	defer ng.mu.RUnlock()

	var result []*SMPAgent

	// Get neighbor nodes
	neighbors := ng.Graph.From(nodeID)
	for neighbors.Next() {
		neighborID := neighbors.Node().ID()
		result = append(result, ng.AgentMap[neighborID])
	}

	// Include center node's tweets if requested
	if includeCenter {
		result = append(result, ng.AgentMap[nodeID])
	}

	return result
}
