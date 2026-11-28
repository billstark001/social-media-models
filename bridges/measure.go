package bridges

import (
	"fmt"
	"math"
	"smp/groups"
	"smp/model"
	"smp/utils"
	"sort"
)

type support struct {
	count                                  [4]int // raw, opinion, post, both compatible
	weight                                 [4]float64
	opLower, opUpper, postLower, postUpper int
}
type reference struct {
	members            []int
	mean, lower, upper float64
}
type postValue struct {
	value                  float64
	source                 int
	available, sourceKnown bool
}

// Observer owns a single ordered stream. Results never refer to reusable state.
// Per-edge work uses membership indexes, not graph conversion or edge sorting.
type Observer struct {
	options         Options
	collection      *collectionPlan
	pairs           map[[2]int64]*pairTrack
	next            map[[2]int64]int
	stopped         map[[2]int64][2]int
	lastStep        int
	started, closed bool
	n               int
	agg             []support
	membership      [][]int
	union           *pairTrack
	unionNext       int
	unionSignature  string
	unionStates     []UnionHolderState
	references      []reference
	referencesDirty bool
	postValues      []postValue
	nativeGraph     utils.NetworkXGraph
	nativeOpinions  []float64
	nativeRecords   []model.PostRecord[float64]
	nativePosts     map[int64][]model.PostRecord[float64]
	nativeSeen      []bool
}

func NewObserver(options Options) (*Observer, error) {
	return NewObserverWithCollection(options, nil)
}

// NewObserverWithCollection resolves output selection before allocating ledger
// histories. Pair signal/reference work remains necessary for union deduplication.
func NewObserverWithCollection(options Options, collection *Collection) (*Observer, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if collection == nil && options.DetailOutput {
		resolved, err := ResolveCollection(nil)
		if err != nil {
			return nil, err
		}
		resolved.Products = append(resolved.Products, "details")
		collection = &resolved
	}
	plan, err := compileCollection(collection)
	if err != nil {
		return nil, err
	}
	options.DetailOutput = plan.products["details"]
	options.FixedPairs = append([][2]int64(nil), options.FixedPairs...)
	return &Observer{options: options, collection: plan, pairs: make(map[[2]int64]*pairTrack), next: make(map[[2]int64]int), stopped: make(map[[2]int64][2]int), referencesDirty: true}, nil
}

// Measure is an independent one-frame measurement using current core members;
// it omits the stream ledger. Observer.Measure freezes references per epoch.
func Measure(step int, opinions []float64, graph utils.NetworkXGraph, posts map[int64][]model.PostRecord[float64], frame groups.Frame, options Options) (Frame, error) {
	o, err := NewObserver(options)
	if err != nil {
		return Frame{}, err
	}
	result, err := o.Measure(step, opinions, graph, posts, frame)
	result.Events = nil
	result.Episodes = nil
	result.Epochs = nil
	result.Summaries = nil
	result.BranchEvents = nil
	result.BranchEpisodes = nil
	result.BranchSummaries = nil
	return result, err
}

func validateInput(step int, x []float64, graph utils.NetworkXGraph, frame groups.Frame) (map[int64]groups.Group, error) {
	if step < 0 || frame.Step != step || len(x) == 0 || !graph.Directed {
		return nil, fmt.Errorf("bridges: invalid step/frame, empty opinions, or undirected graph")
	}
	for _, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("bridges: nonfinite opinion")
		}
	}
	if len(graph.Nodes) != len(x) {
		return nil, fmt.Errorf("bridges: graph node coverage differs from opinion population")
	}
	for i := range x {
		if _, ok := graph.Nodes[int64(i)]; !ok {
			return nil, fmt.Errorf("bridges: missing graph node %d", i)
		}
	}
	observed := make(map[int64]groups.Group, len(frame.Groups))
	for _, group := range frame.Groups {
		if _, exists := observed[group.ID]; exists || group.ID < 0 {
			return nil, fmt.Errorf("bridges: duplicate or invalid group identity")
		}
		observed[group.ID] = group
		if group.State != "observed" {
			continue
		}
		if len(group.Core.Members) == 0 {
			return nil, fmt.Errorf("bridges: empty observed core")
		}
		seen := make(map[int]bool)
		for _, i := range group.Core.Members {
			if i < 0 || i >= len(x) || seen[i] {
				return nil, fmt.Errorf("bridges: invalid or duplicate core member")
			}
			seen[i] = true
		}
	}
	for id, group := range observed {
		if group.State != "observed" {
			delete(observed, id)
		}
	}
	return observed, nil
}

func (o *Observer) Measure(step int, x []float64, graph utils.NetworkXGraph, posts map[int64][]model.PostRecord[float64], frame groups.Frame) (Frame, error) {
	if o == nil || o.pairs == nil || o.closed {
		return Frame{}, fmt.Errorf("bridges: observer is nil, uninitialized or closed")
	}
	observed, err := validateInput(step, x, graph, frame)
	if err != nil {
		return Frame{}, err
	}
	if o.started && (step <= o.lastStep || len(x) != o.n) {
		return Frame{}, fmt.Errorf("bridges: require increasing steps and fixed population")
	}
	// Validate all input before advancing any ledger state.
	for from, adj := range graph.Adjacency {
		if from < 0 || from >= int64(len(x)) {
			return Frame{}, fmt.Errorf("bridges: invalid edge source")
		}
		for to := range adj {
			if to < 0 || to >= int64(len(x)) || to == from {
				return Frame{}, fmt.Errorf("bridges: invalid edge target")
			}
		}
	}
	if cap(o.postValues) < len(x) {
		o.postValues = make([]postValue, len(x))
	} else {
		o.postValues = o.postValues[:len(x)]
		clear(o.postValues)
	}
	z := o.postValues
	for holder, history := range posts {
		if holder < 0 || holder >= int64(len(x)) {
			return Frame{}, fmt.Errorf("bridges: invalid post holder")
		}
		if len(history) == 0 {
			continue
		}
		post := history[len(history)-1]
		if post.AgentID < -1 || post.AgentID >= int64(len(x)) {
			return Frame{}, fmt.Errorf("bridges: invalid latest post author %d; only -1 denotes unavailable provenance", post.AgentID)
		}
		if post.Step < -1 || post.Step > step {
			return Frame{}, fmt.Errorf("bridges: invalid latest post origin step")
		}
		if math.IsNaN(post.Opinion) || math.IsInf(post.Opinion, 0) {
			return Frame{}, fmt.Errorf("bridges: nonfinite latest post")
		}
		z[holder] = postValue{value: post.Opinion, source: int(post.AgentID), available: true, sourceKnown: post.AgentID >= 0 && post.AgentID < int64(len(x))}
	}
	wanted := make(map[[2]int64]bool)
	if len(o.options.FixedPairs) > 0 {
		for _, pair := range o.options.FixedPairs {
			pair = ordered(pair)
			if _, ok := observed[pair[0]]; ok {
				if _, ok := observed[pair[1]]; ok {
					wanted[pair] = true
				}
			}
		}
	} else {
		ids := make([]int64, 0, len(observed))
		for id := range observed {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for i, a := range ids {
			for _, b := range ids[i+1:] {
				wanted[[2]int64{a, b}] = true
			}
		}
	}
	// Pair membership cannot overlap, even when one group is only a caller-
	// supplied fixed reference. Different pair epochs may freeze different sets.
	for pair := range wanted {
		seen := make(map[int]bool)
		for side, id := range pair {
			for _, i := range observed[id].Core.Members {
				if side == 1 && seen[i] {
					return Frame{}, fmt.Errorf("bridges: overlapping pair cores")
				}
				seen[i] = true
			}
		}
	}
	result := Frame{DefinitionVersion: DefinitionVersion, BranchLedgerVersion: BranchLedgerVersion, Step: step, Pairs: []PairFrame{}}
	gap := o.started && (frame.Gap || step != o.lastStep+1)
	for _, pair := range sortedPairKeys(o.pairs) {
		track := o.pairs[pair]
		reason := ""
		if gap {
			reason = "sampling_gap"
		} else if !wanted[pair] {
			reason = "group_unobserved_or_ended"
		} else if track.groupEpoch != [2]int{observed[pair[0]].Epoch, observed[pair[1]].Epoch} {
			reason = "group_epoch_changed"
		}
		if reason != "" {
			end := track.lastStep
			if !gap {
				track.integrate(step)
				end = step
			}
			track.close(end, step, reason, &result)
			delete(o.pairs, pair)
			o.referencesDirty = true
		}
	}
	for _, pair := range sortedWanted(wanted) {
		if epochs, stopped := o.stopped[pair]; stopped {
			if epochs == [2]int{observed[pair[0]].Epoch, observed[pair[1]].Epoch} {
				continue
			}
			delete(o.stopped, pair)
		}
		if _, exists := o.pairs[pair]; exists {
			continue
		}
		o.next[pair]++
		track := newPairTrack(pair, o.next[pair], step, len(x), observed)
		track.configureCollection(o.collection)
		track.initBranches(len(x), o.options.BranchSides)
		o.pairs[pair] = track
		o.referencesDirty = true
		result.Epochs = append(result.Epochs, Epoch{Scope: "pair", GroupIDs: pair, Number: track.epoch, State: "open", Start: step, End: step, CoreMembers: [2][]int{append([]int(nil), track.members[0]...), append([]int(nil), track.members[1]...)}})
	}
	if len(o.pairs) == 0 {
		// Input remains fully validated, but no reference domain needs an edge
		// compatibility pass, holder states, or a fresh membership layout.
		if o.collection.scopes["union"] {
			o.measureUnion(step, gap, &result)
		}
		o.started, o.lastStep, o.n = true, step, len(x)
		return o.collectedFrame(result), nil
	}
	if o.referencesDirty {
		o.rebuildReferences(len(x))
	}
	refs := o.references
	for k := range refs {
		ref := &refs[k]
		ref.mean, ref.lower, ref.upper = 0, math.Inf(1), math.Inf(-1)
		for _, i := range ref.members {
			ref.mean += x[i] / float64(len(ref.members))
			ref.lower = math.Min(ref.lower, x[i])
			ref.upper = math.Max(ref.upper, x[i])
		}
	}
	if o.collection.scopes["union"] {
		o.prepareUnion(len(x))
	}
	needed := len(refs) * len(x)
	if cap(o.agg) < needed {
		o.agg = make([]support, needed)
	} else {
		o.agg = o.agg[:needed]
		clear(o.agg)
	}
	for from, adj := range graph.Adjacency {
		degree := len(adj)
		if degree == 0 || len(o.membership[from]) == 0 {
			continue
		}
		weight := 1 / float64(degree)
		for holder := range adj {
			do := math.Abs(x[from] - x[holder])
			op := do <= o.options.Epsilon
			ol, ou := do <= o.options.Epsilon-o.options.Guard, do <= o.options.Epsilon+o.options.Guard
			cp, pl, pu := false, false, false
			if z[holder].available {
				d := math.Abs(x[from] - z[holder].value)
				cp = d <= o.options.Epsilon
				pl = d <= o.options.Epsilon-o.options.Guard
				pu = d <= o.options.Epsilon+o.options.Guard
			}
			for _, ref := range o.membership[from] {
				a := &o.agg[ref*len(x)+int(holder)]
				a.count[0]++
				a.weight[0] += weight
				if op {
					a.count[1]++
					a.weight[1] += weight
				}
				if cp {
					a.count[2]++
					a.weight[2] += weight
				}
				if op && cp {
					a.count[3]++
					a.weight[3] += weight
				}
				if ol {
					a.opLower++
				}
				if ou {
					a.opUpper++
				}
				if pl {
					a.postLower++
				}
				if pu {
					a.postUpper++
				}
			}
		}
	}
	for _, pair := range sortedPairKeys(o.pairs) {
		track := o.pairs[pair]
		r0, r1 := refs[track.refs[0]], refs[track.refs[1]]
		pf := PairFrame{GroupIDs: pair, Epoch: track.epoch, CoreMeans: [2]float64{r0.mean, r1.mean}, CoreLower: [2]float64{r0.lower, r1.lower}, CoreUpper: [2]float64{r0.upper, r1.upper}, Contact: math.Abs(r0.mean-r1.mean) <= o.options.Epsilon}
		if o.options.StopAtContact && pf.Contact {
			track.integrate(step)
			track.close(step, step, "core_contact", &result)
			o.stopped[pair] = track.groupEpoch
			delete(o.pairs, pair)
			o.referencesDirty = true
			continue
		}
		if o.options.DetailOutput && o.collection.scopes["pair"] {
			pf.Holders = make([]HolderState, 0, len(x))
		}
		pf.Attention = Attention{OpinionKnown: true, PostKnown: true, UnionKnown: true}
		track.integrate(step)
		for holder := range x {
			state := o.holder(holder, x[holder], z[holder], track, refs)
			if o.collection.scopes["union"] {
				o.unionStates[holder].add(state)
			}
			if o.collection.products["snapshots"] && o.collection.scopes["pair"] {
				pf.Counts.add(state)
				pf.Attention.add(state, len(x))
			}
			track.update(step, state, &result)
			track.updatePairBranches(step, &state, &result)
			if o.options.DetailOutput && o.collection.scopes["pair"] {
				pf.Holders = append(pf.Holders, state)
			}
		}
		track.commitStep(step, &result)
		result.Pairs = append(result.Pairs, pf)
	}
	if o.collection.scopes["union"] {
		o.measureUnion(step, gap, &result)
	}
	o.started = true
	o.lastStep = step
	o.n = len(x)
	return o.collectedFrame(result), nil
}

func (o *Observer) collectedFrame(result Frame) Frame {
	if !o.collection.products["snapshots"] || !o.collection.scopes["pair"] {
		result.Pairs = nil
	}
	if !o.collection.products["snapshots"] || !o.collection.scopes["union"] {
		result.Union = nil
	}
	return result
}

// Frozen member incidence is rebuilt only when pair epochs change. Geometry
// still updates every frame from those fixed members' current opinions.
func (o *Observer) rebuildReferences(n int) {
	clear(o.references)
	o.references = o.references[:0]
	refKeys := make(map[string]int)
	if len(o.membership) != n {
		o.membership = make([][]int, n)
	}
	for i := range o.membership {
		o.membership[i] = o.membership[i][:0]
	}
	for _, pair := range sortedPairKeys(o.pairs) {
		track := o.pairs[pair]
		for side, ids := range track.members {
			key := make([]byte, n)
			for _, i := range ids {
				key[i] = 1
			}
			idx, exists := refKeys[string(key)]
			if !exists {
				idx = len(o.references)
				refKeys[string(key)] = idx
				o.references = append(o.references, reference{members: ids})
				for _, i := range ids {
					o.membership[i] = append(o.membership[i], idx)
				}
			}
			track.refs[side] = idx
		}
	}
	o.referencesDirty = false
}

func (a *Attention) add(h HolderState, n int) {
	for _, item := range []struct {
		s                [2]SignalState
		total, bilateral *float64
		available        *bool
	}{{h.O, &a.OpinionTotal, &a.OpinionBoth, &a.OpinionKnown}, {h.P, &a.PostTotal, &a.PostBoth, &a.PostKnown}, {h.U, &a.UnionTotal, &a.UnionBoth, &a.UnionKnown}} {
		if !item.s[0].CountKnown || !item.s[1].CountKnown {
			*item.available = false
			continue
		}
		*item.total += (item.s[0].Weight + item.s[1].Weight) / float64(n)
		*item.bilateral += 2 * math.Min(item.s[0].Weight, item.s[1].Weight) / float64(n)
	}
}

func (o *Observer) holder(holder int, v float64, post postValue, track *pairTrack, refs []reference) HolderState {
	h := HolderState{Holder: holder}
	outside := track.labels[holder] < 0
	om := o.middle(v, outside, track, refs)
	pm := Status{}
	if post.available {
		pm = o.middle(post.value, outside, track, refs)
	} else if !outside {
		pm = known(false)
	}
	for side := 0; side < 2; side++ {
		a := o.agg[track.refs[side]*len(track.labels)+holder]
		h.S[side] = SignalState{Status: known(a.count[0] > 0), Count: a.count[0], Weight: a.weight[0], CountKnown: true, SourceKnown: true}
		og := either(om, known(track.labels[holder] == 1-side))
		pg := Status{}
		if post.available {
			source := Status{}
			if post.sourceKnown {
				source = known(track.labels[post.source] == 1-side)
			}
			pg = either(pm, source)
		}
		h.O[side] = qualified(og, a.count[1], a.weight[1], a.opLower, a.opUpper, true, true, a.count[0])
		h.P[side] = qualified(pg, a.count[2], a.weight[2], a.postLower, a.postUpper, post.available, post.sourceKnown, a.count[0])
		u := SignalState{Status: either(h.O[side].Status, h.P[side].Status), CountKnown: h.O[side].CountKnown && h.P[side].CountKnown, SourceKnown: h.O[side].SourceKnown && h.P[side].SourceKnown, CrossBoundary: either(og, pg)}
		u.Count = h.O[side].Count + h.P[side].Count
		u.Weight = h.O[side].Weight + h.P[side].Weight
		if h.O[side].Status.Active && h.P[side].Status.Active {
			u.Count -= a.count[3]
			u.Weight -= a.weight[3]
		}
		h.U[side] = u
	}
	h.Active = either(h.U[0].Status, h.U[1].Status)
	opinion, postSignal := either(h.O[0].Status, h.O[1].Status), either(h.P[0].Status, h.P[1].Status)
	h.SignalBoth = both(opinion, postSignal)
	h.OpinionOnly = both(opinion, not(postSignal))
	h.PostOnly = both(postSignal, not(opinion))
	h.StructuralOnly = both(either(h.S[0].Status, h.S[1].Status), not(h.Active))
	h.Bilateral = both(h.U[0].Status, h.U[1].Status)
	h.OpinionBilateral = both(h.O[0].Status, h.O[1].Status)
	h.PostBilateral = both(h.P[0].Status, h.P[1].Status)
	h.CrossOP = both(h.O[0].Status, h.P[1].Status)
	h.CrossPO = both(h.P[0].Status, h.O[1].Status)
	h.MixedBilateral = both(h.Bilateral, not(either(h.OpinionBilateral, h.PostBilateral)))
	h.MiddleO = both(om, either(h.O[0].Status, h.O[1].Status))
	h.MiddleP = both(pm, either(h.P[0].Status, h.P[1].Status))
	h.MiddleU = either(h.MiddleO, h.MiddleP)
	for side := 0; side < 2; side++ {
		for k, s := range []Status{h.S[side].Status, h.O[side].Status, h.P[side].Status, h.U[side].Status} {
			bit := uint16(1) << uint(side*4+k)
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
	return h
}
func (o *Observer) middle(value float64, outside bool, track *pairTrack, refs []reference) Status {
	r0, r1 := refs[track.refs[0]], refs[track.refs[1]]
	if r0.mean > r1.mean {
		r0, r1 = r1, r0
	}
	lo, hi := r0.upper+o.options.MiddleMargin, r1.lower-o.options.MiddleMargin
	middle := known(outside && value > lo && value < hi)
	if outside && o.options.Guard > 0 {
		lower := value > lo+o.options.Guard && value < hi-o.options.Guard
		upper := value > lo-o.options.Guard && value < hi+o.options.Guard
		middle.Uncertain = lower != upper
	}
	return middle
}
func qualified(gate Status, count int, weight float64, lower, upper int, valueKnown, sourceKnown bool, raw int) SignalState {
	comp := Status{}
	if valueKnown {
		comp = Status{Known: true, Active: count > 0, Uncertain: (lower > 0) != (upper > 0)}
	}
	if raw == 0 {
		comp = known(false)
	}
	s := SignalState{Status: both(gate, comp), SourceKnown: sourceKnown, CrossBoundary: gate}
	if s.Status.Known {
		s.CountKnown = true
		if s.Status.Active {
			s.Count = count
			s.Weight = weight
		}
	}
	return s
}
func (c *Counts) add(h HolderState) {
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
	if h.S[0].Status.Active || h.S[1].Status.Active {
		c.SupportAny++
	}
	if h.S[0].Status.Active && h.S[1].Status.Active {
		c.SupportBoth++
	}
	for _, item := range []struct {
		s Status
		p *int
	}{{h.SignalBoth, &c.SignalBoth}, {h.OpinionOnly, &c.OpinionOnly}, {h.PostOnly, &c.PostOnly}, {h.StructuralOnly, &c.StructuralOnly}, {h.Bilateral, &c.Bilateral}, {h.OpinionBilateral, &c.OpinionBilateral}, {h.PostBilateral, &c.PostBilateral}, {h.CrossOP, &c.CrossOP}, {h.CrossPO, &c.CrossPO}, {h.MixedBilateral, &c.MixedBilateral}, {h.MiddleO, &c.MiddleO}, {h.MiddleP, &c.MiddleP}, {h.MiddleU, &c.MiddleU}} {
		if item.s.Known && item.s.Active {
			*item.p++
		}
	}
	for _, item := range []struct {
		name string
		s    Status
	}{{"signal_both", h.SignalBoth}, {"opinion_only", h.OpinionOnly}, {"post_only", h.PostOnly}, {"structural_only", h.StructuralOnly}, {"middle_o", h.MiddleO}, {"middle_p", h.MiddleP}, {"middle_u", h.MiddleU}, {"bilateral", h.Bilateral}, {"opinion_bilateral", h.OpinionBilateral}, {"post_bilateral", h.PostBilateral}, {"cross_op", h.CrossOP}, {"cross_po", h.CrossPO}, {"mixed_bilateral", h.MixedBilateral}} {
		if !item.s.Known {
			if c.UnknownByCategory == nil {
				c.UnknownByCategory = make(map[string]int)
			}
			c.UnknownByCategory[item.name]++
		}
	}
	for side := 0; side < 2; side++ {
		if !h.O[side].Status.Known {
			c.SideUnknownO[side]++
		}
		if !h.P[side].Status.Known {
			c.SideUnknownP[side]++
		}
		if h.O[side].Status.Known && h.O[side].Status.Active {
			c.SideO[side]++
		}
		if h.P[side].Status.Known && h.P[side].Status.Active {
			c.SideP[side]++
		}
		if h.U[side].Status.Known && h.U[side].Status.Active {
			c.SideU[side]++
		}
		if !h.U[side].Status.Known {
			c.SideUnknownU[side]++
		}
	}
}
func sortedWanted(m map[[2]int64]bool) [][2]int64 {
	keys := make([][2]int64, 0, len(m))
	for p := range m {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}
func sortedPairKeys(m map[[2]int64]*pairTrack) [][2]int64 {
	keys := make([][2]int64, 0, len(m))
	for p := range m {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}
