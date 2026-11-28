# Bridge observer contracts

`NewObserver(DefaultOptions(epsilon))` creates one ordered, fixed-population
stream. An observer is not concurrent and its zero value is invalid. `Measure`
validates the whole supplied state, including when no core pair is eligible;
an error never advances the lifecycle ledger. Opinion indices and graph node IDs are
dense `0..N-1`. Latest-post original authors are `0..N-1`, or explicit `-1`
when author evidence is unavailable. Other author IDs are rejected. Group IDs
must be nonnegative and unique across the whole frame, including unobserved groups.

`Measure` uses the last record in each holder's supplied placement history.
Do not sort that history by original posting step: reposts can carry older
original timestamps. `NativeMeasure` reads each live agent's actual `CurPost`,
not the last retained `PostMap` entry. It requires a consistent dense native
graph, schedule and agent map, and does not advance the model or consume RNG.

The observer freezes core member IDs when a pair epoch opens and owns those
copies. It updates their current geometry each frame. Membership incidence is
cached until pair epochs change, so an existing pair and a newly opened pair
can legitimately retain different member sets for the same persistent group ID.
`FixedGroupsFrame` declares an experimental cohort; it does not claim dynamic
group confirmation. Empty `FixedPairs` means all eligible pairs.

All returned frames, holder slices, opening reference lists and episode endpoint
pointers are owned by the result. A later call cannot mutate them, and a caller
may mutate a returned result without changing the observer. The default omits
holder detail. Product selection and aggregate series are specified in
[selective collection](../docs/collection.md). A closed observer releases
its graph, post, reference and holder workspaces. While open, small epoch-number
counters remain for previously seen pair IDs so returning IDs cannot reuse an
earlier epoch number; it does not retain completed episode histories internally.

## Observation and uncertainty

Dense exposure uses the preceding known state on `[previous_step, step)`.
Group/reference changes close the old domain at the current boundary and censor
it. A sampling gap, including explicit `Frame.Gap` at adjacent timestamps, closes
at the last observed step and does not interpolate exposure through the gap.
`FinalClose` similarly does not extrapolate beyond the last measurement.

An unknown signal censors the affected holder episode, not the whole pair. It
does not erase whether that holder has already been **observed** active. A known
active state directly after unknown is left-censored initial stock. If a later
known inactive state is followed by active, it is regeneration when active was
observed earlier in the epoch, otherwise first observed formation. This is an
observed-history classification, not proof that the holder never qualified in an
unobserved interval. Exposure excludes unknown states and partitions known time
into active, never-observed-active and seen-inactive holder-rounds.

Numeric guard flags are conservative sensitivity markers, not confidence bounds.
Their boolean aggregation is commutative and associative: a stable positive
witness fixes an OR; a stable negative witness fixes an AND. Episode flags include
numeric sensitivity at entry/exit boundaries as well as active observed states.

## Main ledger and channel detail

The main ledger tracks **U**, meaning at least one qualified O or P side. It
records initial stock, formation, loss, regeneration and continuous role/known-mask
conversion. The deduplicated union has its own ledger and closes/reopens when its
reference-pair domain changes. Pair directions are never invented for union.

`BranchLedgerVersion=1` adds independent opinion, post and structural histories
in `BranchEvents`, `BranchEpisodes` and `BranchSummaries`. Existing `Events`,
`Episodes` and `Summaries` remain U-only. The channel records flatten the same
base schema and add `channel` plus optional `side`. Their full key is
`scope × group_ids × epoch × channel × side`; omitted/nil side means any-side.

Default tracking includes each pair's any-side O/P/S and the deduplicated union's
O/P/S. `Options.BranchSides=true` additionally tracks pair side 0 and side 1 for
each channel. Union never acquires a global left/right side. Structural means
physical `S_any` support, **not** the `structural_only` partition, so its ledger
can be active while U is inactive. Each channel has its own known/seen history,
episodes, censoring and risk exposure; missing P does not interrupt observed O.

Pair branch masks use only two normalized bits for their own channel:
bit 0 = side 0, bit 1 = side 1. A side-specific record keeps just its selected
bit. Union branch masks use bit 0 for that channel's boolean status. These are
different layouts from main U's S/O/P/U masks. Other channels cannot create a
branch conversion. Union has no direction conversions. Channels share their
parent reference epoch and close reason and do not emit duplicate Epoch records.

`BranchSummary.Population` makes its observation domain explicit, with
`KnownHolderRounds + UnknownHolderRounds = Population × (End - Start)`.
Known exposure partitions into active, never-observed-active and seen-inactive
holder-rounds. Sampling gaps are excluded by epoch censoring, never filled as
known inactivity. For example O-only → both → P-only → both leaves U continuous,
while O records loss/regeneration and P records its first observed formation.
Conversion counts are still separate from each channel's entry/exit events.

## Implementation checks

```sh
go test ./bridges -count=1
go test -race ./bridges -count=1
go vet ./bridges
GOMAXPROCS=1 go test ./bridges -run '^$' -bench . -benchtime=1s -count=3 -benchmem
```

Native adaptation reuses structural adjacency and contiguous current-post/opinion
workspaces; it does not serialize per-edge attribute maps. Missing core pairs skip
compatibility aggregation after full input validation. These optimizations preserve
the measurement domain, input rejection and result ownership contracts.
