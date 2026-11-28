package bridges

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"smp/groups"
	"smp/model"
	"testing"
)

// This projection deliberately excludes additive channel-ledger fields. Its
// golden was captured before those fields/ledgers were introduced.
type uParityFrame struct {
	Step      int
	Pairs     []PairFrame
	Union     *UnionFrame
	Events    []Event
	Episodes  []Episode
	Epochs    []Epoch
	Summaries []Summary
}

func uParityHistory(t *testing.T) []uParityFrame {
	t.Helper()
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	opts := detailOptions(.7)
	opts.Guard = 1e-7
	o, err := NewObserver(opts)
	if err != nil {
		t.Fatal(err)
	}
	frames := []uParityFrame{}
	appendFrame := func(frame Frame) {
		frames = append(frames, uParityFrame{frame.Step, frame.Pairs, frame.Union, frame.Events, frame.Episodes, frame.Epochs, frame.Summaries})
	}
	for _, step := range []int{0, 1, 2, 3, 4, 5, 6, 8, 9, 10} {
		switch step {
		case 1:
			p[4][0] = model.PostRecord[float64]{AgentID: 2, Step: 0, Opinion: .8}
		case 2:
			delete(p, 4)
		case 3:
			delete(g.Adjacency[0], 4)
			delete(g.Adjacency[2], 4)
		case 4:
			connectBridge(&g, 0, 4)
			p[4] = []model.PostRecord[float64]{{AgentID: -1, Step: 0, Opinion: .1}}
		case 5:
			f.Groups[0].Core.Members = []int{0, 4}
			f.Groups = append(f.Groups, groups.Group{ID: 30, State: "observed", Core: groups.Core{Members: []int{5}}})
		case 6:
			f.Groups[0].State = "unobserved"
		case 8:
			f.Groups[0].State = "observed"
		case 9:
			p[4][0].AgentID = 2
		case 10:
			x[2] = -.3
			x[3] = -.3
		}
		f.Step = step
		frame, err := o.Measure(step, x, g, p, f)
		if err != nil {
			t.Fatal(err)
		}
		appendFrame(frame)
	}
	close, err := o.FinalClose(12, "parity_horizon")
	if err != nil {
		t.Fatal(err)
	}
	appendFrame(close)
	return frames
}

func TestULedgerByteParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	path := filepath.Join(dir, "testdata", "u_ledger_parity.json.gz")
	got, err := json.Marshal(uParityHistory(t))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_U_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			t.Fatal(err)
		}
		zip := gzip.NewWriter(out)
		if _, err := zip.Write(got); err != nil {
			t.Fatal(err)
		}
		if err := zip.Close(); err != nil {
			t.Fatal(err)
		}
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
		hashes := map[string]string{}
		for _, name := range []string{"types.go", "measure.go", "ledger.go", "union.go", "native.go"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		source, _ := json.MarshalIndent(map[string]any{"purpose": "U-only byte golden captured before independent channel ledgers", "core_sources_sha256": hashes}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "testdata", "u_ledger_parity.source.json"), append(source, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	compressed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if _, err := want.ReadFrom(reader); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("existing U fields changed: generated %d bytes, golden %d bytes", len(got), want.Len())
	}
}
