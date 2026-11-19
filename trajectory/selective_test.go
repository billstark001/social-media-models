package trajectory

import (
	"bytes"
	"reflect"
	"testing"

	"smp/model"
)

func encodedObservationChunk(t testing.TB, precision Precision) []byte {
	t.Helper()
	buffer := NewBuffer(32, precision)
	for step := 0; step < 128; step++ {
		opinions := make([]float64, 32)
		counts := make([]model.AgentNumberRecord, 32)
		sums := make([]model.AgentOpinionSumRecord, 32)
		for id := range opinions {
			opinions[id] = float64((id+step)%64) / 64
			counts[id] = [4]int{id, step % 3, 2, 1}
			sums[id] = [4]float64{opinions[id], -.25, .5, 0}
		}
		if err := buffer.Append(opinions, counts, sums); err != nil {
			t.Fatal(err)
		}
	}
	data, err := EncodeChunk(buffer)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecodeChunkOpinionsMatchesFullChannels(t *testing.T) {
	for _, name := range []string{"float16", "float32", "float64"} {
		t.Run(name, func(t *testing.T) {
			data := encodedObservationChunk(t, Precision{Opinions: name, OpinionSums: name})
			before := append([]byte(nil), data...)
			full, err := DecodeChunk(data)
			if err != nil {
				t.Fatal(err)
			}
			opinions, err := DecodeChunkOpinions(data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(opinions, full.Opinions) || !bytes.Equal(data, before) {
				t.Fatal("selective decoding differs or modified the encoded input")
			}
			// Rows share one allocation, but appending to one row must not
			// overwrite another row or mutate a previous independent result.
			second := append([]float64(nil), opinions[1]...)
			first := append(opinions[0], 99)
			first[0] = 99
			if !reflect.DeepEqual(opinions[1], second) || full.Opinions[0][0] == 99 {
				t.Fatal("decoded row append or separate result aliases another row")
			}
		})
	}
}

func TestDecodeChannelRejectsOversizedExpansion(t *testing.T) {
	raw := make([]byte, 1<<20)
	encoded, err := compressBlock(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []byte{0, 1, 3} {
		if _, err := decodeChannel(encoded, codec, 4, 1, 1); err == nil {
			t.Fatalf("accepted oversized expansion for codec %d", codec)
		}
	}
	single, err := compressBlock(make([]byte, 4))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeChannel(append(append([]byte(nil), single...), single...), 0, 4, 1, 1); err == nil {
		t.Fatal("accepted trailing compressed frame in a channel")
	}
	encoder, err := trajectoryEncoder()
	if err != nil {
		t.Fatal(err)
	}
	encoded = encoder.EncodeAll(raw, nil)
	for _, codec := range []byte{trajectoryZstdByte, trajectoryZstdBit} {
		if _, err := decodeChannel(encoded, codec, 4, 1, 1); err == nil {
			t.Fatalf("accepted oversized expansion for codec %d", codec)
		}
	}
}

func BenchmarkDecodeChunkChannels(b *testing.B) {
	data := encodedObservationChunk(b, Precision{Opinions: "float64", OpinionSums: "float64"})
	for _, opinionsOnly := range []bool{false, true} {
		name := "all"
		if opinionsOnly {
			name = "opinions"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := decodeChunk(data, opinionsOnly); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
