// Command smp-probe evaluates counterfactual HK force from frozen states.
//
// It reads one msgpack-encoded probe.Request from stdin and writes one
// msgpack-encoded probe.Response to stdout. Diagnostics are written to stderr.
package main

import (
	"fmt"
	"io"
	"os"

	"smp/probe"

	"github.com/vmihailenco/msgpack/v5"
)

func run(input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	var request probe.Request
	if err := msgpack.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	response, err := probe.Evaluate(&request)
	if err != nil {
		return fmt.Errorf("evaluate request: %w", err)
	}
	encoded, err := msgpack.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if _, err := output.Write(encoded); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "smp-probe: %v\n", err)
		os.Exit(1)
	}
}
