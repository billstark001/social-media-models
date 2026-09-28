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

var trajectoryMagicV2 = [8]byte{'S', 'M', 'P', 'T', 'R', 'J', '0', '2'}
var trajectoryMagic = [8]byte{'S', 'M', 'P', 'T', 'R', 'J', '0', '3'}

func compressBlock(raw []byte) ([]byte, error) {
	var b bytes.Buffer
	w := lz4.NewWriter(&b)
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func encodeLZ4Channel(values []byte, wordSize, wordsPerRow, rows int) (byte, []byte, error) {
	allZero := true
	for _, v := range values {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return 2, nil, nil
	}
	raw, err := compressBlock(values)
	if err != nil {
		return 0, nil, err
	}
	previous := make([]byte, wordSize*wordsPerRow)
	xor := make([]byte, len(values))
	for row := 0; row < rows; row++ {
		base := row * len(previous)
		for i := 0; i < len(previous); i++ {
			current := values[base+i]
			xor[base+i] = current ^ previous[i]
			previous[i] = current
		}
	}
	encoded, err := compressBlock(xor)
	if err != nil {
		return 0, nil, err
	}
	if len(encoded) < len(raw) {
		return 1, encoded, nil
	}
	return 0, raw, nil
}

func encodeBoolOpinions(raw []byte, wordSize int) ([]byte, bool, error) {
	words := len(raw) / wordSize
	packed := make([]byte, (words+7)/8)
	for i := 0; i < words; i++ {
		value := trajectoryFloat(raw[i*wordSize:], byte(wordSize))
		if value == 0 {
			continue
		}
		if value != 1 {
			return nil, false, nil
		}
		packed[i/8] |= 1 << uint(i%8)
	}
	encoded, err := compressBlock(packed)
	return encoded, true, err
}

func decodeChannel(encoded []byte, codec byte, wordSize, wordsPerRow, rows int) ([]byte, error) {
	if codec == 4 || codec == 5 {
		return decodeShuffledChannel(encoded, codec, wordSize, wordsPerRow*rows)
	}
	if codec == 2 {
		if len(encoded) != 0 {
			return nil, fmt.Errorf("zero channel has a payload")
		}
		return make([]byte, wordSize*wordsPerRow*rows), nil
	}
	if codec == 3 {
		packed, err := io.ReadAll(lz4.NewReader(bytes.NewReader(encoded)))
		if err != nil {
			return nil, err
		}
		words := wordsPerRow * rows
		if len(packed) != (words+7)/8 {
			return nil, fmt.Errorf("bitset length mismatch")
		}
		raw := make([]byte, words*wordSize)
		for i := 0; i < words; i++ {
			if packed[i/8]&(1<<uint(i%8)) != 0 {
				if err := putTrajectoryFloat(raw[i*wordSize:], 1, byte(wordSize)); err != nil {
					return nil, err
				}
			}
		}
		return raw, nil
	}
	r := lz4.NewReader(bytes.NewReader(encoded))
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	want := wordSize * wordsPerRow * rows
	if len(raw) != want {
		return nil, fmt.Errorf("channel decoded length %d, want %d", len(raw), want)
	}
	if codec == 0 {
		return raw, nil
	}
	if codec != 1 {
		return nil, fmt.Errorf("unknown trajectory channel codec %d", codec)
	}
	stride := wordSize * wordsPerRow
	for row := 1; row < rows; row++ {
		base := row * stride
		for i := 0; i < stride; i++ {
			raw[base+i] ^= raw[base-stride+i]
		}
	}
	return raw, nil
}

func EncodeTrajectoryChunk(buffer *TrajectoryBuffer) ([]byte, error) {
	if buffer == nil || buffer.Steps == 0 {
		return nil, fmt.Errorf("empty trajectory chunk")
	}
	if err := buffer.Precision.validate(); err != nil {
		return nil, err
	}
	precision := buffer.Precision.resolved()
	opinionSize, sumSize := precisionCode(precision.Opinions), precisionCode(precision.OpinionSums)
	steps, agents := buffer.Steps, buffer.Agents
	if steps > math.MaxUint32 || agents > math.MaxUint32 {
		return nil, fmt.Errorf("trajectory dimensions too large")
	}
	expected := [3]int{steps * agents * int(opinionSize), steps * agents * 8, steps * agents * 4 * int(sumSize)}
	for i := range expected {
		if len(buffer.Channels[i]) != expected[i] {
			return nil, fmt.Errorf("trajectory channel %d length mismatch", i)
		}
	}
	var output bytes.Buffer
	output.Write(trajectoryMagic[:])
	if err := binary.Write(&output, binary.LittleEndian, uint32(steps)); err != nil {
		return nil, err
	}
	if err := binary.Write(&output, binary.LittleEndian, uint32(agents)); err != nil {
		return nil, err
	}
	output.WriteByte(opinionSize)
	output.WriteByte(sumSize)
	for i, raw := range buffer.Channels {
		wordSize := int(opinionSize)
		wordsPerRow := agents
		if i == 1 {
			wordSize = 2
			wordsPerRow = agents * 4
		}
		if i == 2 {
			wordSize = int(sumSize)
			wordsPerRow = agents * 4
		}
		codec, encoded, err := encodeChannel(raw, wordSize, wordsPerRow, steps, i)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			bitset, isBool, bitErr := encodeBoolOpinions(raw, wordSize)
			if bitErr != nil {
				return nil, bitErr
			}
			if isBool && len(bitset) < len(encoded) {
				codec, encoded = 3, bitset
			}
		}
		if len(encoded) > math.MaxUint32 {
			return nil, fmt.Errorf("trajectory block too large")
		}
		output.WriteByte(codec)
		if err := binary.Write(&output, binary.LittleEndian, uint32(len(encoded))); err != nil {
			return nil, err
		}
		output.Write(encoded)
	}
	return output.Bytes(), nil
}

func LoadTrajectoryChunk(path string) (*AccumulativeModelState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(data)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return nil, err
	}
	if magic != trajectoryMagic && magic != trajectoryMagicV2 {
		return nil, fmt.Errorf("unknown trajectory chunk format")
	}
	var steps, agents uint32
	if err := binary.Read(reader, binary.LittleEndian, &steps); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &agents); err != nil {
		return nil, err
	}
	if steps == 0 || agents == 0 || uint64(steps)*uint64(agents) > 1<<30 {
		return nil, fmt.Errorf("invalid trajectory dimensions")
	}
	opinionSize, sumSize := byte(4), byte(4)
	if magic == trajectoryMagic {
		if opinionSize, err = reader.ReadByte(); err != nil {
			return nil, err
		}
		if sumSize, err = reader.ReadByte(); err != nil {
			return nil, err
		}
		if _, err := precisionName(opinionSize); err != nil {
			return nil, err
		}
		if _, err := precisionName(sumSize); err != nil {
			return nil, err
		}
	}
	channels := make([][]byte, 3)
	for i := 0; i < 3; i++ {
		codec, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if codec == 3 && i != 0 {
			return nil, fmt.Errorf("bitset codec is only valid for opinions")
		}
		var length uint32
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
			return nil, err
		}
		if uint64(length) > uint64(reader.Len()) {
			return nil, fmt.Errorf("truncated trajectory channel")
		}
		encoded := make([]byte, length)
		if _, err := io.ReadFull(reader, encoded); err != nil {
			return nil, err
		}
		wordSize, wordsPerRow := int(opinionSize), int(agents)
		if i == 1 {
			wordSize = 2
			wordsPerRow = int(agents) * 4
		}
		if i == 2 {
			wordSize = int(sumSize)
			wordsPerRow = int(agents) * 4
		}
		channels[i], err = decodeChannel(encoded, codec, wordSize, wordsPerRow, int(steps))
		if err != nil {
			return nil, err
		}
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("trailing trajectory bytes")
	}
	state := NewAccumulativeModelState()
	for t := 0; t < int(steps); t++ {
		opinions := make([]float64, agents)
		numbers := make([][4]int16, agents)
		sums := make([][4]float64, agents)
		for a := 0; a < int(agents); a++ {
			opinions[a] = trajectoryFloat(channels[0][int(opinionSize)*(t*int(agents)+a):], opinionSize)
			for c := 0; c < 4; c++ {
				numbers[a][c] = int16(binary.LittleEndian.Uint16(channels[1][8*(t*int(agents)+a)+2*c:]))
				sums[a][c] = trajectoryFloat(channels[2][int(sumSize)*(4*(t*int(agents)+a)+c):], sumSize)
			}
		}
		state.Opinions = append(state.Opinions, opinions)
		state.AgentNumbers = append(state.AgentNumbers, numbers)
		state.AgentOpinionSums = append(state.AgentOpinionSums, sums)
	}
	return state, nil
}
