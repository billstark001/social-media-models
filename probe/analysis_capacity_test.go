package probe

import (
	"reflect"
	"testing"

	"smp/record"
)

type countingStaticReader struct{ observed int }

func (r *countingStaticReader) ReadStates(steps []int, _ record.Requirements, yield func(record.State) error) error {
	for _, step := range steps {
		r.observed++
		if err := yield(record.State{Step: step, Opinions: []float64{0}}); err != nil {
			return err
		}
	}
	return nil
}

func TestThinnedScanAllocatesOnlyRetainedSnapshots(t *testing.T) {
	steps := make([]int, 10002)
	for i := range steps {
		steps[i] = i
	}
	run := &countingStaticReader{}
	request := &Request{Scan: &ScanOptions{OutputStride: 1000}}
	got, err := measureStaticRun(request, map[string]bool{"terminal": true}, nil,
		driftParams{tolerance: .45}, run, steps)
	if err != nil {
		t.Fatal(err)
	}
	wantSteps := []int{0, 1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000, 9000, 10000, 10001}
	gotSteps := make([]int, len(got))
	for i, result := range got {
		gotSteps[i] = result.Step
	}
	if run.observed != len(steps) || !reflect.DeepEqual(gotSteps, wantSteps) {
		t.Fatalf("thinning changed observation coverage or last frame: observed=%d steps=%v", run.observed, gotSteps)
	}
	if cap(got) > 2*len(got) {
		t.Fatalf("thinned output retains a native-size snapshot buffer: len=%d cap=%d", len(got), cap(got))
	}
}
