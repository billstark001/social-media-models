// Command smp-probe measures frozen states, checkpoints, or persistent runs.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "smp-probe: %v\n", err)
		os.Exit(1)
	}
}
