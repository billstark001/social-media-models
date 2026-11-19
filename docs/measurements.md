# Canonical measurements and offline inputs

`observables.StateMeasurer` selects and combines the existing Go energy,
pathway, binning, landscape and empirical terminal kernels. Their numerical
definitions are unchanged. Each selected measurement validates its own inputs;
unselected measurements impose no requirements. Its zero value is ready to use.

- `smp` records simulation state and optional actual feed events.
- `smp-batch` measures live endpoints through the shared evaluator and collects
  actual exposure during updates.
- `smp-probe` evaluates saved or synthetic states without advancing a simulator.
  Counterfactual force additionally uses posts, a recommender and a probe RNG.
- `record.Reader` reads persistent runs without creating tables, truncating
  histories or changing snapshots.

Event connections use SQLite read-only mode. SQLite may maintain its transient
WAL shared-memory/locking sidecars; recorded database contents and simulation
histories are unchanged. The reader does not copy entire databases to observe them.

The simulator opens event databases in WAL mode with full commit synchronization
and foreign keys enabled on every connection. This also avoids false
`SQLITE_READONLY_DBMOVED` errors from rollback-journal file-identity checks on
ExFAT/FSKit volumes. Keep the database together with any `-wal` and `-shm`
sidecars while it is open; close the simulator before archiving its output.

## Go API

```go
var measure observables.StateMeasurer
result, err := measure.Measure(opinions, graph, observables.StateOptions{
    Energy: &observables.EnergyOptions{Epsilon: 0.45, Scale: 1},
    Pathway: &observables.PathwayOptions{Epsilon: 0.45},
    Bins: []float64{-1, 0, 1},
    Terminal: &terminal.Options{Epsilon: 0.45, MajorMass: 0.02},
})
```

The evaluator supplies no hidden scale or threshold defaults. Each group can
use its own epsilon. Empirical terminal classification uses its existing
`0.5/N` occupied-mass cutoff; `Terminal.OccupiedMass` must be zero.

## Native CLI

Build once with `go build -o smp-probe ./cmd/smp-probe`. These commands require
only the executable and saved files:

```sh
./smp-probe --run-directory ./run/example --steps 0,100,200 \
  --measurements energy,pathway,bins,terminal --bins=-1,0,1

./smp-probe --checkpoint ./state.msgpack.lz4 \
  --measurements force,energy,pathway --grid-step 0.1

./smp-probe --format json < request.json > result.json
./smp-probe --format jsonl < jobs.jsonl > results.jsonl
```

Direct source flags produce JSON. A run's omitted `--steps` selects its last
recorded state. Explicit steps must increase without duplicates. Repeat
`--checkpoint` for checkpoints with matching physical metadata; use independent
JSONL jobs for different runs or configurations. The no-argument MsgPack mode
uses the same request and evaluation API as JSON.

Saved-run responses include `source_max_step`, the actual last recorded state.
For an archived run without `metadata.json`, an explicit `run_metadata` object
can provide its known configuration. Supplying it when a metadata file exists
is rejected; observation never writes or replaces metadata.

A JSONL input contains one request per line. For example:

```json
{"request_id":"run-a","version":1,"run_directory":"./run/a","steps":[0,100],"measurements":["energy","pathway","terminal"]}
{"request_id":"checkpoint-b","version":1,"checkpoint_paths":["./b.msgpack.lz4"],"measurements":["pathway"],"epsilon":0.45}
{"request_id":"synthetic","version":1,"states":[{"step":0,"opinions":[-0.5,0.5]}],"measurements":["terminal"],"terminal":{"epsilon":0.45,"major_mass":0.02}}
```

Each output line carries `line`, optional `request_id`, `status` and either
`result` or `error`. Invalid jobs receive an error and later jobs continue.
Unknown fields and trailing objects are rejected. Command JSON and MsgPack use
the same field names. Go MsgPack clients should configure both encoder and
decoder with `SetCustomStructTag("json")`, as the command does. This leaves the
existing default binary encoding of shared types such as `terminal.Result`
unchanged outside the command protocol.
An oversized JSONL request receives a per-line error after its input is drained;
later jobs still run. The line limit is 64 MiB, including its delimiter.

Sources are mutually exclusive: `states`, `checkpoint_paths`, or
`run_directory`. Inline topology uses the existing directed NetworkX graph
shape with dense node IDs `0..N-1`. Terminal-only requests need only opinions.
Checkpoint inputs use the existing HK/Deffuant float64 checkpoint inspector;
counterfactual force also supports those two continuous dynamics. Numeric
inline states and saved-run channels can use static measurements independently
of force configuration.
Graph-dependent selections require a complete graph. `bins` requires explicit
boundaries. `landscape` and `counterfactual_neighbor_energy` use the opinion
`grid`; the latter also requires `anchor_ids`.
Grids are limited to 1,000,001 points so erroneous spacing cannot trigger an
unbounded allocation before evaluation.

Common `epsilon` overrides continuous-dynamics tolerance for static metrics.
`energy_epsilon` overrides only the energy family. An explicit `terminal`
object supplies complete terminal options, including a valid zero epsilon;
when omitted, terminal uses the common epsilon and `major_mass=0.02`.
Force uses the source dynamics parameters and has an independent probe RNG.
Static measurements do not resolve or consume that RNG.

## Saved-run requirements

The native reader supports the existing accumulated float32 format and current
trajectory manifests, including mixed historical precision. It verifies block
checksums when reading and reports `history_f32`, `history_f64`, or
`checkpoint_f64` in each result.

Graph reconstruction uses exact graph dumps and ordered rewiring events. When
rewiring is possible, interpolation between dumps requires explicit
`RewiringEvent=true`; a declared zero rewiring rate needs no event replay.
Terminal-only reads skip graph reconstruction entirely.

Force reconstruction uses opinion rows for original posts and logged reposts
for their original author, opinion and publication step. A nonzero or unknown
repost rate requires explicit `PostEvent=true`. Missing histories produce errors.
No synthetic posts are substituted for unavailable force inputs.

The reader decodes one trajectory block at a time. Legacy accumulated files
retain their opinion rows and skip unused count/sum channels while validating
the full compressed payload. `probe.Evaluate` retains the
selected frozen states and their results, so memory also scales with the number
of selected steps. States within a job share one probe RNG lifecycle; splitting
a force job into separate jobs changes that sampling lifecycle.

For modern blocks, unused counts and opinion sums are skipped entirely while
the block checksum and framing remain verified. Post and rewiring histories use
one filtered ordered query each, including when selected steps cross many graph
anchors. Old channel-major files still retain `8*N*T` bytes of decoded opinions.

Actual exposure comes from recorded `ViewPosts` events or batch feed traces.
Checkpoints supply a physical state and counterfactual measurements; past actual
slot order and attribution require the corresponding simulation-time records.
Obsolete names such as `ViewTweets` and parameter spelling aliases require an
explicit migration. The reader never migrates data while observing it.

## Python orchestration

```python
from smp_bindings.probe import run_probe_run, run_probe_jobs

result = run_probe_run(
    "./run/a", [0, 100], measurements=["energy", "pathway"],
    binary_path="./smp-probe",
)

for result in run_probe_jobs([
    {"run_directory": "./run/a", "steps": [100], "measurements": ["pathway"]},
    {"checkpoint_paths": ["./b.msgpack.lz4"], "measurements": ["energy"]},
], binary_path="./smp-probe"):
    print(result["results"])
```

The bindings send source paths and options to Go. `run_probe` also accepts
explicit `FrozenSimulationState` inputs for synthetic analyses; static requests
can omit dynamics metadata and provide their measurement parameters directly.
Pass `metadata=...` to `run_probe_run` only for archived directories lacking
`metadata.json`. Historical post reconstruction belongs to the Go reader;
the Python `freeze_record` and `RawSimulationRecord.freeze` APIs have been removed.
Offline pathway callers use `run_probe_run`, `run_probe_checkpoints`, or
`run_probe_jobs`; batch accepts simulation requests only.

## Persistent online studies without Python

Use `smp --job /absolute/path/job.json` to retain the normal trajectory channels,
repost and rewiring logs, graph dumps, and model recovery snapshots. The job file
has this shape; `metadata` uses the same fields as the positional SMP CLI.

```json
{
  "base_path": "/data/run",
  "metadata": {
    "UniqueName": "paired-switch",
    "RNG": {"Algorithm": "pcg64-dxsm-v1", "Seed1": "0x1", "Seed2": "0x2"},
    "DynamicsType": "HK",
    "HKParams": {"Tolerance": 0.45, "Influence": 0.05, "RewiringRate": 0.05, "RepostRate": 0.1},
    "RecsysFactoryType": "OpinionM9",
    "RecSysParams": {"LogRecommendations": false},
    "MaxSimulationStep": 640
  },
  "fixed_horizon": true,
  "observe_at": [128, 144, 640],
  "policy_change": {"recommender": "StructureM9", "at": 128},
  "summary_path": "/data/run/paired-switch/observations.json",
  "exposure_trace": "/data/run/paired-switch/feed.jsonl.gz",
  "trace_start": 128
}
```

Use the RNG algorithm spelling returned by the ordinary CLI's RNG progress event;
explicit seeds are required for jobs. Observation steps and policy triggers are
absolute completed steps. The first observation has zero exposure and relative
step zero; later exposures cover the preceding observation interval. The run
retains every raw step starting at zero, including the reference-policy prefix.
For paired interventions, reuse the initial seed and reference metadata, observe
at the shared switch step, and apply the policy immediately after that observation.
No shortened history is presented as a complete trajectory.

The final `observations.json` includes pathway endpoints, window exposure counts,
terminal classification, final opinions, and the realized switch step/fallback.
Set `fixed_horizon` to continue through the requested horizon after stability.
`policy_change` accepts either `at` or `excess_at_least` with `latest`, an optional
`duration` pulse, and optional `bridge`/`hide_reposts` interventions. Optional
`exposure_trace` collects raw ordered feeds only after `trace_start`; ordinary
trajectory counts and online exposure summaries do not require full SQLite
`ViewPostsDetails` events.

SIGINT or SIGTERM saves an unfinished recovery snapshot and exits with status
130. Repeating the same job resumes its microscopic RNG, active recommender/cache,
policy trigger, partial observation window, and completed endpoints. Gzip traces
seal one member per checkpoint and save a byte offset; recovery discards any tail
written after that snapshot. Repeating a finished job can rebuild a missing
summary. The native runner freezes its resolved job and takes an exclusive run
lock, independently of any Python scheduler.

Research runs use `DataVersion=3`, which older SMP CLIs reject. Ordinary runs
continue to default to version 2. Static offline measurements support both.
Batch resume rejects checkpoints carrying research recovery state; resume those
with the same persistent job instead. Force reconstruction from version-3 run
directories or intervention checkpoints
requires explicit frozen states and an explicitly chosen recommender; it does
not silently substitute the reference policy for an active intervention.
