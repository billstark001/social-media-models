package recsys

import (
	"math/rand/v2"
	"testing"
)

func TestForEachRandomIndexIsPermutation(t *testing.T) {
	for seed := uint64(0); seed < 20; seed++ {
		seen := make(map[int]bool)
		forEachRandomIndex(100, rand.New(rand.NewPCG(seed, seed+1)), func(index int) bool {
			if index < 0 || index >= 100 {
				t.Fatalf("out-of-range index %d", index)
			}
			if seen[index] {
				t.Fatalf("duplicate index %d for seed %d", index, seed)
			}
			seen[index] = true
			return true
		})
		if len(seen) != 100 {
			t.Fatalf("seed %d visited %d indexes, want 100", seed, len(seen))
		}
	}
}

func TestSampleWithoutReplacementSkipsZeroProbability(t *testing.T) {
	population := []int{0, 1, 2, 3}
	probabilities := []float64{0, 1, 0, 2}
	result := sampleWithoutReplacement(
		population,
		4,
		probabilities,
		rand.New(rand.NewPCG(1, 2)),
	)
	if len(result) != 2 {
		t.Fatalf("got %d positive-probability results, want 2: %v", len(result), result)
	}
	for _, value := range result {
		if value != 1 && value != 3 {
			t.Fatalf("selected zero-probability value %d", value)
		}
	}
}
