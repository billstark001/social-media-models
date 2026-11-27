package recsys

import (
	"reflect"
	"testing"

	smprng "smp/rng"
)

func TestOpinionRandomRecommendScratchDoesNotLeakBetweenCalls(t *testing.T) {
	m := structureWorkspaceModel(false, false)
	o := NewOpinionRandom(m, nil, .4, 4, .1, .3)
	m.Recsys = o
	o.PostInit(nil)
	o.PreStep()
	expectedPool := smprng.MustNewPool(smprng.FixedSpec(61, 97))
	state, err := m.RNG.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := expectedPool.Restore(state); err != nil {
		t.Fatal(err)
	}
	// Repeat anchors with different exclusions and requested counts. The old
	// implementation copied each matrix row into a freshly allocated vector.
	for call, anchor := range []int64{0, 1, 0, 7, 3, 1, 7, 0} {
		rates := append([]float64(nil), o.RateMat[anchor]...)
		if call == 2 {
			clear(rates)
			copy(o.RateMat[anchor], rates)
		}
		before := makeRawMat[float64](o.NumNodes, o.NumNodes)
		for row := range before {
			copy(before[row], o.RateMat[row])
		}
		excluded := map[int64]bool{anchor: true, int64(call % o.NumNodes): true}
		count := call % 5
		agent := o.Agents[anchor]
		want := o.recommendFromRates(agent, excluded, count, rates, expectedPool.Stream(smprng.StreamRecommendation))
		got := o.Recommend(agent, excluded, count)
		actualRNG, _ := m.RNG.Snapshot()
		expectedRNG, _ := expectedPool.Snapshot()
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(actualRNG, expectedRNG) {
			t.Fatalf("call=%d anchor=%d: reused buffer changed ordered posts or RNG", call, anchor)
		}
		if !reflect.DeepEqual(o.RateMat, before) {
			t.Fatalf("call=%d: recommendation mutated the shared rate matrix", call)
		}
	}
}
