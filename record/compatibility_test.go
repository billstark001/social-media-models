package record_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"smp/model"
	"smp/record"
	"smp/simulation"
	"smp/trajectory"
)

func compatibilityRun(t *testing.T, bodyActor, unfollow, follow int64) string {
	t.Helper()
	dir, meta := writeRun(t, true, trajectory.Precision{})
	meta["RewiringEvent"] = true
	meta["HKParams"].(map[string]any)["RewiringRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.StoreEvent(&model.EventRecord{Type: "Rewiring", AgentID: 2, Step: 1,
		Body: model.RewiringEventBody{AgentID: bodyActor, Unfollow: unfollow, Follow: follow}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLegacyHeaderActorIsNarrowExplicitAndReadOnly(t *testing.T) {
	dir := compatibilityRun(t, 0, 0, 1)
	before := fingerprints(t, dir)
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	observe := func(legacy bool) ([]record.State, error) {
		var states []record.State
		err := reader.ReadStates([]int{1, 4}, record.Requirements{Graph: true, LegacyHeaderActor: legacy}, func(state record.State) error { states = append(states, state); return nil })
		return states, err
	}
	if _, err := observe(false); err == nil || !strings.Contains(err.Error(), "actor differs") {
		t.Fatalf("default reader accepted migrated actor defect: %v", err)
	}
	states, err := observe(true)
	if err != nil {
		t.Fatal(err)
	}
	if states[0].Graph.Adjacency[2][1] == nil || states[0].Graph.Adjacency[2][0] != nil || states[0].Graph.Adjacency[0][1] == nil {
		t.Fatal("compatibility replay used body actor rather than header actor")
	}
	if _, err := observe(false); err == nil || !strings.Contains(err.Error(), "actor differs") {
		t.Fatalf("explicit compatibility leaked into later strict call: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if after := fingerprints(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("compatibility mode rewrote source data")
	}
}

func TestLegacyHeaderActorPreservesAllOtherReplayChecks(t *testing.T) {
	for name, test := range map[string]struct {
		body, unfollow, follow int64
		want                   string
	}{
		"canonical":        {2, 0, 1, ""},
		"nonzero_mismatch": {1, 0, 1, "actor differs"},
		"missing_unfollow": {0, 1, 0, "missing edge"},
		"self_follow":      {0, 0, 2, "invalid agents"},
		"out_of_range":     {0, 0, 3, "invalid agents"},
	} {
		t.Run(name, func(t *testing.T) {
			reader, err := record.OpenRun(compatibilityRun(t, test.body, test.unfollow, test.follow), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			err = reader.ReadStates([]int{1}, record.Requirements{Graph: true, LegacyHeaderActor: true}, func(record.State) error { return nil })
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("want %q, got %v", test.want, err)
			}
		})
	}
}

func TestOptionalPostsDistinguishesMissingCollectionFromCorruption(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"missing_retention":  func(meta map[string]any) { delete(meta, "PostRetainCount") },
		"missing_collection": func(meta map[string]any) { meta["HKParams"].(map[string]any)["RepostRate"] = .5 },
	} {
		t.Run(name, func(t *testing.T) {
			dir, meta := writeRun(t, true, trajectory.Precision{})
			mutate(meta)
			writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if err := reader.ReadStates([]int{1}, record.Requirements{Posts: true}, func(record.State) error { return nil }); err == nil {
				t.Fatal("force-style strict posts unexpectedly succeeded")
			}
			states := readStates(t, reader, []int{0, 1}, record.Requirements{Posts: true, PostsOptional: true})
			for _, state := range states {
				if state.Posts != nil || len(state.Graph.Nodes) != 3 {
					t.Fatal("missing post channel was invented or removed graph")
				}
			}
		})
	}
	for name, mutate := range map[string]func(map[string]any){
		"invalid_retention":       func(meta map[string]any) { meta["PostRetainCount"] = -1 },
		"invalid_rate":            func(meta map[string]any) { meta["HKParams"].(map[string]any)["RepostRate"] = "bad" },
		"invalid_collection_flag": func(meta map[string]any) { meta["PostEvent"] = "yes" },
		"advertised_missing_database": func(meta map[string]any) {
			meta["HKParams"].(map[string]any)["RepostRate"] = .5
			meta["PostEvent"] = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, meta := writeRun(t, true, trajectory.Precision{})
			mutate(meta)
			writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				return // Invalid typed metadata can fail before any channel is read.
			}
			defer reader.Close()
			if err := reader.ReadStates([]int{1}, record.Requirements{Posts: true, PostsOptional: true}, func(record.State) error { return nil }); err == nil {
				t.Fatal("corrupt or falsely advertised post channel was silently downgraded")
			}
		})
	}
	// An actually attempted corrupt database must fail, rather than downgrade.
	dir, meta := writeRun(t, true, trajectory.Precision{})
	meta["PostEvent"] = true
	meta["HKParams"].(map[string]any)["RepostRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	if err := os.WriteFile(filepath.Join(dir, "events.db"), []byte("not sqlite"), 0644); err != nil {
		t.Fatal(err)
	}
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := reader.ReadStates([]int{1}, record.Requirements{Posts: true, PostsOptional: true}, func(record.State) error { return nil }); err == nil {
		t.Fatal("corrupt post database was silently downgraded")
	}
}
