// Package bridges measures cross-core structural and opinion/post support and
// tracks qualification episodes without changing the simulation.
package bridges

import (
	"fmt"
	"math"
)

const DefinitionVersion = 2
const BranchLedgerVersion = 1

// Options is explicit: a zero epsilon is invalid. Guard marks conservative
// sensitivity near compatibility/band boundaries; it does not change epsilon.
type Options struct {
	Epsilon       float64    `json:"epsilon" msgpack:"epsilon"`
	Guard         float64    `json:"guard" msgpack:"guard"`
	MiddleMargin  float64    `json:"middle_margin" msgpack:"middle_margin"`
	DetailOutput  bool       `json:"detail_output" msgpack:"detail_output"`
	StopAtContact bool       `json:"stop_at_contact" msgpack:"stop_at_contact"`
	BranchSides   bool       `json:"branch_sides" msgpack:"branch_sides"`
	FixedPairs    [][2]int64 `json:"fixed_pairs,omitempty" msgpack:"fixed_pairs,omitempty"`
}

func DefaultOptions(epsilon float64) Options { return Options{Epsilon: epsilon} }

func (o Options) Validate() error {
	if math.IsNaN(o.Epsilon) || math.IsInf(o.Epsilon, 0) || o.Epsilon <= 0 {
		return fmt.Errorf("bridges: epsilon must be positive and finite")
	}
	if math.IsNaN(o.Guard) || math.IsInf(o.Guard, 0) || o.Guard < 0 || o.Guard >= o.Epsilon {
		return fmt.Errorf("bridges: require 0 <= guard < epsilon")
	}
	if math.IsNaN(o.MiddleMargin) || math.IsInf(o.MiddleMargin, 0) || o.MiddleMargin < 0 {
		return fmt.Errorf("bridges: middle margin must be nonnegative and finite")
	}
	seen := make(map[[2]int64]bool)
	for _, pair := range o.FixedPairs {
		pair = ordered(pair)
		if pair[0] < 0 || pair[0] == pair[1] || seen[pair] {
			return fmt.Errorf("bridges: invalid or duplicate fixed pair %v", pair)
		}
		seen[pair] = true
	}
	return nil
}

// Status separates unavailable information from a known negative decision.
// Uncertain is a sensitivity flag, not a probability or confidence interval.
type Status struct {
	Known     bool `json:"known" msgpack:"known"`
	Active    bool `json:"active" msgpack:"active"`
	Uncertain bool `json:"uncertain" msgpack:"uncertain"`
}

// SignalState.Count/Weight describe qualified followers when CountKnown is
// true; unavailable counts must not be interpreted as measured zero.
type SignalState struct {
	Status        Status  `json:"status" msgpack:"status"`
	Count         int     `json:"count" msgpack:"count"`
	Weight        float64 `json:"weight" msgpack:"weight"`
	CountKnown    bool    `json:"count_known" msgpack:"count_known"`
	SourceKnown   bool    `json:"source_known" msgpack:"source_known"`
	CrossBoundary Status  `json:"cross_boundary" msgpack:"cross_boundary"`
}

type HolderState struct {
	Holder           int            `json:"holder" msgpack:"holder"`
	S                [2]SignalState `json:"s" msgpack:"s"`
	O                [2]SignalState `json:"o" msgpack:"o"`
	P                [2]SignalState `json:"p" msgpack:"p"`
	U                [2]SignalState `json:"u" msgpack:"u"`
	Active           Status         `json:"active" msgpack:"active"`
	SignalBoth       Status         `json:"signal_both" msgpack:"signal_both"`
	OpinionOnly      Status         `json:"opinion_only" msgpack:"opinion_only"`
	PostOnly         Status         `json:"post_only" msgpack:"post_only"`
	StructuralOnly   Status         `json:"structural_only" msgpack:"structural_only"`
	Bilateral        Status         `json:"bilateral" msgpack:"bilateral"`
	OpinionBilateral Status         `json:"opinion_bilateral" msgpack:"opinion_bilateral"`
	PostBilateral    Status         `json:"post_bilateral" msgpack:"post_bilateral"`
	CrossOP          Status         `json:"cross_op" msgpack:"cross_op"`
	CrossPO          Status         `json:"cross_po" msgpack:"cross_po"`
	MixedBilateral   Status         `json:"mixed_bilateral" msgpack:"mixed_bilateral"`
	MiddleO          Status         `json:"middle_o" msgpack:"middle_o"`
	MiddleP          Status         `json:"middle_p" msgpack:"middle_p"`
	MiddleU          Status         `json:"middle_u" msgpack:"middle_u"`
	RoleBits         uint16         `json:"role_bits" msgpack:"role_bits"`
	KnownBits        uint16         `json:"known_bits" msgpack:"known_bits"`
	UncertainBits    uint16         `json:"uncertain_bits" msgpack:"uncertain_bits"`
}

type Counts struct {
	SignalBoth        int            `json:"signal_both" msgpack:"signal_both"`
	OpinionOnly       int            `json:"opinion_only" msgpack:"opinion_only"`
	PostOnly          int            `json:"post_only" msgpack:"post_only"`
	StructuralOnly    int            `json:"structural_only" msgpack:"structural_only"`
	Holders           int            `json:"holders" msgpack:"holders"`
	Active            int            `json:"active" msgpack:"active"`
	Inactive          int            `json:"inactive" msgpack:"inactive"`
	Unknown           int            `json:"unknown" msgpack:"unknown"`
	Uncertain         int            `json:"uncertain" msgpack:"uncertain"`
	SupportAny        int            `json:"support_any" msgpack:"support_any"`
	SupportBoth       int            `json:"support_both" msgpack:"support_both"`
	Bilateral         int            `json:"bilateral" msgpack:"bilateral"`
	OpinionBilateral  int            `json:"opinion_bilateral" msgpack:"opinion_bilateral"`
	PostBilateral     int            `json:"post_bilateral" msgpack:"post_bilateral"`
	CrossOP           int            `json:"cross_op" msgpack:"cross_op"`
	CrossPO           int            `json:"cross_po" msgpack:"cross_po"`
	MixedBilateral    int            `json:"mixed_bilateral" msgpack:"mixed_bilateral"`
	MiddleO           int            `json:"middle_o" msgpack:"middle_o"`
	MiddleP           int            `json:"middle_p" msgpack:"middle_p"`
	MiddleU           int            `json:"middle_u" msgpack:"middle_u"`
	SideO             [2]int         `json:"side_o" msgpack:"side_o"`
	SideP             [2]int         `json:"side_p" msgpack:"side_p"`
	SideU             [2]int         `json:"side_u" msgpack:"side_u"`
	SideUnknownO      [2]int         `json:"side_unknown_o" msgpack:"side_unknown_o"`
	SideUnknownP      [2]int         `json:"side_unknown_p" msgpack:"side_unknown_p"`
	SideUnknownU      [2]int         `json:"side_unknown_u" msgpack:"side_unknown_u"`
	UnknownByCategory map[string]int `json:"unknown_by_category,omitempty" msgpack:"unknown_by_category,omitempty"`
}

type PairFrame struct {
	GroupIDs  [2]int64      `json:"group_ids" msgpack:"group_ids"`
	Epoch     int           `json:"epoch" msgpack:"epoch"`
	CoreMeans [2]float64    `json:"core_means" msgpack:"core_means"`
	CoreLower [2]float64    `json:"core_lower" msgpack:"core_lower"`
	CoreUpper [2]float64    `json:"core_upper" msgpack:"core_upper"`
	Contact   bool          `json:"contact" msgpack:"contact"`
	Counts    Counts        `json:"counts" msgpack:"counts"`
	Attention Attention     `json:"attention" msgpack:"attention"`
	Holders   []HolderState `json:"holders,omitempty" msgpack:"holders,omitempty"`
}

// Attention totals use qualified cross-boundary signals. *Both are explicitly
// bilateral subindices; a valid one-sided bridge can have Both == zero.
type Attention struct {
	OpinionTotal float64 `json:"opinion_total" msgpack:"opinion_total"`
	PostTotal    float64 `json:"post_total" msgpack:"post_total"`
	UnionTotal   float64 `json:"union_total" msgpack:"union_total"`
	OpinionBoth  float64 `json:"opinion_both" msgpack:"opinion_both"`
	PostBoth     float64 `json:"post_both" msgpack:"post_both"`
	UnionBoth    float64 `json:"union_both" msgpack:"union_both"`
	OpinionKnown bool    `json:"opinion_known" msgpack:"opinion_known"`
	PostKnown    bool    `json:"post_known" msgpack:"post_known"`
	UnionKnown   bool    `json:"union_known" msgpack:"union_known"`
}

type Event struct {
	Scope         string   `json:"scope" msgpack:"scope"`
	Step          int      `json:"step" msgpack:"step"`
	IntervalStart int      `json:"interval_start" msgpack:"interval_start"`
	IntervalEnd   int      `json:"interval_end" msgpack:"interval_end"`
	GroupIDs      [2]int64 `json:"group_ids" msgpack:"group_ids"`
	Epoch         int      `json:"epoch" msgpack:"epoch"`
	Holder        int      `json:"holder" msgpack:"holder"`
	Type          string   `json:"type" msgpack:"type"`
	FromRole      uint16   `json:"from_role" msgpack:"from_role"`
	ToRole        uint16   `json:"to_role" msgpack:"to_role"`
	FromKnownBits uint16   `json:"from_known_bits" msgpack:"from_known_bits"`
	ToKnownBits   uint16   `json:"to_known_bits" msgpack:"to_known_bits"`
	LeftCensored  bool     `json:"left_censored" msgpack:"left_censored"`
	Uncertain     bool     `json:"uncertain" msgpack:"uncertain"`
	Reason        string   `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

type Episode struct {
	Scope          string   `json:"scope" msgpack:"scope"`
	GroupIDs       [2]int64 `json:"group_ids" msgpack:"group_ids"`
	Epoch          int      `json:"epoch" msgpack:"epoch"`
	Holder         int      `json:"holder" msgpack:"holder"`
	Start          *int     `json:"start" msgpack:"start"`
	ObservedStart  int      `json:"observed_start" msgpack:"observed_start"`
	End            *int     `json:"end" msgpack:"end"`
	ObservationEnd int      `json:"observation_end" msgpack:"observation_end"`
	LeftCensored   bool     `json:"left_censored" msgpack:"left_censored"`
	RightCensored  bool     `json:"right_censored" msgpack:"right_censored"`
	Uncertain      bool     `json:"uncertain" msgpack:"uncertain"`
	Reason         string   `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

// Epoch entries are emitted on opening/closing only, never repeated snapshots.
type Epoch struct {
	Scope          string     `json:"scope" msgpack:"scope"`
	GroupIDs       [2]int64   `json:"group_ids" msgpack:"group_ids"`
	Number         int        `json:"number" msgpack:"number"`
	State          string     `json:"state" msgpack:"state"`
	Start          int        `json:"start" msgpack:"start"`
	End            int        `json:"end" msgpack:"end"`
	ClosedAt       int        `json:"closed_at" msgpack:"closed_at"`
	Reason         string     `json:"reason,omitempty" msgpack:"reason,omitempty"`
	CoreMembers    [2][]int   `json:"core_members,omitempty" msgpack:"core_members,omitempty"`
	ReferencePairs [][3]int64 `json:"reference_pairs,omitempty" msgpack:"reference_pairs,omitempty"`
}

// Summary is emitted once, when its pair epoch closes.
type Summary struct {
	Scope                    string   `json:"scope" msgpack:"scope"`
	GroupIDs                 [2]int64 `json:"group_ids" msgpack:"group_ids"`
	Epoch                    int      `json:"epoch" msgpack:"epoch"`
	Start                    int      `json:"start" msgpack:"start"`
	End                      int      `json:"end" msgpack:"end"`
	InitialPresent           int      `json:"initial_present" msgpack:"initial_present"`
	Formations               int      `json:"formations" msgpack:"formations"`
	Losses                   int      `json:"losses" msgpack:"losses"`
	Regenerations            int      `json:"regenerations" msgpack:"regenerations"`
	Conversions              int      `json:"conversions" msgpack:"conversions"`
	CensoredEpisodes         int      `json:"censored_episodes" msgpack:"censored_episodes"`
	ActiveHolderRounds       int64    `json:"active_holder_rounds" msgpack:"active_holder_rounds"`
	KnownHolderRounds        int64    `json:"known_holder_rounds" msgpack:"known_holder_rounds"`
	NeverSeenHolderRounds    int64    `json:"never_seen_holder_rounds" msgpack:"never_seen_holder_rounds"`
	SeenInactiveHolderRounds int64    `json:"seen_inactive_holder_rounds" msgpack:"seen_inactive_holder_rounds"`
	Reason                   string   `json:"reason" msgpack:"reason"`
}

// Branch records share the U ledger's record schema, with an independent
// channel and optional side. Nil Side means any-side; union has no directions.
// Pair branch masks use bit0=side0 and bit1=side1 for this channel only, not the
// main U record's eight-bit S/O/P/U layout. Union branch masks use only bit0.
type BranchEvent struct {
	Event   `msgpack:",inline"`
	Channel string `json:"channel" msgpack:"channel"`
	Side    *int   `json:"side,omitempty" msgpack:"side,omitempty"`
}

type BranchEpisode struct {
	Episode `msgpack:",inline"`
	Channel string `json:"channel" msgpack:"channel"`
	Side    *int   `json:"side,omitempty" msgpack:"side,omitempty"`
}

type BranchSummary struct {
	Summary             `msgpack:",inline"`
	Channel             string `json:"channel" msgpack:"channel"`
	Side                *int   `json:"side,omitempty" msgpack:"side,omitempty"`
	Population          int    `json:"population" msgpack:"population"`
	UnknownHolderRounds int64  `json:"unknown_holder_rounds" msgpack:"unknown_holder_rounds"`
}

type Frame struct {
	DefinitionVersion   int             `json:"definition_version" msgpack:"definition_version"`
	BranchLedgerVersion int             `json:"branch_ledger_version" msgpack:"branch_ledger_version"`
	Step                int             `json:"step" msgpack:"step"`
	Pairs               []PairFrame     `json:"pairs" msgpack:"pairs"`
	Union               *UnionFrame     `json:"union,omitempty" msgpack:"union,omitempty"`
	Events              []Event         `json:"events,omitempty" msgpack:"events,omitempty"`
	Episodes            []Episode       `json:"episodes,omitempty" msgpack:"episodes,omitempty"`
	Epochs              []Epoch         `json:"epochs,omitempty" msgpack:"epochs,omitempty"`
	Summaries           []Summary       `json:"summaries,omitempty" msgpack:"summaries,omitempty"`
	BranchEvents        []BranchEvent   `json:"branch_events,omitempty" msgpack:"branch_events,omitempty"`
	BranchEpisodes      []BranchEpisode `json:"branch_episodes,omitempty" msgpack:"branch_episodes,omitempty"`
	BranchSummaries     []BranchSummary `json:"branch_summaries,omitempty" msgpack:"branch_summaries,omitempty"`
	Series              []StepStats     `json:"series,omitempty" msgpack:"series,omitempty"`
}

func ordered(p [2]int64) [2]int64 {
	if p[0] > p[1] {
		p[0], p[1] = p[1], p[0]
	}
	return p
}
func known(active bool) Status { return Status{Known: true, Active: active} }
func both(a, b Status) Status {
	uncertain := a.Uncertain || b.Uncertain
	// A stable negative witness fixes an AND, regardless of argument order.
	if (a.Known && !a.Active && !a.Uncertain) || (b.Known && !b.Active && !b.Uncertain) {
		uncertain = false
	}
	if a.Known && !a.Active {
		return Status{Known: true, Uncertain: uncertain}
	}
	if b.Known && !b.Active {
		return Status{Known: true, Uncertain: uncertain}
	}
	return Status{Known: a.Known && b.Known, Active: a.Active && b.Active, Uncertain: uncertain}
}
func either(a, b Status) Status {
	uncertain := a.Uncertain || b.Uncertain
	// A stable positive witness fixes an OR, including unknown alternatives.
	if (a.Known && a.Active && !a.Uncertain) || (b.Known && b.Active && !b.Uncertain) {
		uncertain = false
	}
	if a.Known && a.Active {
		return Status{Known: true, Active: true, Uncertain: uncertain}
	}
	if b.Known && b.Active {
		return Status{Known: true, Active: true, Uncertain: uncertain}
	}
	return Status{Known: a.Known && b.Known, Uncertain: uncertain}
}
func not(a Status) Status {
	return Status{Known: a.Known, Active: a.Known && !a.Active, Uncertain: a.Uncertain}
}
