package record_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"smp/model"
	"smp/record"
	"smp/simulation"
	"smp/trajectory"
	"smp/utils"

	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
)

var historyOpinions = [][]float64{{-.5, 0, .5}, {-.4, .1, .4}, {-.3, .2, .3}, {-.2, .25, .2}, {-.1, .3, .1}}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func metadata() map[string]any {
	return map[string]any{
		"UniqueName": "fixture", "NodeCount": 3, "DynamicsType": "HK", "PostRetainCount": 2,
		"HKParams": map[string]any{"Tolerance": .4, "Influence": 1.0, "RepostRate": 0.0, "RewiringRate": 0.0},
	}
}

func writeGraph(t *testing.T, path string) {
	t.Helper()
	graph := simple.NewDirectedGraph()
	for id := int64(0); id < 3; id++ {
		graph.AddNode(simple.Node(id))
	}
	for id := int64(0); id < 3; id++ {
		graph.SetEdge(graph.NewEdge(graph.Node(id), graph.Node((id+1)%3)))
	}
	data, err := msgpack.Marshal(utils.SerializeGraph(graph))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, ".lz4") {
		var encoded bytes.Buffer
		writer := lz4.NewWriter(&encoded)
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		data = encoded.Bytes()
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func writeRun(t *testing.T, legacy bool, precision trajectory.Precision) (string, map[string]any) {
	t.Helper()
	dir := t.TempDir()
	meta := metadata()
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	writeGraph(t, filepath.Join(dir, "graph-0.msgpack"))
	state := &simulation.AccumulativeModelState{Opinions: historyOpinions}
	for range historyOpinions {
		state.AgentNumbers = append(state.AgentNumbers, make([][4]int16, 3))
		state.AgentOpinionSums = append(state.AgentOpinionSums, make([][4]float64, 3))
	}
	if legacy {
		if err := simulation.SaveAccumulativeModelState(filepath.Join(dir, "acc-state-2026-11-19T010000Z.lz4"), state); err != nil {
			t.Fatal(err)
		}
		return dir, meta
	}
	writer, err := trajectory.Open(dir, 3, precision)
	if err != nil {
		t.Fatal(err)
	}
	buffer := trajectory.NewBuffer(3, precision)
	for step, opinions := range historyOpinions {
		if err := buffer.Append(opinions, make([]model.AgentNumberRecord, 3), make([]model.AgentOpinionSumRecord, 3)); err != nil {
			t.Fatal(err)
		}
		if step == 1 || step == 4 {
			if err := writer.Flush(buffer); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir, meta
}

func readStates(t *testing.T, reader *record.Reader, steps []int, requirements record.Requirements) []record.State {
	t.Helper()
	var states []record.State
	if err := reader.ReadStates(steps, requirements, func(state record.State) error { states = append(states, state); return nil }); err != nil {
		t.Fatal(err)
	}
	return states
}

func fingerprints(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Read-only WAL connections may maintain locking sidecars or create an
		// empty WAL. Every nonempty WAL remains part of persisted history.
		if entry.Name() == "events.db-shm" || (entry.Name() == "events.db-wal" && len(data) == 0) {
			return nil
		}
		sum := sha256.Sum256(data)
		relative, _ := filepath.Rel(dir, path)
		files[relative] = hex.EncodeToString(sum[:])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestReadStatesAcrossBlocksAndLegacy(t *testing.T) {
	for _, test := range []struct {
		name      string
		legacy    bool
		precision trajectory.Precision
		label     string
	}{
		{"float64", false, trajectory.Precision{Opinions: "float64", OpinionSums: "float64"}, "history_f64"},
		{"float16", false, trajectory.Precision{Opinions: "float16", OpinionSums: "float16"}, "history_f16"},
		{"legacy", true, trajectory.Precision{}, "history_f32"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir, _ := writeRun(t, test.legacy, test.precision)
			before := fingerprints(t, dir)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if reader.MaxStep != 4 || reader.Metadata.PostEvent || reader.MetadataFields["PostEvent"] != nil {
				t.Fatalf("changed explicit metadata coverage: %+v", reader.Metadata)
			}
			states := readStates(t, reader, []int{0, 2, 4}, record.Requirements{Posts: true})
			for _, state := range states {
				if state.Precision != test.label || len(state.Graph.Nodes) != 3 || len(state.Posts) != 3 {
					t.Fatalf("incomplete state: %+v", state)
				}
				for id := int64(0); id < 3; id++ {
					post := state.Posts[id][len(state.Posts[id])-1]
					expectedStep := state.Step
					if expectedStep == 0 {
						expectedStep = -1
					}
					if post.AgentID != id || post.Step != expectedStep || post.Opinion != state.Opinions[id] {
						t.Fatalf("bad normal post: %+v, state=%+v", post, state)
					}
				}
			}
			states[0].Opinions[0] = 99
			delete(states[0].Graph.Adjacency[0], 1)
			states[0].Posts[0][0].Opinion = 99
			again := readStates(t, reader, []int{0, 4}, record.Requirements{Posts: true})
			if again[0].Opinions[0] == 99 || again[0].Posts[0][0].Opinion == 99 || again[0].Graph.Adjacency[0][1] == nil {
				t.Fatal("yielded states alias the reader")
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if after := fingerprints(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatalf("read changed files: before=%v after=%v", before, after)
			}
		})
	}
}

func TestReadStatesReplaysRewiringAndSparseReposts(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{Opinions: "float64", OpinionSums: "float64"})
	meta["HKParams"].(map[string]any)["RepostRate"] = .5
	meta["HKParams"].(map[string]any)["RewiringRate"] = .5
	meta["RewiringEvent"], meta["PostEvent"] = true, true
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 8)
	if err != nil {
		t.Fatal(err)
	}
	events := []*model.EventRecord{
		{Type: "Rewiring", AgentID: 0, Step: 1, Body: model.RewiringEventBody{AgentID: 0, Unfollow: 1, Follow: 2}},
		{Type: "Post", AgentID: 1, Step: 1, Body: model.PostEventBody[float64]{IsRepost: true, Record: &model.PostRecord[float64]{AgentID: 0, Step: -1, Opinion: -.5}}},
		{Type: "Post", AgentID: 2, Step: 2, Body: model.PostEventBody[float64]{IsRepost: true, Record: &model.PostRecord[float64]{AgentID: 0, Step: 1, Opinion: -.4}}},
		{Type: "Rewiring", AgentID: 0, Step: 3, Body: model.RewiringEventBody{AgentID: 0, Unfollow: 2, Follow: 1}},
	}
	for _, event := range events {
		if err := db.StoreEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := fingerprints(t, dir)
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	states := readStates(t, reader, []int{1, 2, 4}, record.Requirements{Posts: true})
	if states[0].Graph.Adjacency[0][2] == nil || states[0].Graph.Adjacency[0][1] != nil || states[2].Graph.Adjacency[0][1] == nil {
		t.Fatal("rewiring replay or graph ownership failed")
	}
	if actual := states[0].Posts[1][1]; actual.AgentID != 0 || actual.Step != -1 || actual.Opinion != -.5 {
		t.Fatalf("repost origin lost: %+v", actual)
	}
	if actual := states[0].Posts[2][1]; actual.AgentID != 2 || actual.Step != 1 || actual.Opinion != .4 {
		t.Fatalf("normal post replaced by sparse overlay zero: %+v", actual)
	}
	if actual := states[1].Posts[2][1]; actual.AgentID != 0 || actual.Step != 1 || actual.Opinion != -.4 {
		t.Fatalf("later repost lost: %+v", actual)
	}
	if len(states[2].Posts[2]) != 2 || states[2].Posts[2][0].Step != 3 || states[2].Posts[2][1].Step != 4 {
		t.Fatal("retention did not discard old posts")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if after := fingerprints(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed source files: before=%v after=%v", before, after)
	}
}

func TestReadStatesRequiresOnlyRequestedChannels(t *testing.T) {
	cases := []struct {
		name         string
		configure    func(string, map[string]any)
		steps        []int
		requirements record.Requirements
		message      string
	}{
		{"opinions_without_graph", func(dir string, _ map[string]any) { os.Remove(filepath.Join(dir, "graph-0.msgpack")) }, []int{4}, record.Requirements{}, ""},
		{"missing_graph", func(dir string, _ map[string]any) { os.Remove(filepath.Join(dir, "graph-0.msgpack")) }, []int{4}, record.Requirements{Graph: true}, "no earlier graph"},
		{"unknown_rewiring", func(_ string, meta map[string]any) { delete(meta["HKParams"].(map[string]any), "RewiringRate") }, []int{4}, record.Requirements{Graph: true}, "RewiringEvent"},
		{"no_post_log_static", func(_ string, meta map[string]any) { meta["HKParams"].(map[string]any)["RepostRate"] = .3 }, []int{4}, record.Requirements{Graph: true}, ""},
		{"missing_post_log", func(_ string, meta map[string]any) { meta["HKParams"].(map[string]any)["RepostRate"] = .3 }, []int{4}, record.Requirements{Posts: true}, "PostEvent"},
		{"unknown_repost", func(_ string, meta map[string]any) { delete(meta["HKParams"].(map[string]any), "RepostRate") }, []int{4}, record.Requirements{Posts: true}, "PostEvent"},
		{"missing_retain", func(_ string, meta map[string]any) { delete(meta, "PostRetainCount") }, []int{4}, record.Requirements{Posts: true}, "PostRetainCount"},
		{"null_rate", func(_ string, meta map[string]any) { meta["HKParams"].(map[string]any)["RewiringRate"] = nil }, []int{4}, record.Requirements{Graph: true}, "invalid HKParams.RewiringRate"},
		{"step_zero_posts", func(_ string, meta map[string]any) { delete(meta, "PostRetainCount"); delete(meta, "HKParams") }, []int{0}, record.Requirements{Posts: true}, ""},
		{"exact_compressed_graph", func(dir string, meta map[string]any) {
			writeGraph(t, filepath.Join(dir, "graph-4.msgpack.lz4"))
			delete(meta, "HKParams")
		}, []int{4}, record.Requirements{Graph: true}, ""},
		{"empty_post_log", func(dir string, meta map[string]any) {
			meta["PostEvent"] = true
			meta["HKParams"].(map[string]any)["RepostRate"] = .3
			db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
			if err != nil {
				t.Fatal(err)
			}
			db.Close()
		}, []int{4}, record.Requirements{Posts: true}, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
			test.configure(dir, meta)
			writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
			before := fingerprints(t, dir)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = reader.ReadStates(test.steps, test.requirements, func(record.State) error { return nil })
			if test.message == "" && err != nil || test.message != "" && (err == nil || !strings.Contains(err.Error(), test.message)) {
				t.Fatalf("expected %q, got %v", test.message, err)
			}
			reader.Close()
			if after := fingerprints(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatal("read changed files")
			}
		})
	}
}

func TestReadStatesValidatesSelectionsAndCallback(t *testing.T) {
	dir, _ := writeRun(t, false, trajectory.Precision{}.Resolved())
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, steps := range [][]int{nil, {-1}, {0, 0}, {2, 1}, {5}} {
		if err := reader.ReadStates(steps, record.Requirements{}, func(record.State) error { return nil }); err == nil {
			t.Fatalf("accepted %v", steps)
		}
	}
	stop := errors.New("stop")
	if err := reader.ReadStates([]int{0, 1}, record.Requirements{}, func(record.State) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("lost callback error: %v", err)
	}
	reader.Close()
	if err := reader.ReadStates([]int{0}, record.Requirements{}, func(record.State) error { return nil }); err == nil {
		t.Fatal("read closed reader")
	}
}

func TestReaderRejectsCorruptionAndObsoleteMetadata(t *testing.T) {
	t.Run("checksum", func(t *testing.T) {
		dir, _ := writeRun(t, false, trajectory.Precision{}.Resolved())
		path := filepath.Join(dir, "trajectory-000000000-000000001.smpc")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data[len(data)-1] ^= 1
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		reader, err := record.OpenRun(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		if err := reader.ReadStates([]int{0}, record.Requirements{}, func(record.State) error { return nil }); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("expected checksum error, got %v", err)
		}
	})
	t.Run("alias", func(t *testing.T) {
		dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
		meta["RecSysParams"] = map[string]any{"usecache": true}
		writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
		if _, err := record.OpenRun(dir, nil); err == nil || !strings.Contains(err.Error(), "smp-migrate") {
			t.Fatalf("expected migration error, got %v", err)
		}
	})
	for _, kind := range []string{"old_table", "old_event", "missing_body", "bad_actor"} {
		t.Run(kind, func(t *testing.T) {
			dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
			meta["PostEvent"], meta["RewiringEvent"] = true, true
			meta["HKParams"].(map[string]any)["RepostRate"] = .3
			meta["HKParams"].(map[string]any)["RewiringRate"] = .3
			writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
			db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
			if err != nil {
				t.Fatal(err)
			}
			db.Close()
			raw, err := sql.Open("sqlite3", filepath.Join(dir, "events.db"))
			if err != nil {
				t.Fatal(err)
			}
			query := map[string]string{
				"old_table":    "CREATE TABLE tweet_events (event_id INTEGER)",
				"old_event":    "INSERT INTO events(type,agent_id,step) VALUES ('Tweet',0,4)",
				"missing_body": "INSERT INTO events(type,agent_id,step) VALUES ('Post',0,4)",
				"bad_actor":    "INSERT INTO events(id,type,agent_id,step) VALUES (1,'Rewiring',1,4)",
			}[kind]
			if _, err := raw.Exec(query); err != nil {
				t.Fatal(err)
			}
			if kind == "bad_actor" {
				if _, err := raw.Exec("INSERT INTO rewiring_events VALUES (1,0,2,0)"); err != nil {
					t.Fatal(err)
				}
			}
			raw.Close()
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			err = reader.ReadStates([]int{4}, record.Requirements{Posts: true}, func(record.State) error { return nil })
			if err == nil {
				t.Fatal("accepted invalid events")
			}
			if kind != "missing_body" && !strings.Contains(err.Error(), "smp-migrate") {
				t.Fatalf("expected migration hint, got %v", err)
			}
		})
	}
}

func TestReaderAcceptsVersionTwoTrajectory(t *testing.T) {
	dir, _ := writeRun(t, false, trajectory.Precision{Opinions: "float32", OpinionSums: "float32"})
	var manifest trajectory.Manifest
	data, err := os.ReadFile(filepath.Join(dir, "trajectory.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = 2
	manifest.Precision = trajectory.Precision{}
	for i := range manifest.Chunks {
		chunk := &manifest.Chunks[i]
		path := filepath.Join(dir, chunk.File)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append([]byte("SMPTRJ02"), data[8:16]...), data[18:]...)
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		chunk.SHA256 = hex.EncodeToString(sum[:])
	}
	writeJSON(t, filepath.Join(dir, "trajectory.json"), manifest)
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	states := readStates(t, reader, []int{0, 4}, record.Requirements{Graph: true})
	if states[1].Precision != "history_f32" {
		t.Fatalf("bad precision %q", states[1].Precision)
	}
}

func TestReaderIncludesCommittedWALWithoutChangingHistory(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
	meta["PostEvent"] = true
	meta["HKParams"].(map[string]any)["RepostRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	// Retain an uncheckpointed committed WAL, as in a copied run directory.
	// The source SQLite connection stays in a separate fixture directory.
	source := filepath.Join(t.TempDir(), "events.db")
	schema, err := simulation.OpenEventDB(source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, query := range []string{
		"PRAGMA journal_mode=WAL",
		"INSERT INTO events(id,type,agent_id,step) VALUES (1,'Post',2,4)",
		"INSERT INTO post_events VALUES (1,0,1,-0.4,1)",
	} {
		if _, err := writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(source + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "events.db")+suffix, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	before := fingerprints(t, dir)
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	states := readStates(t, reader, []int{4}, record.Requirements{Posts: true})
	post := states[0].Posts[2][1]
	if post.AgentID != 0 || post.Step != 1 || post.Opinion != -.4 {
		t.Fatalf("committed WAL was ignored: %+v", post)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	after := fingerprints(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed persisted history: before=%v after=%v", before, after)
	}
}
