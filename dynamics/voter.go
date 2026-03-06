package dynamics

import (
	"math/rand"
	"smp/model"
)

// VoterParams are parameters for the Voter dynamics.
type VoterParams struct {
	RewiringRate float64
	RepostRate   float64
}

func DefaultVoterParams() *VoterParams {
	return &VoterParams{RewiringRate: 0.1, RepostRate: 0.3}
}

func (p *VoterParams) GetRepostRate() float64   { return p.RepostRate }
func (p *VoterParams) GetRewiringRate() float64 { return p.RewiringRate }

// Voter implements model.Dynamics[bool, VoterParams].
// At each step it picks one concordant opinion at random and moves toward it
// by Tolerance × opinion-difference. All statistics are still recorded.
type Voter struct {
	rndVals []float64
	stepIdx int
}

var _ model.Dynamics[bool, VoterParams] = (*Voter)(nil)
var _ model.PreStepDynamics = (*Voter)(nil)

// PrepareStep pre-generates n random floats for use in Step calls.
func (d *Voter) PrepareStep(n int) {
	if cap(d.rndVals) < n {
		d.rndVals = make([]float64, n)
	}
	d.rndVals = d.rndVals[:n]
	for i := range d.rndVals {
		d.rndVals[i] = rand.Float64()
	}
	d.stepIdx = 0
}

func (d *Voter) Concordant(myOp, otherOp bool, params *VoterParams) bool {
	return myOp == otherOp
}

func (d *Voter) Step(myOp bool, cN, cR, dN, dR []bool, params *VoterParams) (bool, model.AgentOpinionSumRecord) {
	var sumN, sumR, sumND, sumRD float64
	var opDiff float64
	if myOp {
		opDiff = -1.0
	} else {
		opDiff = 1.0
	}
	sumND = float64(len(dN)) * opDiff
	sumRD = float64(len(dR)) * opDiff

	var rnd float64
	if d.stepIdx < len(d.rndVals) {
		rnd = d.rndVals[d.stepIdx]
		d.stepIdx++
	} else {
		rnd = rand.Float64()
	}

	next := myOp
	if rnd < (sumND+sumRD)/(sumN+sumR+sumND+sumRD) {
		next = !myOp
	}
	return next, model.AgentOpinionSumRecord{sumN, sumR, sumND, sumRD}
}
