package model

import "math/rand"

// RandomActivation manages agent activation and scheduling.
type RandomActivation[O any, P any] struct {
	Model  *SMPModel[O, P]
	Agents []*SMPAgent[O, P]
}

// NewRandomActivation creates a new random activation scheduler.
func NewRandomActivation[O any, P any](model *SMPModel[O, P]) *RandomActivation[O, P] {
	return &RandomActivation[O, P]{
		Model:  model,
		Agents: make([]*SMPAgent[O, P], 0),
	}
}

// AddAgent adds an agent to the scheduler.
func (ra *RandomActivation[O, P]) AddAgent(agent *SMPAgent[O, P]) {
	ra.Agents = append(ra.Agents, agent)
}

// Step activates all agents in random order.
func (ra *RandomActivation[O, P]) Step() {
	if ps, ok := any(ra.Model.Dynamics).(PreStepDynamics); ok {
		ps.PrepareStep(len(ra.Agents))
	}
	indices := make([]int, len(ra.Agents))
	for i := range indices {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		indices[i], indices[j] = indices[j], indices[i]
	}
	for _, i := range indices {
		ra.Agents[i].Step()
	}
}
