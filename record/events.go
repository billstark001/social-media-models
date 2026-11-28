package record

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"

	"smp/model"
	"smp/utils"

	_ "github.com/mattn/go-sqlite3"
)

func (r *Reader) eventDB() (*sql.DB, error) {
	if r.db != nil {
		return r.db, nil
	}
	path, err := filepath.Abs(filepath.Join(r.dir, "events.db"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("required event database is unavailable: %w", err)
	}
	uri := utils.FileURI(path)
	query := url.Values{"mode": {"ro"}, "_query_only": {"1"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open read-only events: %w", err)
	}
	// Graph and post streams may remain open together.
	db.SetMaxOpenConns(2)
	var old string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('tweet_events','view_tweets_events') LIMIT 1").Scan(&old)
	if err == nil {
		db.Close()
		return nil, fmt.Errorf("obsolete event table %s; run smp-migrate events on this database", old)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		db.Close()
		return nil, fmt.Errorf("inspect event schema: %w", err)
	}
	r.db = db
	return db, nil
}

type rewiringCursor struct {
	rows                                   *sql.Rows
	id, actor, bodyActor, unfollow, follow int64
	step                                   int
	ready                                  bool
	legacyHeaderActor                      bool
	seenStep                               int
	seenActors                             map[int64]struct{}
}

func (r *Reader) openRewiring(windows [][2]int, legacyHeaderActor bool) (*rewiringCursor, error) {
	db, err := r.eventDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT e.id,e.step,e.agent_id,r.agent_id,r.unfollow,r.follow
		FROM events AS e LEFT JOIN rewiring_events AS r ON r.event_id=e.id
		WHERE e.type='Rewiring' AND e.step>=? AND e.step<=? AND (`+stepWindowPredicate(windows)+`) ORDER BY e.step,e.id`, windows[0][0], windows[len(windows)-1][1])
	if err != nil {
		return nil, fmt.Errorf("read canonical rewiring events (migrate legacy schemas with smp-migrate): %w", err)
	}
	cursor := &rewiringCursor{rows: rows, legacyHeaderActor: legacyHeaderActor}
	if err := cursor.next(); err != nil {
		cursor.close()
		return nil, err
	}
	return cursor, nil
}

func (c *rewiringCursor) close() { c.rows.Close() }

func (c *rewiringCursor) next() error {
	c.ready = c.rows.Next()
	if !c.ready {
		return c.rows.Err()
	}
	var actor, unfollow, follow sql.NullInt64
	if err := c.rows.Scan(&c.id, &c.step, &c.actor, &actor, &unfollow, &follow); err != nil {
		return fmt.Errorf("read rewiring body: %w", err)
	}
	if !actor.Valid || !unfollow.Valid || !follow.Valid {
		return fmt.Errorf("rewiring event %d has no complete body", c.id)
	}
	c.bodyActor, c.unfollow, c.follow = actor.Int64, unfollow.Int64, follow.Int64
	return nil
}

func (c *rewiringCursor) apply(graph *utils.NetworkXGraph, start, end, agents int) error {
	valid := func(id int64) bool { return id >= 0 && id < int64(agents) }
	for c.ready && c.step <= end {
		if c.step > start {
			if c.actor != c.bodyActor && !(c.legacyHeaderActor && c.bodyActor == 0) {
				return fmt.Errorf("rewiring event %d actor differs from its header; run smp-migrate events", c.id)
			}
			if !valid(c.actor) || !valid(c.unfollow) || !valid(c.follow) || c.actor == c.follow || c.actor == c.unfollow {
				return fmt.Errorf("invalid agents in rewiring event %d", c.id)
			}
			if c.seenActors == nil {
				c.seenActors = make(map[int64]struct{})
			}
			if c.seenStep != c.step {
				clear(c.seenActors)
				c.seenStep = c.step
			}
			if _, seen := c.seenActors[c.actor]; seen {
				return fmt.Errorf("duplicate Rewiring proposal for agent %d at step %d", c.actor, c.step)
			}
			c.seenActors[c.actor] = struct{}{}
			targets := graph.Adjacency[c.actor]
			if _, exists := targets[c.unfollow]; !exists {
				return fmt.Errorf("rewiring event %d removes missing edge %d to %d", c.id, c.actor, c.unfollow)
			}
			if _, exists := targets[c.follow]; !exists {
				delete(targets, c.unfollow)
				targets[c.follow] = map[string]any{}
			}
		}
		if err := c.next(); err != nil {
			return err
		}
	}
	return nil
}

type postCursor struct {
	rows      *sql.Rows
	id, actor int64
	step      int
	post      model.PostRecord[float64]
	repost    bool
	ready     bool
}

func postWindows(steps []int, retain int) [][2]int {
	var windows [][2]int
	for _, step := range steps {
		if step == 0 {
			continue
		}
		start := max(1, step-retain+1)
		if last := len(windows) - 1; last >= 0 && start <= windows[last][1]+1 {
			windows[last][1] = step
		} else {
			windows = append(windows, [2]int{start, step})
		}
	}
	return windows
}

// A balanced CASE selects disjoint windows in logarithmic expression depth
// without building an OR chain or repeating unindexed event-table scans. Only
// validated integer step bounds enter this SQL; no external strings do.
func stepWindowPredicate(windows [][2]int) string {
	if len(windows) == 1 {
		return fmt.Sprintf("e.step BETWEEN %d AND %d", windows[0][0], windows[0][1])
	}
	middle := len(windows) / 2
	return fmt.Sprintf("CASE WHEN e.step<%d THEN (%s) ELSE (%s) END", windows[middle][0], stepWindowPredicate(windows[:middle]), stepWindowPredicate(windows[middle:]))
}

func (r *Reader) openPosts(windows [][2]int) (*postCursor, error) {
	db, err := r.eventDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT e.id,e.type,e.step,e.agent_id,p.agent_id,p.step,p.opinion,p.is_repost
		FROM events AS e LEFT JOIN post_events AS p ON p.event_id=e.id
		WHERE e.type IN ('Post','Tweet') AND e.step>=? AND e.step<=? AND (`+stepWindowPredicate(windows)+`) ORDER BY e.step,e.id`, windows[0][0], windows[len(windows)-1][1])
	if err != nil {
		return nil, fmt.Errorf("read canonical Post events (migrate legacy schemas with smp-migrate): %w", err)
	}
	cursor := &postCursor{rows: rows}
	if err := cursor.next(); err != nil {
		cursor.close()
		return nil, err
	}
	return cursor, nil
}

func (c *postCursor) close() { c.rows.Close() }

func (c *postCursor) next() error {
	c.ready = c.rows.Next()
	if !c.ready {
		return c.rows.Err()
	}
	var kind string
	var author, step sql.NullInt64
	var opinion sql.NullFloat64
	var repost sql.NullBool
	if err := c.rows.Scan(&c.id, &kind, &c.step, &c.actor, &author, &step, &opinion, &repost); err != nil {
		return fmt.Errorf("read Post body (migrate obsolete is_retweet with smp-migrate): %w", err)
	}
	if kind == "Tweet" {
		return fmt.Errorf("obsolete Tweet event %d; run smp-migrate events", c.id)
	}
	if !author.Valid || !step.Valid || !opinion.Valid || !repost.Valid {
		return fmt.Errorf("Post event %d has no complete body", c.id)
	}
	c.post = model.PostRecord[float64]{AgentID: author.Int64, Step: int(step.Int64), Opinion: opinion.Float64}
	c.repost = repost.Bool
	return nil
}

func (c *postCursor) at(step, agents int) (map[int64]model.PostRecord[float64], error) {
	posts := make(map[int64]model.PostRecord[float64])
	seen := make(map[int64]bool)
	for c.ready && c.step == step {
		if c.actor < 0 || c.actor >= int64(agents) || c.post.AgentID < 0 || c.post.AgentID >= int64(agents) || math.IsNaN(c.post.Opinion) || math.IsInf(c.post.Opinion, 0) {
			return nil, fmt.Errorf("invalid Post event %d", c.id)
		}
		if seen[c.actor] {
			return nil, fmt.Errorf("duplicate Post for agent %d at step %d", c.actor, step)
		}
		seen[c.actor] = true
		if (!c.repost && (c.post.AgentID != c.actor || c.post.Step != step)) || (c.repost && (c.post.Step < -1 || c.post.Step >= step)) {
			return nil, fmt.Errorf("Post event %d has inconsistent origin or step", c.id)
		}
		// Scenario persists only reposts. Ordinary posts are represented by
		// the post-commit opinion row; a missing event is an ordinary post.
		if c.repost {
			posts[c.actor] = c.post
		}
		if err := c.next(); err != nil {
			return nil, err
		}
	}
	return posts, nil
}
