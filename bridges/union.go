package bridges

import "strconv"

// Union states are existential over pairs, with no invented left/right sides.
// Bilateral means that some single reference pair has bilateral support, not that
// two unrelated one-sided pairs can be combined into a new bilateral bridge.
type UnionHolderState struct {
	SignalBoth       Status `json:"signal_both" msgpack:"signal_both"`
	OpinionOnly      Status `json:"opinion_only" msgpack:"opinion_only"`
	PostOnly         Status `json:"post_only" msgpack:"post_only"`
	StructuralOnly   Status `json:"structural_only" msgpack:"structural_only"`
	Holder           int    `json:"holder" msgpack:"holder"`
	Structural       Status `json:"structural" msgpack:"structural"`
	Opinion          Status `json:"opinion" msgpack:"opinion"`
	Post             Status `json:"post" msgpack:"post"`
	Active           Status `json:"active" msgpack:"active"`
	Bilateral        Status `json:"bilateral" msgpack:"bilateral"`
	OpinionBilateral Status `json:"opinion_bilateral" msgpack:"opinion_bilateral"`
	PostBilateral    Status `json:"post_bilateral" msgpack:"post_bilateral"`
	MixedBilateral   Status `json:"mixed_bilateral" msgpack:"mixed_bilateral"`
	MiddleOpinion    Status `json:"middle_opinion" msgpack:"middle_opinion"`
	MiddlePost       Status `json:"middle_post" msgpack:"middle_post"`
	MiddleActive     Status `json:"middle_active" msgpack:"middle_active"`
	RoleBits         uint16 `json:"role_bits" msgpack:"role_bits"`
	KnownBits        uint16 `json:"known_bits" msgpack:"known_bits"`
	UncertainBits    uint16 `json:"uncertain_bits" msgpack:"uncertain_bits"`
}
type UnionCounts struct {
	SignalBoth        int            `json:"signal_both" msgpack:"signal_both"`
	OpinionOnly       int            `json:"opinion_only" msgpack:"opinion_only"`
	PostOnly          int            `json:"post_only" msgpack:"post_only"`
	StructuralOnly    int            `json:"structural_only" msgpack:"structural_only"`
	Holders           int            `json:"holders" msgpack:"holders"`
	StructuralAny     int            `json:"structural_any" msgpack:"structural_any"`
	OpinionAny        int            `json:"opinion_any" msgpack:"opinion_any"`
	PostAny           int            `json:"post_any" msgpack:"post_any"`
	Active            int            `json:"active" msgpack:"active"`
	Inactive          int            `json:"inactive" msgpack:"inactive"`
	Unknown           int            `json:"unknown" msgpack:"unknown"`
	Uncertain         int            `json:"uncertain" msgpack:"uncertain"`
	Bilateral         int            `json:"bilateral" msgpack:"bilateral"`
	OpinionBilateral  int            `json:"opinion_bilateral" msgpack:"opinion_bilateral"`
	PostBilateral     int            `json:"post_bilateral" msgpack:"post_bilateral"`
	MixedBilateral    int            `json:"mixed_bilateral" msgpack:"mixed_bilateral"`
	MiddleOpinionAny  int            `json:"middle_opinion_any" msgpack:"middle_opinion_any"`
	MiddlePostAny     int            `json:"middle_post_any" msgpack:"middle_post_any"`
	MiddleActive      int            `json:"middle_active" msgpack:"middle_active"`
	UnknownByCategory map[string]int `json:"unknown_by_category,omitempty" msgpack:"unknown_by_category,omitempty"`
}
type UnionFrame struct {
	Epoch   int                `json:"epoch" msgpack:"epoch"`
	Counts  UnionCounts        `json:"counts" msgpack:"counts"`
	Holders []UnionHolderState `json:"holders,omitempty" msgpack:"holders,omitempty"`
}

func (o *Observer) prepareUnion(n int) {
	if cap(o.unionStates) < n {
		o.unionStates = make([]UnionHolderState, n)
	} else {
		o.unionStates = o.unionStates[:n]
	}
	for j := range o.unionStates {
		o.unionStates[j] = UnionHolderState{Holder: j, Structural: known(false), Opinion: known(false), Post: known(false), Active: known(false), Bilateral: known(false), OpinionBilateral: known(false), PostBilateral: known(false), MixedBilateral: known(false), MiddleOpinion: known(false), MiddlePost: known(false), MiddleActive: known(false)}
	}
}
func (h *UnionHolderState) add(p HolderState) {
	h.Structural = either(h.Structural, either(p.S[0].Status, p.S[1].Status))
	h.Opinion = either(h.Opinion, either(p.O[0].Status, p.O[1].Status))
	h.Post = either(h.Post, either(p.P[0].Status, p.P[1].Status))
	h.Active = either(h.Active, p.Active)
	h.Bilateral = either(h.Bilateral, p.Bilateral)
	h.OpinionBilateral = either(h.OpinionBilateral, p.OpinionBilateral)
	h.PostBilateral = either(h.PostBilateral, p.PostBilateral)
	h.MixedBilateral = either(h.MixedBilateral, p.MixedBilateral)
	h.MiddleOpinion = either(h.MiddleOpinion, p.MiddleO)
	h.MiddlePost = either(h.MiddlePost, p.MiddleP)
	h.MiddleActive = either(h.MiddleActive, p.MiddleU)
}
func (h *UnionHolderState) roles() {
	h.SignalBoth = both(h.Opinion, h.Post)
	h.OpinionOnly = both(h.Opinion, not(h.Post))
	h.PostOnly = both(h.Post, not(h.Opinion))
	h.StructuralOnly = both(h.Structural, not(h.Active))
	for k, s := range []Status{h.Structural, h.Opinion, h.Post, h.Active, h.Bilateral, h.OpinionBilateral, h.PostBilateral, h.MixedBilateral, h.SignalBoth} {
		bit := uint16(1) << uint(k)
		if s.Known {
			h.KnownBits |= bit
		}
		if s.Known && s.Active {
			h.RoleBits |= bit
		}
		if s.Uncertain {
			h.UncertainBits |= bit
		}
	}
}
func (c *UnionCounts) add(h UnionHolderState) {
	c.Holders++
	if !h.Active.Known {
		c.Unknown++
	} else if h.Active.Active {
		c.Active++
	} else {
		c.Inactive++
	}
	if h.Active.Uncertain {
		c.Uncertain++
	}
	for _, item := range []struct {
		name string
		s    Status
		p    *int
	}{{"signal_both", h.SignalBoth, &c.SignalBoth}, {"opinion_only", h.OpinionOnly, &c.OpinionOnly}, {"post_only", h.PostOnly, &c.PostOnly}, {"structural_only", h.StructuralOnly, &c.StructuralOnly}, {"structural", h.Structural, &c.StructuralAny}, {"opinion", h.Opinion, &c.OpinionAny}, {"post", h.Post, &c.PostAny}, {"bilateral", h.Bilateral, &c.Bilateral}, {"opinion_bilateral", h.OpinionBilateral, &c.OpinionBilateral}, {"post_bilateral", h.PostBilateral, &c.PostBilateral}, {"mixed_bilateral", h.MixedBilateral, &c.MixedBilateral}, {"middle_opinion", h.MiddleOpinion, &c.MiddleOpinionAny}, {"middle_post", h.MiddlePost, &c.MiddlePostAny}, {"middle_active", h.MiddleActive, &c.MiddleActive}} {
		if item.s.Known {
			if item.s.Active {
				*item.p++
			}
		} else {
			if c.UnknownByCategory == nil {
				c.UnknownByCategory = make(map[string]int)
			}
			c.UnknownByCategory[item.name]++
		}
	}
}
func (o *Observer) measureUnion(step int, gap bool, result *Frame) {
	key := []byte{}
	for _, p := range result.Pairs {
		key = strconv.AppendInt(key, p.GroupIDs[0], 10)
		key = append(key, ':')
		key = strconv.AppendInt(key, p.GroupIDs[1], 10)
		key = append(key, ':')
		key = strconv.AppendInt(key, int64(p.Epoch), 10)
		key = append(key, ';')
	}
	signature := string(key)
	if o.union != nil && (gap || signature != o.unionSignature) {
		end := o.union.lastStep
		reason := "pair_reference_changed"
		if gap {
			reason = "sampling_gap"
		} else {
			o.union.integrate(step)
			end = step
		}
		o.union.close(end, step, reason, result)
		o.union = nil
	}
	if len(result.Pairs) == 0 {
		o.unionSignature = ""
		return
	}
	if o.union == nil {
		o.unionNext++
		o.union = &pairTrack{scope: "union", epoch: o.unionNext, start: step, lastStep: step, holders: make([]holderMemory, len(o.unionStates)), summary: Summary{Scope: "union", Epoch: o.unionNext, Start: step, End: step}}
		o.union.initBranches(len(o.unionStates), false)
		refs := make([][3]int64, len(result.Pairs))
		for i, p := range result.Pairs {
			refs[i] = [3]int64{p.GroupIDs[0], p.GroupIDs[1], int64(p.Epoch)}
		}
		result.Epochs = append(result.Epochs, Epoch{Scope: "union", Number: o.unionNext, State: "open", Start: step, End: step, ReferencePairs: refs})
	}
	o.unionSignature = signature
	uf := &UnionFrame{Epoch: o.union.epoch}
	o.union.integrate(step)
	for j := range o.unionStates {
		h := &o.unionStates[j]
		h.roles()
		uf.Counts.add(*h)
		o.union.update(step, HolderState{Holder: j, Active: h.Active, RoleBits: h.RoleBits, KnownBits: h.KnownBits, UncertainBits: h.UncertainBits}, result)
		o.union.updateUnionBranches(step, h, result)
	}
	o.union.commitStep(step)
	if o.options.DetailOutput {
		uf.Holders = append([]UnionHolderState(nil), o.unionStates...)
	}
	result.Union = uf
}
