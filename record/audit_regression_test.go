package record_test

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"smp/model"
	"smp/record"
	"smp/simulation"
	"smp/trajectory"
)

func TestDuplicateRewiringProposalsCannotChangeOneActorTwiceInOneRound(t *testing.T) {
	for _, compatibility := range []bool{false, true} {
		dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
		meta["RewiringEvent"] = true
		meta["HKParams"].(map[string]any)["RewiringRate"] = .5
		writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
		actor, first, second := int64(0), int64(1), int64(2)
		if compatibility {
			actor, first, second = 1, 2, 0
		}
		db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, ends := range [][2]int64{{first, second}, {second, first}} {
			if err := db.StoreEvent(&model.EventRecord{Type: "Rewiring", AgentID: actor, Step: 1,
				Body: model.RewiringEventBody{AgentID: 0, Unfollow: ends[0], Follow: ends[1]}}); err != nil {
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
		defer reader.Close()
		called := false
		err = reader.ReadStates([]int{1}, record.Requirements{Graph: true, LegacyHeaderActor: compatibility}, func(record.State) error { called = true; return nil })
		if err == nil || !strings.Contains(err.Error(), "duplicate Rewiring") || called {
			t.Fatalf("duplicate proposal was replayed as real topology: error=%v called=%v", err, called)
		}
		// A failed replay owns only its discarded graph; it must not poison a
		// subsequent independent request on the same reader.
		states := readStates(t, reader, []int{0}, record.Requirements{Graph: true})
		if states[0].Graph.Adjacency[actor][first] == nil {
			t.Fatal("failed replay mutated the next request's anchor")
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObservationGuardRecoversFromNestedAndCallbackErrors(t *testing.T) {
	dir, _ := writeRun(t, false, trajectory.Precision{}.Resolved())
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	stop := errors.New("callback stopped")
	err = reader.ReadStatesBorrowed([]int{0, 4}, record.Requirements{}, func(record.State) error {
		nested := reader.ReadStates([]int{4}, record.Requirements{}, func(record.State) error { t.Fatal("reentered an active reader"); return nil })
		if nested == nil || !strings.Contains(nested.Error(), "active observation") {
			t.Fatalf("nested read was not rejected before touching caches/SQL: %v", nested)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("callback error was lost: %v", err)
	}
	if err := reader.ReadStates([]int{-1}, record.Requirements{}, func(record.State) error { return nil }); err == nil {
		t.Fatal("invalid steps accepted")
	}
	states := readStates(t, reader, []int{0, 4}, record.Requirements{})
	if len(states) != 2 {
		t.Fatal("a failed read left the observation guard set")
	}
}

func TestConcurrentReadIsRejectedWithoutTouchingActiveBuffers(t *testing.T) {
	dir, _ := writeRun(t, false, trajectory.Precision{}.Resolved())
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	done := make(chan error, 1)
	go func() {
		done <- reader.ReadStatesBorrowed([]int{0}, record.Requirements{}, func(record.State) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("initial observation stopped before callback: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("initial observation did not reach callback")
	}
	err = reader.ReadStates([]int{4}, record.Requirements{}, func(record.State) error { t.Fatal("concurrent read entered active buffers"); return nil })
	if err == nil || !strings.Contains(err.Error(), "active observation") {
		t.Fatalf("concurrent read was not rejected: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(readStates(t, reader, []int{4}, record.Requirements{})) != 1 {
		t.Fatal("completed observation did not release guard")
	}
}

func TestRewiringActorsCanRepeatInDifferentRounds(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
	meta["RewiringEvent"] = true
	meta["HKParams"].(map[string]any)["RewiringRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	for i, ends := range [][2]int64{{1, 2}, {2, 1}} {
		if err := db.StoreEvent(&model.EventRecord{Type: "Rewiring", AgentID: 0, Step: i + 1, Body: model.RewiringEventBody{AgentID: 0, Unfollow: ends[0], Follow: ends[1]}}); err != nil {
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
	defer reader.Close()
	states := readStates(t, reader, []int{1, 2}, record.Requirements{Graph: true})
	if states[0].Graph.Adjacency[0][2] == nil || states[1].Graph.Adjacency[0][1] == nil {
		t.Fatal("duplicate check leaked between rounds")
	}
}

func TestDuplicateRepostsInOneRoundAreAlreadyRejected(t *testing.T) {
	dir, meta := writeRun(t, false, trajectory.Precision{}.Resolved())
	meta["PostEvent"] = true
	meta["HKParams"].(map[string]any)["RepostRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.StoreEvent(&model.EventRecord{Type: "Post", AgentID: 1, Step: 1, Body: model.PostEventBody[float64]{IsRepost: true, Record: &model.PostRecord[float64]{AgentID: 0, Step: -1, Opinion: -.5}}}); err != nil {
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
	defer reader.Close()
	called := false
	err = reader.ReadStates([]int{1}, record.Requirements{Posts: true, PostsOptional: true}, func(record.State) error { called = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "duplicate Post") || called {
		t.Fatalf("duplicate display updates became a real post history: error=%v called=%v", err, called)
	}
	state := readStates(t, reader, []int{0}, record.Requirements{Posts: true})[0]
	if len(state.Posts[1]) != 1 || state.Posts[1][0].Step != -1 {
		t.Fatal("failed post replay changed initial post/retention semantics")
	}
}
