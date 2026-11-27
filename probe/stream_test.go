package probe

import (
	"math"
	"strings"
	"testing"

	"smp/record"
	"smp/terminal"
)

type staticSourceFunc func([]int, record.Requirements, func(record.State) error) error

func (f staticSourceFunc) ReadStates(steps []int, requirements record.Requirements, yield func(record.State) error) error {
	return f(steps, requirements, yield)
}

func TestStaticRunMeasuresBeforeAdvancingSourceWithoutRetainingStates(t *testing.T) {
	request := &Request{Terminal: &terminal.Options{Epsilon: .1, MajorMass: .02}}
	selected := map[string]bool{"terminal": true}
	opinions := []float64{0, 0}
	source := staticSourceFunc(func(steps []int, requirements record.Requirements, yield func(record.State) error) error {
		if requirements.Graph || requirements.Posts {
			t.Fatal("terminal-only stream requested unused microscopic channels")
		}
		for _, step := range steps {
			if step == 1 {
				opinions[0], opinions[1] = -1, 1
			}
			if err := yield(record.State{Step: step, Opinions: opinions, Precision: "history_f64"}); err != nil {
				return err
			}
			if len(request.States) != 0 {
				t.Fatal("stream retained a frozen state on the request")
			}
		}
		// A reader may release its working state when the stream finishes.
		opinions[0], opinions[1] = math.NaN(), math.NaN()
		return nil
	})
	results, err := measureStaticRun(request, selected, nil, driftParams{}, source, []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Terminal.KMajor != 1 || results[1].Terminal.KMajor != 2 {
		t.Fatalf("states were materialized before measurement or results alias source storage: %+v", results)
	}
	if results[0].Precision != "history_f64" || results[1].Step != 1 || len(request.States) != 0 {
		t.Fatal("stream lost state identity or retained microscopic input")
	}
}

func TestStaticRunStopsAtMeasurementFailureWithoutReturningPartialResults(t *testing.T) {
	request := &Request{Terminal: &terminal.Options{Epsilon: .1, MajorMass: .02}}
	called := 0
	source := staticSourceFunc(func(steps []int, requirements record.Requirements, yield func(record.State) error) error {
		for _, step := range steps {
			called++
			if err := yield(record.State{Step: step, Opinions: []float64{math.NaN()}}); err != nil {
				return err
			}
		}
		return nil
	})
	results, err := measureStaticRun(request, map[string]bool{"terminal": true}, nil, driftParams{}, source, []int{0, 1, 2})
	if err == nil || !strings.Contains(err.Error(), "finite") || called != 1 || results != nil || len(request.States) != 0 {
		t.Fatalf("measurement was deferred until after reading later states: calls=%d results=%v error=%v", called, results, err)
	}
}
