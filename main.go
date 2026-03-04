package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"smp/dynamics"
	"smp/model"
	"smp/simulation"
	"syscall"
)

const MAX_SIM_COUNT = 15000

func main() {
	metadata := &simulation.ScenarioMetadata{

		HKParams: dynamics.HKParams{

			Decay:        0.01,
			Tolerance:    0.45,
			RewiringRate: 0.05,
			RepostRate:   0.3,
		},

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

		RecsysFactoryType: "Random",
		NetworkType:       "Random",
		NodeCount:         500,
		NodeFollowCount:   15,

		MaxSimulationStep: MAX_SIM_COUNT,

		UniqueName: "test",
	}

	args := os.Args
	basePath := args[1]
	metadataJson := []byte(args[2])

	err := json.Unmarshal(metadataJson, metadata)
	if err != nil {
		log.Fatalf("Failed to unmarshal metadata file: %v", err)
	}

	scenario := simulation.NewScenario(basePath, metadata, false)

	if !scenario.Load() {
		scenario.Init()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	scenario.StepTillEnd(ctx)
}
