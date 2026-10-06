package simulation_test

import (
	"path/filepath"
	"smp/model"
	"smp/simulation"
	"testing"
)

// Keep the original exported signatures usable across the package extraction.
var _ func(string, int, simulation.TrajectoryPrecision) (*simulation.TrajectoryWriter, error) = simulation.OpenTrajectory
var _ func(*simulation.TrajectoryBuffer) ([]byte, error) = simulation.EncodeTrajectoryChunk
var _ func(string) (*simulation.AccumulativeModelState, error) = simulation.LoadTrajectoryChunk

func TestPublicTrajectoryAPIStillRoundTrips(t *testing.T) {
	precision := simulation.TrajectoryPrecision{Opinions: "float64", OpinionSums: "float64"}
	state := simulation.NewTrajectoryAccumulativeState(2, precision)
	if err := state.Packed.Append([]float64{-.25, .75}, []model.AgentNumberRecord{{1, 2, 3, 4}, {4, 3, 2, 1}}, []model.AgentOpinionSumRecord{{.5, 0, 0, 0}, {0, 0, -.5, 0}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writer, err := simulation.OpenTrajectory(dir, 2, precision)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(state.Packed); err != nil {
		t.Fatal(err)
	}
	if err := writer.Verify(); err != nil {
		t.Fatal(err)
	}
	decoded, err := simulation.LoadTrajectoryChunk(filepath.Join(dir, writer.Manifest.Chunks[0].File))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Len() != 1 || decoded.Opinions[0][1] != .75 || decoded.AgentNumbers[0][0][2] != 3 || decoded.AgentOpinionSums[0][1][2] != -.5 {
		t.Fatal("public trajectory API changed its decoded data")
	}
}
