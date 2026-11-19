package record

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestStepWindowPredicateHandlesManyDisjointRanges(t *testing.T) {
	// A flat OR expression exceeds SQLite's expression-depth limit here.
	// The bounded query must continue to select only required body intervals.
	windows := make([][2]int, 2000)
	for index := range windows {
		windows[index] = [2]int{3*index + 1, 3*index + 2}
	}
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	err = db.QueryRow(`WITH RECURSIVE e(step) AS (VALUES(0) UNION ALL SELECT step+1 FROM e WHERE step<6000)
		SELECT COUNT(*) FROM e WHERE (` + stepWindowPredicate(windows) + `)`).Scan(&count)
	if err != nil || count != 4000 {
		t.Fatalf("disjoint-window predicate failed: count=%d error=%v", count, err)
	}
}

func TestGraphReplayWindowsExcludeExactAnchorsAndUnusedGaps(t *testing.T) {
	g := graphReader{reader: &Reader{anchors: []int{0, 4, 8}}, steps: []int{0, 1, 2, 4, 5, 8, 10}}
	want := [][2]int{{1, 2}, {5, 5}, {9, 10}}
	if actual := g.replayWindows(); !reflect.DeepEqual(actual, want) {
		t.Fatalf("graph replay intervals = %v, want %v", actual, want)
	}
}
