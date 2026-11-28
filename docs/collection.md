# Selective group and bridge collection

Collection selects products independently of scientific group/bridge options.
All profiles use the same observer. Filtering precedes entity-record and
unrequested-lifecycle-history allocation.

```json
{
  "measurements": ["groups", "bridges"],
  "collection": {"profile": "plot"},
  "output": {"directory": "D:/results/a-run", "archive_native": false},
  "scan": {"start": 0, "stride": 1, "output_stride": 50}
}
```

Direct-source CLI flags expose the same selection:

```sh
smp-probe --run-directory path/to/run --scan --measurements groups,bridges --collection-profile plot --channels U,O,P,S --scopes union --output-directory path/to/result
```

`--products` overrides the profile's product list; `--archive-native` requires
`--output-directory`. Stdin JSON/JSONL/MessagePack use the request fields above.

`collection.channels` selects independent `U`, `O`, `P`, `S` ledgers.
`collection.scopes` selects `pair` and/or `union`. `collection.products` selects
`summaries`, `series`, `events`, `episodes`, `conversions`, `group_events`,
`snapshots`, and `details`. Explicit arrays override the profile's corresponding
array. Invalid, duplicate, or empty choices are errors. Holder `details` imply
`snapshots`; the resolved selection records this dependency.

| Profile | Channels | Scopes | Products |
| --- | --- | --- | --- |
| `plot` | U/O/P/S | union | summaries, series |
| `lifecycle` | U/O/P/S | union | summaries, series, events, episodes, conversions |
| `audit` (default) | U/O/P/S | pair, union | summaries, events, episodes, conversions, group_events, snapshots |

`events` selects initial stock and formation/loss/regeneration records.
`conversions` separately selects continuous role/availability change records.
Summaries and series retain their conversion counters even if individual
conversion records are not requested. Missing products are unavailable, not
measured empty ledgers. `analysis.collection` and output metadata record actual
capabilities. Scientific versions are separate from the storage schema version.

With no explicit collection, `Options.DetailOutput=true` is translated to the
`details` product. Old `scan.include_events` / `scan.include_episodes` booleans
are shorthand selectors for this same collector. A false flag removes products
before observation; it does not generate rows merely to discard them later.

## Computation and observation

`plot` observes every native state. It constructs no entity event or episode
objects. Each selected ledger retains active/known/seen state needed to separate
formation from regeneration and handle unknown/censoring. Union-only collection
does not allocate pair lifecycle histories. Pair references and signal
projections remain necessary for deduplicated union decisions. U still depends
on O/P qualification when separate O/P ledgers are unrequested. Snapshot-only
collection allocates no lifecycle histories.

`output_stride` only thins snapshots. `stride > 1` changes observation to a
gap-censored history; it is not a size optimization for exact event statistics.
No-pair intervals are outside the bridge observation domain, not measured zero
bridge counts included in its denominator.

## Aggregate series

Each `StepStats` identifies scope, pair IDs, epoch, channel and optional side.
`Step` assigns event counters to their observed boundary. Holder-round exposure
integrates the preceding state on `[IntervalStart, IntervalEnd)` in that epoch.
It partitions time into active, never-seen, seen-inactive and unknown exposure.
`Active`, `Known`, `NeverSeen`, `SeenInactive`, `Unknown` are boundary stocks.

Open/close records may have zero duration. An old epoch closing and a new epoch
opening may share a step; preserve both records. `Closing` marks closure.
Counters include initial stock, formation, loss, regeneration, conversion,
censored episodes, uncertain F/L/R and unknown entries/exits. Final closure does
not integrate beyond the last observation, and gaps are not interpolated.
Changing an analysis window does not reset the original epoch's seen history.

Per-run series and summaries support equal-run or pooled rates, integer windows,
native-time or t/source-T bins, and distributions of time-weighted mean counts.
They do not recover individual trajectories or episode lifetime distributions.
Request entity products when those are needed.

## Persistence and ownership

Without `output`, the response accumulates selected products in memory. With
`output.directory`, each frame is written to SQLite before the next native state
is read. The response contains metadata and a small receipt, not full histories.
The directory must be outside the source saved run; completed outputs are not
overwritten.

`results.sqlite` is the canonical selected dataset. `metadata.json` records
population, source horizon, definition/configuration versions, observation
coverage, resolved collection, completion status and file receipts. Successful
metadata marks completion. Optional `native.msgpack.zst` preserves the selected
native response using ordinary named maps, without a shared-field layout.
Archiving does not enable uncollected products. CSV is an on-demand export;
native JSON payloads are not generated.

Physical schema, enums, completion and recovery details are specified in
[collection storage](collection_storage.md).

File-backed collection retains no run-long product arrays. Direct observer
results and in-memory responses own their data. Later observations cannot mutate
prior results. File errors abort incomplete output rather than returning a
completed receipt.
