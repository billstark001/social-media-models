package trajectory

import (
	"bytes"
	"testing"
)

func TestTrajectoryShuffleTransformsPreserveBits(t *testing.T) {
	for _, wordSize := range []int{2, 4} {
		for _, words := range []int{1, 7, 8, 9, 137} {
			raw := make([]byte, wordSize*words)
			for i := range raw {
				raw[i] = byte((i*71 + i*i*13) % 256)
			}
			if got := unshuffleBytes(shuffleBytes(raw, wordSize), wordSize); !bytes.Equal(got, raw) {
				t.Fatalf("byte shuffle changed %d words of %d bytes", words, wordSize)
			}
			if got := unshuffleBits(shuffleBits(raw, wordSize), wordSize, words); !bytes.Equal(got, raw) {
				t.Fatalf("bit shuffle changed %d words of %d bytes", words, wordSize)
			}
			for _, codec := range []byte{trajectoryZstdByte, trajectoryZstdBit} {
				encoder, err := trajectoryEncoder()
				if err != nil {
					t.Fatal(err)
				}
				var transformed []byte
				if codec == trajectoryZstdByte {
					transformed = shuffleBytes(raw, wordSize)
				} else {
					transformed = shuffleBits(raw, wordSize)
				}
				decoded, err := decodeChannel(encoder.EncodeAll(transformed, nil), codec, wordSize, words, 1)
				if err != nil || !bytes.Equal(decoded, raw) {
					t.Fatalf("codec %d failed for %d words of %d bytes: %v", codec, words, wordSize, err)
				}
			}
		}
	}
}
