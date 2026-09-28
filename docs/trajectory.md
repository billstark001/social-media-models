# Trajectory storage and precision

Persistent runs write a `trajectory.json` manifest and numbered `.smpc` blocks.
`RawSimulationRecord` and `TrajectoryReader` are the Python readers. Existing
version-2 blocks and legacy `acc-state-*.lz4` files remain readable.

## Precision choices

`ScenarioMetadata.TrajectoryPrecision` selects storage precision independently:

```json
"TrajectoryPrecision": {"opinions": "float32", "opinion_sums": "float16"}
```

Each field accepts `float16`, `float32`, or `float64`; omitted fields use the
shown defaults. This affects historical trajectory rows only. Model arithmetic,
complete recovery snapshots, and named research checkpoints retain `float64`.
The four agent counts remain exact `int16`.

The default keeps opinions at `float32` because direct `float16` quantization
hides slow motion: in a 500-agent, 5000-step HK run at α=0.001 and q=0.5,
about 92% of individual changes over the last 100 steps became zero. For the
same run, converting the four opinion sums to `float16` produced a
99th-percentile error of about `1.1e-8` in the HK single-step drift computed
from those sums and exact counts. Threshold crossings and other observables
still require case-specific validation. Use `float64` when historical values
must retain model precision; use `float16` opinions only when quantized slow
motion is acceptable.

The writer converts each sample once into a reusable packed buffer. It does not
retain a separate unconverted `float64` history. A 256-step, 500-agent default
buffer holds about 2.44 MiB of raw channel bytes (`4+8+8` bytes per agent and
step), before temporary compressor workspace.

Observed `.smpc` totals (binary MiB): the 500-agent case is a fixed-seed
full run with each precision setting; the 200-agent figures re-encode an
existing `float32` source run.

| Opinions / sums | 500 agents × 5001 rows | 200 agents × 2001 rows |
|---|---:|---:|
| float16 / float16 | 14.65 | 2.62 |
| **float32 / float16 (default)** | **19.16** | **3.32** |
| float32 / float32 | 30.95 | 5.19 |
| float64 / float64 | 64.65 | — |

These totals exclude graphs, events, snapshots, checkpoints, and the manifest.
For the 500-agent full run, wall times were 11.38 s (`float16/float16`),
9.29 s (default), 9.92 s (`float32/float32`), and 12.00 s
(`float64/float64`). These single-run timings include simulation and all
output I/O, so small differences should not be interpreted as stable speed
rankings. Compression ratio and runtime vary with the data. A direct
`float64` run is needed to measure that setting: expanding rounded `float32`
source data gives an unrepresentative result.

## Manifest and blocks

The version-3 manifest records the agent count, next completed-row index,
selected precision, and ordered block descriptors (`start`, inclusive `end`,
filename, SHA-256). Blocks are contiguous from row zero. Writers publish a
block by writing a temporary file, renaming it, then atomically replacing the
manifest. On recovery, blocks newer than the latest complete model snapshot
are discarded. Readers verify a block checksum when they load it; recovery
checks the index and file presence without rereading all history.

Each new block has eight-byte magic `SMPTRJ03`, little-endian `uint32` step and
agent counts, one byte each for opinion and opinion-sum word widths (`2`, `4`,
or `8`), then three channel records:

1. opinions: `(step, agent)` IEEE `float16`, `float32`, or `float64`;
2. four agent counts: `(step, agent, component)` signed `int16`;
3. four opinion sums: `(step, agent, component)` IEEE `float16`, `float32`, or `float64`.

A channel record holds one codec byte, little-endian `uint32` payload length,
and that many payload bytes. Codec 0 stores raw bytes in an LZ4 frame. Codec 1
stores each word XORed with the preceding row's corresponding word in LZ4;
the first row uses zero. Codec 2 encodes an all-zero channel without a payload.
Codec 3 packs exactly `0.0`/`1.0` opinions as bits before LZ4. Codec 4
byte-shuffles words before Zstd. Codec 5 transposes word bits into planes
before Zstd. The writer picks the smallest candidate for each channel. All
codecs restore the selected stored precision exactly. `float16` conversion
rounds to nearest, ties to even, and rejects values outside its finite range.

Version-2 `SMPTRJ02` blocks have implicit `float32` opinions and sums and stay
readable. On resume, a version-2 run may gain version-3 blocks; the upgraded
manifest records `legacy_chunks`, the count of initial old blocks. Python
reports a common dtype for a channel containing both precisions, and probe
states label the precision of their particular row.

Graph checkpoints are `graph-<step>.msgpack.lz4`. Rewiring and repost events
remain in `events.db`. The historical graph is reconstructed from a checkpoint
and ordered events. Historical ordinary post opinions come from the stored
opinion channel. Such reconstructed states cannot start exact stochastic
branches.

`checkpoint-<step>.msgpack.lz4` files are independent complete model states.
Their envelope includes physical metadata, completed step, and consecutive
stable step count; the model payload retains `float64` opinions, graph, posts,
recommender data, and all named RNG states. The batch runner accepts only these
metadata-bearing checkpoints as exact branch sources. Rotating
`snapshot-*.msgpack` files support crash recovery.
