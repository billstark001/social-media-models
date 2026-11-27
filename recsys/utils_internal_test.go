package recsys

import (
	"container/heap"
	"math"
	"math/rand/v2"
	"reflect"
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

type referenceWeightedHeap []weightedItem

func (h referenceWeightedHeap) Len() int           { return len(h) }
func (h referenceWeightedHeap) Less(i, j int) bool { return h[i].key < h[j].key }
func (h referenceWeightedHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *referenceWeightedHeap) Push(x any)        { *h = append(*h, x.(weightedItem)) }
func (h *referenceWeightedHeap) Pop() any {
	last := (*h)[len(*h)-1]
	*h = (*h)[:len(*h)-1]
	return last
}

// Keep the original interface-based heap as an oracle: equal keys must retain
// its ordering too, because candidate order controls later history draws.
func sampleWithReferenceHeap(population []int, n int, probabilities []float64, rng *rand.Rand) []int {
	if n <= 0 {
		return nil
	}
	selected := make(referenceWeightedHeap, 0, min(n, len(population)))
	for i, p := range probabilities {
		if p <= 0 || math.IsNaN(p) {
			continue
		}
		item := weightedItem{key: math.Log1p(-rng.Float64()) / p, index: i}
		if len(selected) < n {
			heap.Push(&selected, item)
		} else if item.key > selected[0].key {
			selected[0] = item
			heap.Fix(&selected, 0)
		}
	}
	result := make([]int, len(selected))
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = population[heap.Pop(&selected).(weightedItem).index]
	}
	return result
}

func TestWeightedSamplerMatchesReferenceHeapAndRNG(t *testing.T) {
	population := make([]int, 97)
	for i := range population {
		population[i] = 1000 + i*7
	}
	for seed := uint64(0); seed < 20; seed++ {
		for _, count := range []int{0, 1, 2, 14, 97, 100} {
			for _, ties := range []bool{false, true} {
				probabilities := make([]float64, len(population))
				for i := range probabilities {
					if ties {
						probabilities[i] = math.Inf(1) // every key is -0
					} else {
						probabilities[i] = float64((i*17)%23) / 23
					}
				}
				probabilities[1], probabilities[5], probabilities[11] = -1, math.NaN(), 0
				actualRNG := rand.New(rand.NewPCG(seed, seed+1))
				expectedRNG := rand.New(rand.NewPCG(seed, seed+1))
				actual := sampleWithoutReplacement(population, count, probabilities, actualRNG)
				expected := sampleWithReferenceHeap(population, count, probabilities, expectedRNG)
				if !reflect.DeepEqual(actual, expected) || actualRNG.Uint64() != expectedRNG.Uint64() {
					t.Fatalf("seed=%d count=%d ties=%t: got %v, want %v (or RNG state differs)", seed, count, ties, actual, expected)
				}
			}
		}
	}
}

type weightedSequenceSource struct {
	values []uint64
	draws  int
}

func (s *weightedSequenceSource) Uint64() uint64 {
	value := s.values[s.draws%len(s.values)]
	s.draws++
	return value
}

func TestWeightedSamplerExtremeKeysMatchReferenceHeap(t *testing.T) {
	// Force zero Float64 draws (-0 keys), subnormal weights (-Inf keys), and
	// mixed finite/infinite weights. Their ties must retain the old heap order.
	for _, weights := range [][]float64{
		{},
		{0, math.Copysign(0, -1), -1, math.Inf(-1), math.NaN()},
		{math.Inf(1), 1, math.SmallestNonzeroFloat64, math.Inf(1), 2, math.MaxFloat64},
		{math.SmallestNonzeroFloat64, math.SmallestNonzeroFloat64, 1e-310, 1e-300},
	} {
		population := make([]int, len(weights))
		for i := range population {
			population[i] = 2*i + 7
		}
		for count := -1; count <= len(weights)+1; count++ {
			actualSource := &weightedSequenceSource{values: []uint64{0, ^uint64(0), 1 << 63, 1, 1 << 62}}
			expectedSource := &weightedSequenceSource{values: actualSource.values}
			actual := sampleWithoutReplacement(population, count, weights, rand.New(actualSource))
			expected := sampleWithReferenceHeap(population, count, weights, rand.New(expectedSource))
			if !reflect.DeepEqual(actual, expected) || actualSource.draws != expectedSource.draws {
				t.Fatalf("weights=%v count=%d: got %v/%d draws, want %v/%d draws", weights, count, actual, actualSource.draws, expected, expectedSource.draws)
			}
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
