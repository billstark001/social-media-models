package recsys

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

func TestTieSamplerScratchLifecycleDoesNotChangeDraws(t *testing.T) {
	leftSource := rand.NewPCG(71, 113)
	rightSource := rand.NewPCG(71, 113)
	left, right := rand.New(leftSource), rand.New(rightSource)
	var reused tieSampler
	// The warmed scratch must have no influence on a subsequent query.
	reused.reset(31)
	warm := rand.New(rand.NewPCG(8, 9))
	for range 13 {
		reused.draw(warm)
	}
	sizes := []int{1, 7, 2, 19, 3, 31, 1, 4}
	for query := range 256 {
		n := sizes[query%len(sizes)]
		if query%17 == 0 {
			reused.epoch = ^uint32(0)
		}
		reused.reset(n)
		var fresh tieSampler
		fresh.reset(n)
		seen := make([]bool, n)
		for range n {
			got, want := reused.draw(left), fresh.draw(right)
			if got != want {
				t.Fatalf("query %d: warmed/restored scratch changed draw %d to %d", query, want, got)
			}
			if got < 0 || got >= n || seen[got] {
				t.Fatalf("query %d: invalid permutation member %d", query, got)
			}
			seen[got] = true
		}
		leftState, _ := leftSource.MarshalBinary()
		rightState, _ := rightSource.MarshalBinary()
		if !bytes.Equal(leftState, rightState) {
			t.Fatalf("query %d: scratch lifecycle changed RNG consumption", query)
		}
	}
}

func TestTieSamplerSingletonDoesNotConsumeRNG(t *testing.T) {
	source := rand.NewPCG(71, 113)
	rng := rand.New(source)
	before, _ := source.MarshalBinary()
	var sampler tieSampler
	sampler.reset(1)
	if got := sampler.draw(rng); got != 0 {
		t.Fatalf("singleton draw = %d", got)
	}
	after, _ := source.MarshalBinary()
	if !bytes.Equal(before, after) {
		t.Fatal("singleton consumed RNG")
	}
}

func TestTieSamplerHasNoPreferredPosition(t *testing.T) {
	const n, trials = 7, 8192
	counts := [n][n]int{}
	rng := rand.New(rand.NewPCG(812, 371))
	var sampler tieSampler
	for range trials {
		sampler.reset(n)
		for position := range n {
			counts[position][sampler.draw(rng)]++
		}
	}
	// The fixed-seed fixture permits a generous >4-standard-deviation band;
	// every original candidate must have uniform mass at every output position.
	for position := range n {
		for candidate, count := range counts[position] {
			if delta := count - trials/n; delta < -160 || delta > 160 {
				t.Fatalf("position %d candidate %d occurs %d/%d times", position, candidate, count, trials)
			}
		}
	}
}
