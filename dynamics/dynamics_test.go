package dynamics_test

import (
	"math"
	"math/rand"
	"testing"

	"smp/dynamics"
	"smp/model"
)

// ---- HK tests ----

func TestHKConcordant(t *testing.T) {
	hk := &dynamics.HK{}
	p := &dynamics.HKParams{Tolerance: 0.3}

	cases := []struct {
		a, b float64
		want bool
	}{
		{0.0, 0.2, true},
		{0.0, 0.3, true}, // exactly at boundary
		{0.0, 0.31, false},
		{0.5, -0.5, false},
		{-0.5, -0.4, true},
	}
	for _, c := range cases {
		got := hk.Concordant(c.a, c.b, p)
		if got != c.want {
			t.Errorf("Concordant(%v, %v, tol=%v) = %v, want %v", c.a, c.b, p.Tolerance, got, c.want)
		}
	}
}

func TestHKStepNoNeighbors(t *testing.T) {
	hk := &dynamics.HK{}
	p := &dynamics.HKParams{Tolerance: 0.3, Influence: 1.0}

	next, opSum := hk.Step(0.5, nil, nil, nil, nil, p)
	if next != 0.5 {
		t.Errorf("expected opinion unchanged (0.5), got %v", next)
	}
	if opSum != (model.AgentOpinionSumRecord{}) {
		t.Errorf("expected zero opSum, got %v", opSum)
	}
}

func TestHKStepConcordantNeighbors(t *testing.T) {
	hk := &dynamics.HK{}
	p := &dynamics.HKParams{Tolerance: 0.5, Influence: 1.0}

	// myOp=0, concordantNeighbors=[0.2, 0.4], decay=1
	// mean delta = ((0.2-0) + (0.4-0)) / 2 = 0.3
	// nextOp = 0 + 0.3 * 1.0 = 0.3
	next, opSum := hk.Step(0.0, []float64{0.2, 0.4}, nil, nil, nil, p)
	want := 0.3
	if math.Abs(next-want) > 1e-9 {
		t.Errorf("Step opinion: got %v, want %v", next, want)
	}
	wantSumN := 0.2 + 0.4
	if math.Abs(opSum[0]-wantSumN) > 1e-9 {
		t.Errorf("opSum[0] (cN sum): got %v, want %v", opSum[0], wantSumN)
	}
	if opSum[1] != 0 || opSum[2] != 0 || opSum[3] != 0 {
		t.Errorf("unexpected non-zero opSum entries: %v", opSum)
	}
}

func TestHKStepInfluenceLessThanOne(t *testing.T) {
	hk := &dynamics.HK{}
	p := &dynamics.HKParams{Tolerance: 0.5, Influence: 0.5}

	// myOp=0.0, concordant=[0.4], decay=0.5 → next = 0 + 0.4 * 0.5 = 0.2
	next, _ := hk.Step(0.0, []float64{0.4}, nil, nil, nil, p)
	if math.Abs(next-0.2) > 1e-9 {
		t.Errorf("got %v, want 0.2", next)
	}
}

func TestHKStepMixedConcordantRecommended(t *testing.T) {
	hk := &dynamics.HK{}
	p := &dynamics.HKParams{Tolerance: 0.5, Influence: 1.0}

	// cN=[0.2], cR=[0.4], dN=[0.8], dR=[-0.9]
	// mean delta from concordant = ((0.2-0) + (0.4-0)) / 2 = 0.3
	// discordant sums should be present
	next, opSum := hk.Step(0.0, []float64{0.2}, []float64{0.4}, []float64{0.8}, []float64{-0.9}, p)
	if math.Abs(next-0.3) > 1e-9 {
		t.Errorf("next opinion: got %v, want 0.3", next)
	}
	if math.Abs(opSum[2]-0.8) > 1e-9 {
		t.Errorf("opSum[2] (dN sum): got %v, want 0.8", opSum[2])
	}
	if math.Abs(opSum[3]-(-0.9)) > 1e-9 {
		t.Errorf("opSum[3] (dR sum): got %v, want -0.9", opSum[3])
	}
}

func TestHKDefaultParams(t *testing.T) {
	p := dynamics.DefaultHKParams()
	if p.Tolerance <= 0 || p.Influence <= 0 || p.RepostRate <= 0 {
		t.Errorf("default HKParams should have positive fields: %+v", p)
	}
	if p.GetRepostRate() != p.RepostRate {
		t.Error("GetRepostRate mismatch")
	}
	if p.GetRewiringRate() != p.RewiringRate {
		t.Error("GetRewiringRate mismatch")
	}
}

// ---- Deffuant tests ----

func TestDeffuantConcordant(t *testing.T) {
	d := &dynamics.Deffuant{}
	p := &dynamics.DeffuantParams{Tolerance: 0.25}

	if !d.Concordant(0.0, 0.25, p) {
		t.Error("expected concordant at boundary")
	}
	if d.Concordant(0.0, 0.26, p) {
		t.Error("expected discordant beyond boundary")
	}
}

func TestDeffuantStepNoNeighbors(t *testing.T) {
	d := &dynamics.Deffuant{}
	p := &dynamics.DeffuantParams{Tolerance: 0.25}

	next, opSum := d.Step(0.5, nil, nil, nil, nil, p)
	if next != 0.5 {
		t.Errorf("no neighbors: opinion should be unchanged, got %v", next)
	}
	if opSum != (model.AgentOpinionSumRecord{}) {
		t.Errorf("expected zero opSum, got %v", opSum)
	}
}

func TestDeffuantStepMoveToward(t *testing.T) {
	// With a single concordant neighbor, opinion must move toward it.
	d := &dynamics.Deffuant{}
	p := &dynamics.DeffuantParams{Tolerance: 0.5}

	myOp := 0.0
	neighborOp := 0.4
	// next = 0 + 0.5 * (0.4 - 0) = 0.2
	next, _ := d.Step(myOp, []float64{neighborOp}, nil, nil, nil, p)
	want := myOp + p.Tolerance*(neighborOp-myOp)
	if math.Abs(next-want) > 1e-9 {
		t.Errorf("got %v, want %v", next, want)
	}
}

func TestDeffuantStepRandomChoice(t *testing.T) {
	// With multiple concordant neighbors, the step should pick exactly one
	// and move toward it by Tolerance * delta.
	d := &dynamics.Deffuant{}
	p := &dynamics.DeffuantParams{Tolerance: 0.3}

	rng := rand.New(rand.NewSource(42))
	_ = rng // not injected, but we can verify outcome is within expected range

	cN := []float64{0.1, 0.2, 0.3}
	myOp := 0.0
	next, _ := d.Step(myOp, cN, nil, nil, nil, p)

	// next must equal myOp + 0.3*(picked - myOp) for some picked in cN
	valid := false
	for _, o := range cN {
		expected := myOp + p.Tolerance*(o-myOp)
		if math.Abs(next-expected) < 1e-9 {
			valid = true
			break
		}
	}
	if !valid {
		t.Errorf("Deffuant step result %v doesn't match any concordant neighbor", next)
	}
}

func TestDeffuantStepStatsRecorded(t *testing.T) {
	// Verify opinion sums are computed even when no concordant neighbor exists.
	d := &dynamics.Deffuant{}
	p := &dynamics.DeffuantParams{Tolerance: 0.1}

	// myOp=0, no concordant, dN=[0.5], dR=[-0.6]
	next, opSum := d.Step(0.0, nil, nil, []float64{0.5}, []float64{-0.6}, p)
	if next != 0.0 {
		t.Errorf("opinion should not change without concordant neighbors; got %v", next)
	}
	if math.Abs(opSum[2]-0.5) > 1e-9 {
		t.Errorf("opSum[2] should be 0.5, got %v", opSum[2])
	}
	if math.Abs(opSum[3]-(-0.6)) > 1e-9 {
		t.Errorf("opSum[3] should be -0.6, got %v", opSum[3])
	}
}

func TestDeffuantDefaultParams(t *testing.T) {
	p := dynamics.DefaultDeffuantParams()
	if p.Tolerance <= 0 || p.RepostRate <= 0 {
		t.Errorf("default DeffuantParams should have positive fields: %+v", p)
	}
	if p.GetRepostRate() != p.RepostRate {
		t.Error("GetRepostRate mismatch")
	}
}

// ---- Interface compile-time checks ----

func TestInterfaceCompliance(t *testing.T) {
	var _ model.Dynamics[float64, dynamics.HKParams] = (*dynamics.HK)(nil)
	var _ model.Dynamics[float64, dynamics.DeffuantParams] = (*dynamics.Deffuant)(nil)
	var _ model.AgentBehaviorParams = (*dynamics.HKParams)(nil)
	var _ model.AgentBehaviorParams = (*dynamics.DeffuantParams)(nil)
}
