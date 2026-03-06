package dynamics

import (
	"math"
	"math/rand/v2"
	"smp/model"
)

// DeffuantParams are parameters for the Deffuant dynamics.
type DeffuantParams struct {
	Tolerance    float64
	Influence    float64
	RewiringRate float64
	RepostRate   float64
}

func DefaultDeffuantParams() *DeffuantParams {
	return &DeffuantParams{Tolerance: 0.25, Influence: 1.0, RewiringRate: 0.1, RepostRate: 0.3}
}

func (p *DeffuantParams) GetRepostRate() float64   { return p.RepostRate }
func (p *DeffuantParams) GetRewiringRate() float64 { return p.RewiringRate }

// Deffuant implements model.Dynamics[float64, DeffuantParams].
// At each step it picks one concordant opinion at random and moves toward it
// by Influence × opinion-difference. All statistics are still recorded.
type Deffuant struct {
	rndVals []float64
	stepIdx int
}

var _ model.Dynamics[float64, DeffuantParams] = (*Deffuant)(nil)
var _ model.PreStepDynamics = (*Deffuant)(nil)

// PrepareStep pre-generates n random floats for use in Step calls.
func (d *Deffuant) PrepareStep(n int) {
	if cap(d.rndVals) < n {
		d.rndVals = make([]float64, n)
	}
	d.rndVals = d.rndVals[:n]
	for i := range d.rndVals {
		d.rndVals[i] = rand.Float64()
	}
	d.stepIdx = 0
}

func (d *Deffuant) Concordant(myOp, otherOp float64, params *DeffuantParams) bool {
	return math.Abs(myOp-otherOp) <= params.Tolerance
}

func (d *Deffuant) Step(myOp float64, cN, cR, dN, dR []float64, params *DeffuantParams) (float64, model.AgentOpinionSumRecord) {
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
	total := len(cN) + len(cR)
	if total > 0 {
		var rnd float64
		if d.stepIdx < len(d.rndVals) {
			rnd = d.rndVals[d.stepIdx]
			d.stepIdx++
		} else {
			rnd = rand.Float64()
		}
		idx := int(rnd * float64(total))
		var picked float64
		if idx < len(cN) {
			picked = cN[idx]
		} else {
			picked = cR[idx-len(cN)]
		}
		next = myOp + params.Influence*(picked-myOp)
	}
	return next, model.AgentOpinionSumRecord{sumN, sumR, sumND, sumRD}
}
