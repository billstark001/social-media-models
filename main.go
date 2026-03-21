package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"smp/model"
	"smp/simulation"
	"strings"
	"syscall"
)

const MAX_SIM_COUNT = 15000

func usage(program string) {
	log.Printf("Usage: %s <base_path> <metadata_json> [parsable_progress]", program)
}

func main() {
	metadata := &simulation.ScenarioMetadata{

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
	}

	args := os.Args
	if len(args) < 3 {
		log.Printf("missing required arguments: <base_path> and <metadata_json>")
		usage(args[0])
		os.Exit(2)
	}

	basePath := args[1]
	metadataJson := []byte(args[2])

	err := json.Unmarshal(metadataJson, metadata)
	if err != nil {
		log.Fatalf("Failed to unmarshal metadata file: %v", err)
	}

	if err := metadata.Validate(); err != nil {
		log.Fatalf("Invalid metadata: %v", err)
	}

	outputParsableProgress := false
	if len(args) > 3 {
		v := strings.ToLower(strings.TrimSpace(args[3]))
		outputParsableProgress = v == "1" || v == "yes" || v == "true" || v == "ok"
	}

	scenario := simulation.NewScenario(basePath, metadata, outputParsableProgress)

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
