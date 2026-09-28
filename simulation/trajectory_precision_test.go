package simulation

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"smp/model"
)

func TestTrajectoryPrecisionCombinationsAndPackedBuffer(t *testing.T) {
	levels := []string{"float16", "float32", "float64"}
	for _, opinionLevel := range levels {
		for _, sumLevel := range levels {
			precision := TrajectoryPrecision{Opinions: opinionLevel, OpinionSums: sumLevel}
			t.Run(opinionLevel+"_"+sumLevel, func(t *testing.T) {
				state := NewAccumulativeModelState()
				state.Opinions = [][]float64{{1.0 / 3, -0.75}, {1.0/3 + 1e-5, -0.5}}
				state.AgentNumbers = [][][4]int16{{{1, 2, 3, 4}, {5, 6, 7, 8}}, {{2, 3, 4, 5}, {6, 7, 8, 9}}}
				state.AgentOpinionSums = [][][4]float64{{{1.0 / 3, -1.0 / 7, 0, 2}, {4, 5, 6, 7}}, {{1.0/3 + 1e-5, -1.0 / 7, 0, 2}, {4, 5, 6, 7}}}
				buffer := newTrajectoryBuffer(2, precision)
				for step := range state.Opinions {
					counts := make([]model.AgentNumberRecord, 2)
					sums := make([]model.AgentOpinionSumRecord, 2)
					for agent := range counts {
						for c := range counts[agent] {
							counts[agent][c] = int(state.AgentNumbers[step][agent][c])
							sums[agent][c] = state.AgentOpinionSums[step][agent][c]
						}
					}
					if err := buffer.Append(state.Opinions[step], counts, sums); err != nil {
						t.Fatal(err)
					}
				}
				packed, err := EncodeTrajectoryChunk(buffer)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "chunk.smpc")
				if err := os.WriteFile(path, packed, 0644); err != nil {
					t.Fatal(err)
				}
				decoded, err := LoadTrajectoryChunk(path)
				if err != nil {
					t.Fatal(err)
				}
				for step := range state.Opinions {
					for agent := range state.Opinions[step] {
						if decoded.Opinions[step][agent] != roundedTrajectoryValue(state.Opinions[step][agent], opinionLevel) {
							t.Fatal("opinion precision mismatch")
						}
						if decoded.AgentNumbers[step][agent] != state.AgentNumbers[step][agent] {
							t.Fatal("count mismatch")
						}
						for c := 0; c < 4; c++ {
							if decoded.AgentOpinionSums[step][agent][c] != roundedTrajectoryValue(state.AgentOpinionSums[step][agent][c], sumLevel) {
								t.Fatal("opinion sum precision mismatch")
							}
						}
					}
				}
				buffer.Reset()
				if buffer.Steps != 0 || len(buffer.Channels[0]) != 0 {
					t.Fatal("buffer reset failed")
				}
			})
		}
	}
}

func roundedTrajectoryValue(value float64, level string) float64 {
	switch level {
	case "float16":
		bits, _ := float16Bits(value)
		return float16Value(bits)
	case "float32":
		return float64(float32(value))
	default:
		return value
	}
}

func TestFloat16EdgesAndV2Compatibility(t *testing.T) {
	for _, tc := range []struct {
		value float64
		bits  uint16
	}{
		{0, 0}, {math.Copysign(0, -1), 0x8000}, {1, 0x3c00}, {-2, 0xc000}, {1.0 / 3, 0x3555},
		{1 + math.Ldexp(1, -11), 0x3c00}, {1 + math.Ldexp(1, -11) + math.Ldexp(1, -40), 0x3c01},
		{math.Ldexp(1, -24), 1}, {math.Ldexp(1, -14), 0x0400}, {65504, 0x7bff},
	} {
		bits, err := float16Bits(tc.value)
		if err != nil || bits != tc.bits {
			t.Fatalf("float16(%g) = %#x, %v; want %#x", tc.value, bits, err, tc.bits)
		}
	}
	if _, err := float16Bits(65505); err == nil {
		t.Fatal("float16 overflow not rejected")
	}
	state := NewAccumulativeModelState()
	state.Opinions = [][]float64{{0.25}}
	state.AgentNumbers = [][][4]int16{{{1, 2, 3, 4}}}
	state.AgentOpinionSums = [][][4]float64{{{0.5, 0, 0, 0}}}
	newChunk, err := EncodeTrajectoryChunk(testBufferFromState(t, state, TrajectoryPrecision{Opinions: "float32", OpinionSums: "float32"}))
	if err != nil {
		t.Fatal(err)
	}
	oldChunk := append(append([]byte{}, trajectoryMagicV2[:]...), newChunk[8:16]...)
	oldChunk = append(oldChunk, newChunk[18:]...)
	path := filepath.Join(t.TempDir(), "legacy.smpc")
	if err := os.WriteFile(path, oldChunk, 0644); err != nil {
		t.Fatal(err)
	}
	decoded, err := LoadTrajectoryChunk(path)
	if err != nil || decoded.Opinions[0][0] != 0.25 || decoded.AgentOpinionSums[0][0][0] != 0.5 {
		t.Fatalf("version-2 block unreadable: %v", err)
	}
}

func testBufferFromState(t *testing.T, state *AccumulativeModelState, precision TrajectoryPrecision) *TrajectoryBuffer {
	t.Helper()
	buffer := newTrajectoryBuffer(len(state.Opinions[0]), precision.resolved())
	for step := range state.Opinions {
		counts := make([]model.AgentNumberRecord, len(state.Opinions[step]))
		sums := make([]model.AgentOpinionSumRecord, len(counts))
		for agent := range counts {
			for c := range counts[agent] {
				counts[agent][c] = int(state.AgentNumbers[step][agent][c])
				sums[agent][c] = state.AgentOpinionSums[step][agent][c]
			}
		}
		if err := buffer.Append(state.Opinions[step], counts, sums); err != nil {
			t.Fatal(err)
		}
	}
	return buffer
}

func saveTestTrajectoryChunk(t *testing.T, path string, state *AccumulativeModelState, precision TrajectoryPrecision) {
	t.Helper()
	data, err := EncodeTrajectoryChunk(testBufferFromState(t, state, precision))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
