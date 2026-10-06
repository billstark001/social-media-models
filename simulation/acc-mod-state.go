package simulation

import (
	"smp/model"
	"smp/trajectory"
)

type AccumulativeModelState struct {
	// Decoded rows are used by the legacy accumulated-state format. New
	// persistent runs fill Packed directly and leave these slices empty.
	// (step, agent)
	Opinions [][]float64
	// (step, agent, type)
	AgentNumbers [][][4]int16
	// (step, agent, type)
	AgentOpinionSums [][][4]float64
	Packed           *trajectory.Buffer

	UnsafePostEvent int
}

func NewTrajectoryAccumulativeState(agents int, precision trajectory.Precision) *AccumulativeModelState {
	return &AccumulativeModelState{Packed: trajectory.NewBuffer(agents, precision.Resolved())}
}

func (s *AccumulativeModelState) Len() int {
	if s.Packed != nil {
		return s.Packed.Steps
	}
	return len(s.Opinions)
}

func (s *AccumulativeModelState) appendSample(opinions []float64, counts []model.AgentNumberRecord, sums []model.AgentOpinionSumRecord) {
	if s.Packed != nil {
		if err := s.Packed.Append(opinions, counts, sums); err != nil {
			panic(err)
		}
		return
	}
	s.Opinions = append(s.Opinions, opinions)
	s.AgentNumbers = append(s.AgentNumbers, int32sToInt16s4(counts))
	s.AgentOpinionSums = append(s.AgentOpinionSums, agentOpinionSumsToFloat64s(sums))
}

func NewAccumulativeModelState() *AccumulativeModelState {
	return &AccumulativeModelState{
		Opinions:         make([][]float64, 0),
		AgentNumbers:     make([][][4]int16, 0),
		AgentOpinionSums: make([][][4]float64, 0),
	}
}

func int32sToInt16s4(src [][4]int) [][4]int16 {
	dst := make([][4]int16, len(src))
	for i, v := range src {
		dst[i][0] = int16(v[0])
		dst[i][1] = int16(v[1])
		dst[i][2] = int16(v[2])
		dst[i][3] = int16(v[3])
	}
	return dst
}

func agentOpinionSumsToFloat64s(src []model.AgentOpinionSumRecord) [][4]float64 {
	dst := make([][4]float64, len(src))
	for i, v := range src {
		dst[i] = v
	}
	return dst
}
