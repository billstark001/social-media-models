package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"smp/batch"
	"syscall"
)

func main() {
	progressMode := flag.String(
		"progress",
		"none",
		"progress on stderr: none, human, or jsonl",
	)
	progressStepInterval := flag.Int(
		"progress-step-interval",
		0,
		"emit within-run progress every N steps (0 disables it)",
	)
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "smp-batch reads JSONL requests from stdin")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer cancel()
	if err := batch.Run(ctx, os.Stdin, os.Stdout, batch.Options{
		Progress:             os.Stderr,
		ProgressMode:         *progressMode,
		ProgressStepInterval: *progressStepInterval,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
