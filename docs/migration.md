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
| Dynamics | baked into `SMPAgentStep` | `dynamics.HK` / `dynamics.Deffuant` / `dynamics.Galam` / `dynamics.Voter` implementing `model.Dynamics[O,P]` |
| `ScenarioMetadata` dynamics params | embedded `dynamics.HKParams` | `DynamicsType string` + separate `HKParams`/`DeffuantParams`/`GalamParams`/`VoterParams` fields |
| `Scenario.Model` field type | `*SMPModel[float64, HKParams]` | `simulation.IModel` (type-assert to `*Float64ModelWrapper[P]` or `*BoolModelWrapper[P]` when needed) |
| Snapshot format | bare `SMPModelDumpData[float64, HKParams]` msgpack | `RawSnapshotData{DynamicsType, Data}` wrapping dynamics-specific msgpack bytes |
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

### One-time msgpack migration script (Python)

v2 snapshots are bare `SMPModelDumpData` msgpack blobs. v3 wraps them in a `RawSnapshotData{DynamicsType, Data}` envelope. Use the script below to migrate:

```python
import msgpack, sys, pathlib

def migrate_snapshot(path: str, dynamics_type: str = "HK"):
    data = pathlib.Path(path).read_bytes()
    inner = msgpack.unpackb(data, raw=False)

    # v1→v2: Rename Tweets → Posts at the top level
    if b"Tweets" in inner:
        inner[b"Posts"] = inner.pop(b"Tweets")

    # v2→v3: wrap in RawSnapshotData envelope
    inner_bytes = msgpack.packb(inner)
    envelope = {"DynamicsType": dynamics_type, "Data": inner_bytes}
    pathlib.Path(path).write_bytes(msgpack.packb(envelope))
    print(f"migrated {path}")

for p in sys.argv[1:]:
    migrate_snapshot(p)
```

Usage:

```bash
# HK is the default; pass --dynamics Deffuant if applicable
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

Also add the new `DynamicsType` key. Old simulations used HK dynamics, so:

```bash
# patch field renames and inject DynamicsType
sed -i \
  -e 's/"Decay"/"Influence"/g' \
  -e 's/"RetweetRate"/"RepostRate"/g' \
  -e 's/"TweetRetainCount"/"PostRetainCount"/g' \
  -e 's/"TweetEvent"/"PostEvent"/g' \
  -e 's/"ViewTweetsEvent"/"ViewPostsEvent"/g' \
  ./run/*/metadata.json

# inject DynamicsType if missing (jq)
jq '.DynamicsType //= "HK"' metadata.json > tmp.json && mv tmp.json metadata.json
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
// HK (float64 opinions)
m := model.NewSMPModelFloat64(graph, nil, modelParams, agentParams, &dynamics.HK{}, collectOpts, nil)

// Deffuant (float64 opinions)
dParams := dynamics.DefaultDeffuantParams()
m := model.NewSMPModelFloat64(graph, nil, modelParams, dParams, &dynamics.Deffuant{}, collectOpts, nil)

// Galam (bool opinions)
gParams := dynamics.DefaultGalamParams()
ops := make([]bool, nodeCount)  // initialise with random booleans as needed
m := model.NewSMPModel(graph, &ops, modelParams, gParams, &dynamics.Galam{}, collectOpts, nil)

// Voter (bool opinions)
vParams := dynamics.DefaultVoterParams()
m := model.NewSMPModel(graph, &ops, modelParams, vParams, &dynamics.Voter{}, collectOpts, nil)
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

For `float64`-opinion dynamics (HK, Deffuant):

```go
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

For `bool`-opinion dynamics (Galam, Voter), use `PostEventBody[bool]` / `ViewPostsEventBody[bool]` instead. The event DB always stores bool opinions as `0.0` (false) / `1.0` (true) in the `opinion` column.

### 7. Accessing the underlying model from `Scenario`

`Scenario.Model` is now the `simulation.IModel` interface. Type-assert to the concrete wrapper when you need direct model access:

```go
// HK example
if w, ok := scenario.Model.(*simulation.Float64ModelWrapper[dynamics.HKParams]); ok {
    ops := w.M.CollectOpinions()
}

// Voter example
if w, ok := scenario.Model.(*simulation.BoolModelWrapper[dynamics.VoterParams]); ok {
    _ = w.M.CollectOpinions()
}
```

### 8. Setting `DynamicsType` in `ScenarioMetadata`

The old embedded `dynamics.HKParams` is replaced by an explicit `DynamicsType` string plus separate params fields:

```go
// v2 (HK-only, embedded params)
meta := &simulation.ScenarioMetadata{
    HKParams: dynamics.HKParams{Tolerance: 0.3, Influence: 0.5},
    // ...
}

// v3 (multi-dynamics)
import "smp/simulation"
meta := &simulation.ScenarioMetadata{
    DynamicsType: simulation.DynamicsTypeHK,  // or DynamicsTypeDeffuant / Galam / Voter
    HKParams:     dynamics.HKParams{Tolerance: 0.3, Influence: 0.5},
    // DeffuantParams / GalamParams / VoterParams are ignored unless selected
    // ...
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
