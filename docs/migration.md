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
| `RewiringEventBody` | no `AgentID` field | `AgentID int64` (the acting agent), recovered from the parent event header during migration |
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

Bare `SMPModelDumpData` dumps need a `RawSnapshotData{DynamicsType, Data}` envelope. The maintained migration handles both bare and already enveloped snapshots, `.msgpack` and `.msgpack.lz4`, and conflicting old/new field names:

```bash
# HK is the default for a bare dump; use --dynamics for other dynamics
smp-migrate snapshot ./run/my-sim/snapshot-*.msgpack
smp-migrate snapshot --dynamics Deffuant ./run/my-sim/snapshot-*.msgpack
```

Existing physical state, RNG positions and recommender state are preserved. Rewriting bytes changes the checkpoint SHA-256, including branch seeds derived from that hash. Already canonical files are left untouched.

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

```bash
smp-migrate events ./run/my-sim/events.db
```

The migration transactionally renames or merges old tables, normalizes event types, and backfills rewiring actors from `events.agent_id`. Conflicting duplicate rows abort the transaction. Runtime readers require canonical table names.

### Recommendation parameter aliases

Use canonical `RecSysParams` keys such as `NoiseStd`, `MixRate`, `UseCache` and `LogRecommendations` for all dynamics. Lowercase/camelcase aliases are rejected with migration guidance instead of silently selecting a default:

```bash
smp-migrate metadata ./run/my-sim/metadata.json
smp-migrate snapshot ./run/my-sim/checkpoint-*.msgpack.lz4
```

Both commands are idempotent; conflicting aliases leave the file unchanged. The unused Go alias `GetDefaultRecsysFactoryDefs()` has been removed; use `GetFloat64RecsysFactories[dynamics.HKParams]()` in callers.

### Historical integer overrides

Numeric recommendation parameters now interpret equivalent integer and floating values identically across Go, JSON and MsgPack. The old Go factory accepted only `float64` (or `bool`) overrides; integer overrides used its defaults instead. Ordinary JSON simulation requests decoded their numbers as `float64` and are unaffected.

For a checkpoint produced by the old Go API with ignored integer overrides, explicitly remove those overrides to preserve its historical continuation:

```bash
smp-migrate snapshot ./old.msgpack.lz4 --drop-legacy-integer-overrides
```

This option is only for those old producers. It leaves floating parameters, physical model bytes and RNG states unchanged. It rewrites metadata and changes the file SHA-256 and hash-derived branch seeds. No runtime fallback selects the old rule. Metadata-only migrations preserve the opaque model bytes.

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

These are alternative constructor calls. Supply a non-nil RNG pool (`smprng` below imports `smp/rng`), with a separate pool for each independent simulation.

```go
rngPool := smprng.MustNewPool(smprng.FixedSpec(1, 2))

// HK (float64 opinions)
m := model.NewSMPModelFloat64(graph, nil, modelParams, agentParams, &dynamics.HK{}, collectOpts, nil, rngPool)

// Deffuant (float64 opinions)
dParams := dynamics.DefaultDeffuantParams()
m := model.NewSMPModelFloat64(graph, nil, modelParams, dParams, &dynamics.Deffuant{}, collectOpts, nil, rngPool)

// Galam (bool opinions)
gParams := dynamics.DefaultGalamParams()
ops := make([]bool, nodeCount)  // initialize with random booleans as needed
m := model.NewSMPModel(graph, &ops, modelParams, gParams, &dynamics.Galam{}, collectOpts, nil, rngPool)

// Voter (bool opinions)
vParams := dynamics.DefaultVoterParams()
m := model.NewSMPModel(graph, &ops, modelParams, vParams, &dynamics.Voter{}, collectOpts, nil, rngPool)
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
    "math/rand/v2"

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

func (d *MyDynamics) Step(my float64, cN, cR, dN, dR []float64, p *MyParams, rng *rand.Rand) (float64, model.AgentOpinionSumRecord) {
    // custom update rule
    return my, model.AgentOpinionSumRecord{}
}
```

## Persistent research checkpoints (DataVersion 3)

Persistent online research jobs now save policy and observation recovery state alongside the microscopic snapshot. These jobs use `DataVersion=3`; their `metadata.json` continues to identify the reference recommender, while the snapshot records the currently active policy. Use a version-3-capable SMP binary to resume them. Do not remove the version marker or rewrite their recommender field to make an older binary accept them. Ordinary simulations still default to `DataVersion=2` and preserve their existing encoding.

Go callers now use `simulation.PolicyChange`, `simulation.Exposure`, and `simulation.SlotCounts` directly instead of the former batch-local types. `batch.ExperimentSpec` and endpoint wire field names are unchanged. There are no forwarding aliases for those moved Go types.
