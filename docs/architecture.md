# Social-Media-Models: Architecture

This document describes the internal structure of the simulation framework.

---

## Package Overview

```
smp/
├── model/          Core simulation types and interfaces (generic)
├── dynamics/       Opinion dynamics implementations (HK, Deffuant, Galam, Voter)
├── recsys/         Recommendation system implementations
├── simulation/     Scenario execution, serialization, event logging
├── trajectory/     Persisted opinion/count/sum channel blocks
├── record/         Read-only saved-run reconstruction
├── observables/    Static energy, pathway, bins and landscape kernels
├── terminal/       Empirical terminal classification
├── groups/         Opinion density cores and observed group lineage
├── bridges/        Cross-core support and holder/union lifecycle observation
├── probe/          Shared offline request and stream evaluation
├── smp_bindings/   Python transport, typed records and orchestration
└── utils/          Graph utilities (ER, small-world, serialize)
```

---

## Offline measurement pipeline

`record.Reader` reconstructs the completed state requested by `probe` without
resuming a simulation. `observables.StateMeasurer` evaluates selected static
kernels. `groups.Detector` uses opinions alone; `groups.Tracker` associates
confirmed cores through one ordered stream. `bridges.Observer` consumes those
group frames, the directed follow graph and available latest displayed posts,
then owns its frozen-reference epochs and holder/union ledgers. It never
substitutes a dynamic detector's current members for an open frozen cohort.
`bridges.NativeMeasure` adapts an already existing model without advancing it.

`probe.Evaluate` is the shared JSON/MsgPack/JSONL and Go entry point. Each request
owns fresh observers; output thinning occurs after observation. Python's
`analyze_run`, `ProbeClient` and typed record wrapper call this API rather than
implementing measurement arithmetic. Counterfactual force retains its separate
recommender/RNG lifecycle.

The reader's `ReadStates` API yields owned snapshots. The static pipeline uses
`ReadStatesBorrowed` for callback-only, read-only views and retains only owned
measurement results. One reader/workspace belongs to one ordered stream;
independent streams can use independent instances. The owned reader API remains
available for force inputs and callers that retain microscopic snapshots.

See [the API catalog and input ownership](measurements.md),
[group/bridge contracts](groups.md), and [trajectory storage](trajectory.md).

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
import "math/rand/v2"

type Dynamics[O any, P any] interface {
    Concordant(myOpinion, otherOpinion O, params *P) bool
    Step(myOpinion O, cN, cR, dN, dR []O, params *P, rng *rand.Rand) (O, AgentOpinionSumRecord)
}
```

`cN` / `cR` = concordant neighbor / recommended opinion slices. `dN` / `dR` = discordant equivalents. Returns the next opinion and four-slot statistics.

### Agent Behavior Parameters

Params types that control reposting and rewiring implement:

```go
type AgentBehaviorParams interface {
    GetRepostRate() float64
    GetRewiringRate() float64
}
```

All four params types — `dynamics.HKParams`, `dynamics.DeffuantParams`, `dynamics.GalamParams`, and `dynamics.VoterParams` — satisfy this interface.

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

Four dynamics are provided. HK and Deffuant use `O = float64`; Galam and Voter use `O = bool`.

| Dynamics | Params type | Opinion type | Update rule |
|---|---|---|---|
| `HK` | `HKParams` | `float64` | Mean-field bounded confidence |
| `Deffuant` | `DeffuantParams` | `float64` | Pairwise bounded confidence |
| `Galam` | `GalamParams` | `bool` | Flip conditioned on summed opinion differences |
| `Voter` | `VoterParams` | `bool` | Stochastic flip from normalized opinion differences |

### Hegselmann-Krause (`HK`)

**Params**: `HKParams{Tolerance, Influence, RewiringRate, RepostRate}`

**Concordant**: `|myOp − otherOp| ≤ Tolerance`

**Update**: `nextOp = myOp + Influence × mean(concordant opinions − myOp)`

All statistics (concordant/discordant neighbor and recommended opinion deltas) are recorded in `AgentOpinionSumRecord`.

### Deffuant (`Deffuant`)

**Params**: `DeffuantParams{Tolerance, Influence, RewiringRate, RepostRate}`

**Concordant**: same as HK.

**Update**: pick one concordant opinion `o*` at random; `nextOp = myOp + Influence × (o* − myOp)`.

Statistics are computed over _all_ concordant/discordant opinions regardless of which one was chosen.

### Galam (`Galam`)

**Params**: `GalamParams{Influence, RewiringRate, RepostRate}`

**Opinion type**: `bool`

**Concordant**: `myOp == otherOp`

**Update**: if the summed discordant opinion differences are positive, flip with probability `Influence`. See [`dynamics/galam.go`](../dynamics/galam.go) for the implemented rule.

### Voter (`Voter`)

**Params**: `VoterParams{Influence, RewiringRate, RepostRate}`

**Opinion type**: `bool`

**Concordant**: `myOp == otherOp`

**Update**: compare a uniform draw with `Influence × discordant-difference sum / total-difference sum`. See [`dynamics/voter.go`](../dynamics/voter.go) for the implemented rule.

---

## `recsys` Package

All recommendation systems implement `model.SMPModelRecommendationSystem[O, P]`.

| Name | Strategy |
|------|----------|
| `Random` | Sample random agents |
| `Opinion` | Nearest neighbors in opinion space (sorted index) |
| `Structure` | Common-neighbor count in the social graph |
| `OpinionRandom` | Opinion-similarity weighted random sampling |
| `StructureRandom` | Structure-similarity weighted random sampling |
| `Mix` | Blend two systems with a fixed ratio |

All are generic `[O, P]`. `Opinion` and `OpinionRandom` convert supported opinions to `float64` for scoring, including `false → 0` and `true → 1` for boolean dynamics.

---

## `simulation` Package

### Scenario

`Scenario` holds an `IModel` (see below), the serializer, accumulative-state tracker, and optional SQLite event DB. The concrete model type is selected at runtime by `ScenarioMetadata.DynamicsType`.

```
Init()          build graph, create model (dispatched by DynamicsType), open DB
Load()          restore the latest model snapshot and matching stored trajectory
StepTillEnd()   run until MaxSimulationStep or context cancel
```

### `IModel` Interface and Wrappers

`IModel` abstracts over `SMPModel[O, P]` so that `Scenario` can operate without knowing the concrete type parameters:

```go
type IModel interface {
    GetCurStep() int
    SetCurStep(int)
    GetGraph() *simple.DirectedGraph
    StepModel() (int, float64)
    InitPosts()
    Accumulate(*AccumulativeModelState)
    ValidateAcc(*AccumulativeModelState) bool
    RawDump() ([]byte, error)
    GetOpinions() []float64
}
```

Two generic wrapper types implement this interface:

| Wrapper | Wraps | Used by |
|---|---|---|
| `Float64ModelWrapper[P]` | `SMPModel[float64, P]` | HK, Deffuant |
| `BoolModelWrapper[P]` | `SMPModel[bool, P]` | Galam, Voter |

### `ScenarioMetadata.DynamicsType`

The string field `DynamicsType` selects which dynamics (and matching params) to use:

| Value | Dynamics | Params field used | Opinion type |
|---|---|---|---|
| `"HK"` (default) | `dynamics.HK` | `HKParams` | `float64` |
| `"Deffuant"` | `dynamics.Deffuant` | `DeffuantParams` | `float64` |
| `"Galam"` | `dynamics.Galam` | `GalamParams` | `bool` |
| `"Voter"` | `dynamics.Voter` | `VoterParams` | `bool` |

### Serialization Files (per simulation ID)

| File pattern | Format | Content |
|---|---|---|
| `snapshot-*.msgpack` | msgpack | `RawSnapshotData{DynamicsType, Data}` wrapping a msgpack-encoded `SMPModelDumpData[O,P]` |
| `trajectory.json` | JSON | Precision, block ranges, filenames, and checksums |
| `trajectory-*.smpc` | binary + channel compression | Bounded blocks of opinions, counts, and opinion sums |
| `acc-state-*.lz4` | binary + LZ4 | Legacy accumulated opinions, counts, and sums |
| `graph-<step>.msgpack.lz4` | msgpack + LZ4 | Graph at a completed step; legacy uncompressed files remain readable |
| `checkpoint-<step>.msgpack.lz4` | msgpack + LZ4 | Exact model state and physical metadata for research branches |
| `finished-*.msgpack` | msgpack | Completion marker |
| `metadata.json` | JSON | `ScenarioMetadata` |
| `events.db` | SQLite | Optional per-event log |

### Event DB Schema

```sql
events          (id, type TEXT, agent_id, step)
rewiring_events (event_id FK, agent_id, unfollow, follow)
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
      ├── Dynamics[O,P]  ←  HK / Deffuant / Galam / Voter      │
      └── SMPModelRecommendationSystem[O,P]                   │
                                                              │
      Model.Step()                                            │
        ├── Recsys.PreStep()                                  │
        ├── Schedule.Step() → each agent calls Step()         │
        │     ├── GetNeighbors + GetRecommendation            │
        │     ├── PartitionPosts (Concordant)                 │
        │     ├── Dynamics.Step → nextOpinion, opSum          │
        │     └── repost / rewire decisions                   │
        ├── Recsys.PreCommit()                               │
        ├── commit opinions, posts, rewirings                 │
        └── Recsys.PostStep(changed)                          │
                                                              │
Simulation.Scenario  (Model field: IModel)                    │
        ├── IModel.Accumulate → AccumulativeModelState        │
        ├── IModel.RawDump  → RawSnapshotData (msgpack)       │
        └── EventDB (SQLite) if enabled                       │
```
