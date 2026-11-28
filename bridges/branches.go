package bridges

const (
	ChannelOpinion    = "opinion"
	ChannelPost       = "post"
	ChannelStructural = "structural"
)

type branchIdentity struct {
	channel string
	side    int // -1 is any-side. Union never has side-specific ledgers.
}

func (t *pairTrack) initBranches(n int, sides bool) {
	for _, channel := range []string{ChannelOpinion, ChannelPost, ChannelStructural} {
		add := func(side int) {
			t.branches = append(t.branches, &pairTrack{scope: t.scope, ids: t.ids, epoch: t.epoch,
				start: t.start, lastStep: t.lastStep, holders: make([]holderMemory, n),
				summary: Summary{Scope: t.scope, GroupIDs: t.ids, Epoch: t.epoch, Start: t.start, End: t.start},
				branch:  &branchIdentity{channel: channel, side: side}})
		}
		add(-1)
		if sides && t.scope == "pair" {
			add(0)
			add(1)
		}
	}
}

func (t *pairTrack) commitStep(step int) {
	t.lastStep = step
	for _, branch := range t.branches {
		branch.lastStep = step
	}
}

func (b *branchIdentity) sidePointer() *int {
	if b.side < 0 {
		return nil
	}
	v := b.side
	return &v // each public record owns its optional side value
}

func (t *pairTrack) emitEvent(event Event, result *Frame) {
	if t.branch == nil {
		result.Events = append(result.Events, event)
		return
	}
	result.BranchEvents = append(result.BranchEvents, BranchEvent{Event: event, Channel: t.branch.channel, Side: t.branch.sidePointer()})
}

func (t *pairTrack) emitEpisode(episode Episode, result *Frame) {
	if t.branch == nil {
		result.Episodes = append(result.Episodes, episode)
		return
	}
	result.BranchEpisodes = append(result.BranchEpisodes, BranchEpisode{Episode: episode, Channel: t.branch.channel, Side: t.branch.sidePointer()})
}

func (t *pairTrack) emitSummary(result *Frame) {
	if t.branch == nil {
		result.Summaries = append(result.Summaries, t.summary)
		return
	}
	n := len(t.holders)
	result.BranchSummaries = append(result.BranchSummaries, BranchSummary{Summary: t.summary,
		Channel: t.branch.channel, Side: t.branch.sidePointer(), Population: n,
		UnknownHolderRounds: int64(n)*int64(t.summary.End-t.summary.Start) - t.summary.KnownHolderRounds})
}

func pairChannelStatuses(h *HolderState, channel string) [2]Status {
	switch channel {
	case ChannelOpinion:
		return [2]Status{h.O[0].Status, h.O[1].Status}
	case ChannelPost:
		return [2]Status{h.P[0].Status, h.P[1].Status}
	case ChannelStructural:
		return [2]Status{h.S[0].Status, h.S[1].Status}
	default:
		panic("bridges: internal invalid branch channel")
	}
}

func projectPairChannel(holder int, statuses [2]Status, side int) ledgerState {
	state := ledgerState{Holder: holder}
	if side < 0 {
		state.Active = either(statuses[0], statuses[1])
	} else {
		state.Active = statuses[side]
	}
	for s, status := range statuses {
		if side >= 0 && s != side {
			continue
		}
		bit := uint16(1) << uint(s)
		if status.Known {
			state.KnownBits |= bit
		}
		if status.Known && status.Active {
			state.RoleBits |= bit
		}
		if status.Uncertain {
			state.UncertainBits |= bit
		}
	}
	return state
}

func (t *pairTrack) updatePairBranches(step int, h *HolderState, result *Frame) {
	for _, branch := range t.branches {
		state := projectPairChannel(h.Holder, pairChannelStatuses(h, branch.branch.channel), branch.branch.side)
		branch.updateState(step, state, result)
	}
}

func (t *pairTrack) updateUnionBranches(step int, h *UnionHolderState, result *Frame) {
	for _, branch := range t.branches {
		var status Status
		switch branch.branch.channel {
		case ChannelOpinion:
			status = h.Opinion
		case ChannelPost:
			status = h.Post
		case ChannelStructural:
			status = h.Structural
		default:
			panic("bridges: internal invalid union branch channel")
		}
		state := ledgerState{Holder: h.Holder, Active: status}
		if status.Known {
			state.KnownBits = 1
		}
		if status.Known && status.Active {
			state.RoleBits = 1
		}
		if status.Uncertain {
			state.UncertainBits = 1
		}
		branch.updateState(step, state, result)
	}
}
