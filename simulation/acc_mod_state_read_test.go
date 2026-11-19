package simulation_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"smp/simulation"

	"github.com/pierrec/lz4/v4"
)

func TestLoadAccumulativeStateRejectsMalformedPayload(t *testing.T) {
	encode := func(steps, agents int32, payload []byte) []byte {
		var raw, compressed bytes.Buffer
		if err := binary.Write(&raw, binary.LittleEndian, [2]int32{steps, agents}); err != nil {
			t.Fatal(err)
		}
		raw.Write(payload)
		writer := lz4.NewWriter(&compressed)
		if _, err := writer.Write(raw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return compressed.Bytes()
	}
	valid := encode(1, 1, make([]byte, 28))
	corrupted := append([]byte(nil), valid...)
	corrupted[len(corrupted)-1] ^= 1
	cases := []struct {
		name    string
		data    []byte
		message string
	}{
		{"empty", nil, "dimensions"},
		{"truncated_lz4", valid[:len(valid)/2], "read accumulated-state"},
		{"negative", encode(-1, 2, nil), "dimensions"},
		{"zero", encode(1, 0, nil), "dimensions"},
		{"overflow", encode(1<<31-1, 1<<31-1, nil), "dimensions"},
		{"huge_row_short_payload", encode(1, 1<<31-1, nil), "payload"},
		{"huge_steps_short_payload", encode(1<<31-1, 1, nil), "payload"},
		{"short", encode(2, 1, make([]byte, 28)), "payload"},
		{"trailing", encode(1, 1, make([]byte, 29)), "payload"},
		{"appended_frame", append(append([]byte(nil), valid...), valid...), "trailing"},
		{"checksum", corrupted, "payload"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "acc-state.lz4")
			if err := os.WriteFile(path, test.data, 0644); err != nil {
				t.Fatal(err)
			}
			loaders := map[string]func() error{
				"all":      func() error { _, err := simulation.LoadAccumulativeModelState(path); return err },
				"opinions": func() error { _, err := simulation.LoadAccumulativeOpinions(path); return err },
			}
			for name, load := range loaders {
				t.Run(name, func(t *testing.T) {
					if err := load(); err == nil || !strings.Contains(err.Error(), test.message) {
						t.Fatalf("expected %q error, got %v", test.message, err)
					}
				})
			}
		})
	}
}

func TestLoadAccumulativeOpinionsMatchesFullChannels(t *testing.T) {
	state := &simulation.AccumulativeModelState{
		Opinions:         [][]float64{{-.5, .25}, {-.25, .5}},
		AgentNumbers:     [][][4]int16{{{1, 2, 3, 4}, {5, 6, 7, 8}}, {{9, 10, 11, 12}, {13, 14, 15, 16}}},
		AgentOpinionSums: [][][4]float64{{{-.25, .5, -.75, 1}, {1, -.75, .5, -.25}}, {{.5, -.25, 1, -.75}, {-.75, 1, -.25, .5}}},
	}
	path := filepath.Join(t.TempDir(), "acc-state.lz4")
	if err := simulation.SaveAccumulativeModelState(path, state); err != nil {
		t.Fatal(err)
	}
	full, err := simulation.LoadAccumulativeModelState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full, state) {
		t.Fatalf("full channels changed: got %+v", full)
	}
	opinions, err := simulation.LoadAccumulativeOpinions(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opinions, full.Opinions) {
		t.Fatalf("opinions-only differs: got %v, want %v", opinions, full.Opinions)
	}
}
