package simulation

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"smp/model"
	"smp/utils"
)

func eventDBTestDirectory(t *testing.T) string {
	t.Helper()
	root := os.Getenv("SMP_EVENT_DB_TEST_DIR")
	if root == "" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp(root, "smp-event-db-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}

func readOnlyEventDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	uri := utils.FileURI(path)
	uri.RawQuery = url.Values{"mode": {"ro"}, "_query_only": {"1"}}.Encode()
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEventDBWALSettingsCoverEveryPooledConnection(t *testing.T) {
	name := "events ? #&.db"
	if runtime.GOOS == "windows" {
		name = "events #&.db" // '?' is not a legal Windows filename character.
	}
	path := filepath.Join(eventDBTestDirectory(t), name)
	db, err := OpenEventDB(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, conn := range []*sql.Conn{first, second} {
		var journal string
		var foreignKeys, synchronous int
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatal(err)
		}
		if journal != "wal" || foreignKeys != 1 || synchronous != 2 {
			t.Fatalf("unexpected pooled SQLite settings: journal=%q foreign_keys=%d synchronous=%d", journal, foreignKeys, synchronous)
		}
	}
}

func TestEventDBWALSupportsReopenReplayAndReadOnlyConnections(t *testing.T) {
	path := filepath.Join(eventDBTestDirectory(t), "events.db")
	db, err := OpenEventDB(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	events := []*model.EventRecord{
		{Type: "Post", AgentID: 0, Step: 1, Body: model.PostEventBody[float64]{Record: &model.PostRecord[float64]{AgentID: 0, Step: 1, Opinion: .25}}},
		{Type: "Rewiring", AgentID: 0, Step: 1, Body: model.RewiringEventBody{AgentID: 0, Unfollow: 1, Follow: 2}},
		{Type: "ViewPosts", AgentID: 0, Step: 1, Body: model.ViewPostsEventBody[float64]{}},
	}
	for _, event := range events {
		if err := db.StoreEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	actual, err := db.GetEvents()
	if err != nil || len(actual) != len(events) {
		t.Fatalf("nested event replay: %d events, error=%v", len(actual), err)
	}
	reader := readOnlyEventDB(t, path)
	var count int
	if err := reader.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil || count != 3 {
		t.Fatalf("WAL reader: count=%d error=%v", count, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenEventDB(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	tail := &model.EventRecord{Type: "Post", AgentID: 1, Step: 2, Body: model.PostEventBody[float64]{Record: &model.PostRecord[float64]{AgentID: 1, Step: 2, Opinion: -.25}}}
	if err := db.StoreEvent(tail); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteEventsAfterStep(2); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reader = readOnlyEventDB(t, path)
	if err := reader.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil || count != 3 {
		t.Fatalf("closed WAL reader: count=%d error=%v", count, err)
	}
	if err := reader.QueryRow("SELECT count(*) FROM post_events").Scan(&count); err != nil || count != 1 {
		t.Fatalf("foreign-key cascade: count=%d error=%v", count, err)
	}
}

func TestEventDBCloseAndDeleteReturnBufferedWriteFailures(t *testing.T) {
	for _, operation := range []string{"close", "delete"} {
		t.Run(operation, func(t *testing.T) {
			db, err := OpenEventDB(filepath.Join(t.TempDir(), "events.db"), 100)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.StoreEvent(&model.EventRecord{Type: "unknown", Step: 1}); err != nil {
				t.Fatal(err)
			}
			if operation == "close" {
				err = db.Close()
			} else {
				err = db.DeleteEventsAfterStep(0)
				defer db.Close()
				var count int
				if queryErr := db.db.QueryRow("SELECT count(*) FROM events").Scan(&count); queryErr != nil || count != 0 {
					t.Fatalf("invalid batch was not rolled back: count=%d error=%v", count, queryErr)
				}
			}
			if err == nil || !strings.Contains(err.Error(), "unknown event type") {
				t.Fatalf("lost write failure: %v", err)
			}
		})
	}
}
