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

### Generic Parameterization

Every core type is parameterized over:

- **`O`** — opinion type (default `float64`; can be extended to `bool`, `[2]float64`, etc.)
- **`P`** — agent-parameter type (default `dynamics.HKParams`)

### Agent Behaviour

Each agent per step:

1. **Views** posts from followed neighbors and from the recommendation system.
2. **Partitions** posts into concordant (`|Δopinion| ≤ Tolerance`) and discordant.
3. **Updates opinion** via the chosen dynamics rule.
4. **Reposts** a concordant post with probability `RepostRate`, otherwise publishes a new post.
5. **Rewires** — with probability `RewiringRate`, unfollows a discordant neighbor and follows a concordant stranger.

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
| `Structure` | Common-neighbor count |
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

## Build

This project depends on `github.com/mattn/go-sqlite3`, so the Go toolchain needs a working C compiler.

macOS / Linux:

```bash
go build -o smp .
```

Windows PowerShell:

```powershell
go build -o smp.exe .
```

If `go-sqlite3` fails to compile on Windows, install a GCC-compatible toolchain first, for example MinGW-w64 via `winget install --id MSYS2.MSYS2 -e`, then build again from an MSYS2 MinGW shell or a PowerShell session with GCC on `PATH`.

## CLI Usage

The compiled binary (`smp`) accepts the following positional arguments:

```
smp <base_path> <metadata_json> [parsable_progress]
```

| Argument | Required | Description |
|----------|----------|-------------|
| `base_path` | Yes | Root output directory for simulation runs |
| `metadata_json` | Yes | JSON string of `ScenarioMetadata` fields |
| `parsable_progress` | No | Enable machine-readable progress output on stdout (`1`, `true`, `yes`, `ok`). Default: `false` |

Example:

```bash
./smp ./run '{"UniqueName":"run-001","DynamicsType":"HK",...}' 1
```

Windows PowerShell example:

```powershell
./smp.exe ./run '{"UniqueName":"run-001","DynamicsType":"HK",...}' 1
```

When parsable progress is enabled, each step emits a line of the form:

```
TASK:<name>;TYPE:PROGRESS;STEP:<n>;
```

### Parameter Types

The following TypeScript definition matches the runtime metadata fields used by the CLI:

```ts
export type DynamicsType = "HK" | "Deffuant" | "Galam" | "Voter";
export type NetworkType = "Random";

export type RecsysFactoryTypeFloat64 =
    | "Random"
    | "Opinion"
    | "Structure"
    | "OpinionRandom"
    | "StructureRandom"
    | "OpinionM9"
    | "StructureM9";

export type RecsysFactoryTypeBool =
    | "Random"
    | "Structure"
    | "StructureRandom"
    | "StructureM9";

export interface HKParams {
    Tolerance: number;
    Influence: number;   // [0, 1]
    RewiringRate: number; // [0, 1]
    RepostRate: number;   // [0, 1]
}

export interface DeffuantParams {
    Tolerance: number;
    Influence: number;   // [0, 1]
    RewiringRate: number; // [0, 1]
    RepostRate: number;   // [0, 1]
}

export interface GalamParams {
    Influence: number;   // [0, 1]
    RewiringRate: number; // [0, 1]
    RepostRate: number;   // [0, 1]
}

export interface VoterParams {
    Influence: number;   // [0, 1]
    RewiringRate: number; // [0, 1]
    RepostRate: number;   // [0, 1]
}

export interface CollectItemOptions {
    AgentNumber: boolean;
    OpinionSum: boolean;
    RewiringEvent: boolean;
    ViewPostsEvent: boolean;
    PostEvent: boolean;
}

export interface RecSysParams {
    // For float64 recsys path (HK/Deffuant)
    NoiseStd?: number;
    OpRandomNoiseStd?: number;
    UseCache?: boolean;
    Tolerance?: number;
    Steepness?: number;
    RandomRatio?: number;
    MixRate?: number;

    // For bool recsys path (Galam/Voter)
    NoiseStd?: number;
    UseCache?: boolean;
    Steepness?: number;
    RandomRatio?: number;
    MixRate?: number;
}

interface ScenarioMetadataBase {
    UniqueName: string;
    DynamicsType: DynamicsType;
    MaxSimulationStep: number;
    NetworkType: NetworkType;
    NodeCount: number;
    NodeFollowCount: number;
    RecsysCount: number;
    PostRetainCount: number;
    CollectItemOptions?: CollectItemOptions;
    AgentNumber?: boolean;
    OpinionSum?: boolean;
    RewiringEvent?: boolean;
    ViewPostsEvent?: boolean;
    PostEvent?: boolean;
    RecSysParams?: RecSysParams;
}

export type ScenarioMetadata =
    | (ScenarioMetadataBase & {
            DynamicsType: "HK";
            HKParams: HKParams;
            RecsysFactoryType: RecsysFactoryTypeFloat64;
        })
    | (ScenarioMetadataBase & {
            DynamicsType: "Deffuant";
            DeffuantParams: DeffuantParams;
            RecsysFactoryType: RecsysFactoryTypeFloat64;
        })
    | (ScenarioMetadataBase & {
            DynamicsType: "Galam";
            GalamParams: GalamParams;
            RecsysFactoryType: RecsysFactoryTypeBool;
        })
    | (ScenarioMetadataBase & {
            DynamicsType: "Voter";
            VoterParams: VoterParams;
            RecsysFactoryType: RecsysFactoryTypeBool;
        });
```

Validation rules currently enforced by the Go runtime:

- UniqueName: non-empty, no leading/trailing spaces, max length 128, chars in [A-Za-z0-9._-]
- DynamicsType: required, must be one of HK / Deffuant / Galam / Voter
- RecsysCount: > 0
- PostRetainCount: >= 0
- MaxSimulationStep: > 0
- NodeCount: >= 2
- NodeFollowCount: in [1, NodeCount-1]
- NetworkType: currently only Random
- Influence / RewiringRate / RepostRate: in [0, 1]
- Tolerance: must be finite and >= 0

## Testing

```bash
go test ./...
```

## Migration from v1

See [`docs/migration.md`](docs/migration.md) for a step-by-step guide covering code, msgpack snapshots, metadata JSON, and SQLite schemas.

---

## Python Bindings (`smp_bindings`)

`smp_bindings` is a Python package for reading and analyzing simulation output produced by the Go runtime.

### Installation

```bash
pip install -e .          # editable install from repo root
# or
pip install .             # regular install
```

Dependencies: `msgpack`, `numpy`, `networkx`, `lz4`.

### IDE Configuration (VSCode/Pylance)

After installing `smp_bindings`, VSCode's Pylance linter may not immediately recognize the package even though the Python interpreter can import it normally. To fix this:

1. **Select the correct Python interpreter in VSCode**
   - Press `Cmd+Shift+P` (macOS) or `Ctrl+Shift+P` (Linux/Windows)
   - Search for "Python: Select Interpreter"
   - Choose your conda/virtual environment (e.g., `./miniconda3/envs/your-env/bin/python`)

2. **Install in editable mode** (recommended for development)

   ```bash
   pip install -e /path/to/social-media-models
   ```

3. **Restart VSCode** to allow Pylance to re-index packages

The project is preconfigured with:

- `.vscode/settings.json` — Pylance type-checking mode and Python environment detection
- `pyproject.toml` — `[tool.pyright]` configuration for type checking
- `smp_bindings/py.typed` — marker file indicating type support

These configurations ensure that Pylance correctly discovers and analyzes the `smp_bindings` package alongside your code.

### Loading simulation output

```python
from smp_bindings import (
    load_accumulative_model_state,
    load_gonum_graph_dump,
    load_snapshot,
    load_events_db,
    load_event_body,
    batch_load_event_bodies,
    get_events_by_step_range,
)

# --- Accumulative time-series state (LZ4 binary) ---
acc = load_accumulative_model_state("run/my-sim/acc-state-1000.lz4")
print(acc["opinions"].shape)       # (steps+1, agents)
print(acc["agent_numbers"].shape)  # (steps+1, agents, 4)

# --- Graph dump (msgpack) ---
import networkx as nx
g: nx.DiGraph = load_gonum_graph_dump("run/my-sim/graph-0.msgpack")

# --- Model snapshot (v2 msgpack envelope) ---
snap = load_snapshot("run/my-sim/snapshot-1000.msgpack")
print(snap["dynamics_type"])   # e.g. "HK"
print(snap["data"].keys())

# --- SQLite event database ---
db = load_events_db("run/my-sim/events.db")

# all Post events between step 10 and 20
events = get_events_by_step_range(db, 10, 20, type_="Post")
events = batch_load_event_bodies(db, events, event_type="Post")
for e in events:
    print(e.body.record.opinion, e.body.is_repost)

db.close()
```

### `RawSimulationRecord` — high-level helper

```python
from smp_bindings import RawSimulationRecord

metadata = {"UniqueName": "run-001", ...}   # dict matching ScenarioMetadata fields
with RawSimulationRecord("./run", metadata) as rec:
    # rec.opinions        shape (steps+1, agents)
    # rec.agent_numbers   shape (steps+1, agents, 4)
    # rec.agents          int
    # rec.max_step        int
    g = rec.get_graph(500)   # reconstructed DiGraph at step 500
```

### Running simulations from Python

`smp_bindings` can launch the compiled Go binary as a subprocess, parse its
per-step progress output, and run multiple simulations concurrently.

```python
from smp_bindings import run_simulations, is_simulation_finished

scenarios = [
    {
        "UniqueName": "run-hk-001",
        "DynamicsType": "HK",
        "HKParams": {"Influence": 0.01, "Tolerance": 0.45,
                     "RewiringRate": 0.05, "RepostRate": 0.3},
        "PostRetainCount": 3, "RecsysCount": 5,
        "RecsysFactoryType": "Random", "NetworkType": "Random",
        "NodeCount": 500, "NodeFollowCount": 15,
        "MaxSimulationStep": 5000,
    },
    # ... more scenarios
]

completed = run_simulations(
    binary_path="./smp",          # path to compiled Go binary
    base_path="./run",            # output root directory
    scenarios=scenarios,
    max_concurrent=4,             # max parallel simulations (default 4)
    show_progress=True,           # print per-step progress (None = auto-detect tty)
    skip_finished=True,           # skip simulations that already have a finished mark
)
print("Completed:", completed)

# Check a single simulation manually
print(is_simulation_finished("./run", scenarios[0]))
```

`show_progress=None` (default) auto-detects whether stdout is a terminal:
in interactive sessions each simulation prints live step counts; in batch /
CI runs it falls back to a `tqdm` overall bar if available, or plain print
counts otherwise.

### Migration CLI

Migrate old v1/v2 simulation output to the current format:

```bash
# Migrate msgpack snapshots (wraps them in RawSnapshotData envelope)
smp-migrate snapshot ./run/my-sim/snapshot-*.msgpack
# or with explicit dynamics type:
smp-migrate snapshot --dynamics Deffuant ./run/my-sim/snapshot-*.msgpack

# Migrate SQLite event databases (renames tables/columns, updates type strings)
smp-migrate events ./run/my-sim/events.db
```

Equivalently, run as a module:

```bash
python -m smp_bindings.migrate snapshot ./run/my-sim/snapshot-*.msgpack
python -m smp_bindings.migrate events   ./run/my-sim/events.db
```

---

## Further Documentation

- [`docs/architecture.md`](docs/architecture.md) — package structure, data-flow diagram, serialization schema
- [`docs/migration.md`](docs/migration.md) — breaking changes and migration scripts
- [`docs/ide-setup.md`](docs/ide-setup.md) — VSCode/Pylance configuration for `smp_bindings` development
