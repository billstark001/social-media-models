package trajectory

import (
	"math"
	"path/filepath"
	"testing"
)

func TestChunkRoundTripAndCompression(t *testing.T) {
	state := newTestBlock()
	for step := 0; step < 128; step++ {
		opinions := make([]float64, 20)
		numbers := make([][4]int16, 20)
		sums := make([][4]float64, 20)
		for agent := 0; agent < 20; agent++ {
			opinions[agent] = float64(agent) / 20
			if step%17 == 0 {
				opinions[agent] += float64(step) / 10000
			}
			numbers[agent] = [4]int16{int16(agent), 0, 3, 1}
			sums[agent] = [4]float64{float64(agent) / 7, 0, 0, 0}
		}
		state.Opinions = append(state.Opinions, opinions)
		state.AgentNumbers = append(state.AgentNumbers, numbers)
		state.AgentOpinionSums = append(state.AgentOpinionSums, sums)
	}
	path := filepath.Join(t.TempDir(), "chunk.smpc")
	saveTestChunk(t, path, state, Precision{Opinions: "float64", OpinionSums: "float64"})
	decoded, err := LoadChunk(path)
	if err != nil {
		t.Fatal(err)
	}
	for step := range state.Opinions {
		for agent := range state.Opinions[step] {
			if math.Float64bits(state.Opinions[step][agent]) != math.Float64bits(decoded.Opinions[step][agent]) || state.AgentNumbers[step][agent] != decoded.AgentNumbers[step][agent] {
				t.Fatalf("changed row at %d/%d", step, agent)
			}
			for c := 0; c < 4; c++ {
				if math.Float64bits(state.AgentOpinionSums[step][agent][c]) != math.Float64bits(decoded.AgentOpinionSums[step][agent][c]) {
					t.Fatalf("changed sum at %d/%d/%d", step, agent, c)
				}
			}
		}
	}
}

func TestTrajectoryBooleanAndZeroChannels(t *testing.T) {
	state := newTestBlock()
	for step := 0; step < 17; step++ {
		ops := make([]float64, 19)
		for agent := range ops {
			if (agent+step)%3 == 0 {
				ops[agent] = 1
			}
		}
		state.Opinions = append(state.Opinions, ops)
		state.AgentNumbers = append(state.AgentNumbers, make([][4]int16, 19))
		state.AgentOpinionSums = append(state.AgentOpinionSums, make([][4]float64, 19))
	}
	path := filepath.Join(t.TempDir(), "bool.smpc")
	saveTestChunk(t, path, state, Precision{})
	decoded, err := LoadChunk(path)
	if err != nil {
		t.Fatal(err)
	}
	for step := range state.Opinions {
		for agent := range state.Opinions[step] {
			if decoded.Opinions[step][agent] != state.Opinions[step][agent] {
				t.Fatal("boolean opinion mismatch")
			}
		}
	}
}
