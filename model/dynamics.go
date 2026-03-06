package model

// Dynamics defines the interface for opinion update dynamics.
// O is the opinion type (e.g. float64), P is the parameters type.
type Dynamics[O any, P any] interface {
	// Concordant returns true if otherOpinion is within tolerance of myOpinion.
	Concordant(myOpinion, otherOpinion O, params *P) bool
	// Step computes the next opinion and opinion-sum statistics.
	// cN = concordant neighbor opinions, cR = concordant recommended opinions,
	// dN = discordant neighbor opinions, dR = discordant recommended opinions.
	Step(myOpinion O, cN, cR, dN, dR []O, params *P) (nextOpinion O, opinionSum AgentOpinionSumRecord)
}

// PreStepDynamics is an optional interface for Dynamics implementations that
// can batch pre-generate random numbers before a full round of agent steps.
// RandomActivation calls PrepareStep(agentCount) once before iterating agents.
type PreStepDynamics interface {
	PrepareStep(agentCount int)
}
