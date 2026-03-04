package dynamics

import (
	"math"
	"math/rand"
	"smp/model"
)

// DeffuantParams are parameters for the Deffuant dynamics.
type DeffuantParams struct {
	Tolerance    float64
	RewiringRate float64
	RepostRate   float64
}

func DefaultDeffuantParams() *DeffuantParams {
	return &DeffuantParams{Tolerance: 0.25, RewiringRate: 0.1, RepostRate: 0.3}
}

func (p *DeffuantParams) GetRepostRate() float64   { return p.RepostRate }
func (p *DeffuantParams) GetRewiringRate() float64 { return p.RewiringRate }

// Deffuant implements model.Dynamics[float64, DeffuantParams].
// At each step it picks one concordant opinion at random and moves toward it
// by Tolerance × opinion-difference. All statistics are still recorded.
type Deffuant struct{}

var _ model.Dynamics[float64, DeffuantParams] = (*Deffuant)(nil)

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
	all := make([]float64, 0, len(cN)+len(cR))
	all = append(all, cN...)
	all = append(all, cR...)
	if len(all) > 0 {
		picked := all[rand.Intn(len(all))]
		next = myOp + params.Tolerance*(picked-myOp)
	}
	return next, model.AgentOpinionSumRecord{sumN, sumR, sumND, sumRD}
}
