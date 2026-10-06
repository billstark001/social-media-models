// Package trajectory stores microscopic observations in compressed time blocks.
// It does not construct, advance or restore a simulation.
package trajectory

// Block is a decoded trajectory chunk. It carries observations only, without
// Scenario state, checkpoint data, RNG or persistence scheduling counters.
type Block struct {
	Opinions         [][]float64
	AgentNumbers     [][][4]int16
	AgentOpinionSums [][][4]float64
}
