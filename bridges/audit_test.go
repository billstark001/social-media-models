package bridges

import (
	"reflect"
	"smp/groups"
	"smp/model"
	"smp/utils"
	"testing"
)

func TestGuardLogicIsCommutativeAndRobustWitnessDominates(t *testing.T) {
	states := []Status{{}, {Uncertain: true}, known(false), {Known: true, Uncertain: true}, known(true), {Known: true, Active: true, Uncertain: true}}
	for _, a := range states {
		for _, b := range states {
			if either(a, b) != either(b, a) || both(a, b) != both(b, a) {
				t.Fatalf("order-dependent guard logic: a=%+v b=%+v OR=%+v/%+v AND=%+v/%+v", a, b, either(a, b), either(b, a), both(a, b), both(b, a))
			}
			for _, c := range states {
				if either(either(a, b), c) != either(a, either(b, c)) || both(both(a, b), c) != both(a, both(b, c)) {
					t.Fatalf("nonassociative guard logic: %+v %+v %+v", a, b, c)
				}
			}
		}
	}
	if either(Status{Known: true, Active: true, Uncertain: true}, known(true)) != known(true) {
		t.Fatal("a stable positive witness must make OR stable")
	}
	if both(Status{Known: true, Uncertain: true}, known(false)) != known(false) {
		t.Fatal("a stable negative witness must make AND stable")
	}
}

func TestEpisodeIncludesNumericEntryAndExitBoundaryFlags(t *testing.T) {
	for _, entry := range []bool{false, true} {
		tr := &pairTrack{scope: "pair", holders: make([]holderMemory, 1)}
		var frame Frame
		tr.update(0, HolderState{Holder: 0, Active: Status{Known: true, Uncertain: entry}}, &frame)
		tr.lastStep = 0
		tr.integrate(1)
		tr.update(1, HolderState{Holder: 0, Active: known(true)}, &frame)
		tr.lastStep = 1
		tr.integrate(2)
		tr.update(2, HolderState{Holder: 0, Active: Status{Known: true, Uncertain: !entry}}, &frame)
		if len(frame.Episodes) != 1 || !frame.Episodes[0].Uncertain {
			t.Fatalf("episode dropped uncertain boundary entry=%v: %+v", entry, frame)
		}
	}
}

func TestUnknownPreservesObservedSeenHistoryAndExcludesExposure(t *testing.T) {
	tr := &pairTrack{scope: "pair", holders: make([]holderMemory, 1)}
	var frame Frame
	states := []Status{known(true), {}, known(false), known(true), known(false)}
	for step, s := range states {
		tr.integrate(step)
		tr.update(step, HolderState{Holder: 0, Active: s}, &frame)
		tr.lastStep = step
	}
	tr.close(4, 4, "test_end", &frame)
	s := frame.Summaries[0]
	if s.InitialPresent != 1 || s.Formations != 0 || s.Regenerations != 1 || s.Losses != 1 || s.CensoredEpisodes != 1 || s.ActiveHolderRounds != 2 || s.KnownHolderRounds != 3 || s.SeenInactiveHolderRounds != 1 || s.NeverSeenHolderRounds != 0 {
		t.Fatalf("unknown interval corrupted observed history/exposure: %+v", s)
	}
}

func TestInvalidAuthorDoesNotAdvanceObserver(t *testing.T) {
	for _, author := range []int64{-2, 6, 100} {
		x, g, p, f := bridgeFixture()
		o, _ := NewObserver(DefaultOptions(.7))
		if _, err := o.Measure(0, x, g, p, f); err != nil {
			t.Fatal(err)
		}
		f.Step = 1
		p[4][0].AgentID = author
		if _, err := o.Measure(1, x, g, p, f); err == nil {
			t.Fatalf("invalid author %d was silently unknown", author)
		}
		p[4][0].AgentID = -1
		if _, err := o.Measure(1, x, g, p, f); err != nil {
			t.Fatalf("failed validation consumed a frame: %v", err)
		}
	}
}

func TestPairAndUnionOrderAndReturnedReferenceOwnership(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	f.Groups = append(f.Groups, groups.Group{ID: 30, State: "observed", Core: groups.Core{Members: []int{5}}})
	a, _ := NewObserver(detailOptions(.7))
	b, _ := NewObserver(detailOptions(.7))
	for step := 0; step < 4; step++ {
		f.Step = step
		reversed := f
		reversed.Groups = append([]groups.Group(nil), f.Groups...)
		for i, j := 0, len(reversed.Groups)-1; i < j; i, j = i+1, j-1 {
			reversed.Groups[i], reversed.Groups[j] = reversed.Groups[j], reversed.Groups[i]
		}
		first, err := a.Measure(step, x, g, p, f)
		if err != nil {
			t.Fatal(err)
		}
		second, err := b.Measure(step, x, g, p, reversed)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("input group order changed frame at %d", step)
		}
		if step == 0 {
			for i := range first.Epochs {
				if first.Epochs[i].Scope == "pair" {
					first.Epochs[i].CoreMembers[0][0] = 4
				}
			}
			first.Pairs[0].Holders[4].Active = known(false)
			first.Union.Holders[4].Active = known(false)
		}
		if step == 1 {
			f.Groups[0].Core.Members = []int{0, 4}
		}
		if step == 2 {
			f.Groups = f.Groups[:2]
		}
	}
}

func TestReferenceCacheDistinguishesOldAndNewEpochMemberSets(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	o, _ := NewObserver(detailOptions(.7))
	if _, err := o.Measure(0, x, g, p, f); err != nil {
		t.Fatal(err)
	}
	f.Step = 1
	f.Groups[0].Core.Members = []int{0, 4}
	f.Groups = append(f.Groups, groups.Group{ID: 30, State: "observed", Core: groups.Core{Members: []int{5}}})
	frame, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range frame.Pairs {
		if pair.GroupIDs == [2]int64{10, 20} {
			if pair.CoreMeans[0] != (x[0]+x[1])/2 || pair.Counts.Active != 1 {
				t.Fatalf("old reference was relabelled: %+v", pair)
			}
		} else if pair.GroupIDs == [2]int64{10, 30} {
			if pair.CoreMeans[0] != (x[0]+x[4])/2 || pair.Counts.Active != 0 {
				t.Fatalf("new epoch reused stale membership: %+v", pair)
			}
		}
	}
	f.Step = 2
	f.Groups[0].State = "unobserved"
	frame, err = o.Measure(2, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range frame.Events {
		if event.Type == "loss" || event.Type == "regeneration" {
			t.Fatalf("domain removal manufactured an event: %+v", event)
		}
	}
	if frame.Union.Counts.Active != 0 || frame.Union.Epoch != 3 {
		t.Fatal("union reference change was not censored/reopened")
	}
	f.Step = 3
	f.Groups[0].State = "observed"
	frame, err = o.Measure(3, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range frame.Events {
		if event.Type == "regeneration" {
			t.Fatal("returning reference manufactured regeneration")
		}
	}
	f.Step = 5
	frame, err = o.Measure(5, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range frame.Summaries {
		if s.End != 3 || s.ActiveHolderRounds != 0 {
			t.Fatalf("gap/reference combination extrapolated exposure: %+v", s)
		}
	}
}

func TestEmptyDomainStillValidatesAndZeroObserverReturnsError(t *testing.T) {
	x, g, p, f := bridgeFixture()
	f.Groups = nil
	var zero Observer
	if _, err := zero.Measure(0, x, g, p, f); err == nil {
		t.Fatal("zero observer must require NewObserver")
	}
	if _, err := zero.FinalClose(0); err == nil {
		t.Fatal("zero observer close should return an error")
	}
	o, _ := NewObserver(DefaultOptions(.7))
	p[0][0].AgentID = 100
	if _, err := o.Measure(0, x, g, p, f); err == nil {
		t.Fatal("empty domain bypassed author validation")
	}
	p[0][0].AgentID = 0
	g.Adjacency[0][100] = nil
	if _, err := o.Measure(0, x, g, p, f); err == nil {
		t.Fatal("empty domain bypassed graph validation")
	}
	delete(g.Adjacency[0], 100)
	if _, err := o.Measure(0, x, g, p, f); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableGroupIdentitiesAreValidatedAtomically(t *testing.T) {
	for _, bad := range []groups.Group{{ID: 10, State: "unobserved"}, {ID: -1, State: "unobserved"}} {
		x, g, p, f := bridgeFixture()
		o, _ := NewObserver(DefaultOptions(.7))
		f.Groups = append(f.Groups, bad)
		if _, err := o.Measure(0, x, g, p, f); err == nil {
			t.Fatalf("invalid unavailable group identity accepted: %+v", bad)
		}
		f.Groups = f.Groups[:2]
		if _, err := o.Measure(0, x, g, p, f); err != nil {
			t.Fatalf("invalid group input advanced the ledger: %v", err)
		}
	}
}

func TestExplicitAdjacentGapDoesNotIntegrateTheUnknownInterval(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	o, _ := NewObserver(DefaultOptions(.7))
	if _, err := o.Measure(0, x, g, p, f); err != nil {
		t.Fatal(err)
	}
	f.Step = 1
	f.Gap = true
	frame, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range frame.Summaries {
		if summary.End != 0 || summary.KnownHolderRounds != 0 || summary.ActiveHolderRounds != 0 {
			t.Fatalf("explicit gap was ignored because timestamps are adjacent: %+v", summary)
		}
	}
	for _, event := range frame.Events {
		if event.Type != "initial_present" || !event.LeftCensored {
			t.Fatalf("gap produced an ordinary event: %+v", event)
		}
	}
}

func TestNativeWorkspaceUsesCurrentPostAndOwnsReturnedData(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 1, 0)
	p[0][0] = model.PostRecord[float64]{AgentID: 2, Step: 0, Opinion: -.1}
	m := auditNativeFixture(x, g, p)
	m.Grid.PostMap[0] = []*model.PostRecord[float64]{{AgentID: 0, Step: -1, Opinion: x[0]}}
	native, _ := NewObserver(detailOptions(.7))
	direct, _ := NewObserver(detailOptions(.7))
	for step := 0; step < 3; step++ {
		f.Step = step
		got, err := NativeMeasure(step, m, native, f)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := direct.Measure(step, x, g, p, f)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("native workspace differs from direct input at step %d", step)
		}
		if step == 0 {
			if !holderAt(t, got, 0).P[0].Status.Active {
				t.Fatal("retained history substituted for displayed post")
			}
			got.Pairs[0].Holders[0].P[0].Count = 999
		}
		if step == 1 {
			m.Grid.AgentMap[0].CurPost = nil
			delete(p, 0)
			m.Graph.RemoveEdge(1, 0)
			delete(g.Adjacency[1], 0)
			m.Graph.SetEdge(m.Graph.NewEdge(m.Graph.Node(2), m.Graph.Node(4)))
			connectBridge(&g, 2, 4)
		}
	}
	if _, err := native.FinalClose(2); err != nil {
		t.Fatal(err)
	}
	if len(native.nativeGraph.Adjacency) != 0 || len(native.references) != 0 || len(native.agg) != 0 {
		t.Fatal("closed observer retained native/reference workspace")
	}
}

func TestInvalidNativeModelReturnsErrorWithoutConsumingStep(t *testing.T) {
	x, g, p, f := bridgeFixture()
	m := auditNativeFixture(x, g, p)
	o, _ := NewObserver(DefaultOptions(.7))
	schedule := m.Schedule
	m.Schedule = nil
	if _, err := NativeMeasure(0, m, o, f); err == nil {
		t.Fatal("missing schedule accepted")
	}
	m.Schedule = schedule
	agent := m.Schedule.Agents[0]
	m.Schedule.Agents[0] = nil
	if _, err := NativeMeasure(0, m, o, f); err == nil {
		t.Fatal("nil native agent accepted")
	}
	m.Schedule.Agents[0] = agent
	if _, err := NativeMeasure(0, m, o, f); err != nil {
		t.Fatalf("invalid native request advanced ledger: %v", err)
	}
}

func auditBenchmarkFixture() ([]float64, utils.NetworkXGraph, map[int64][]model.PostRecord[float64], groups.Frame) {
	n := 500
	x := make([]float64, n)
	g := utils.NetworkXGraph{Directed: true, Nodes: map[int64]map[string]any{}, Adjacency: map[int64]map[int64]any{}}
	p := map[int64][]model.PostRecord[float64]{}
	f := groups.Frame{Groups: []groups.Group{{ID: 1, State: "observed"}, {ID: 2, State: "observed"}}}
	for i := range x {
		x[i] = -.6
		if i >= 250 {
			x[i] = .6
		}
		if i%50 == 0 {
			x[i] = 0
		} else {
			side := i / 250
			f.Groups[side].Core.Members = append(f.Groups[side].Core.Members, i)
		}
		g.Nodes[int64(i)] = nil
		g.Adjacency[int64(i)] = map[int64]any{}
		p[int64(i)] = []model.PostRecord[float64]{{AgentID: int64(i), Step: 0, Opinion: x[i]}}
		for d := 1; d <= 15; d++ {
			g.Adjacency[int64(i)][int64((i+d*31)%n)] = nil
		}
	}
	return x, g, p, f
}

func auditNativeFixture(x []float64, g utils.NetworkXGraph, p map[int64][]model.PostRecord[float64]) *model.SMPModel[float64, struct{}] {
	graph := utils.DeserializeGraph(&g)
	grid := model.NewNetworkGrid[float64, struct{}](graph)
	schedule := &model.RandomActivation[float64, struct{}]{}
	for i, v := range x {
		post := p[int64(i)][0]
		agent := &model.SMPAgent[float64, struct{}]{ID: int64(i), CurOpinion: v, CurPost: &post}
		grid.AgentMap[int64(i)] = agent
		schedule.Agents = append(schedule.Agents, agent)
	}
	return &model.SMPModel[float64, struct{}]{Graph: graph, Grid: grid, Schedule: schedule}
}

func BenchmarkObserverNoGroups500(b *testing.B) {
	x, g, p, f := auditBenchmarkFixture()
	f.Groups = nil
	o, _ := NewObserver(DefaultOptions(.7))
	b.ReportAllocs()
	b.ResetTimer()
	for step := 0; step < b.N; step++ {
		f.Step = step
		if _, err := o.Measure(step, x, g, p, f); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNative500(b *testing.B) {
	x, g, p, f := auditBenchmarkFixture()
	m := auditNativeFixture(x, g, p)
	o, _ := NewObserver(DefaultOptions(.7))
	b.ReportAllocs()
	b.ResetTimer()
	for step := 0; step < b.N; step++ {
		f.Step = step
		if _, err := NativeMeasure(step, m, o, f); err != nil {
			b.Fatal(err)
		}
	}
}
