package bridges

import (
	"fmt"
	"smp/groups"
)

type holderMemory struct {
	known, active, seen, uncertain bool
	role, knownBits                uint16
	uncertainBits                  uint16
	open                           *Episode
	episodeOpen, initialized       bool
}
type pairTrack struct {
	scope                  string
	ids                    [2]int64
	epoch, start, lastStep int
	groupEpoch             [2]int
	members                [2][]int
	labels                 []int
	refs                   [2]int
	holders                []holderMemory
	summary                Summary
	branch                 *branchIdentity
	branches               []*pairTrack
	collection             *collectionPlan
	disabled               bool
	series                 StepStats
	seriesPending          bool
}

func (t *pairTrack) configureCollection(plan *collectionPlan) {
	t.collection = plan
	t.disabled = plan != nil && (!plan.histories() || !plan.scopes[t.scope] || !plan.channels["U"])
	if t.disabled {
		t.holders = nil
	}
}
func (t *pairTrack) wants(product string) bool {
	return t.collection == nil || t.collection.products[product]
}
func (t *pairTrack) wantsEvent(kind string) bool {
	if t.disabled {
		return false
	}
	if kind == "conversion" {
		return t.wants("conversions")
	}
	return t.wants("events")
}
func (t *pairTrack) beginSeries(step int) {
	if t.disabled || !t.wants("series") || t.seriesPending {
		return
	}
	t.series = StepStats{Scope: t.scope, GroupIDs: t.ids, Epoch: t.epoch, Channel: "U", Step: step, IntervalStart: t.lastStep, IntervalEnd: step, Population: len(t.holders)}
	if t.branch != nil {
		t.series.Channel = channelCode(t.branch.channel)
		t.series.Side = t.branch.sidePointer()
	}
	t.seriesPending = true
}
func (t *pairTrack) stock(h *holderMemory) {
	if !t.seriesPending {
		return
	}
	s := &t.series
	if !h.known {
		s.Unknown++
	} else {
		s.Known++
		if h.active {
			s.Active++
		} else if h.seen {
			s.SeenInactive++
		} else {
			s.NeverSeen++
		}
	}
}
func (t *pairTrack) flushSeries(result *Frame, closing bool) {
	if !t.seriesPending {
		return
	}
	t.series.Closing = closing
	result.Series = append(result.Series, t.series)
	t.seriesPending = false
}

func newPairTrack(ids [2]int64, epoch, step, n int, observed map[int64]groups.Group) *pairTrack {
	t := &pairTrack{scope: "pair", ids: ids, epoch: epoch, start: step, lastStep: step, labels: make([]int, n), holders: make([]holderMemory, n)}
	for i := range t.labels {
		t.labels[i] = -1
	}
	for side, id := range ids {
		t.members[side] = append([]int(nil), observed[id].Core.Members...)
		t.groupEpoch[side] = observed[id].Epoch
		for _, i := range t.members[side] {
			t.labels[i] = side
		}
	}
	t.summary = Summary{Scope: "pair", GroupIDs: ids, Epoch: epoch, Start: step, End: step}
	return t
}

func (t *pairTrack) integrate(step int) {
	t.beginSeries(step)
	dt := step - t.lastStep
	if dt > 0 {
		for _, h := range t.holders {
			if h.known {
				t.summary.KnownHolderRounds += int64(dt)
				if t.seriesPending {
					t.series.KnownHolderRounds += int64(dt)
				}
				if h.active {
					t.summary.ActiveHolderRounds += int64(dt)
					if t.seriesPending {
						t.series.ActiveHolderRounds += int64(dt)
					}
				} else if h.seen {
					t.summary.SeenInactiveHolderRounds += int64(dt)
					if t.seriesPending {
						t.series.SeenInactiveHolderRounds += int64(dt)
					}
				} else {
					t.summary.NeverSeenHolderRounds += int64(dt)
					if t.seriesPending {
						t.series.NeverSeenHolderRounds += int64(dt)
					}
				}
			} else if t.seriesPending {
				t.series.UnknownHolderRounds += int64(dt)
			}
		}
	}
	for _, branch := range t.branches {
		branch.integrate(step)
	}
	// lastStep is committed after updating the current state. Call integrate
	// at most once before either update or epoch closure.
}

func copyEpisode(e Episode) Episode {
	if e.Start != nil {
		v := *e.Start
		e.Start = &v
	}
	if e.End != nil {
		v := *e.End
		e.End = &v
	}
	return e
}
func (t *pairTrack) closeHolder(holder, end int, censored bool, reason string, result *Frame) {
	h := &t.holders[holder]
	if !h.episodeOpen {
		return
	}
	if censored {
		t.summary.CensoredEpisodes++
		if t.seriesPending {
			t.series.CensoredEpisodes++
		}
	}
	if h.open != nil {
		episode := copyEpisode(*h.open)
		episode.ObservationEnd = end
		episode.RightCensored = censored
		episode.Reason = reason
		if !censored {
			v := end
			episode.End = &v
		}
		t.emitEpisode(episode, result)
	}
	h.open = nil
	h.episodeOpen = false
}

func (t *pairTrack) update(step int, state HolderState, result *Frame) {
	t.updateState(step, ledgerState{state.Holder, state.Active, state.RoleBits, state.KnownBits, state.UncertainBits}, result)
}

// Every U/channel ledger runs the same lifecycle engine on a small projection.
// Channel projections cannot accidentally compare another channel's role bits.
type ledgerState struct {
	Holder                             int
	Active                             Status
	RoleBits, KnownBits, UncertainBits uint16
}

func (t *pairTrack) updateState(step int, state ledgerState, result *Frame) {
	if t.disabled {
		return
	}
	t.beginSeries(step)
	h := &t.holders[state.Holder]
	if !state.Active.Known {
		if t.seriesPending && h.known {
			t.series.UnknownEntries++
		}
		if h.episodeOpen {
			t.closeHolder(state.Holder, step, true, "signal_unknown", result)
		}
		h.known = false
		h.active = false
		h.uncertain = state.Active.Uncertain
		h.role = state.RoleBits
		h.knownBits = state.KnownBits
		h.uncertainBits = state.UncertainBits
		h.initialized = true
		t.stock(h)
		return
	}
	if t.seriesPending && h.initialized && !h.known {
		t.series.UnknownExits++
	}
	previousKnown, previousActive := h.known, h.active
	event := ""
	if state.Active.Active && (!previousKnown || !previousActive) {
		if !previousKnown {
			event = "initial_present"
			t.summary.InitialPresent++
		} else if h.seen {
			event = "regeneration"
			t.summary.Regenerations++
		} else {
			event = "formation"
			t.summary.Formations++
		}
		if t.wants("episodes") {
			episode := Episode{Scope: t.scope, GroupIDs: t.ids, Epoch: t.epoch, Holder: state.Holder, ObservedStart: step, ObservationEnd: step, LeftCensored: !previousKnown, Uncertain: state.Active.Uncertain || previousKnown && h.uncertain}
			if previousKnown {
				v := step
				episode.Start = &v
			}
			h.open = &episode
		}
		h.episodeOpen = true
		h.seen = true
	} else if previousKnown && previousActive && !state.Active.Active {
		event = "loss"
		t.summary.Losses++
		if h.open != nil && state.Active.Uncertain {
			h.open.Uncertain = true
		}
		t.closeHolder(state.Holder, step, false, "qualification_lost", result)
	} else if previousKnown && previousActive && state.Active.Active && (h.role != state.RoleBits || h.knownBits != state.KnownBits) {
		event = "conversion"
		t.summary.Conversions++
	}
	uncertain := h.uncertain || state.Active.Uncertain || h.uncertainBits != 0 || state.UncertainBits != 0
	if t.seriesPending {
		switch event {
		case "initial_present":
			t.series.InitialPresent++
		case "formation":
			t.series.Formations++
			if uncertain {
				t.series.UncertainFormations++
			}
		case "loss":
			t.series.Losses++
			if uncertain {
				t.series.UncertainLosses++
			}
		case "regeneration":
			t.series.Regenerations++
			if uncertain {
				t.series.UncertainRegenerations++
			}
		case "conversion":
			t.series.Conversions++
		}
	}
	if event != "" && t.wantsEvent(event) {
		reason := ""
		if event == "initial_present" {
			reason = "epoch_start_or_after_unknown"
		}
		if event == "conversion" {
			reason = "role_changed"
			if h.knownBits != state.KnownBits {
				reason = "availability_changed"
			}
		}
		t.emitEvent(Event{Scope: t.scope, Step: step, IntervalStart: t.lastStep, IntervalEnd: step, GroupIDs: t.ids, Epoch: t.epoch, Holder: state.Holder, Type: event,
			FromRole: h.role, ToRole: state.RoleBits, FromKnownBits: h.knownBits, ToKnownBits: state.KnownBits, LeftCensored: !previousKnown, Uncertain: uncertain, Reason: reason}, result)
	}
	if h.open != nil && state.Active.Uncertain {
		h.open.Uncertain = true
	}
	h.known = true
	h.active = state.Active.Active
	h.uncertain = state.Active.Uncertain
	h.role = state.RoleBits
	h.knownBits = state.KnownBits
	h.uncertainBits = state.UncertainBits
	h.initialized = true
	t.stock(h)
}

func (t *pairTrack) close(end, observedAt int, reason string, result *Frame) {
	t.beginSeries(end)
	for i := range t.holders {
		t.stock(&t.holders[i])
		t.closeHolder(i, end, true, reason, result)
	}
	t.flushSeries(result, true)
	t.summary.End = end
	t.summary.Reason = reason
	t.emitSummary(result)
	if t.branch == nil {
		result.Epochs = append(result.Epochs, Epoch{Scope: t.scope, GroupIDs: t.ids, Number: t.epoch, State: "closed", Start: t.start, End: end, ClosedAt: observedAt, Reason: reason})
	}
	for _, branch := range t.branches {
		branch.close(end, observedAt, reason, result)
	}
}

// FinalClose emits right-censored episodes and final summaries once. It never
// extrapolates through unsampled time after the last Measure call.
func (o *Observer) FinalClose(step int, reasons ...string) (Frame, error) {
	if o == nil || o.pairs == nil {
		return Frame{}, fmt.Errorf("bridges: nil or uninitialized observer")
	}
	if step < 0 || o.started && step < o.lastStep {
		return Frame{}, fmt.Errorf("bridges: final step precedes last observation")
	}
	result := Frame{DefinitionVersion: DefinitionVersion, BranchLedgerVersion: BranchLedgerVersion, Step: step, Pairs: []PairFrame{}}
	if o.closed {
		return o.collectedFrame(result), nil
	}
	reason := "observation_end"
	if len(reasons) > 0 && reasons[0] != "" {
		reason = reasons[0]
	}
	for _, pair := range sortedPairKeys(o.pairs) {
		t := o.pairs[pair]
		t.close(t.lastStep, step, reason, &result)
		delete(o.pairs, pair)
	}
	if o.union != nil {
		o.union.close(o.union.lastStep, step, reason, &result)
		o.union = nil
	}
	// Closed results own their data; retaining a closed observer should not
	// retain its largest historical graph/reference/holder workspace.
	o.agg, o.membership, o.references, o.unionStates, o.postValues = nil, nil, nil, nil, nil
	o.nativeOpinions, o.nativeRecords, o.nativeSeen, o.nativePosts = nil, nil, nil, nil
	o.nativeGraph.Adjacency, o.nativeGraph.Nodes, o.nativeGraph.Graph = nil, nil, nil
	o.next, o.stopped, o.unionSignature = nil, nil, ""
	o.closed = true
	return o.collectedFrame(result), nil
}
