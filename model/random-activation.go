package model

import "math/rand"

// RandomActivation manages agent activation and scheduling
type RandomActivation struct {
	Model  *SMPModel
	Agents []*SMPAgent
}

// NewRandomActivation creates a new random activation scheduler
func NewRandomActivation(model *SMPModel) *RandomActivation {
	return &RandomActivation{
		Model:  model,
		Agents: make([]*SMPAgent, 0),
	}
}

// AddAgent adds an agent to the scheduler
func (ra *RandomActivation) AddAgent(agent *SMPAgent) {
	ra.Agents = append(ra.Agents, agent)
}

// Step activates all agents in random order
func (ra *RandomActivation) Step() {
	// Create a shuffled index array
	indices := make([]int, len(ra.Agents))
	for i := range indices {
		indices[i] = i
	}

	// Fisher-Yates shuffle
	for i := len(indices) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		indices[i], indices[j] = indices[j], indices[i]
	}

	// Activate agents in shuffled order
	for _, i := range indices {
		ra.Agents[i].Step()
	}
}
