# Opinion groups: detection and observed lineage

For product/channel/scope selection, dense aggregate series and SQLite output,
see [selective collection](collection.md). That contract supersedes assumptions
that every analysis response must contain every lifecycle record.

`groups` owns an opinion-only detector and one-stream identity tracker. It does
not read records, inspect a follow graph or posts, advance a simulator, consume
RNG, or classify terminal outcomes. `probe` supplies the stream and selects
measurements; bridge observables consume confirmed observed groups separately.
There is no two-means fallback or separate legacy group API.

```go
options := groups.DefaultOptions(0.45)
tracker, err := groups.NewTracker(options)
frame, err := tracker.Observe(completedStep, opinions)
closing, err := tracker.FinalClose(lastObservedStep, "record_end")

var detector groups.Detector // zero-value workspace
detection, err := detector.Detect(opinions, options)
```

All settings are explicit. `DefaultOptions(epsilon)` returns bandwidth
`epsilon/6`, 1024 cells, minimum core mass 25, valley ratio .5,
half-prominence level .5, support in at least two of three bandwidths,
confirmation 3, exit 3, per-core Jaccard threshold .5, and lineage overlap
threshold .5. Zero options are invalid. Opinions must be finite and in [-1,1];
epsilon and bandwidth must be positive. Tracker population size is fixed.

## Single-state detector

The histogram covers the full physical domain [-1,1], including both endpoints.
Gaussian smoothing uses reflecting half-sample boundaries and a five-sigma
truncated, normalized kernel. The three bandwidths are h/sqrt(2), h, h*sqrt(2).
The implementation caches kernels and density arrays, and convolves occupied
histogram bins rather than allocating a fresh grid per state.

Local maxima include strict endpoint maxima and midpoint indices of flat local
maximum plateaus. Sub-macroscopic raw mode basins are removed before valley
comparisons. A shallow valley removes only its weaker adjacent peak, then
valleys are recomputed; exact ties remove the right peak. Final membership is
the closed half-prominence interval, followed by the minimum-core-mass rule.
The reference-bandwidth candidate survives when peak locations match at least
two bandwidths within max(h,h_other). This cross-band test is about peak
location, not temporal membership stability.

`Core.ID` is local to a detection and ordered by peak. `Members` contains sorted
agent indices. `Lower` and `Upper` are bin-edge coordinates of the closed
half-prominence support; `BasinLower` and `BasinUpper` are density-grid centers
of adjacent valleys/domain-edge cells. `SupportWidth` is the actual opinion
range of members. These are different quantities. No additional margin is
applied here; bridge compatibility/uncertainty settings remain separate.

`Detection.Labels` uses local core IDs, with -1 for residuals. Every agent is
retained as a member or residual; a tiny intermediate population is not forced
into the nearest large core. Single compact, single diffuse, multiple and
unresolved density states are reported separately. Detection of a core does
not imply a useful cross-group pair or an actual communication bridge.

`groups/testdata/legacy_detection.json` is a static numerical oracle generated
from the previously frozen Python Gaussian KDE detector; its source SHA256 is
included. It covers two/three modes, physical-domain boundaries, a point mass,
tiny intermediate shoulders, under-threshold mass, asymmetric populations and
five outcome-independent uniform random initial populations. Tests require
exact memberships, labels, scale support and mode counts, with 1e-9 numeric
tolerance for density/peak/support values. Production code never imports paper
artifacts or Python. This is a detector compatibility oracle, not a public
legacy bridge implementation. Floating-point summation order differs between
sparse Go convolution and SciPy; bitwise density equality is not promised.

## Persistent identities

Only confirmed groups are emitted in `Frame.Groups`; tentative cores remain
visible in `Frame.Detection`. Each candidate needs `Confirm` successive observed
memberships with its own Jaccard >= `JaccardMin`. Averaging a stable large group
with an unstable group cannot make the latter pass.

Confirmed groups have monotonically assigned persistent int64 IDs and retain
their first-observed, confirmation and last-observed steps. A continuing
one-parent/one-core association must itself pass the Jaccard threshold.
Memberships and support coordinates update on observed continuation; a group
ID does not freeze its members. Consumers that need frozen cohorts must
explicitly freeze them at their own epoch boundary.

The first observation's confirmed candidates emit `initial_present` with
`left_censored=true`, never a newly observed birth. A later candidate without
an observed predecessor emits `birth` after confirmation. `Event.Step` is when
the event becomes reportable; `IntervalStart/End` describe the first observed
qualification change. Confirmation time must not be silently backdated.

Lineage edges use member intersection divided by the smaller membership size;
this containment coefficient is distinct from Jaccard. Connected supported
components describe possible mergers/splits. Every child must independently
pass confirmation before committing a lineage transition. A confirmed merge
or split terminates parent IDs and creates new child IDs with explicit parent
and child lists. Many-to-many or low-Jaccard one-to-one replacement is
`reconfiguration`. These transitions do not also emit ordinary birth/fade
events. Ambiguous components awaiting confirmation keep their parents
`unobserved`; their tentative children do not enter bridge risk sets.

An unmatched group first becomes `State="unobserved"`. Its current macrocore
qualification is unavailable, even though its identity and last known core
remain recorded. Recovery before confirmed exit restores the same identity.
`Exit` consecutive missing dense observations emit `fade`: operational loss of
observed macrocore qualification, **not physical group death**. Stream closure
emits only right-censored `censored` events; it never invents a fade or death.

## Sampling gaps and ownership

Steps must increase. A gap larger than one completed native round emits
`unknown_interval`, sets `Frame.Gap`, increments the observational `Epoch`, and
resets consecutive missing-observation exit counts. A missing gap endpoint is
sample one of a new exit run;
only following consecutive native steps increase it. Repeated sparse gaps
reset it again. Candidate Jaccard evidence
may still accumulate across observed samples, allowing sparse data to identify
the current groups. Such new confirmations are always `initial_present` and
left-censored; they do not assert continuous qualification or a birth inside
the gap. Persistent IDs across a gap denote a membership association, not proof
that no unobserved split/merge occurred.

Bridge observers must close their prior activity epoch at each sampling gap or
unobserved group, then open any newly observable pair as initial stock. Output
thinning after dense observation is distinct from sparse input sampling.

When a merge/split candidate is confirmed using samples across a gap, its
children are `initial_present` and left-censored. `Parents` preserves membership
association evidence; it does not assert a merge/split inside the missing
interval. A future reporting step supplied to `FinalClose` remains the event's
reporting time, while its censoring interval ends at the last actual observation.

Returned slices own their storage and cannot be overwritten by a later call.
The tracker copies detector and membership values retained internally. One
Detector/Tracker belongs to one stream and is not safe for concurrent use;
independent instances can run concurrently.

## Probe and Python integration

Select `groups` to observe opinions alone. Select `bridges` to additionally
reconstruct the directed graph and available retained posts; it also enables
group tracking. Neither static selection constructs a simulator or resolves
its recommender/RNG. Options for unselected measurements are ignored. Explicit
`groups` and `bridges` option objects are complete settings, validated before
reading the source. Omitting them chooses the recorded/default epsilon and
the native versioned defaults.

`run_probe_run(..., steps=None)` continues to select only the last recorded
state. Supply `scan={...}` or use `analyze_run` to observe an interval. Native
`scan.stride` controls observations, so values above one mark unknown gaps;
`scan.output_stride` only reduces returned snapshots. Lifecycle records in
`response.analysis` use every observed state and are independent of snapshot
thinning. The last observed snapshot is always retained. A scan ending before
the source maximum closes surviving groups/bridge epochs with
`analysis_horizon`; an observation of the final recorded state closes with
`record_end`. Both are censoring, rather than observed loss.

For an explicit experiment, `bridge_cores=[[...], [...]]` freezes reference
cohorts instead of using detected lineages as bridge references. At least two
nonempty, disjoint cohorts of valid agent IDs are required. Explicit cohorts
may be smaller than `MinCore`. The response reports
`analysis.reference_mode="fixed_cohorts"`: bridge `group_ids` are one-based
input cohort IDs in this namespace, while `results[].groups` and group events
still describe independent dynamic lineages. Equal integer IDs across those
namespaces do not establish identity. Opening bridge epoch records carry the
actual frozen members, and union openings carry the pair-epoch references.

Unavailable collection evidence is distinct from a measured negative post
signal. Bridge-only saved-run observations may return nil posts when retention
metadata was not recorded, or when the repost rate is nonzero/unknown and
`PostEvent` was not explicitly enabled. The response records
`analysis.posts_unavailable_reason`; post-dependent status remains unknown,
while available graph/opinion signals are measured. A documented zero repost
rate permits reconstruction of original posts from recorded opinions. Invalid
metadata, corrupt recorded events and a missing advertised database remain
errors. Force reconstruction always requires its post inputs. The reader
does not invent posts to make a bridge known.

```python
from smp_bindings import ProbeClient, analyze_run

with ProbeClient("./smp-probe") as client:
    for run_directory in run_directories:
        result = analyze_run(run_directory, epsilon=0.45,
                             output_stride=20, client=client)
        consume(result["analysis"])
```

`ProbeClient` reuses one sequential JSONL worker. Each job creates fresh
observers; identities and RNG state do not cross jobs. A transport/job error
closes the client, and its context manager terminates the worker.
Calls from threads are serialized. A client belongs to its creating process:
after fork, construct a new client in the child. Closing or finalizing an
inherited client does not flush pipes or signal the parent's native worker.
Bulk JSONL jobs use a bounded wait after input EOF and reap a worker that fails
to exit, as does explicit client cleanup.
The caller owns returned results. An idle client does not retain the completed
inline input, the decoded reply payload or its full JSON text between jobs.
`RawSimulationRecord.evaluate_probe` forwards the same analysis selections and
returns typed `ProbeResponse`/`ProbeAnalysis`/group/bridge records. The binding
performs no group or bridge arithmetic.

Inline JSON and MsgPack post histories must explicitly contain non-null
`AgentID`, `Step` and `Opinion` in every record. Explicit zero is a valid author
ID or value. For bridges, `AgentID=-1` denotes unavailable author evidence;
omit the holder's post history when its displayed value is unavailable. Force
requires known in-range authors and complete histories. Missing/null fields
are rejected at decoding and cannot silently become author/value zero. Native
saved-record reconstruction keeps its existing representation and validation.

## Bridge role-bit layout

`role_bits`, `known_bits` and `uncertain_bits` use the same bit positions within
their respective pair or union scope. A set role bit denotes a known positive
status. A cleared bit is a known negative only when its known bit is set;
otherwise it is unknown. Uncertainty bits record numeric sensitivity, not a
confidence interval. The `scope` on lifecycle events identifies the layout of
their `from_role`/`to_role` and corresponding known masks.

| Pair bits | Signal |
|---|---|
| 0, 1, 2, 3 | S, O, P, U for side 0 |
| 4, 5, 6, 7 | S, O, P, U for side 1 |

Side indices follow `pair.group_ids`, independently of their current geometric
left/right order. Pair middle and bilateral/partition statuses are explicit
fields rather than additional role bits.

| Union bit | Status field |
|---|---|
| 0 | `structural` |
| 1 | `opinion` |
| 2 | `post` |
| 3 | `active` |
| 4 | `bilateral` |
| 5 | `opinion_bilateral` |
| 6 | `post_bilateral` |
| 7 | `mixed_bilateral` |
| 8 | `signal_both` |

Pair and union masks have separate layouts. Keep their scope and definition
version when storing masks. The full status fields retain availability detail;
decoding a role mask alone cannot recover unknown values or qualified weights.

## Independent channel ledgers

`bridges.Frame.BranchLedgerVersion=1` identifies the additive
`branch_events`, `branch_episodes` and `branch_summaries` collections. The
existing `events`, `episodes` and `summaries` remain U-holder ledgers. Branch
`channel` is `structural`, `opinion` or `post`, with its own known/seen history,
formation, loss, regeneration, episodes and exposure integrals. A post signal
can regenerate while an opinion signal keeps U continuously active. Structural
support describes raw incoming edges; it does not by itself qualify a U bridge.
An S-any loss requires the last supporting edge from both reference audiences
to disappear; a side loss requires the last edge from that side. Removing an
edge while support remains does not count as a structural-holder loss.

Default branch records concern either side of a pair; absent `side` means any.
Set `bridges.Options.BranchSides` / `branch_sides=true` for additional independent
pair-side records. Their `side=0/1` follows `group_ids`. Union branches always
deduplicate over all reference pairs and have no side records. Branch masks
have their own layout: pair bits 0/1 are the channel's side 0/1; a side record
retains only its side bit. Union branch bit 0 is the channel union status. These
masks must not be decoded using the main U role-bit tables above.

Branches share the published pair/union epoch boundaries and frozen references.
Unknown observations censor active episodes and exclude unknown time from known
risk; later known positives resume as left-censored `initial_present`, rather
than inferred regeneration. Gaps, reference changes and final closure retain the
same censoring semantics as U. `population` and `unknown_holder_rounds` on each
branch summary make its exposure partition explicit:
`known = active + never_seen + seen_inactive` and
`population * (end-start) = known + unknown`. The integral covers native observed
intervals, not the number of returned snapshots.

`analysis.events_included` and `analysis.episodes_included` state whether the
corresponding U and branch collections were requested. Summary collections are
always retained. A deliberately omitted collection does not establish a zero
event count. The [canonical paper definition](../../extended-hk-model-paper/docs/group_bridge_definitions.md)
specifies the scientific interpretation; this section describes the API layout.

## Checks and performance

```sh
go test ./groups -count=1
go test -race ./groups -count=1
go vet ./groups
go test ./groups -run '^$' -bench . -benchtime=100x -count=3 -benchmem
```

Measured on Windows/amd64, Go 1.27.4, i7-11800H, with concurrent development
activity: cached 500-agent two-point detection took about 21--41 microseconds,
8576 bytes and 5 allocations/call; a diffuse 500-point population took
504--553 microseconds, 8320 bytes and 3 allocations/call. Stable tracker calls
took about 27--43 microseconds, 26560 bytes and 33 allocations/call. Output
ownership accounts for retained result allocations. These are local benchmark
observations, not whole-scan speedup guarantees.

A subsequent ownership-preserving audit shares one private immutable detection
among internal active/pending references and the closing snapshot. The same
500-agent tracker benchmark now allocates 22464 bytes and 31 allocations/call:
4096 fewer bytes, about 15.4%. Observed runtimes overlapped the earlier range;
this allocation reduction is not a measured whole-scan speedup. Returned member
and detection buffers remain caller-owned.

An initial generic modulo reflection loop took roughly 1.47--1.53 ms for the
same diffuse case. An equivalent fast boundary branch reduced that cost while
preserving all numerical oracles. Tests additionally compare sparse reflection
with an independent dense sum, including kernels wider than the domain, check
unit histogram mass and all-agent retention, and exercise birth/fade,
temporary invisibility, merge/split/reconfiguration, sparse sampling, validation
and output ownership independently of KDE.
