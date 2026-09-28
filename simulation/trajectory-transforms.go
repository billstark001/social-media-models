package simulation

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	trajectoryZstdByte = 4
	trajectoryZstdBit  = 5
)

var trajectoryEncoder = sync.OnceValues(func() (*zstd.Encoder, error) {
	return zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
})

var trajectoryBetterEncoder = sync.OnceValues(func() (*zstd.Encoder, error) {
	return zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderConcurrency(1))
})

var trajectoryDecoder = sync.OnceValues(func() (*zstd.Decoder, error) {
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
})

// encodeChannel chooses the smallest lossless representation for one bounded
// time block. The LZ4 codecs remain readable for already-written trajectories.
func encodeChannel(values []byte, wordSize, wordsPerRow, rows, channel int) (byte, []byte, error) {
	bestCodec, best, err := encodeLZ4Channel(values, wordSize, wordsPerRow, rows)
	if err != nil || bestCodec == 2 {
		return bestCodec, best, err
	}
	getEncoder := trajectoryEncoder
	if channel == 2 {
		getEncoder = trajectoryBetterEncoder
	}
	encoder, err := getEncoder()
	if err != nil {
		return 0, nil, err
	}
	for _, candidate := range []struct {
		codec byte
		data  []byte
	}{
		{trajectoryZstdByte, shuffleBytes(values, wordSize)},
		{trajectoryZstdBit, shuffleBits(values, wordSize)},
	} {
		encoded := encoder.EncodeAll(candidate.data, nil)
		if len(encoded) < len(best) {
			bestCodec, best = candidate.codec, encoded
		}
	}
	return bestCodec, best, nil
}

func shuffleBytes(raw []byte, wordSize int) []byte {
	words := len(raw) / wordSize
	out := make([]byte, len(raw))
	for word := 0; word < words; word++ {
		for byteIndex := 0; byteIndex < wordSize; byteIndex++ {
			out[byteIndex*words+word] = raw[word*wordSize+byteIndex]
		}
	}
	return out
}

func unshuffleBytes(shuffled []byte, wordSize int) []byte {
	words := len(shuffled) / wordSize
	out := make([]byte, len(shuffled))
	for word := 0; word < words; word++ {
		for byteIndex := 0; byteIndex < wordSize; byteIndex++ {
			out[word*wordSize+byteIndex] = shuffled[byteIndex*words+word]
		}
	}
	return out
}

// Each bit plane contains ceil(words/8) bytes. Padding bits are discarded on
// decode, so the transform preserves every original float or integer bit.
func shuffleBits(raw []byte, wordSize int) []byte {
	words := len(raw) / wordSize
	planeBytes := (words + 7) / 8
	out := make([]byte, wordSize*8*planeBytes)
	for word := 0; word < words; word++ {
		for bit := 0; bit < wordSize*8; bit++ {
			if raw[word*wordSize+bit/8]&(1<<uint(bit%8)) != 0 {
				out[bit*planeBytes+word/8] |= 1 << uint(word%8)
			}
		}
	}
	return out
}

func unshuffleBits(shuffled []byte, wordSize, words int) []byte {
	planeBytes := (words + 7) / 8
	out := make([]byte, wordSize*words)
	for word := 0; word < words; word++ {
		for bit := 0; bit < wordSize*8; bit++ {
			if shuffled[bit*planeBytes+word/8]&(1<<uint(word%8)) != 0 {
				out[word*wordSize+bit/8] |= 1 << uint(bit%8)
			}
		}
	}
	return out
}

func decodeShuffledChannel(encoded []byte, codec byte, wordSize, words int) ([]byte, error) {
	decoder, err := trajectoryDecoder()
	if err != nil {
		return nil, err
	}
	decoded, err := decoder.DecodeAll(encoded, nil)
	if err != nil {
		return nil, err
	}
	switch codec {
	case trajectoryZstdByte:
		if len(decoded) != wordSize*words {
			return nil, fmt.Errorf("byte-shuffled channel length mismatch")
		}
		return unshuffleBytes(decoded, wordSize), nil
	case trajectoryZstdBit:
		if len(decoded) != wordSize*8*((words+7)/8) {
			return nil, fmt.Errorf("bit-shuffled channel length mismatch")
		}
		return unshuffleBits(decoded, wordSize, words), nil
	default:
		return nil, fmt.Errorf("unknown shuffled trajectory codec %d", codec)
	}
}
