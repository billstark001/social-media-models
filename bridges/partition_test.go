package bridges

import "testing"

func TestSignalBothIsNotBilateral(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	o, _ := NewObserver(detailOptions(.7))
	frame, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	h := holderAt(t, frame, 4)
	if !h.SignalBoth.Active || h.Bilateral.Active || !frame.Union.Holders[4].SignalBoth.Active || frame.Union.Holders[4].Bilateral.Active {
		t.Fatalf("single-side O+P must be signal-both, not bilateral: %+v", h)
	}
	c := frame.Union.Counts
	if c.SignalBoth != 1 || c.OpinionOnly != 0 || c.PostOnly != 0 {
		t.Fatalf("bad exclusive global signal classes: %+v", c)
	}
}

func TestUnionSignalBothAcrossPairsDoesNotInventBilateral(t *testing.T) {
	h := UnionHolderState{Structural: known(false), Opinion: known(false), Post: known(false), Active: known(false), Bilateral: known(false), OpinionBilateral: known(false), PostBilateral: known(false), MixedBilateral: known(false)}
	one := HolderState{Active: known(true), Bilateral: known(false), OpinionBilateral: known(false), PostBilateral: known(false), MixedBilateral: known(false)}
	for s := 0; s < 2; s++ {
		one.S[s].Status = known(false)
		one.O[s].Status = known(false)
		one.P[s].Status = known(false)
	}
	one.O[0].Status = known(true)
	h.add(one)
	one.O[0].Status = known(false)
	one.P[1].Status = known(true)
	h.add(one)
	h.roles()
	if !h.SignalBoth.Known || !h.SignalBoth.Active || h.Bilateral.Active {
		t.Fatalf("union O/P vs pair bilateral conflated: %+v", h)
	}
}

func TestRiskExposurePartitionsKnownTime(t *testing.T) {
	x, g, p, f := bridgeFixture()
	o, _ := NewObserver(detailOptions(.7))
	if _, err := o.Measure(0, x, g, p, f); err != nil {
		t.Fatal(err)
	}
	connectBridge(&g, 0, 4)
	f.Step = 1
	if _, err := o.Measure(1, x, g, p, f); err != nil {
		t.Fatal(err)
	}
	delete(g.Adjacency[0], 4)
	f.Step = 2
	if _, err := o.Measure(2, x, g, p, f); err != nil {
		t.Fatal(err)
	}
	f.Step = 3
	if _, err := o.Measure(3, x, g, p, f); err != nil {
		t.Fatal(err)
	}
	close, err := o.FinalClose(3)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range close.Summaries {
		if s.KnownHolderRounds != s.ActiveHolderRounds+s.NeverSeenHolderRounds+s.SeenInactiveHolderRounds || s.ActiveHolderRounds != 1 || s.SeenInactiveHolderRounds != 1 || s.NeverSeenHolderRounds != 16 {
			t.Fatalf("risk exposure partition wrong: %+v", s)
		}
	}
}
