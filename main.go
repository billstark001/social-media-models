package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"smp/model"
	"smp/simulation"
	"syscall"
)

const MAX_SIM_COUNT = 15000

func main() {
	metadata := &simulation.ScenarioMetadata{

		SMPAgentParams: model.SMPAgentParams{

			Decay:        0.01,
			Tolerance:    0.45,
			RewiringRate: 0.05,
			RetweetRate:  0.3,
		},

		SMPModelPureParams: model.SMPModelPureParams{

			TweetRetainCount: 3,
			RecsysCount:      10,
		},

		CollectItemOptions: model.CollectItemOptions{

			AgentNumber:   true,
			OpinionSum:    true,
			RewiringEvent: true,
			TweetEvent:    true,
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
	// metadataJson, err := os.ReadFile(metadataPath)
	// if err != nil {
	// 	log.Fatalf("Failed to load metadata file: %v", err)
	// }

	// basePath := "./run"
	// metadataJson := []byte(`{}`)

	err := json.Unmarshal(metadataJson, metadata)
	if err != nil {
		log.Fatalf("Failed to unmarshal metadata file: %v", err)
	}

	// TODO install output parsable progress in python script
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
