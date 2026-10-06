package simulation

import "smp/trajectory"

// These exported names preserve the existing public trajectory API. Simulation
// internals use trajectory directly; no private forwarding helpers are retained.
type TrajectoryPrecision = trajectory.Precision
type TrajectoryBuffer = trajectory.Buffer
type TrajectoryWriter = trajectory.Writer
type TrajectoryManifest = trajectory.Manifest
type TrajectoryChunk = trajectory.Chunk

func OpenTrajectory(dir string, agents int, precision TrajectoryPrecision) (*TrajectoryWriter, error) {
	return trajectory.Open(dir, agents, precision)
}

func EncodeTrajectoryChunk(buffer *TrajectoryBuffer) ([]byte, error) {
	return trajectory.EncodeChunk(buffer)
}

func LoadTrajectoryChunk(path string) (*AccumulativeModelState, error) {
	block, err := trajectory.LoadChunk(path)
	if err != nil {
		return nil, err
	}
	return &AccumulativeModelState{
		Opinions: block.Opinions, AgentNumbers: block.AgentNumbers,
		AgentOpinionSums: block.AgentOpinionSums,
	}, nil
}
