package recsys

import "math/rand/v2"

// tieSampler visits a uniform permutation lazily. Epoch marks give a fresh
// virtual identity array without clearing or copying the entire tie group.
// It is query scratch, never a persistent ranking or part of a checkpoint.
type tieSampler struct {
	positions []int
	marks     []uint32
	epoch     uint32
	remaining int
}

func (s *tieSampler) reset(n int) {
	if n > 1 && len(s.positions) < n {
		s.positions = make([]int, n)
		s.marks = make([]uint32, n)
	}
	s.epoch++
	if s.epoch == 0 {
		clear(s.marks)
		s.epoch = 1
	}
	s.remaining = n
}

func (s *tieSampler) value(index int) int {
	if index < len(s.marks) && s.marks[index] == s.epoch {
		return s.positions[index]
	}
	return index
}

// draw removes one index; a singleton consumes no RNG. Callers reject invalid
// placements from the permutation, which leaves a uniform order on valid slots.
func (s *tieSampler) draw(rng *rand.Rand) int {
	index := 0
	if s.remaining > 1 {
		index = rng.IntN(s.remaining)
	}
	chosen := s.value(index)
	s.remaining--
	if index != s.remaining {
		s.positions[index] = s.value(s.remaining)
		s.marks[index] = s.epoch
	}
	return chosen
}
