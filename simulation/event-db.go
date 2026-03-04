package simulation

import (
	"database/sql"
	"fmt"
	model "smp/model"
	"sync"

	_ "github.com/mattn/go-sqlite3"
	"github.com/vmihailenco/msgpack/v5"
)

type EventDB struct {
	db        *sql.DB
	batchSize int
	mu        sync.Mutex
	cache     []*model.EventRecord

	eventStmt      *sql.Stmt
	rewiringStmt   *sql.Stmt
	postStmt       *sql.Stmt
	viewPostsStmt  *sql.Stmt
}

// OpenEventDB opens a database from a file and specifies the batch write size.
func OpenEventDB(filename string, batchSize int) (*EventDB, error) {
	db, err := sql.Open("sqlite3", filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			agent_id INTEGER NOT NULL,
			step INTEGER NOT NULL
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create events table: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS rewiring_events (
			event_id INTEGER PRIMARY KEY,
			unfollow INTEGER NOT NULL,
			follow INTEGER NOT NULL,
			FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create rewiring_events table: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS post_events (
			event_id INTEGER PRIMARY KEY,
			agent_id INTEGER NOT NULL,
			step INTEGER NOT NULL,
			opinion REAL NOT NULL,
			is_repost BOOLEAN NOT NULL,
			FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create post_events table: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS view_posts_events (
			event_id INTEGER PRIMARY KEY,
			data BLOB NOT NULL,
			FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create view_posts_events table: %w", err)
	}

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	eventStmt, err := db.Prepare("INSERT INTO events (type, agent_id, step) VALUES (?, ?, ?)")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to prepare event insert: %w", err)
	}

	rewiringStmt, err := db.Prepare("INSERT INTO rewiring_events (event_id, unfollow, follow) VALUES (?, ?, ?)")
	if err != nil {
		eventStmt.Close()
		db.Close()
		return nil, fmt.Errorf("failed to prepare rewiring insert: %w", err)
	}

	postStmt, err := db.Prepare("INSERT INTO post_events (event_id, agent_id, step, opinion, is_repost) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		eventStmt.Close()
		rewiringStmt.Close()
		db.Close()
		return nil, fmt.Errorf("failed to prepare post insert: %w", err)
	}

	viewPostsStmt, err := db.Prepare("INSERT INTO view_posts_events (event_id, data) VALUES (?, ?)")
	if err != nil {
		eventStmt.Close()
		rewiringStmt.Close()
		postStmt.Close()
		db.Close()
		return nil, fmt.Errorf("failed to prepare view_posts insert: %w", err)
	}

	return &EventDB{
		db:            db,
		batchSize:     batchSize,
		cache:         make([]*model.EventRecord, 0, batchSize),
		eventStmt:     eventStmt,
		rewiringStmt:  rewiringStmt,
		postStmt:      postStmt,
		viewPostsStmt: viewPostsStmt,
	}, nil
}

func (edb *EventDB) Close() error {
	edb.Flush()
	edb.mu.Lock()
	defer edb.mu.Unlock()
	edb.eventStmt.Close()
	edb.rewiringStmt.Close()
	edb.postStmt.Close()
	edb.viewPostsStmt.Close()
	return edb.db.Close()
}

func (edb *EventDB) StoreEvent(event *model.EventRecord) error {
	edb.mu.Lock()
	defer edb.mu.Unlock()
	edb.cache = append(edb.cache, event)
	if len(edb.cache) >= edb.batchSize {
		return edb.flushLocked()
	}
	return nil
}

func (edb *EventDB) Flush() error {
	edb.mu.Lock()
	defer edb.mu.Unlock()
	return edb.flushLocked()
}

func (edb *EventDB) flushLocked() error {
	if len(edb.cache) == 0 {
		return nil
	}
	tx, err := edb.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	eventIDs := make([]int64, len(edb.cache))
	for i, event := range edb.cache {
		res, err := tx.Stmt(edb.eventStmt).Exec(event.Type, event.AgentID, event.Step)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert event: %w", err)
		}
		eventID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to get last insert ID: %w", err)
		}
		eventIDs[i] = eventID
	}

	for i, event := range edb.cache {
		switch event.Type {
		case "Rewiring":
			if body, ok := event.Body.(model.RewiringEventBody); ok {
				_, err := tx.Stmt(edb.rewiringStmt).Exec(eventIDs[i], body.Unfollow, body.Follow)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to insert rewiring event: %w", err)
				}
			} else {
				tx.Rollback()
				return fmt.Errorf("invalid RewiringEventBody type")
			}
		case "Post":
			if body, ok := event.Body.(model.PostEventBody[float64]); ok {
				if body.Record == nil {
					tx.Rollback()
					return fmt.Errorf("post record is nil")
				}
				_, err := tx.Stmt(edb.postStmt).Exec(eventIDs[i], body.Record.AgentID, body.Record.Step, body.Record.Opinion, body.IsRepost)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to insert post event: %w", err)
				}
			} else {
				tx.Rollback()
				return fmt.Errorf("invalid PostEventBody type")
			}
		case "ViewPosts":
			if body, ok := event.Body.(model.ViewPostsEventBody[float64]); ok {
				data, err := msgpack.Marshal(body)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to marshal ViewPostsEventBody: %w", err)
				}
				_, err = tx.Stmt(edb.viewPostsStmt).Exec(eventIDs[i], data)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to insert view posts event: %w", err)
				}
			} else {
				tx.Rollback()
				return fmt.Errorf("invalid ViewPostsEventBody type")
			}
		default:
			tx.Rollback()
			return fmt.Errorf("unknown event type: %s", event.Type)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	edb.cache = edb.cache[:0]
	return nil
}

func (edb *EventDB) DeleteEventsAfterStep(step int) error {
	edb.Flush()
	edb.mu.Lock()
	defer edb.mu.Unlock()
	_, err := edb.db.Exec("DELETE FROM events WHERE step >= ?", step)
	if err != nil {
		return fmt.Errorf("failed to delete events: %w", err)
	}
	return nil
}

func (edb *EventDB) GetEvents() ([]*model.EventRecord, error) {
	rows, err := edb.db.Query(`
		SELECT e.id, e.type, e.agent_id, e.step FROM events e
		ORDER BY e.step ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []*model.EventRecord
	for rows.Next() {
		var id int64
		event := &model.EventRecord{}
		err := rows.Scan(&id, &event.Type, &event.AgentID, &event.Step)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}

		switch event.Type {
		case "Rewiring":
			var body model.RewiringEventBody
			err = edb.db.QueryRow(
				"SELECT unfollow, follow FROM rewiring_events WHERE event_id = ?", id,
			).Scan(&body.Unfollow, &body.Follow)
			if err != nil {
				return nil, fmt.Errorf("failed to scan rewiring event: %w", err)
			}
			event.Body = body

		case "Post":
			var body model.PostEventBody[float64]
			var agentID, step int64
			var opinion float64
			var isRepost bool

			err = edb.db.QueryRow(
				"SELECT agent_id, step, opinion, is_repost FROM post_events WHERE event_id = ?", id,
			).Scan(&agentID, &step, &opinion, &isRepost)
			if err != nil {
				return nil, fmt.Errorf("failed to scan post event: %w", err)
			}

			body.Record = &model.PostRecord[float64]{
				AgentID: agentID,
				Step:    int(step),
				Opinion: opinion,
			}
			body.IsRepost = isRepost
			event.Body = body

		case "ViewPosts":
			var data []byte
			err = edb.db.QueryRow(
				"SELECT data FROM view_posts_events WHERE event_id = ?", id,
			).Scan(&data)
			if err != nil {
				return nil, fmt.Errorf("failed to scan view posts event: %w", err)
			}

			var body model.ViewPostsEventBody[float64]
			err = msgpack.Unmarshal(data, &body)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal view posts event: %w", err)
			}
			event.Body = body
		}

		events = append(events, event)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating events: %w", err)
	}

	return events, nil
}
