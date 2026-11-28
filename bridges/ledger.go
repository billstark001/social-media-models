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
	dt := step - t.lastStep
	if dt <= 0 {
		return
	}
	for _, h := range t.holders {
		if h.known {
			t.summary.KnownHolderRounds += int64(dt)
			if h.active {
				t.summary.ActiveHolderRounds += int64(dt)
			} else if h.seen {
				t.summary.SeenInactiveHolderRounds += int64(dt)
			} else {
				t.summary.NeverSeenHolderRounds += int64(dt)
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
	if h.open == nil {
		return
	}
	episode := copyEpisode(*h.open)
	episode.ObservationEnd = end
	episode.RightCensored = censored
	episode.Reason = reason
	if censored {
		t.summary.CensoredEpisodes++
	}
	if !censored {
		v := end
		episode.End = &v
	}
	t.emitEpisode(episode, result)
	h.open = nil
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
	h := &t.holders[state.Holder]
	if !state.Active.Known {
		if h.open != nil {
			t.closeHolder(state.Holder, step, true, "signal_unknown", result)
		}
		h.known = false
		h.active = false
		h.uncertain = state.Active.Uncertain
		h.role = state.RoleBits
		h.knownBits = state.KnownBits
		h.uncertainBits = state.UncertainBits
		return
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
		episode := Episode{Scope: t.scope, GroupIDs: t.ids, Epoch: t.epoch, Holder: state.Holder, ObservedStart: step, ObservationEnd: step, LeftCensored: !previousKnown, Uncertain: state.Active.Uncertain || previousKnown && h.uncertain}
		if previousKnown {
			v := step
			episode.Start = &v
		}
		h.open = &episode
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
	if event != "" {
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
			FromRole: h.role, ToRole: state.RoleBits, FromKnownBits: h.knownBits, ToKnownBits: state.KnownBits, LeftCensored: !previousKnown, Uncertain: h.uncertain || state.Active.Uncertain || h.uncertainBits != 0 || state.UncertainBits != 0, Reason: reason}, result)
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
}

func (t *pairTrack) close(end, observedAt int, reason string, result *Frame) {
	for i := range t.holders {
		t.closeHolder(i, end, true, reason, result)
	}
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
		return result, nil
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
	return result, nil
}
