# Migration Guide — v1 → v2

This guide covers breaking changes introduced in the v2 refactor and explains how to update existing simulation records and code.

---

## Breaking Changes Summary

| Area | v1 | v2 |
|------|----|----|
| Post type | `TweetRecord{AgentID, Step, Opinion float64}` | `PostRecord[O]{AgentID, Step, Opinion O}` |
| Repost type | `TweetEventBody{Record, IsRetweet}` | `PostEventBody[O]{Record, IsRepost}` |
| View-posts type | `ViewTweetsEventBody` | `ViewPostsEventBody[O]` |
| Agent params | `model.SMPAgentParams{Tolerance, Decay, RewiringRate, RetweetRate}` | `dynamics.HKParams{Tolerance, Influence, RewiringRate, RepostRate}` |
| Dynamics | baked into `SMPAgentStep` | `dynamics.HK` / `dynamics.Deffuant` implementing `model.Dynamics[O,P]` |
| Field: retain count | `SMPModelPureParams.TweetRetainCount` | `SMPModelPureParams.PostRetainCount` |
| Field: decay/influence | `SMPAgentParams.Decay` | `HKParams.Influence` |
| Field: repost rate | `SMPAgentParams.RetweetRate` | `HKParams.RepostRate` |
| Collect option | `CollectItemOptions.ViewTweetsEvent` | `CollectItemOptions.ViewPostsEvent` |
| Collect option | `CollectItemOptions.TweetEvent` | `CollectItemOptions.PostEvent` |
| Model collect | `CollectTweets()` | `CollectPosts()` |
| Grid field | `NetworkGrid.TweetMap` | `NetworkGrid.PostMap` |
| Grid method | `AddTweet` | `AddPost` |
| Model init func | `NewSMPModel` (opinions `[]float64`) | `NewSMPModelFloat64` or generic `NewSMPModel[O,P]` |
| SQLite table | `tweet_events`, `is_retweet` | `post_events`, `is_repost` |
| Event type string | `"Tweet"` | `"Post"` |
| Package | _none_ | new `smp/dynamics` package |

---

## Migrating Saved Simulation Records

Snapshot files (`.msgpack`) contain a serialized `SMPModelDumpData`. The relevant field renames are:

| Old field name | New field name |
|---|---|
| `Tweets` | `Posts` |
| `SMPModelPureParams.TweetRetainCount` | `SMPModelPureParams.PostRetainCount` |

### One-time msgpack rename script (Python)

```python
import msgpack, sys, pathlib

def migrate_snapshot(path: str):
    data = pathlib.Path(path).read_bytes()
    obj = msgpack.unpackb(data, raw=False)

    # Rename Tweets → Posts at the top level
    if b"Tweets" in obj:
        obj[b"Posts"] = obj.pop(b"Tweets")

    pathlib.Path(path).write_bytes(msgpack.packb(obj))
    print(f"migrated {path}")

for p in sys.argv[1:]:
    migrate_snapshot(p)
```

Usage:

```bash
python migrate_snapshot.py ./run/my-sim/snapshot-*.msgpack
```

### Metadata JSON

If you have `metadata.json` files that embed `SMPAgentParams`, rename the field keys:

| Old JSON key | New JSON key |
|---|---|
| `Decay` | `Influence` |
| `RetweetRate` | `RepostRate` |
| `TweetRetainCount` | `PostRetainCount` |
| `TweetEvent` | `PostEvent` |
| `ViewTweetsEvent` | `ViewPostsEvent` |

Quick sed command:

```bash
sed -i \
  -e 's/"Decay"/"Influence"/g' \
  -e 's/"RetweetRate"/"RepostRate"/g' \
  -e 's/"TweetRetainCount"/"PostRetainCount"/g' \
  -e 's/"TweetEvent"/"PostEvent"/g' \
  -e 's/"ViewTweetsEvent"/"ViewPostsEvent"/g' \
  ./run/*/metadata.json
```

### Accumulative state files (`.lz4`)

The binary `.lz4` format is **unchanged** — dimensions and element types are identical. No migration needed.

### SQLite event database

The table and column names changed. To migrate:

```sql
-- run inside the events.db file
ALTER TABLE tweet_events RENAME TO post_events;
ALTER TABLE post_events RENAME COLUMN is_retweet TO is_repost;

-- update the type strings in the events table
UPDATE events SET type = 'Post' WHERE type = 'Tweet';
UPDATE events SET type = 'ViewPosts' WHERE type = 'ViewTweets';
```

---

## Migrating Go Code

### 1. Replace `model.SMPAgentParams` with `dynamics.HKParams`

```go
// v1
params := &model.SMPAgentParams{Tolerance: 0.3, Decay: 0.5, RetweetRate: 0.2, RewiringRate: 0.1}

// v2
import "smp/dynamics"
params := &dynamics.HKParams{Tolerance: 0.3, Influence: 0.5, RepostRate: 0.2, RewiringRate: 0.1}
```

### 2. Pass a `Dynamics` implementation to `NewSMPModel`

```go
// v2 — HK dynamics
m := model.NewSMPModelFloat64(graph, nil, modelParams, agentParams, &dynamics.HK{}, collectOpts, nil)

// v2 — Deffuant dynamics
dParams := dynamics.DefaultDeffuantParams()
m := model.NewSMPModelFloat64(graph, nil, modelParams, dParams, &dynamics.Deffuant{}, collectOpts, nil)
```

### 3. Rename `TweetRetainCount` → `PostRetainCount`

```go
// v2
mp := &model.SMPModelParams[float64, dynamics.HKParams]{
    SMPModelPureParams: model.SMPModelPureParams{PostRetainCount: 3, RecsysCount: 10},
}
```

### 4. Use `CollectPosts()` instead of `CollectTweets()`

```go
posts := m.CollectPosts()   // map[int64][]PostRecord[float64]
```

### 5. Collect options rename

```go
// v2
opts := &model.CollectItemOptions{
    PostEvent:      true,   // was TweetEvent
    ViewPostsEvent: true,   // was ViewTweetsEvent
    AgentNumber:    true,
    OpinionSum:     true,
    RewiringEvent:  true,
}
```

### 6. Event type assertions in event handlers

```go
// v2 event handler
switch event.Type {
case "Post":
    body := event.Body.(model.PostEventBody[float64])
    _ = body.IsRepost
case "ViewPosts":
    body := event.Body.(model.ViewPostsEventBody[float64])
    _ = body.NeighborConcordant
case "Rewiring":
    body := event.Body.(model.RewiringEventBody)
}
```

---

## Implementing a Custom Dynamics Model

```go
package mydynamics

import (
    "smp/model"
)

type MyParams struct {
    Tolerance    float64
    RepostRate   float64
    RewiringRate float64
}

func (p *MyParams) GetRepostRate() float64   { return p.RepostRate }
func (p *MyParams) GetRewiringRate() float64 { return p.RewiringRate }

type MyDynamics struct{}

var _ model.Dynamics[float64, MyParams] = (*MyDynamics)(nil)

func (d *MyDynamics) Concordant(my, other float64, p *MyParams) bool {
    return my == other // example: exact agreement only
}

func (d *MyDynamics) Step(my float64, cN, cR, dN, dR []float64, p *MyParams) (float64, model.AgentOpinionSumRecord) {
    // custom update rule
    return my, model.AgentOpinionSumRecord{}
}
```
