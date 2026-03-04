package dynamics

import (
	"math"
	"smp/model"
)

// HKParams are parameters for the Hegselmann-Krause dynamics.
type HKParams struct {
	Tolerance    float64
	Decay        float64
	RewiringRate float64
	RepostRate   float64
}

func DefaultHKParams() *HKParams {
	return &HKParams{Tolerance: 0.25, Decay: 1.0, RewiringRate: 0.1, RepostRate: 0.3}
}

func (p *HKParams) GetRepostRate() float64   { return p.RepostRate }
func (p *HKParams) GetRewiringRate() float64 { return p.RewiringRate }

// HK implements model.Dynamics[float64, HKParams].
type HK struct{}

// compile-time interface check
var _ model.Dynamics[float64, HKParams] = (*HK)(nil)

func (h *HK) Concordant(myOp, otherOp float64, params *HKParams) bool {
	return math.Abs(myOp-otherOp) <= params.Tolerance
}

func (h *HK) Step(myOp float64, cN, cR, dN, dR []float64, params *HKParams) (float64, model.AgentOpinionSumRecord) {
	total := len(cN) + len(cR)
	var sumN, sumR, sumND, sumRD float64
	for _, o := range cN {
		sumN += o - myOp
	}
	for _, o := range cR {
		sumR += o - myOp
	}
	for _, o := range dN {
		sumND += o - myOp
	}
	for _, o := range dR {
		sumRD += o - myOp
	}
	next := myOp
	if total > 0 {
		next = myOp + (sumN+sumR)/float64(total)*params.Decay
	}
	return next, model.AgentOpinionSumRecord{sumN, sumR, sumND, sumRD}
}
