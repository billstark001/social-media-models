package simulation

import (
	"smp/model"
	"smp/trajectory"
)

const DefaultMaxSimulationStep = 15000

// DefaultScenarioMetadata returns the defaults used by the command-line
// runners before applying a caller's JSON overrides.
func DefaultScenarioMetadata() *ScenarioMetadata {
	return &ScenarioMetadata{
		SMPModelPureParams: model.SMPModelPureParams{
			PostRetainCount: 3,
			RecsysCount:     10,
		},
		CollectItemOptions: model.CollectItemOptions{
			AgentNumber:   true,
			OpinionSum:    true,
			RewiringEvent: true,
			PostEvent:     true,
		},
		RecsysFactoryType:   "Random",
		NetworkType:         "Random",
		NodeCount:           500,
		NodeFollowCount:     15,
		MaxSimulationStep:   DefaultMaxSimulationStep,
		TrajectoryPrecision: trajectory.Precision{Opinions: "float32", OpinionSums: "float16"},
	}
}
