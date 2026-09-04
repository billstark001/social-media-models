package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"smp/simulation"
	"strings"
	"syscall"
)

func usage(program string) {
	log.Printf("Usage: %s <base_path> <metadata_json> [json_progress]", program)
}

func main() {
	metadata := simulation.DefaultScenarioMetadata()

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

	outputJSONProgress := false
	if len(args) > 3 {
		v := strings.ToLower(strings.TrimSpace(args[3]))
		outputJSONProgress = v == "1" || v == "yes" || v == "true" || v == "ok"
	}

	// The production CLI always persists graph/model dumps. Library callers
	// can disable them through NewScenarioWithOptions for lightweight analysis
	// or tests.
	scenario := simulation.NewScenarioWithOptions(
		basePath,
		metadata,
		simulation.ScenarioOptions{
			OutputJSONProgress: outputJSONProgress,
			EnableDumps:        true,
		},
	)

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
