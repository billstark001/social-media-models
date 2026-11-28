package groups

import (
	"math"
	"reflect"
	"testing"
)

func TestGapLineageConfirmationIsOnlyLeftCensoredPresence(t *testing.T) {
	for _, split := range []bool{false, true} {
		for _, priorSamples := range []int{1, 2} {
			r := tracker(t)
			a, b, all := ids(0, 50), ids(50, 100), ids(0, 100)
			initial, changed := detection(a, b), detection(all)
			parents, children := []int64{1, 2}, []int64{3}
			if split {
				initial, changed = changed, initial
				parents, children = []int64{1}, []int64{2, 3}
			}
			for step := 0; step < 3; step++ {
				r.observeDetection(step, initial)
			}
			for step := 3; step < 3+priorSamples; step++ {
				r.observeDetection(step, changed)
			}
			f := r.observeDetection(10, changed)
			if priorSamples == 1 {
				f = r.observeDetection(11, changed)
			}
			if len(events(f, "merge"))+len(events(f, "split"))+len(events(f, "reconfiguration"))+len(events(f, "birth")) != 0 {
				t.Fatalf("unknown interval produced an observed lineage event: %+v", f.Events)
			}
			present := events(f, "initial_present")
			if len(present) != len(children) {
				t.Fatalf("want one censored presence per confirmed child: %+v", f.Events)
			}
			for i, event := range present {
				if !event.LeftCensored || !reflect.DeepEqual(event.Parents, parents) || !reflect.DeepEqual(event.GroupIDs, []int64{children[i]}) {
					t.Fatalf("lost association evidence or left censoring: %+v", event)
				}
			}
		}
	}
}

func TestFinalCloseDoesNotExtrapolateLastObservation(t *testing.T) {
	r := tracker(t)
	for step := 0; step < 3; step++ {
		r.observeDetection(step, detection(ids(0, 50)))
	}
	f, err := r.FinalClose(100, "analysis_horizon")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Events) != 1 || f.Events[0].IntervalEnd != 2 || f.Events[0].Step != 100 || !f.Events[0].RightCensored || !f.Gap {
		t.Fatalf("closure extrapolated qualification into unsampled time: %+v", f)
	}
}

func TestTinyBandwidthKeepsFiniteNormalizedPointMassKernel(t *testing.T) {
	for _, bandwidth := range []float64{1e-200, math.SmallestNonzeroFloat64} {
		o := DefaultOptions(.45)
		o.Bandwidth = bandwidth
		x := make([]float64, 100)
		for i := range x {
			if i < 50 {
				x[i] = -.6
			} else {
				x[i] = .6
			}
		}
		var detector Detector
		d, err := detector.Detect(x, o)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Cores) != 2 || len(d.Residual) != 0 {
			t.Fatalf("valid narrow bandwidth lost the point masses: %+v", d)
		}
		for _, density := range detector.density {
			sum := 0.0
			for _, v := range density {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatal("narrow kernel created non-finite density")
				}
				sum += v * 2 / float64(o.GridCells)
			}
			if math.Abs(sum-1) > 1e-12 {
				t.Fatalf("kernel lost mass: %.17g", sum)
			}
		}
	}
}

func TestExtremeBandwidthRequestsFailBeforeCacheAllocation(t *testing.T) {
	x := make([]float64, 50)
	var detector Detector
	baseline, err := detector.Detect(x, DefaultOptions(.45))
	if err != nil {
		t.Fatal(err)
	}
	for _, bandwidth := range []float64{1e308, 1.7e308, math.MaxFloat64} {
		o := DefaultOptions(.45)
		o.Bandwidth = bandwidth
		if _, err := detector.Detect(x, o); err == nil {
			t.Fatalf("unbounded smoothing allocation accepted: %g", bandwidth)
		}
	}
	for _, epsilon := range []float64{math.SmallestNonzeroFloat64, 1e308, 1.7e308, math.MaxFloat64} {
		if _, err := NewTracker(DefaultOptions(epsilon)); err == nil {
			t.Fatalf("unsupported derived bandwidth accepted for epsilon %g", epsilon)
		}
	}
	again, err := detector.Detect(x, DefaultOptions(.45))
	if err != nil || !reflect.DeepEqual(baseline, again) {
		t.Fatalf("invalid cold requests changed cached detection: %v", err)
	}
}

func TestPendingAndActivePrivateDetectionSharingPreservesOwnership(t *testing.T) {
	r := tracker(t)
	a := ids(0, 50)
	for step := 0; step < 3; step++ {
		f := r.observeDetection(step, detection(a))
		f.Detection.Cores[0].Members[0] = 999
		if len(f.Groups) > 0 {
			f.Groups[0].Core.Members[0] = 888
		}
	}
	f := r.observeDetection(3, detection(a))
	if len(f.Groups) != 1 || f.Groups[0].ID != 1 || f.Groups[0].FirstObservedStep != 0 {
		t.Fatal("returned buffers changed candidate confirmation or active identity")
	}
	f.Detection.Cores[0].Members[0] = 777
	closed, err := r.FinalClose(3)
	if err != nil || closed.Detection.Cores[0].Members[0] != 0 || closed.Groups[0].Core.Members[0] != 0 {
		t.Fatalf("private core sharing escaped through closing output: %+v %v", closed, err)
	}
}

func TestTinyHalfLevelCannotGiveOneAgentToTwoCores(t *testing.T) {
	o := DefaultOptions(.45)
	o.Half = 1e-300
	x := make([]float64, 101)
	for i := 0; i < 100; i++ {
		if i < 50 {
			x[i] = -.2
		} else {
			x[i] = .2
		}
	}
	var detector Detector
	d, err := detector.Detect(x, o)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[int]bool)
	for _, core := range d.Cores {
		for _, member := range core.Members {
			if seen[member] {
				t.Fatalf("rounded half-prominence level overlaps cores at agent %d", member)
			}
			seen[member] = true
		}
	}
}

func TestHalfLevelRoundingPreservesMembershipPartitionAcrossGrids(t *testing.T) {
	x := make([]float64, 101)
	for i := 0; i < 100; i++ {
		if i < 50 {
			x[i] = -.2
		} else {
			x[i] = .2
		}
	}
	var detector Detector
	for _, grid := range []int{4, 64, 1024} {
		for _, half := range []float64{math.SmallestNonzeroFloat64, 1e-300, .01, .5, math.Nextafter(1, 0)} {
			for _, bandwidth := range []float64{.005, .075, .5} {
				o := DefaultOptions(.45)
				o.GridCells, o.Half, o.Bandwidth = grid, half, bandwidth
				d, err := detector.Detect(x, o)
				if err != nil {
					t.Fatal(err)
				}
				seen := make([]bool, len(x))
				for _, core := range d.Cores {
					if core.Mass != len(core.Members) || !isFiniteAudit(core.DensityLevel) {
						t.Fatalf("invalid core mass/level: %+v", core)
					}
					for _, member := range core.Members {
						if seen[member] || d.Labels[member] != core.ID {
							t.Fatalf("membership partition failed: grid=%d half=%g bandwidth=%g", grid, half, bandwidth)
						}
						seen[member] = true
					}
				}
				for _, member := range d.Residual {
					if seen[member] || d.Labels[member] != -1 {
						t.Fatal("residual overlaps a core")
					}
					seen[member] = true
				}
				for _, present := range seen {
					if !present {
						t.Fatal("an opinion disappeared from membership/residual")
					}
				}
			}
		}
	}
}

func isFiniteAudit(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func TestGapEndpointStartsANewDenseMissingRun(t *testing.T) {
	r := tracker(t)
	for step := 0; step < 3; step++ {
		r.observeDetection(step, detection(ids(0, 50)))
	}
	r.observeDetection(3, detection())
	for _, step := range []int{7, 8} {
		if f := r.observeDetection(step, detection()); len(events(f, "fade")) != 0 {
			t.Fatal("pre-gap missing sample leaked into the new dense exit count")
		}
	}
	f := r.observeDetection(9, detection())
	if len(events(f, "fade")) != 1 || events(f, "fade")[0].IntervalEnd != 7 {
		t.Fatalf("three consecutive observed absences at 7,8,9 did not confirm exit: %+v", f)
	}
}

func TestRepeatedSparseMissingSamplesDoNotAccumulateExitEvidence(t *testing.T) {
	r := tracker(t)
	a := ids(0, 50)
	for step := 0; step < 3; step++ {
		r.observeDetection(step, detection(a))
	}
	for _, step := range []int{6, 8, 10, 12} {
		f := r.observeDetection(step, detection())
		if !f.Gap || len(events(f, "fade")) != 0 || len(f.Groups) != 1 || f.Groups[0].State != "unobserved" {
			t.Fatalf("sparse absences were counted as native consecutive lifetime: %+v", f)
		}
	}
	f := r.observeDetection(15, detection(a))
	if len(f.Groups) != 1 || f.Groups[0].ID != 1 || f.Groups[0].State != "observed" {
		t.Fatal("recovery lost the censored membership association")
	}
}
