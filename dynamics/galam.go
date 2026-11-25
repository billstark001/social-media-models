package dynamics

import (
	"math/rand/v2"
	"smp/model"
)

// GalamParams are parameters for the Galam dynamics.
type GalamParams struct {
	Influence    float64
	RewiringRate float64
	RepostRate   float64
}

func DefaultGalamParams() *GalamParams {
	return &GalamParams{Influence: 1.0, RewiringRate: 0.1, RepostRate: 0.3}
}

func (p *GalamParams) GetRepostRate() float64   { return p.RepostRate }
func (p *GalamParams) GetRewiringRate() float64 { return p.RewiringRate }

// Galam implements model.Dynamics[bool, GalamParams].
// A positive summed discordant opinion difference permits a flip with
// probability Influence. All opinion-difference statistics are recorded.
type Galam struct{}

var _ model.Dynamics[bool, GalamParams] = (*Galam)(nil)

func (d *Galam) Concordant(myOp, otherOp bool, params *GalamParams) bool {
	return myOp == otherOp
}

func (d *Galam) Step(myOp bool, cN, cR, dN, dR []bool, params *GalamParams, rng *rand.Rand) (bool, model.AgentOpinionSumRecord) {
	var sumN, sumR, sumND, sumRD float64
	var opDiff float64
	if myOp {
		opDiff = -1.0
	} else {
		opDiff = 1.0
	}
	sumND = float64(len(dN)) * opDiff
	sumRD = float64(len(dR)) * opDiff

	next := myOp
	if sumND+sumRD > sumN+sumR {
		if rng.Float64() < params.Influence {
			next = !myOp
		}
	}
	return next, model.AgentOpinionSumRecord{sumN, sumR, sumND, sumRD}
}
