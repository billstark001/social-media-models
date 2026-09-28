# Social Media Models

A Go implementation of agent-based social media simulation models with pluggable opinion dynamics. The framework supports multiple opinion-dynamics rules (Hegselmann-Krause, Deffuant) and several recommendation strategies.

## Repository Structure

```
├── cmd/smp/        Persistent simulator command
├── cmd/smp-batch/  No-file JSONL batch simulator
├── cmd/smp-probe/  Frozen-state counterfactual drift evaluator
├── model/          Core types and interfaces (generic over opinion type O and params type P)
├── dynamics/       Opinion dynamics: HK (Hegselmann-Krause) and Deffuant
├── recsys/         Recommendation systems (random, opinion, structure, and hybrids)
├── probe/          Go probe protocol and HK/Deffuant drift measurement
├── rng/            Reproducible named random streams and snapshot state
├── simulation/     Scenario runner, serialization (msgpack + LZ4), SQLite event log
├── utils/          Graph utilities (ER, small-world, serialize/deserialize)
└── docs/           Architecture overview and migration guide
```

## Quick Start

```go
import (
    "smp/dynamics"
    "smp/model"
    smprng "smp/rng"
    "smp/utils"
)

rngPool := smprng.MustNewPool(smprng.FixedSpec(1, 2))
graph := utils.CreateRandomNetwork(
    500, 0.03, rngPool.Stream(smprng.StreamNetwork),
) // 500 nodes, ~15 follows each

params := dynamics.DefaultHKParams()           // Hegselmann-Krause params
params.Tolerance = 0.45

mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
mp.PostRetainCount = 3

m := model.NewSMPModelFloat64(
    graph, nil, mp, params, &dynamics.HK{},
    &model.CollectItemOptions{AgentNumber: true, OpinionSum: true},
    nil,
    rngPool,
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
| **Deffuant** | Pick one concordant opinion at random; move by `Influence × Δ` | `DeffuantParams` |

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

Recovery snapshots are stored as **msgpack** files. New accumulated time-series
data uses bounded binary blocks with channel-specific LZ4 or Zstd compression.
Events (posts, rewirings, view-posts) are optionally logged to **SQLite**.

The current on-disk data format is `DataVersion: 2`; metadata without
`DataVersion` is interpreted as legacy version 0. Version 1 remains readable
and introduced the resolved root RNG specification in `metadata.json` and the
`finished-*.msgpack` marker. Version 2 adds bounded trajectory blocks,
independent opinion and opinion-sum precision, and exact research checkpoints.
New runs use version 2, including when given version 1 metadata. Model
snapshots store the current states of the named RNG streams, so a resumed run
produces the same continuation as an uninterrupted run.

See [`docs/architecture.md`](docs/architecture.md) for the full file layout.

## Build

This project depends on `github.com/mattn/go-sqlite3`, so the Go toolchain needs a working C compiler.

macOS / Linux:

```bash
make build-all
# equivalent:
go build -o ./smp ./cmd/smp
go build -o ./smp-batch ./cmd/smp-batch
go build -o ./smp-probe ./cmd/smp-probe
```

Windows PowerShell:

```powershell
go build -o smp.exe ./cmd/smp
go build -o smp-batch.exe ./cmd/smp-batch
go build -o smp-probe.exe ./cmd/smp-probe
```

If `go-sqlite3` fails to compile on Windows, install a GCC-compatible toolchain first, for example MinGW-w64 via `winget install --id MSYS2.MSYS2 -e`, then build again from an MSYS2 MinGW shell or a PowerShell session with GCC on `PATH`.

## CLI Usage

The compiled binary (`smp`) accepts the following positional arguments:

```
smp <base_path> <metadata_json> [json_progress]
```

| Argument | Required | Description |
|----------|----------|-------------|
| `base_path` | Yes | Root output directory for simulation runs |
| `metadata_json` | Yes | JSON string of `ScenarioMetadata` fields |
| `json_progress` | No | Enable compact JSONL progress on stdout (`1`, `true`, `yes`, `ok`). Default: `false` |

Example:

```bash
./smp ./run '{"UniqueName":"run-001","DynamicsType":"HK",...}' 1
```

Windows PowerShell example:

```powershell
./smp.exe ./run '{"UniqueName":"run-001","DynamicsType":"HK",...}' 1
```

Machine progress uses the same compact JSONL codec as `smp-batch`. The
persistent command rate-limits step events to at most four per second, so
encoding cost does not grow with simulation step rate. A progress line is:

```json
{"v":1,"id":"run-001","t":"progress","s":1200,"m":5000}
```

The resolved RNG parameters are also emitted once at startup:

```json
{"v":1,"id":"run-001","t":"rng","a":"pcg64-dxsm-v1","x":"<seed1>","y":"<seed2>"}
```

The short wire keys expand to `version`, `request_id`, `type`, `step`,
`max_step`, `stop_reason`, `algorithm`, `seed1`, and `seed2`. Event types are
`rng`, `start`, `progress`, and `done`. The Python codec exposes descriptive
field names; callers do not need to interpret the short keys directly.

The `smp` command persists graph checkpoints, events, recovery snapshots, and
the three accumulated per-agent channels. New runs write bounded time chunks
instead of rewriting the full history at every save. Each channel keeps its
selected stored precision (default `float32` opinions, `float16` opinion sums,
and exact `int16` counts). Set `TrajectoryPrecision` in metadata to choose
`float16`, `float32`, or `float64` independently for opinions and sums.
Chunks select a lossless LZ4 codec or byte/bit shuffle with Zstd per channel;
graph checkpoints are compressed too. The format is described in
[docs/trajectory.md](docs/trajectory.md), including measured space use in two
slow-convergence runs.

`CheckpointSteps` in scenario metadata requests exact, named `float64` research
checkpoints at completed steps, including step zero. They retain the graph,
posts, recommender state, RNG streams, and halt counter. Historical rows are
for analysis; they are not exact branch starting points. Library callers that
only need an in-memory run can use
`NewScenarioWithOptions(..., ScenarioOptions{EnableDumps: false})`. In that
mode the scenario performs no filesystem I/O, creates no serializer/event
database, and does not allocate or update `AccumulativeModelState`. It is not
resumable; callers must retain the returned summaries themselves.

## No-file batch simulations and checkpoint experiments

`smp-batch` keeps one Go process alive for a JSONL stream and runs every item
entirely in memory. Each non-empty input line has this shape:

```json
{
  "schema_version": 1,
  "request_id": "cell/replicate-0001",
  "metadata": {
    "UniqueName": "cell-replicate-0001",
    "DynamicsType": "HK",
    "HKParams": {
      "Tolerance": 0.45,
      "Influence": 0.05,
      "RewiringRate": 0.05,
      "RepostRate": 0.0
    },
    "RNG": {
      "Algorithm": "pcg64-dxsm-v1",
      "Seed1": "0x0000000000000001",
      "Seed2": "0x0000000000000002"
    }
  },
  "output": {
    "final_opinions": false,
    "terminal": {
      "major_mass": 0.02,
      "position_resolution": 0.0,
      "mass_resolution": 0.002
    }
  }
}
```

Missing metadata fields receive the same defaults as `smp`. The batch runner
forces all history/event collection flags off because its current summaries
need only the final in-memory state. A successful response reports the
resolved RNG, protocol SHA-256, step/stop reason, and final opinion count,
mean, population variance, minimum, and maximum. The optional `terminal` block
applies the common atomic-measure component classifier and returns status,
category, both component counts, component masses, and threshold margins.
`major_mass=0` is normalized to the version-1 default `0.02`; production
protocols should still send it explicitly. For an empirical population of
size `N`, use `mass_resolution=1/N` and `position_resolution=0`; a binned
comparison should instead report its bin width. Set `final_opinions` only when
a downstream analysis genuinely needs the raw terminal row.

```bash
./smp-batch < requests.jsonl > results.jsonl
./smp-batch --progress jsonl --progress-step-interval 1000 \
  < requests.jsonl > results.jsonl 2> progress.jsonl
```

Progress is opt-in and always uses stderr, leaving stdout result-only JSONL.
Its JSONL events use the same compact `v/id/t/s/m/r` envelope documented
above. Step events are disabled unless `--progress-step-interval` is positive.
Malformed request lines yield an error response and do not abort subsequent
items. Every response is flushed before the next request, so an orchestrator
can resume by protocol hash/request id after interruption. The no-file runner
itself deliberately creates no resume markers.

Python can run a complete request list through one process:

```python
from smp_bindings import run_batch

responses = run_batch(
    "./smp-batch",
    requests,
    progress=lambda event: print(event),
    progress_step_interval=1000,
)
```

Use `BatchClient` when requests are generated incrementally, or
`run_batch_parallel(..., processes=N)` to load-balance them over multiple
long-lived Go processes. `smp-batch` itself remains sequential so that each
stdin line has exactly one immediately flushed stdout response.

For custom orchestration, `smp_bindings.codec` publicly exposes the shared
progress decoder and versioned batch request/response codecs.
`smp_bindings.process` exposes executable validation, process-group creation,
and safe termination helpers used by both built-in runners.

For repeated continuations of one exact research checkpoint, include an
`experiment` object. `mode: "resume"` reuses its saved RNG and requires one
replicate. `mode: "branch"` derives stable independent future streams from the
explicit base RNG, checkpoint hash, and replicate index. `observe_at` values
are relative steps and may include zero. A branch advances only once to its
largest requested time. For example, the following fields can be added to a
normal batch request with matching complete physical metadata:

```json
{
  "experiment": {
    "checkpoint": "run/pilot/checkpoint-000000004.msgpack.lz4",
    "mode": "branch",
    "replicates": 64,
    "observe_at": [0, 1, 4, 16]
  },
  "output": {
    "energy": true,
    "bins": [-1, -0.5, 0, 0.5, 1]
  }
}
```

Each replicate returns its own observations, stop reason, and completed step.
Binned population masses sum to one; the row-major directed-edge matrix sums
to the observed mean out-degree. Retain per-replicate vectors for joint drift
and covariance estimates. `full_state` adds exact opinions and edges at each
endpoint. `checkpoint_at` optionally writes complete endpoint checkpoints;
for multiple replicates its paths need a `{replicate}` placeholder. Otherwise
checkpoint experiments write no output files. `stop_on_halt` can stop long
branches early and leaves later requested observations absent.

## Frozen-state probe

`smp-probe` evaluates the recommender for a hypothetical agent opinion
`x ∈ [-1, 1]` on one or more frozen simulation steps. It does not resume the
simulation and does not require an analysis checkpoint: Python reconstructs a
minimal frozen state from the saved opinion rows, network snapshots/rewiring
events, and repost events, then sends that state to Go. Recommendation logic is
therefore implemented only once, in Go.

The original `Recommend` method and simulation path are unchanged. Built-in
recommenders additionally implement `RecommendAt(agent, opinion, ..., rng)`.
The explicit probe RNG is independent of the simulation RNG, so analysis cannot
change a later simulation continuation. Opinion-independent recommenders reuse
one recommendation sample across all grid points in a replicate.

The result contains, for each step and grid point:

- `f_probe`: the expected one-step opinion drift;
- `f_neighbor` and `f_recommendation`: additive components whose means sum to
  `f_probe`;
- concordant neighbor/recommendation counts for node-only or node-edge
  analysis;
- sample count, active count, and population variance.

The `measurements` list can select `force`, `energy`, `landscape`, and
`counterfactual_neighbor_energy`; an omitted list retains the original `force`
measurement. Energy uses the truncated quadratic kernel and actual directed
edge count. The landscape reports population energy and negative gradient,
concordant mass, and edge energy conditioned on current source-opinion bins,
with counts and validity masks. Counterfactual neighbor energy fixes one
anchor's neighbors while changing its query opinion; it is separate from the
current source-conditioned edge landscape. The `force` output reports opinion
drift and includes the update second moment, total update variance, and
zero-update probability. Set
`per_anchor` to get each anchor's statistics across recommendation replicates.

`run_probe_checkpoints([...], measurements=[...])` reads exact `float64`
research checkpoints directly in Go. Measurements from reconstructed
`RawSimulationRecord` steps are marked `history_f16`, `history_f32`, or
`history_f64` according to the stored opinion row. Static energy requests
do not initialize the recommender or consume an RNG stream.

For HK, `f_probe` is its deterministic bounded-confidence drift. For Deffuant,
it is the exact conditional expectation of uniformly selecting one concordant
post and applying `Influence × Δ`. The command intentionally rejects
boolean-opinion Galam/Voter states.

The binary protocol is msgpack on stdin/stdout. Its current version is 1.
Requests without `version` are treated as version 0 and remain accepted;
new clients send `version: 1`. Responses always include `version: 1`, which
the Python binding checks before interpreting measurements. Most users should
call it through the Python binding:

```python
from smp_bindings import RawSimulationRecord

with RawSimulationRecord("./run", metadata) as rec:
    result = rec.evaluate_probe(
        steps=[0, 100, 500],
        h=0.02,
        replicates=20,
        binary_path="./smp-probe",
    )

print(result["rng"])  # generated or supplied probe RNG
points = result["results"][0]["points"]
f_probe = [point["f_probe"]["mean"] for point in points]
```

When `rng` is omitted, the binding uses the resolved RNG in `metadata.json`
when available; old data without one receives a generated RNG returned in the
response. If `RepostRate` is nonzero, `PostEvent` collection must have been
enabled because repost history cannot otherwise be reconstructed exactly. If
`RewiringRate` is nonzero, intermediate steps likewise require
`RewiringEvent`, unless that exact step already has a graph dump.

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

export interface RNGSpec {
    Algorithm: "pcg64-dxsm-v1";
    Seed1: string; // hexadecimal uint64, e.g. "0x0000000000000001"
    Seed2: string;
}

interface ScenarioMetadataBase {
    DataVersion?: 2; // generated as 2 when omitted; absent stored data means v0
    RNG?: RNGSpec;   // generated, emitted, and persisted when omitted
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
    TrajectoryPrecision?: { opinions?: "float16" | "float32" | "float64";
                            opinion_sums?: "float16" | "float32" | "float64" };
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
make test
make test-python
make benchmark
make benchmark-recsys
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
    TrajectoryReader,
    load_gonum_graph_dump,
    load_snapshot,
    load_events_db,
    load_event_body,
    batch_load_event_bodies,
    get_events_by_step_range,
)

# --- Lazy accumulated time-series channels ---
trajectory = TrajectoryReader("run/my-sim")
print(trajectory.channel("opinions").shape)       # (steps+1, agents)
print(trajectory.channel("agent_numbers").shape)  # (steps+1, agents, 4)
print(trajectory.row("opinions", 1000))

# --- Graph dump (msgpack) ---
import networkx as nx
g: nx.DiGraph = load_gonum_graph_dump("run/my-sim/graph-0.msgpack.lz4")

# --- Named exact checkpoint, when requested by CheckpointSteps ---
snap = load_snapshot("run/my-sim/checkpoint-000001000.msgpack.lz4")
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
