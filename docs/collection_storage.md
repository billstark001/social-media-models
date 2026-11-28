# File-backed probe collections

Collection selection controls scientific products; `output` controls their
persistence. They are independent of bridge definition options. A request may
set:

```json
{
  "collection": {"profile": "plot"},
  "output": {
    "directory": "D:/analysis/run-001",
    "archive_native": false,
    "metadata": {"dataset": "example", "run_id": "001"}
  }
}
```

The output directory must be disjoint from the original saved-run directory.
Existing symlinks are resolved before checking both ancestor directions. A
completed collection is never overwritten. Independent runs use independent
databases, so parallel readers do not contend for a global SQLite writer.

## Products and metadata

`results.sqlite` is the sole ordinary persistence of selected scientific data.
The observer passes each completed frame directly to prepared SQLite inserts;
the response does not retain run-length event, episode, series or snapshot
arrays. A transaction and bounded SQLite page cache control memory. No complete
native JSON or CSV copy is written.

`metadata.json` is written last and marks completion. It contains schema and
definition versions, population, source terminal, observation coverage, resolved
selection, actual analysis options, unavailable-post reason, row counts and
payload hashes. Caller `output.metadata` is kept under `user_metadata`; it cannot
overwrite these authoritative fields. The protocol response returns a small
`output` receipt with all payload hashes, including the metadata file itself.

An operating-system advisory lock prevents simultaneous writers. Process exit
releases the lock. An incomplete marker identifies unpublished files that the
next writer may remove and rebuild. Abort removes the writer's incomplete
payloads. The persistent zero-byte lock file is not a scientific product.

## SQLite schema version 1

`PRAGMA user_version=1` identifies this physical schema. Changes to physical
record fields require a schema version change and a corresponding reader
migration; deriving scalar columns from typed Go records does not remove that
requirement.

| Table | Physical layout |
| --- | --- |
| `run_info` | One completed run's population, terminal and definition versions |
| `domains` | Unique integer ID for scope, group pair and epoch |
| `symbols` | Shared integer dictionary for textual state, event type and reason |
| `epochs` | Domain reference, opening/closing fields and optional frozen references |
| `summaries` | Domain, channel, side, exact integer counts and risk exposures |
| `step_stats` | Domain, channel, side, interval bounds, boundary stock and transition counts |
| `events`, `episodes` | Optional selected holder-level lifecycle records |
| `group_events` | Optional lineage records |
| `snapshots` | Optional step plus ordinary MessagePack snapshot map |

Normalized records reference `domain_id`; they do not repeat scope/group/epoch
strings. Channel IDs are U=0, O=1, P=2, S=3. Scope IDs are pair=1, union=2. Side
-1 means any-side; 0/1 are explicit pair-side ledgers. Scalar counts, masks and
times use SQLite integers without floating-point conversion. Optional episode
start/end are SQL NULL. Infrequently queried arrays (frozen core members,
reference pairs and lineage ID lists) use ordinary MessagePack blobs. The main
statistical fields remain directly queryable SQL columns.

Row IDs retain emission order. An epoch close and a new epoch opening at the
same step remain separate rows; `interval_start`/`interval_end` identify the old
state's exposure independently of the new boundary stock. Selection metadata
distinguishes uncollected tables from available tables containing zero rows.

Pair epoch references and frozen members are shared reference metadata needed
to audit a deduplicated union. They remain available in a union-only collection;
their presence does not imply that independent pair summaries, risks, events or
episodes were collected. Readers may filter the epoch table by pair scope while
still rejecting requests for an unselected pair lifecycle ledger.

## Optional native archive

`archive_native=true` additionally writes `native.msgpack.zst`. It is an ordinary
named-map response reconstructed from the selected SQLite records one row at a
time, with standard MessagePack serialization followed by zstd. It uses no shared
field-table or private array-row encoding. Native archive selection does not
enable additional measurement products. Its result may intentionally lack
unselected holder histories or diagnostics. The selected products and their
availability remain explicit in the archive's analysis metadata.

The archive is for optional transport/debugging preservation. Statistical
consumers should query the database instead of materializing it into memory.
