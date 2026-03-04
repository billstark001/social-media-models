# Social-Media-Models: Architecture

This document describes the internal structure of the simulation framework.

---

## Package Overview

```
smp/
├── model/          Core simulation types and interfaces (generic)
├── dynamics/       Opinion dynamics implementations (HK, Deffuant)
├── recsys/         Recommendation system implementations
├── simulation/     Scenario execution, serialization, event logging
└── utils/          Graph utilities (ER, small-world, serialize)
```

---

## `model` Package

### Generic Type Parameters

Every core type carries two type parameters:

| Parameter | Meaning | Typical value |
|-----------|---------|---------------|
| `O` | Opinion type | `float64` |
| `P` | Agent-parameters type | `dynamics.HKParams` |

### Key Types

| Type | Description |
|------|-------------|
| `PostRecord[O]` | A single post: `{AgentID, Step, Opinion}` |
| `SMPAgent[O, P]` | One social-media user/agent |
| `SMPModel[O, P]` | The simulation world |
| `NetworkGrid[O, P]` | Maps nodes → agents and node → post history |
| `RandomActivation[O, P]` | Random-order agent scheduler |
| `Dynamics[O, P]` | Interface for opinion-update rules |
| `SMPModelRecommendationSystem[O, P]` | Interface for recommendation strategies |
| `AgentSumRecord[T]` | Generic `[4]T` statistics record |
| `AgentOpinionSumRecord` | `AgentSumRecord[float64]` alias |

### Dynamics Interface

```go
type Dynamics[O any, P any] interface {
    Concordant(myOpinion, otherOpinion O, params *P) bool
    Step(myOpinion O, cN, cR, dN, dR []O, params *P) (O, AgentOpinionSumRecord)
}
```

`cN` / `cR` = concordant neighbor / recommended opinion slices.  
`dN` / `dR` = discordant equivalents.  
Returns the next opinion and four-slot statistics.

### Agent Behavior Parameters

Params types that control reposting and rewiring implement:

```go
type AgentBehaviorParams interface {
    GetRepostRate() float64
    GetRewiringRate() float64
}
```

Both `dynamics.HKParams` and `dynamics.DeffuantParams` satisfy this interface.

### Agent Step Logic

Each `SMPAgent[O, P].Step()`:

1. Reads latest posts from followed neighbors.
2. Requests recommendations from `model.Recsys`.
3. Calls `model.PartitionPosts` with `model.Dynamics.Concordant` to split posts.
4. Calls `model.Dynamics.Step` to obtain `nextOpinion` and `opinionSum`.
5. Decides to repost (with probability `RepostRate`) or publish a new post.
6. Optionally rewires one discordant follow to a concordant stranger (probability `RewiringRate`).

---

## `dynamics` Package

### Hegselmann-Krause (`HK`)

**Params**: `HKParams{Tolerance, Decay, RewiringRate, RepostRate}`

**Concordant**: `|myOp − otherOp| ≤ Tolerance`

**Update**: `nextOp = myOp + Decay × mean(concordant opinions − myOp)`

All statistics (concordant/discordant neighbour and recommended opinion deltas) are recorded in `AgentOpinionSumRecord`.

### Deffuant (`Deffuant`)

**Params**: `DeffuantParams{Tolerance, RewiringRate, RepostRate}`

**Concordant**: same as HK.

**Update**: pick one concordant opinion `o*` at random; `nextOp = myOp + Tolerance × (o* − myOp)`.

Statistics are computed over _all_ concordant/discordant opinions regardless of which one was chosen.

---

## `recsys` Package

All recommendation systems implement `model.SMPModelRecommendationSystem[O, P]`.

| Name | Strategy |
|------|----------|
| `Random` | Sample random agents |
| `Opinion` | Nearest neighbours in opinion space (sorted index) |
| `Structure` | Common-neighbour count in the social graph |
| `OpinionRandom` | Opinion-similarity weighted random sampling |
| `StructureRandom` | Structure-similarity weighted random sampling |
| `Mix` | Blend two systems with a fixed ratio |

All are generic `[O, P]`; `Opinion` is specialized to `O = float64` because it performs arithmetic on opinions.

---

## `simulation` Package

### Scenario

`Scenario` wraps `SMPModel[float64, dynamics.HKParams]`, the serializer, accumulative-state tracker, and optional SQLite event DB.

```
Init()          build graph, create model, open DB
Load()          restore from latest snapshot + acc-state
StepTillEnd()   run until MaxSimulationStep or context cancel
```

### Serialization Files (per simulation ID)

| File pattern | Format | Content |
|---|---|---|
| `snapshot-*.msgpack` | msgpack | Full `SMPModelDumpData` (graph + opinions + posts) |
| `acc-state-*.lz4` | binary + LZ4 | `AccumulativeModelState` (opinions, counts, sums over all steps) |
| `graph-<step>.msgpack` | msgpack | Graph at specific step |
| `finished-*.msgpack` | msgpack | Completion marker |
| `metadata.json` | JSON | `ScenarioMetadata` |
| `events.db` | SQLite | Optional per-event log |

### Event DB Schema

```sql
events          (id, type TEXT, agent_id, step)
rewiring_events (event_id FK, unfollow, follow)
post_events     (event_id FK, agent_id, step, opinion REAL, is_repost BOOL)
view_posts_events (event_id FK, data BLOB)  -- msgpack-encoded ViewPostsEventBody
```

---

## Data Flow Diagram

```
Graph (gonum/graph)
      │
      ▼
NewSMPModel[O,P] ────────────────────────────────────────────┐
      │                                                        │
      ├── NetworkGrid[O,P]  (AgentMap, PostMap)               │
      ├── RandomActivation[O,P]                               │
      ├── Dynamics[O,P]  ←  dynamics.HK / dynamics.Deffuant  │
      └── SMPModelRecommendationSystem[O,P]                   │
                                                              │
      Model.Step()                                            │
        ├── Recsys.PreStep()                                  │
        ├── Schedule.Step() → each agent calls Step()         │
        │     ├── GetNeighbors + GetRecommendation            │
        │     ├── PartitionPosts (Concordant)                 │
        │     ├── Dynamics.Step → nextOpinion, opSum          │
        │     └── repost / rewire decisions                   │
        ├── Recsys.PostStep(changed)                          │
        └── commit opinions, posts, rewirings                 │
                                                              │
Simulation.Scenario                                           │
        ├── Accumulate state → AccumulativeModelState         │
        ├── Snapshot (msgpack) every N steps                  │
        └── EventDB (SQLite) if enabled                       │
```
