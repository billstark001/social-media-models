package groups

import (
	"fmt"
	"sort"
)

type Group struct {
	ID                int64  `json:"id" msgpack:"id"`
	Core              Core   `json:"core" msgpack:"core"`
	FirstObservedStep int    `json:"first_observed_step" msgpack:"first_observed_step"`
	ConfirmedStep     int    `json:"confirmed_step" msgpack:"confirmed_step"`
	LastObservedStep  int    `json:"last_observed_step" msgpack:"last_observed_step"`
	State             string `json:"state" msgpack:"state"`
	Epoch             int    `json:"epoch" msgpack:"epoch"`
}

// Event describes observed qualification or lineage, never physical death.
// Step is the confirmation/report time; intervals concern first observed change.
type Event struct {
	Type          string  `json:"type" msgpack:"type"`
	Step          int     `json:"step" msgpack:"step"`
	IntervalStart int     `json:"interval_start" msgpack:"interval_start"`
	IntervalEnd   int     `json:"interval_end" msgpack:"interval_end"`
	GroupIDs      []int64 `json:"group_ids" msgpack:"group_ids"`
	Parents       []int64 `json:"parents" msgpack:"parents"`
	Children      []int64 `json:"children" msgpack:"children"`
	LeftCensored  bool    `json:"left_censored" msgpack:"left_censored"`
	RightCensored bool    `json:"right_censored" msgpack:"right_censored"`
	Reason        string  `json:"reason" msgpack:"reason"`
}

type Frame struct {
	Step      int       `json:"step" msgpack:"step"`
	Detection Detection `json:"detection" msgpack:"detection"`
	Groups    []Group   `json:"groups" msgpack:"groups"`
	Events    []Event   `json:"events" msgpack:"events"`
	Gap       bool      `json:"gap" msgpack:"gap"`
	Epoch     int       `json:"epoch" msgpack:"epoch"`
	Status    string    `json:"status" msgpack:"status"`
}

type trackedGroup struct {
	group                 Group
	missing, firstMissing int
}
type candidate struct {
	core       Core
	parents    []int64
	age, first int
	initial    bool
}

// Tracker is one run's prospective, individually stable core identity ledger.
// It owns no simulator or RNG. NewTracker validates explicit options.
type Tracker struct {
	options         Options
	detector        Detector
	active          map[int64]*trackedGroup
	pending         []candidate
	last, epoch     int
	population      int
	nextID          int64
	started, closed bool
	lastDetection   Detection
}

func NewTracker(options Options) (*Tracker, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Tracker{options: options, active: make(map[int64]*trackedGroup), last: -1, nextID: 1}, nil
}

func cloneCore(core Core) Core     { core.Members = append([]int(nil), core.Members...); return core }
func cloneGroup(group Group) Group { group.Core = cloneCore(group.Core); return group }

func cloneDetection(d Detection) Detection {
	d.Labels = append([]int(nil), d.Labels...)
	d.Residual = append([]int(nil), d.Residual...)
	cores := make([]Core, len(d.Cores))
	for i, core := range d.Cores {
		cores[i] = cloneCore(core)
	}
	d.Cores = cores
	return d
}

// MemberOverlap compares sorted, unique member IDs. The second return value
// is intersection/min(mass), used only for lineage support, not stability.
func MemberOverlap(a, b []int) (jaccard, overlap float64) {
	i, j, intersection := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case b[j] < a[i]:
			j++
		default:
			intersection++
			i++
			j++
		}
	}
	if union := len(a) + len(b) - intersection; union > 0 {
		jaccard = float64(intersection) / float64(union)
	}
	if mass := min(len(a), len(b)); mass > 0 {
		overlap = float64(intersection) / float64(mass)
	}
	return
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (t *Tracker) Observe(step int, x []float64) (Frame, error) {
	if t == nil || t.closed {
		return Frame{}, fmt.Errorf("group tracker is closed or nil")
	}
	if step < 0 || t.started && step <= t.last {
		return Frame{}, fmt.Errorf("group observations require increasing nonnegative completed steps")
	}
	if t.started && len(x) != t.population {
		return Frame{}, fmt.Errorf("group population axis changed within a stream")
	}
	detection, err := t.detector.Detect(x, t.options)
	if err != nil {
		return Frame{}, err
	}
	frame := t.observeDetection(step, detection)
	t.population = len(x)
	return frame, nil
}

type component struct {
	parents []int64
	cores   []int
}

func (t *Tracker) components(d Detection, ids []int64) []component {
	edges := make([][]bool, len(ids))
	for i, id := range ids {
		edges[i] = make([]bool, len(d.Cores))
		for j, core := range d.Cores {
			jac, overlap := MemberOverlap(t.active[id].group.Core.Members, core.Members)
			edges[i][j] = jac >= t.options.JaccardMin || overlap >= t.options.LineageOverlap
		}
	}
	seenP, seenC := make([]bool, len(ids)), make([]bool, len(d.Cores))
	result := make([]component, 0)
	for root := 0; root < len(ids)+len(d.Cores); root++ {
		if root < len(ids) && seenP[root] || root >= len(ids) && seenC[root-len(ids)] {
			continue
		}
		queue := []int{root}
		c := component{}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			if node < len(ids) {
				if seenP[node] {
					continue
				}
				seenP[node] = true
				c.parents = append(c.parents, ids[node])
				for j, connected := range edges[node] {
					if connected && !seenC[j] {
						queue = append(queue, len(ids)+j)
					}
				}
			} else {
				j := node - len(ids)
				if seenC[j] {
					continue
				}
				seenC[j] = true
				c.cores = append(c.cores, j)
				for i := range ids {
					if edges[i][j] && !seenP[i] {
						queue = append(queue, i)
					}
				}
			}
		}
		sort.Slice(c.parents, func(i, j int) bool { return c.parents[i] < c.parents[j] })
		sort.Ints(c.cores)
		result = append(result, c)
	}
	return result
}

func (t *Tracker) add(core Core, first, step int) int64 {
	id := t.nextID
	t.nextID++
	t.active[id] = &trackedGroup{group: Group{ID: id, Core: core, FirstObservedStep: first, ConfirmedStep: step, LastObservedStep: step, State: "observed", Epoch: t.epoch}, firstMissing: -1}
	return id
}

// observeDetection is also exercised with transparent synthetic core fixtures
// so temporal semantics can be checked independently of density numerics.
func (t *Tracker) observeDetection(step int, d Detection) Frame {
	// One private immutable detection supplies active/pending memberships and
	// the closing snapshot. Returned Detection/Groups remain independently owned.
	internal := cloneDetection(d)
	first := !t.started
	gap := t.started && step > t.last+1
	frame := Frame{Step: step, Detection: d, Groups: make([]Group, 0), Events: make([]Event, 0), Gap: gap, Epoch: t.epoch, Status: "warming_up"}
	ids := make([]int64, 0, len(t.active))
	for id := range t.active {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if gap {
		t.epoch++
		// Stability concerns the observed samples. Missing native states do not
		// erase membership association, but they left-censor new confirmations.
		for i := range t.pending {
			t.pending[i].initial = true
		}
		frame.Epoch = t.epoch
		frame.Events = append(frame.Events, Event{Type: "unknown_interval", Step: step, IntervalStart: t.last, IntervalEnd: step, GroupIDs: append([]int64(nil), ids...), RightCensored: true, Reason: "sampling gap; intervening qualification and lineage are unknown"})
		for _, g := range t.active {
			g.missing = 0
			g.firstMissing = -1
			g.group.Epoch = t.epoch
		}
	}
	used := make([]bool, len(t.pending))
	next := make([]candidate, 0, len(d.Cores))
	for _, part := range t.components(internal, ids) {
		if len(part.cores) == 0 {
			g := t.active[part.parents[0]]
			g.group.State = "unobserved"
			if g.missing == 0 {
				g.firstMissing = step
			}
			g.missing++
			if g.missing >= t.options.Exit {
				frame.Events = append(frame.Events, Event{Type: "fade", Step: step, IntervalStart: g.group.LastObservedStep, IntervalEnd: g.firstMissing, GroupIDs: []int64{g.group.ID}, Reason: "consecutive missing macrocore qualifications; not physical death"})
				delete(t.active, g.group.ID)
			}
			continue
		}
		if len(part.parents) == 1 && len(part.cores) == 1 {
			g := t.active[part.parents[0]]
			core := internal.Cores[part.cores[0]]
			jac, _ := MemberOverlap(g.group.Core.Members, core.Members)
			if jac >= t.options.JaccardMin {
				g.group.Core = core
				g.group.LastObservedStep = step
				g.group.State = "observed"
				g.missing = 0
				g.firstMissing = -1
				continue
			}
		}
		local := make([]candidate, 0, len(part.cores))
		ready := true
		for _, j := range part.cores {
			core := internal.Cores[j]
			best, bestJ := -1, 0.
			for k, previous := range t.pending {
				if used[k] || !sameIDs(previous.parents, part.parents) {
					continue
				}
				jac, _ := MemberOverlap(previous.core.Members, core.Members)
				if jac >= t.options.JaccardMin && (best < 0 || jac > bestJ) {
					best, bestJ = k, jac
				}
			}
			c := candidate{core: core, parents: append([]int64(nil), part.parents...), age: 1, first: step, initial: first || gap}
			if best >= 0 {
				used[best] = true
				c.age = t.pending[best].age + 1
				c.first = t.pending[best].first
				c.initial = t.pending[best].initial
			}
			local = append(local, c)
			ready = ready && c.age >= t.options.Confirm
		}
		if !ready {
			next = append(next, local...)
			for _, id := range part.parents {
				g := t.active[id]
				g.group.State = "unobserved"
				g.missing = 0
				g.firstMissing = -1
			}
			continue
		}
		children := make([]int64, 0, len(local))
		for _, c := range local {
			children = append(children, t.add(c.core, c.first, step))
		}
		if len(part.parents) == 0 {
			for i, c := range local {
				kind, reason := "birth", "new qualified macrocore without an observed predecessor"
				if c.initial {
					kind, reason = "initial_present", "first-observation candidate or post-gap left-censored presence"
				}
				frame.Events = append(frame.Events, Event{Type: kind, Step: step, IntervalStart: max(0, c.first-1), IntervalEnd: c.first, GroupIDs: []int64{children[i]}, Children: []int64{children[i]}, LeftCensored: c.initial, Reason: reason})
			}
		} else {
			leftCensored := false
			for _, c := range local {
				leftCensored = leftCensored || c.initial
			}
			kind := "reconfiguration"
			if len(part.parents) > 1 && len(children) == 1 {
				kind = "merge"
			}
			if len(part.parents) == 1 && len(children) > 1 {
				kind = "split"
			}
			start, end := step, 0
			for _, id := range part.parents {
				start = min(start, t.active[id].group.LastObservedStep)
				delete(t.active, id)
			}
			for _, c := range local {
				end = max(end, c.first)
			}
			if leftCensored {
				for _, id := range children {
					frame.Events = append(frame.Events, Event{Type: "initial_present", Step: step, IntervalStart: start, IntervalEnd: end, GroupIDs: []int64{id}, Parents: append([]int64(nil), part.parents...), Children: []int64{id}, LeftCensored: true, Reason: "post-gap confirmed presence; parents record membership association, intervening lineage is unknown"})
				}
			} else {
				frame.Events = append(frame.Events, Event{Type: kind, Step: step, IntervalStart: start, IntervalEnd: end, GroupIDs: append([]int64(nil), children...), Parents: append([]int64(nil), part.parents...), Children: append([]int64(nil), children...), Reason: "confirmed overlap lineage; new child identities, no ordinary birth/death"})
			}
		}
	}
	t.pending = next
	t.last = step
	t.started = true
	t.lastDetection = internal
	ids = ids[:0]
	for id := range t.active {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	observed := 0
	for _, id := range ids {
		g := cloneGroup(t.active[id].group)
		frame.Groups = append(frame.Groups, g)
		if g.State == "observed" {
			observed++
		}
	}
	switch {
	case gap:
		frame.Status = "unknown_interval"
	case observed > 0:
		frame.Status = "observed"
	case len(t.active) > 0:
		frame.Status = "unobserved"
	case len(d.Cores) == 0:
		frame.Status = "unresolved"
	}
	return frame
}

// FinalClose right-censors remaining qualified identities. An optional reason
// (for example record_end) is recorded; closing never invents fade or death.
func (t *Tracker) FinalClose(step int, reasons ...string) (Frame, error) {
	if t == nil || t.closed {
		return Frame{}, fmt.Errorf("group tracker is closed or nil")
	}
	if step < 0 || t.started && step < t.last {
		return Frame{}, fmt.Errorf("group close precedes last observation")
	}
	reason := "record_end"
	if len(reasons) > 1 {
		return Frame{}, fmt.Errorf("at most one group close reason")
	}
	if len(reasons) == 1 {
		reason = reasons[0]
	}
	frame := Frame{Step: step, Detection: cloneDetection(t.lastDetection), Groups: make([]Group, 0), Events: make([]Event, 0), Epoch: t.epoch, Status: "closed", Gap: t.started && step > t.last}
	ids := make([]int64, 0, len(t.active))
	for id := range t.active {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		g := t.active[id].group
		frame.Groups = append(frame.Groups, cloneGroup(g))
		frame.Events = append(frame.Events, Event{Type: "censored", Step: step, IntervalStart: g.LastObservedStep, IntervalEnd: t.last, GroupIDs: []int64{id}, RightCensored: true, Reason: reason})
	}
	t.closed = true
	return frame, nil
}
