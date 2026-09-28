package simulation

import (
	"encoding/binary"
	"fmt"
	"slices"
	"smp/model"
)

// TrajectoryBuffer converts each sample once, directly into the on-disk word
// widths. Its backing arrays are reused after every flush.
type TrajectoryBuffer struct {
	Agents    int
	Steps     int
	Precision TrajectoryPrecision
	Channels  [3][]byte
}

func newTrajectoryBuffer(agents int, precision TrajectoryPrecision) *TrajectoryBuffer {
	b := &TrajectoryBuffer{Agents: agents, Precision: precision}
	sizes := [3]int{int(precisionCode(precision.Opinions)), 8, 4 * int(precisionCode(precision.OpinionSums))}
	for i, size := range sizes {
		b.Channels[i] = make([]byte, 0, trajectoryChunkSteps*agents*size)
	}
	return b
}

func (b *TrajectoryBuffer) Append(opinions []float64, counts []model.AgentNumberRecord, sums []model.AgentOpinionSumRecord) error {
	if len(opinions) != b.Agents || len(counts) != b.Agents || len(sums) != b.Agents {
		return fmt.Errorf("trajectory sample agent count mismatch")
	}
	opSize, sumSize := precisionCode(b.Precision.Opinions), precisionCode(b.Precision.OpinionSums)
	startOp, startCount, startSum := len(b.Channels[0]), len(b.Channels[1]), len(b.Channels[2])
	for i, n := range [3]int{b.Agents * int(opSize), b.Agents * 8, b.Agents * 4 * int(sumSize)} {
		oldLen := len(b.Channels[i])
		b.Channels[i] = slices.Grow(b.Channels[i], n)
		b.Channels[i] = b.Channels[i][:oldLen+n]
	}
	for a := 0; a < b.Agents; a++ {
		if err := putTrajectoryFloat(b.Channels[0][startOp+a*int(opSize):], opinions[a], opSize); err != nil {
			return fmt.Errorf("opinion agent %d: %w", a, err)
		}
		for c := 0; c < 4; c++ {
			count := counts[a][c]
			if count < -32768 || count > 32767 {
				return fmt.Errorf("agent count %d exceeds int16 range", count)
			}
			binary.LittleEndian.PutUint16(b.Channels[1][startCount+8*a+2*c:], uint16(int16(count)))
			if err := putTrajectoryFloat(b.Channels[2][startSum+int(sumSize)*(4*a+c):], sums[a][c], sumSize); err != nil {
				return fmt.Errorf("opinion sum agent %d component %d: %w", a, c, err)
			}
		}
	}
	b.Steps++
	return nil
}

func (b *TrajectoryBuffer) Reset() {
	b.Steps = 0
	for i := range b.Channels {
		b.Channels[i] = b.Channels[i][:0]
	}
}
