package simulation

import (
	model "smp/model"
)

type AccumulativeModelState struct {
	// (step, agent)
	Opinions [][]float32
	// (step, agent, type)
	AgentNumbers [][][4]int16
	// (step, agent, type)
	AgentOpinionSums [][][4]float32

	UnsafePostEvent int
}

func NewAccumulativeModelState() *AccumulativeModelState {
	return &AccumulativeModelState{
		Opinions:         make([][]float32, 0),
		AgentNumbers:     make([][][4]int16, 0),
		AgentOpinionSums: make([][][4]float32, 0),
	}
}

func float64sToFloat32s(src []float64) []float32 {
	dst := make([]float32, len(src))
	for i, v := range src {
		dst[i] = float32(v)
	}
	return dst
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

func float64sToFloat32s4(src []model.AgentOpinionSumRecord) [][4]float32 {
	dst := make([][4]float32, len(src))
	for i, v := range src {
		dst[i][0] = float32(v[0])
		dst[i][1] = float32(v[1])
		dst[i][2] = float32(v[2])
		dst[i][3] = float32(v[3])
	}
	return dst
}
