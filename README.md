# Social Media Models

A Go implementation of agent-based social media simulation models with pluggable opinion dynamics. The framework supports multiple opinion-dynamics rules (Hegselmann-Krause, Deffuant) and several recommendation strategies.

## Repository Structure

```
├── model/          Core types and interfaces (generic over opinion type O and params type P)
├── dynamics/       Opinion dynamics: HK (Hegselmann-Krause) and Deffuant
├── recsys/         Recommendation systems (random, opinion, structure, and hybrids)
├── simulation/     Scenario runner, serialization (msgpack + LZ4), SQLite event log
├── utils/          Graph utilities (ER, small-world, serialize/deserialize)
└── docs/           Architecture overview and migration guide
```

## Quick Start

```go
import (
    "smp/dynamics"
    "smp/model"
    "smp/utils"
)

graph := utils.CreateRandomNetwork(500, 0.03)  // 500 nodes, ~15 follows each

params := dynamics.DefaultHKParams()           // Hegselmann-Krause params
params.Tolerance = 0.45

mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
mp.PostRetainCount = 3

m := model.NewSMPModelFloat64(
    graph, nil, mp, params, &dynamics.HK{},
    &model.CollectItemOptions{AgentNumber: true, OpinionSum: true},
    nil,
)
m.SetAgentCurPosts()

for range 1000 {
    m.Step(true)
}
```

## Model Architecture

### Generic Parameterisation

Every core type is parameterised over:

- **`O`** — opinion type (default `float64`; can be extended to `bool`, `[2]float64`, etc.)
- **`P`** — agent-parameter type (default `dynamics.HKParams`)

### Agent Behaviour

Each agent per step:

1. **Views** posts from followed neighbours and from the recommendation system.
2. **Partitions** posts into concordant (`|Δopinion| ≤ Tolerance`) and discordant.
3. **Updates opinion** via the chosen dynamics rule.
4. **Reposts** a concordant post with probability `RepostRate`, otherwise publishes a new post.
5. **Rewires** — with probability `RewiringRate`, unfollows a discordant neighbour and follows a concordant stranger.

### Opinion Dynamics

| Dynamics | Update Rule | Params |
|----------|-------------|--------|
| **HK** (Hegselmann-Krause) | Move to weighted mean of concordant opinions × `Influence` | `HKParams` |
| **Deffuant** | Pick one concordant opinion at random; move by `Tolerance × Δ` | `DeffuantParams` |

### Recommendation Systems

| Name | Strategy |
|------|----------|
| `Random` | Uniformly random |
| `Opinion` | Nearest in opinion space |
| `Structure` | Common-neighbour count |
| `OpinionRandom` | Opinion-distance weighted random |
| `StructureRandom` | Structure-similarity weighted random |
| `Mix` | Blend of two systems |

### Key Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `Tolerance` | Concordance threshold | 0.25 |
| `Influence` | Opinion influence rate (HK only) | 1.0 |
| `RepostRate` | Probability of reposting vs. new post | 0.3 |
| `RewiringRate` | Probability of rewiring per step | 0.1 |
| `PostRetainCount` | Post history depth per agent | 3 |
| `RecsysCount` | Recommendations per agent per step | 10 |

## Serialization

Snapshots are stored as **msgpack** files; accumulative time-series data uses a compact **binary + LZ4** format. Events (posts, rewirings, view-posts) are optionally logged to **SQLite**.

See [`docs/architecture.md`](docs/architecture.md) for the full file layout.

## Testing

```bash
go test ./...
```

## Migration from v1

See [`docs/migration.md`](docs/migration.md) for a step-by-step guide covering code, msgpack snapshots, metadata JSON, and SQLite schemas.

## Further Documentation

- [`docs/architecture.md`](docs/architecture.md) — package structure, data-flow diagram, serialization schema
- [`docs/migration.md`](docs/migration.md) — breaking changes and migration scripts
