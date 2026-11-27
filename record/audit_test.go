package record_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"smp/model"
	"smp/record"
	"smp/simulation"
	"smp/trajectory"

	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
)

func TestOpenRunExplicitMetadataOnlyWhenMissing(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
	if _, err := record.OpenRun(dir, meta); err == nil || !strings.Contains(err.Error(), "cannot override") {
		t.Fatalf("accepted explicit metadata over persisted configuration: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := record.OpenRun(dir, nil); err == nil {
		t.Fatal("accepted missing metadata without an explicit source")
	}
	before := fingerprints(t, dir)
	reader, err := record.OpenRun(dir, meta)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	states := readStates(t, reader, []int{4}, record.Requirements{})
	if len(states) != 1 || reader.MetadataFields["PostEvent"] != nil || reader.Metadata.PostEvent {
		t.Fatal("explicit metadata was filled with implicit collection defaults")
	}
	if after := fingerprints(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("explicit metadata observation changed the persisted run")
	}
}

func TestHistoricalHKWithoutDynamicsTypeUsesExplicitParametersReadOnly(t *testing.T) {
	dir, meta := writeRun(t, true, trajectory.Precision{})
	delete(meta, "DynamicsType")
	delete(meta, "NodeCount")
	delete(meta, "RewiringEvent")
	meta["AgentNumber"] = true
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	before := fingerprints(t, dir)
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reader.Metadata.DynamicsType != "HK" || reader.MetadataFields["DynamicsType"] != nil {
		t.Fatal("historical HK interpretation changed explicit metadata coverage")
	}
	states := readStates(t, reader, []int{0, 2, 4}, record.Requirements{Graph: true})
	if len(states) != 3 || states[2].Graph.Adjacency[0][1] == nil {
		t.Fatal("missing dynamics field hid explicit zero rewiring")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, fingerprints(t, dir)) {
		t.Fatal("historical HK observation wrote inferred metadata")
	}
}

func TestHistoricalHKInferenceDoesNotReplaceExplicitDynamics(t *testing.T) {
	for _, dynamics := range []string{"", "Deffuant"} {
		t.Run(dynamics, func(t *testing.T) {
			dir, meta := writeRun(t, true, trajectory.Precision{})
			meta["DynamicsType"] = dynamics
			writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if reader.Metadata.DynamicsType != dynamics {
				t.Fatal("HK parameters replaced an explicitly persisted dynamics type")
			}
		})
	}
}

func TestReadStatesClosedDuringCallbackReturnsError(t *testing.T) {
	var empty record.Reader
	if err := empty.ReadStates([]int{0}, record.Requirements{}, func(record.State) error { return nil }); err == nil {
		t.Fatal("accepted an unopened reader")
	}
	dir, _ := writeRun(t, false, trajectory.Precision{}.Resolved())
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	err = reader.ReadStates([]int{0, 1}, record.Requirements{}, func(record.State) error {
		called++
		return reader.Close()
	})
	if err == nil || !strings.Contains(err.Error(), "closed") || called != 1 {
		t.Fatalf("closing within a callback did not stop cleanly: called=%d error=%v", called, err)
	}
}

func TestReadStatesCloseWithOpenEventStreams(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
	meta["PostEvent"], meta["RewiringEvent"] = true, true
	meta["HKParams"].(map[string]any)["RepostRate"] = .5
	meta["HKParams"].(map[string]any)["RewiringRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []*model.EventRecord{
		{Type: "Post", AgentID: 0, Step: 4, Body: model.PostEventBody[float64]{IsRepost: true, Record: &model.PostRecord[float64]{AgentID: 1, Step: 3, Opinion: .2}}},
		{Type: "Rewiring", AgentID: 0, Step: 4, Body: model.RewiringEventBody{AgentID: 0, Unfollow: 1, Follow: 2}},
	} {
		if err := db.StoreEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = reader.ReadStates([]int{1, 4}, record.Requirements{Posts: true}, func(record.State) error { return reader.Close() })
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closing with active SQL streams did not stop cleanly: %v", err)
	}
}

func TestExactAnchorSkipsSupersededRewiringBodies(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
	meta["RewiringEvent"] = true
	meta["HKParams"].(map[string]any)["RewiringRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	writeGraph(t, filepath.Join(dir, "graph-4.msgpack"))
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.StoreEvent(&model.EventRecord{Type: "Rewiring", AgentID: 0, Step: 1, Body: model.RewiringEventBody{AgentID: 0, Unfollow: 1, Follow: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO events(type,agent_id,step) VALUES ('Rewiring',0,2)"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	states := readStates(t, reader, []int{1, 4}, record.Requirements{Graph: true})
	if states[0].Graph.Adjacency[0][2] == nil || states[1].Graph.Adjacency[0][1] == nil {
		t.Fatal("exact anchor did not replace the earlier rewired graph")
	}
	if err := reader.ReadStates([]int{3}, record.Requirements{Graph: true}, func(record.State) error { return nil }); err == nil {
		t.Fatal("ignored a malformed body within a required rewiring interval")
	}
}

func TestSparsePostWindowsSkipUnusedBodies(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
	meta["PostRetainCount"], meta["PostEvent"] = 1, true
	meta["HKParams"].(map[string]any)["RepostRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO events(type,agent_id,step) VALUES ('Post',0,2)"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	readStates(t, reader, []int{1, 4}, record.Requirements{Posts: true})
	if err := reader.ReadStates([]int{2}, record.Requirements{Posts: true}, func(record.State) error { return nil }); err == nil {
		t.Fatal("ignored a malformed body inside a required retention window")
	}
}

func TestExplicitNullMetadataIsNotAnImplicitZero(t *testing.T) {
	for _, field := range []string{"DataVersion", "RewiringRate", "RepostRate"} {
		t.Run(field, func(t *testing.T) {
			dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
			if field == "DataVersion" {
				meta[field] = nil
			} else {
				meta["HKParams"].(map[string]any)[field] = nil
			}
			writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
			reader, err := record.OpenRun(dir, nil)
			if field == "DataVersion" {
				if err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("accepted null %s: %v", field, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			err = reader.ReadStates([]int{4}, record.Requirements{Posts: true}, func(record.State) error { return nil })
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("accepted null %s: %v", field, err)
			}
		})
	}
}

func TestCompressedGraphChecksFooterAndTrailingObjects(t *testing.T) {
	for _, corruption := range []string{"checksum", "trailing_object"} {
		t.Run(corruption, func(t *testing.T) {
			dir, _ := writeRun(t, false, trajectory.Precision{}.Resolved())
			if err := os.Remove(filepath.Join(dir, "graph-0.msgpack")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "graph-0.msgpack.lz4")
			writeGraph(t, path)
			if corruption == "checksum" {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data[len(data)-1] ^= 1
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			} else {
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					t.Fatal(err)
				}
				writer := lz4.NewWriter(file)
				if err := msgpack.NewEncoder(writer).Encode("unexpected object"); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if err := reader.ReadStates([]int{0}, record.Requirements{Graph: true}, func(record.State) error { return nil }); err == nil {
				t.Fatal("accepted corrupt or trailing compressed graph data")
			}
		})
	}
}
