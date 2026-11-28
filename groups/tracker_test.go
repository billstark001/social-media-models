package groups

import (
	"math"
	"reflect"
	"testing"
)

func ids(a, b int) []int {
	r := make([]int, b-a)
	for i := range r {
		r[i] = a + i
	}
	return r
}
func detection(members ...[]int) Detection {
	d := Detection{Cores: make([]Core, len(members)), MacroK: len(members)}
	for i, m := range members {
		d.Cores[i] = Core{ID: i, Members: append([]int(nil), m...), Mass: len(m), Peak: float64(i), Lower: -1, Upper: 1}
	}
	return d
}
func events(frame Frame, kind string) []Event {
	r := []Event{}
	for _, e := range frame.Events {
		if e.Type == kind {
			r = append(r, e)
		}
	}
	return r
}
func tracker(t *testing.T) *Tracker {
	t.Helper()
	r, err := NewTracker(DefaultOptions(.45))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInitialBirthUnobservedRecoveryFadeAndRightCensor(t *testing.T) {
	r := tracker(t)
	a, b := ids(0, 50), ids(50, 100)
	for step := 0; step < 2; step++ {
		if f := r.observeDetection(step, detection(a)); len(f.Groups) != 0 {
			t.Fatal("unconfirmed group exposed")
		}
	}
	f := r.observeDetection(2, detection(a))
	initial := events(f, "initial_present")
	if len(initial) != 1 || !initial[0].LeftCensored || f.Groups[0].ID != 1 || f.Groups[0].State != "observed" {
		t.Fatalf("bad initial qualification: %+v", f)
	}
	r.observeDetection(3, detection(a, b))
	r.observeDetection(4, detection(a, b))
	f = r.observeDetection(5, detection(a, b))
	birth := events(f, "birth")
	if len(birth) != 1 || birth[0].LeftCensored || birth[0].Step != 5 || birth[0].IntervalStart != 2 || birth[0].IntervalEnd != 3 {
		t.Fatalf("confirmation and first observation clocks mixed: %+v", birth)
	}
	f = r.observeDetection(6, detection(a))
	if len(f.Groups) != 2 || f.Groups[1].State != "unobserved" || len(events(f, "fade")) != 0 {
		t.Fatal("one missing detection became death/fade")
	}
	f = r.observeDetection(7, detection(a, b))
	if f.Groups[1].ID != 2 || len(f.Events) != 0 {
		t.Fatal("temporary invisibility invented a new identity")
	}
	r.observeDetection(8, detection(a))
	r.observeDetection(9, detection(a))
	f = r.observeDetection(10, detection(a))
	if len(events(f, "fade")) != 1 || len(f.Groups) != 1 || events(f, "fade")[0].IntervalStart != 7 || events(f, "fade")[0].IntervalEnd != 8 {
		t.Fatalf("wrong delayed qualification fade: %+v", f)
	}
	closed, err := r.FinalClose(10, "analysis_horizon")
	if err != nil {
		t.Fatal(err)
	}
	if len(closed.Events) != 1 || closed.Events[0].Type != "censored" || !closed.Events[0].RightCensored || closed.Events[0].Reason != "analysis_horizon" {
		t.Fatal("close invented a physical loss")
	}
	if _, err := r.FinalClose(10); err == nil {
		t.Fatal("duplicate closing accepted")
	}
}

func TestMergeAndSplitHaveExplicitNewChildLineage(t *testing.T) {
	r := tracker(t)
	a, b, all := ids(0, 50), ids(50, 100), ids(0, 100)
	for step := 0; step < 3; step++ {
		r.observeDetection(step, detection(a, b))
	}
	for step := 3; step < 5; step++ {
		f := r.observeDetection(step, detection(all))
		for _, g := range f.Groups {
			if g.State != "unobserved" {
				t.Fatal("unconfirmed merge exposed as continuing observed parent")
			}
		}
	}
	f := r.observeDetection(5, detection(all))
	merge := events(f, "merge")
	if len(merge) != 1 || !reflect.DeepEqual(merge[0].Parents, []int64{1, 2}) || !reflect.DeepEqual(merge[0].Children, []int64{3}) || len(f.Groups) != 1 || f.Groups[0].ID != 3 {
		t.Fatalf("wrong merge lineage: %+v", f)
	}
	if len(events(f, "birth"))+len(events(f, "fade")) != 0 {
		t.Fatal("merge double-counted ordinary births/fades")
	}
	r.observeDetection(6, detection(a, b))
	r.observeDetection(7, detection(a, b))
	f = r.observeDetection(8, detection(a, b))
	split := events(f, "split")
	if len(split) != 1 || !reflect.DeepEqual(split[0].Parents, []int64{3}) || !reflect.DeepEqual(split[0].Children, []int64{4, 5}) {
		t.Fatalf("split recycled old identities or lost ancestry: %+v", f)
	}
}

func TestEachCandidateMustPassStabilityInsteadOfMeanJaccard(t *testing.T) {
	r := tracker(t)
	a, b, shift := ids(0, 100), ids(100, 200), ids(150, 250)
	r.observeDetection(0, detection(a, b))
	r.observeDetection(1, detection(a, shift))
	f := r.observeDetection(2, detection(a, shift))
	if len(f.Groups) != 1 || !reflect.DeepEqual(f.Groups[0].Core.Members, a) {
		t.Fatalf("stable core masked an unstable candidate: %+v", f.Groups)
	}
	// The average score is (1+1/3)/2 > .5, but the second core has only
	// two consecutive individually stable observed memberships at step 2.
}

func TestManyToManyReconfigurationIsNotOrdinaryBirthDeath(t *testing.T) {
	r := tracker(t)
	a, b := ids(0, 50), ids(50, 100)
	for step := 0; step < 3; step++ {
		r.observeDetection(step, detection(a, b))
	}
	c := append(ids(0, 25), ids(50, 75)...)
	d := append(ids(25, 50), ids(75, 100)...)
	r.observeDetection(3, detection(c, d))
	r.observeDetection(4, detection(c, d))
	f := r.observeDetection(5, detection(c, d))
	if len(events(f, "reconfiguration")) != 1 || len(events(f, "birth"))+len(events(f, "fade")) != 0 || len(f.Groups) != 2 {
		t.Fatalf("many-to-many ancestry lost: %+v", f)
	}
}

func TestSparseSamplesCanConfirmOnlyLeftCensoredPresence(t *testing.T) {
	r := tracker(t)
	a := ids(0, 50)
	r.observeDetection(0, detection(a))
	f := r.observeDetection(3, detection(a))
	if !f.Gap || len(events(f, "unknown_interval")) != 1 || len(f.Groups) != 0 {
		t.Fatal("gap treated as dense confirmation")
	}
	f = r.observeDetection(7, detection(a))
	initial := events(f, "initial_present")
	if !f.Gap || f.Epoch != 2 || len(f.Groups) != 1 || len(initial) != 1 || !initial[0].LeftCensored || len(events(f, "birth")) != 0 {
		t.Fatalf("sparse current groups unavailable or fictitious birth: %+v", f)
	}
	r.observeDetection(8, detection())
	f = r.observeDetection(10, detection())
	if len(f.Groups) != 1 || f.Groups[0].State != "unobserved" || len(events(f, "fade")) != 0 {
		t.Fatal("gap counted toward consecutive missing native samples")
	}
	f = r.observeDetection(11, detection(a))
	if f.Groups[0].ID != 1 {
		t.Fatal("unknown sampling interval recycled identity")
	}
}

func TestTrackerResultOwnershipAndOverlap(t *testing.T) {
	r := tracker(t)
	a := ids(0, 50)
	for step := 0; step < 2; step++ {
		r.observeDetection(step, detection(a))
	}
	f := r.observeDetection(2, detection(a))
	f.Groups[0].Core.Members[0] = 999
	f.Detection.Cores[0].Members[0] = 888
	f = r.observeDetection(3, detection(a))
	if f.Groups[0].ID != 1 {
		t.Fatal("returned state mutated persistent membership")
	}
	f.Detection.Cores[0].Members[0] = 777
	closed, err := r.FinalClose(3)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Detection.Cores[0].Members[0] != 0 {
		t.Fatal("closing result aliases earlier returned density detection")
	}
	jac, overlap := MemberOverlap(ids(0, 100), ids(0, 50))
	if jac != .5 || overlap != 1 {
		t.Fatal("Jaccard and containment lineage coefficients conflated")
	}
}

func TestTrackerValidationDoesNotAdvanceOnFailure(t *testing.T) {
	if _, err := NewTracker(Options{}); err == nil {
		t.Fatal("implicit hidden defaults accepted")
	}
	r := tracker(t)
	x := make([]float64, 100)
	if _, err := r.Observe(4, x); err != nil {
		t.Fatal(err)
	}
	for _, step := range []int{-1, 4, 3} {
		if _, err := r.Observe(step, x); err == nil {
			t.Fatal("non-increasing step accepted")
		}
	}
	x[0] = math.NaN()
	if _, err := r.Observe(5, x); err == nil {
		t.Fatal("nonfinite input accepted")
	}
	x[0] = 0
	if _, err := r.Observe(5, make([]float64, 99)); err == nil {
		t.Fatal("population axis silently changed")
	}
	if _, err := r.Observe(5, x); err != nil {
		t.Fatal("failed observations advanced tracker")
	}
	if _, err := r.FinalClose(4); err == nil {
		t.Fatal("close before last sample accepted")
	}
}

func BenchmarkTracker500(b *testing.B) {
	x := make([]float64, 500)
	for i := range x {
		if i < 250 {
			x[i] = -.6
		} else {
			x[i] = .6
		}
	}
	o := DefaultOptions(.45)
	r, err := NewTracker(o)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Observe(i, x); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	step := 3
	for b.Loop() {
		if _, err := r.Observe(step, x); err != nil {
			b.Fatal(err)
		}
		step++
	}
}
