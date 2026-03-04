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

	// 预编译语句
	eventStmt      *sql.Stmt
	rewiringStmt   *sql.Stmt
	tweetStmt      *sql.Stmt
	viewTweetsStmt *sql.Stmt
}

// OpenEventDB 从文件打开数据库，指定批量写入大小
func OpenEventDB(filename string, batchSize int) (*EventDB, error) {
	db, err := sql.Open("sqlite3", filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 创建事件表
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

	// 创建rewiring事件表
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

	// 创建tweet事件表
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS tweet_events (
			event_id INTEGER PRIMARY KEY,
			agent_id INTEGER NOT NULL,
			step INTEGER NOT NULL,
			opinion REAL NOT NULL,
			is_retweet BOOLEAN NOT NULL,
			FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tweet_events table: %w", err)
	}

	// 创建view_tweets事件表 (使用msgpack存储)
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS view_tweets_events (
			event_id INTEGER PRIMARY KEY,
			data BLOB NOT NULL,
			FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create view_tweets_events table: %w", err)
	}

	// 启用外键约束
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// 预编译语句
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

	tweetStmt, err := db.Prepare("INSERT INTO tweet_events (event_id, agent_id, step, opinion, is_retweet) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		eventStmt.Close()
		rewiringStmt.Close()
		db.Close()
		return nil, fmt.Errorf("failed to prepare tweet insert: %w", err)
	}

	viewTweetsStmt, err := db.Prepare("INSERT INTO view_tweets_events (event_id, data) VALUES (?, ?)")
	if err != nil {
		eventStmt.Close()
		rewiringStmt.Close()
		tweetStmt.Close()
		db.Close()
		return nil, fmt.Errorf("failed to prepare view_tweets insert: %w", err)
	}

	return &EventDB{
		db:             db,
		batchSize:      batchSize,
		cache:          make([]*model.EventRecord, 0, batchSize),
		eventStmt:      eventStmt,
		rewiringStmt:   rewiringStmt,
		tweetStmt:      tweetStmt,
		viewTweetsStmt: viewTweetsStmt,
	}, nil
}

// Close 关闭数据库连接及释放资源
func (edb *EventDB) Close() error {
	edb.Flush() // 确保所有缓存写入
	edb.mu.Lock()
	defer edb.mu.Unlock()
	edb.eventStmt.Close()
	edb.rewiringStmt.Close()
	edb.tweetStmt.Close()
	edb.viewTweetsStmt.Close()
	return edb.db.Close()
}

// StoreEvent 缓存事件，批量写入
func (edb *EventDB) StoreEvent(event *model.EventRecord) error {
	edb.mu.Lock()
	defer edb.mu.Unlock()

	edb.cache = append(edb.cache, event)
	if len(edb.cache) >= edb.batchSize {
		return edb.flushLocked()
	}
	return nil
}

// Flush 强制写入所有缓存事件
func (edb *EventDB) Flush() error {
	edb.mu.Lock()
	defer edb.mu.Unlock()
	return edb.flushLocked()
}

// flushLocked 需在持有锁情况下调用
func (edb *EventDB) flushLocked() error {
	if len(edb.cache) == 0 {
		return nil
	}
	tx, err := edb.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// 记录每个事件的ID
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

	// 插入各类型子表
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
		case "Tweet":
			if body, ok := event.Body.(model.TweetEventBody); ok {
				if body.Record == nil {
					tx.Rollback()
					return fmt.Errorf("tweet record is nil")
				}
				_, err := tx.Stmt(edb.tweetStmt).Exec(eventIDs[i], body.Record.AgentID, body.Record.Step, body.Record.Opinion, body.IsRetweet)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to insert tweet event: %w", err)
				}
			} else {
				tx.Rollback()
				return fmt.Errorf("invalid TweetEventBody type")
			}
		case "ViewTweets":
			if body, ok := event.Body.(model.ViewTweetsEventBody); ok {
				data, err := msgpack.Marshal(body)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to marshal ViewTweetsEventBody: %w", err)
				}
				_, err = tx.Stmt(edb.viewTweetsStmt).Exec(eventIDs[i], data)
				if err != nil {
					tx.Rollback()
					return fmt.Errorf("failed to insert view tweets event: %w", err)
				}
			} else {
				tx.Rollback()
				return fmt.Errorf("invalid ViewTweetsEventBody type")
			}
		default:
			tx.Rollback()
			return fmt.Errorf("unknown event type: %s", event.Type)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 清空缓存
	edb.cache = edb.cache[:0]
	return nil
}

// DeleteEventsAfterStep 删除步骤大于等于指定值的所有事件
func (edb *EventDB) DeleteEventsAfterStep(step int) error {
	edb.Flush() // 保证缓存已写入再删
	edb.mu.Lock()
	defer edb.mu.Unlock()
	_, err := edb.db.Exec("DELETE FROM events WHERE step >= ?", step)
	if err != nil {
		return fmt.Errorf("failed to delete events: %w", err)
	}
	return nil
}

// GetEvents 获取所有事件（示例如何从数据库加载事件）
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

		// 根据事件类型获取具体内容
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

		case "Tweet":
			var body model.TweetEventBody
			var agentID, step int64
			var opinion float64
			var isRetweet bool

			err = edb.db.QueryRow(
				"SELECT agent_id, step, opinion, is_retweet FROM tweet_events WHERE event_id = ?", id,
			).Scan(&agentID, &step, &opinion, &isRetweet)
			if err != nil {
				return nil, fmt.Errorf("failed to scan tweet event: %w", err)
			}

			body.Record = &model.TweetRecord{
				AgentID: agentID,
				Step:    int(step),
				Opinion: opinion,
			}
			body.IsRetweet = isRetweet
			event.Body = body

		case "ViewTweets":
			var data []byte
			err = edb.db.QueryRow(
				"SELECT data FROM view_tweets_events WHERE event_id = ?", id,
			).Scan(&data)
			if err != nil {
				return nil, fmt.Errorf("failed to scan view tweets event: %w", err)
			}

			var body model.ViewTweetsEventBody
			err = msgpack.Unmarshal(data, &body)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal view tweets event: %w", err)
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
