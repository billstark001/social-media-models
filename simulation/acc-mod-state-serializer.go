package simulation

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/pierrec/lz4/v4"
)

// SaveAccumulativeModelState writes the legacy channel-major LZ4 format.
func SaveAccumulativeModelState(path string, state *AccumulativeModelState) error {
	var buf bytes.Buffer

	// The sample count includes the initial step-zero state.
	steps := int32(len(state.Opinions))
	if steps == 0 {
		return fmt.Errorf("empty opinions")
	}
	agents := int32(len(state.Opinions[0]))

	binary.Write(&buf, binary.LittleEndian, steps)
	binary.Write(&buf, binary.LittleEndian, agents)

	for _, agentsSlice := range state.Opinions {
		for _, value := range agentsSlice {
			binary.Write(&buf, binary.LittleEndian, float32(value))
		}
	}

	for _, agSlice := range state.AgentNumbers {
		for _, arr := range agSlice {
			binary.Write(&buf, binary.LittleEndian, arr)
		}
	}

	for _, agSlice := range state.AgentOpinionSums {
		for _, arr := range agSlice {
			for _, value := range arr {
				binary.Write(&buf, binary.LittleEndian, float32(value))
			}
		}
	}

	var out bytes.Buffer
	w := lz4.NewWriter(&out)
	_, err := w.Write(buf.Bytes())
	if err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	return os.WriteFile(path, out.Bytes(), 0644)
}

// LoadAccumulativeModelState validates and reads a legacy accumulated state.
func LoadAccumulativeModelState(path string) (*AccumulativeModelState, error) {
	return loadAccumulativeState(path, true)
}

// LoadAccumulativeOpinions reads only opinions from a legacy accumulated state.
// Other channels are streamed to discard, but their length and LZ4 integrity
// are still checked. It does not allocate their decoded arrays or full payload.
func LoadAccumulativeOpinions(path string) ([][]float64, error) {
	state, err := loadAccumulativeState(path, false)
	if err != nil {
		return nil, err
	}
	return state.Opinions, nil
}

func loadAccumulativeState(path string, allChannels bool) (*AccumulativeModelState, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	r := lz4.NewReader(file)
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("read accumulated-state dimensions: %w", err)
	}
	steps := int64(int32(binary.LittleEndian.Uint32(header[:4])))
	agents := int64(int32(binary.LittleEndian.Uint32(header[4:])))
	// Each step/agent contributes float32 opinions, four int16 counts and
	// four float32 sums. Bound all dimension arithmetic before reading rows.
	const cellBytes = int64(4 + 4*2 + 4*4)
	maxPayload := int64(int(^uint(0)>>1)) - 1
	if steps <= 0 || agents <= 0 || steps > maxPayload/cellBytes/agents {
		return nil, fmt.Errorf("invalid accumulated-state dimensions %d by %d", steps, agents)
	}
	// Grow the row buffer only as bytes arrive. A corrupt huge dimension
	// followed by a short payload must not allocate its claimed row size.
	var buffer bytes.Buffer
	readRow := func(bytes int64, channel string, step int64) ([]byte, error) {
		buffer.Reset()
		var data []byte
		var err error
		if int64(buffer.Cap()) >= bytes {
			data = buffer.Bytes()[:int(bytes)]
			_, err = io.ReadFull(r, data)
		} else {
			_, err = io.CopyN(&buffer, r, bytes)
			data = buffer.Bytes()
		}
		if err != nil {
			return nil, fmt.Errorf("read accumulated-state payload (%s step %d): %w", channel, step, err)
		}
		return data, nil
	}
	readFloat := func(data []byte) float64 {
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(data)))
	}
	state := &AccumulativeModelState{Opinions: make([][]float64, 0, min(int(steps), 256))}
	for step := int64(0); step < steps; step++ {
		data, err := readRow(4*agents, "opinions", step)
		if err != nil {
			return nil, err
		}
		row := make([]float64, int(agents))
		for id := range row {
			row[id] = readFloat(data[4*id:])
		}
		state.Opinions = append(state.Opinions, row)
	}
	if allChannels {
		state.AgentNumbers = make([][][4]int16, 0, min(int(steps), 256))
		for step := int64(0); step < steps; step++ {
			data, err := readRow(8*agents, "counts", step)
			if err != nil {
				return nil, err
			}
			row := make([][4]int16, int(agents))
			for id := range row {
				for bucket := range row[id] {
					row[id][bucket] = int16(binary.LittleEndian.Uint16(data[8*id+2*bucket:]))
				}
			}
			state.AgentNumbers = append(state.AgentNumbers, row)
		}
		state.AgentOpinionSums = make([][][4]float64, 0, min(int(steps), 256))
		for step := int64(0); step < steps; step++ {
			data, err := readRow(16*agents, "sums", step)
			if err != nil {
				return nil, err
			}
			row := make([][4]float64, int(agents))
			for id := range row {
				for bucket := range row[id] {
					row[id][bucket] = readFloat(data[16*id+4*bucket:])
				}
			}
			state.AgentOpinionSums = append(state.AgentOpinionSums, row)
		}
	} else if _, err := io.CopyN(io.Discard, r, (cellBytes-4)*steps*agents); err != nil {
		return nil, fmt.Errorf("read accumulated-state payload (unselected channels): %w", err)
	}
	// Reading once beyond the exact channel length checks both trailing bytes
	// and the decompressor footer, including checksum failures after the data.
	var trailing [1]byte
	if n, err := io.ReadFull(r, trailing[:]); n != 0 {
		return nil, fmt.Errorf("accumulated-state payload has trailing bytes")
	} else if err != io.EOF {
		return nil, fmt.Errorf("read accumulated-state payload footer: %w", err)
	}
	if n, err := file.Read(trailing[:]); n != 0 {
		return nil, fmt.Errorf("accumulated-state file has trailing bytes")
	} else if err != io.EOF {
		return nil, fmt.Errorf("read accumulated-state file footer: %w", err)
	}
	return state, nil
}
