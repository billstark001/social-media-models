package bridges

import (
	"math"
	"reflect"
	"smp/groups"
	"smp/model"
	"smp/utils"
	"testing"
)

func bridgeFixture() ([]float64, utils.NetworkXGraph, map[int64][]model.PostRecord[float64], groups.Frame) {
	x := []float64{-.6, -.55, .55, .6, 0, .9}
	g := utils.NetworkXGraph{Directed: true, Nodes: map[int64]map[string]any{}, Adjacency: map[int64]map[int64]any{}}
	posts := map[int64][]model.PostRecord[float64]{}
	for i, v := range x {
		g.Nodes[int64(i)] = map[string]any{}
		g.Adjacency[int64(i)] = map[int64]any{}
		posts[int64(i)] = []model.PostRecord[float64]{{AgentID: int64(i), Step: 0, Opinion: v}}
	}
	f := groups.Frame{Step: 0, Groups: []groups.Group{{ID: 10, State: "observed", Core: groups.Core{Members: []int{0, 1}}}, {ID: 20, State: "observed", Core: groups.Core{Members: []int{2, 3}}}}}
	return x, g, posts, f
}
func connectBridge(g *utils.NetworkXGraph, from, to int) {
	g.Adjacency[int64(from)][int64(to)] = map[string]any{}
}
func detailOptions(eps float64) Options { o := DefaultOptions(eps); o.DetailOutput = true; return o }
func holderAt(t *testing.T, f Frame, j int) HolderState {
	t.Helper()
	if len(f.Pairs) != 1 || len(f.Pairs[0].Holders) <= j {
		t.Fatalf("missing detailed holder %d: %+v", j, f)
	}
	return f.Pairs[0].Holders[j]
}

func pairOnly(f Frame) Frame {
	events := []Event{}
	for _, v := range f.Events {
		if v.Scope == "pair" {
			events = append(events, v)
		}
	}
	f.Events = events
	episodes := []Episode{}
	for _, v := range f.Episodes {
		if v.Scope == "pair" {
			episodes = append(episodes, v)
		}
	}
	f.Episodes = episodes
	epochs := []Epoch{}
	for _, v := range f.Epochs {
		if v.Scope == "pair" {
			epochs = append(epochs, v)
		}
	}
	f.Epochs = epochs
	summaries := []Summary{}
	for _, v := range f.Summaries {
		if v.Scope == "pair" {
			summaries = append(summaries, v)
		}
	}
	f.Summaries = summaries
	return f
}

func TestMixedSignalsAndContinuousRoleConversion(t *testing.T) {
	x, g, p, f := bridgeFixture()
	x[4] = -.2
	p[4][0].Opinion = .2
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	o, _ := NewObserver(detailOptions(.7))
	first, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	h := holderAt(t, first, 4)
	if !h.O[0].Status.Active || h.O[1].Status.Active || h.P[0].Status.Active || !h.P[1].Status.Active || !h.MixedBilateral.Active || !h.Bilateral.Active || h.OpinionBilateral.Active || h.PostBilateral.Active {
		t.Fatalf("incorrect mixed-side classification: %+v", h)
	}
	x[4] = .2
	p[4][0].Opinion = -.2
	f.Step = 1
	second, err := o.Measure(1, x, g, p, f)
	second = pairOnly(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 1 || second.Events[0].Type != "conversion" || len(second.Episodes) != 0 {
		t.Fatalf("continuous role change created a loss/birth: %+v", second)
	}
	if second.Events[0].FromRole == second.Events[0].ToRole {
		t.Fatal("conversion lost branch identity")
	}
	closed, err := o.FinalClose(1)
	closed = pairOnly(closed)
	if err != nil {
		t.Fatal(err)
	}
	if len(closed.Episodes) != 1 || !closed.Episodes[0].RightCensored || closed.Summaries[0].Conversions != 1 || closed.Summaries[0].Losses != 0 {
		t.Fatalf("bad conversion ledger: %+v", closed)
	}
	if holderAt(t, first, 4).RoleBits != h.RoleBits {
		t.Fatal("old frame aliases observer workspace")
	}
}

func TestCrossBoundaryGateExcludesSameCoreAndAcceptsOppositeSource(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 1, 0)
	connectBridge(&g, 2, 0)
	result, err := Measure(0, x, g, p, f, detailOptions(1.3))
	if err != nil {
		t.Fatal(err)
	}
	h := holderAt(t, result, 0)
	if !h.S[0].Status.Active || !h.S[1].Status.Active || h.O[0].Status.Active || !h.O[1].Status.Active || !h.Active.Active || h.Bilateral.Active {
		t.Fatalf("same-side exposure became a cross-core bridge: %+v", h)
	}
	if !result.Pairs[0].Contact {
		t.Fatal("contact should be marked")
	}
}

func TestPostUsesOriginalAuthorAndLastRetainedRecord(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 1, 0)
	p[0] = []model.PostRecord[float64]{{AgentID: 0, Step: -1, Opinion: -.6}, {AgentID: 2, Step: 0, Opinion: -.1}}
	result, err := Measure(0, x, g, p, f, detailOptions(.7))
	if err != nil {
		t.Fatal(err)
	}
	h := holderAt(t, result, 0)
	if h.O[0].Status.Active || !h.P[0].Status.Active || !h.P[0].SourceKnown || h.P[1].Status.Active {
		t.Fatalf("post holder/author or latest-order confused: %+v", h)
	}
}

func TestUnknownPostSourceCensorsWithoutLossAndRestartsLeftCensored(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 1, 0)
	p[0] = []model.PostRecord[float64]{{AgentID: 2, Step: 0, Opinion: -.1}}
	o, _ := NewObserver(detailOptions(.7))
	first, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if !holderAt(t, first, 0).Active.Active {
		t.Fatal("expected initial bridge")
	}
	p[0][0].AgentID = -1
	f.Step = 1
	unknown, err := o.Measure(1, x, g, p, f)
	unknown = pairOnly(unknown)
	if err != nil {
		t.Fatal(err)
	}
	h := holderAt(t, unknown, 0)
	if h.Active.Known || h.P[0].Status.Known || h.P[0].CountKnown {
		t.Fatalf("unknown source treated as negative: %+v", h)
	}
	if unknown.Pairs[0].Counts.SideUnknownP[0] != 1 {
		t.Fatal("compact frame hid unknown P as zero")
	}
	if len(unknown.Events) != 0 || len(unknown.Episodes) != 1 || !unknown.Episodes[0].RightCensored || unknown.Episodes[0].Reason != "signal_unknown" {
		t.Fatalf("unknown state emitted physical loss: %+v", unknown)
	}
	p[0][0].AgentID = 2
	f.Step = 2
	restored, err := o.Measure(2, x, g, p, f)
	restored = pairOnly(restored)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Events) != 1 || restored.Events[0].Type != "initial_present" || !restored.Events[0].LeftCensored {
		t.Fatalf("unobserved interval became a regeneration: %+v", restored)
	}
}

func TestUnknownAuthorCanStillHaveKnownMiddleSignal(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	p[4][0] = model.PostRecord[float64]{AgentID: -1, Step: 0, Opinion: .1}
	result, err := Measure(0, x, g, p, f, detailOptions(.7))
	if err != nil {
		t.Fatal(err)
	}
	h := holderAt(t, result, 4)
	if !h.P[0].Status.Known || !h.P[0].Status.Active || h.P[0].SourceKnown {
		t.Fatalf("available middle signal unnecessarily became unknown: %+v", h)
	}
}

func TestMainFormationLossRegenerationAndEpisodeExposure(t *testing.T) {
	x, g, p, f := bridgeFixture()
	o, _ := NewObserver(DefaultOptions(.7))
	for step := 0; step < 4; step++ {
		if step == 1 || step == 3 {
			connectBridge(&g, 0, 4)
		}
		if step == 2 {
			delete(g.Adjacency[0], 4)
		}
		f.Step = step
		result, err := o.Measure(step, x, g, p, f)
		result = pairOnly(result)
		if err != nil {
			t.Fatal(err)
		}
		if step > 0 {
			expected := []string{"", "formation", "loss", "regeneration"}[step]
			if len(result.Events) != 1 || result.Events[0].Type != expected {
				t.Fatalf("step%d: %+v", step, result)
			}
		}
	}
	final, err := o.FinalClose(3)
	if err != nil {
		t.Fatal(err)
	}
	s := final.Summaries[0]
	if s.Formations != 1 || s.Losses != 1 || s.Regenerations != 1 || s.InitialPresent != 0 || s.ActiveHolderRounds != 1 {
		t.Fatalf("bad event/exposure summary: %+v", s)
	}
	again, _ := o.FinalClose(3)
	if len(again.Summaries) != 0 || len(again.Episodes) != 0 {
		t.Fatal("final summary duplicated")
	}
}

func TestGroupUnknownClosesEpochAndSameIDRestartsInitial(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	o, _ := NewObserver(DefaultOptions(.7))
	_, _ = o.Measure(0, x, g, p, f)
	f.Step = 1
	f.Groups[0].State = "unobserved"
	closed, err := o.Measure(1, x, g, p, f)
	closed = pairOnly(closed)
	if err != nil {
		t.Fatal(err)
	}
	if len(closed.Pairs) != 0 || len(closed.Events) != 0 || len(closed.Summaries) != 1 || closed.Summaries[0].Losses != 0 || closed.Summaries[0].ActiveHolderRounds != 1 {
		t.Fatalf("group unknown emitted bridge deaths: %+v", closed)
	}
	f.Step = 2
	f.Groups[0].State = "observed"
	reopened, err := o.Measure(2, x, g, p, f)
	reopened = pairOnly(reopened)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Events) != 1 || reopened.Events[0].Type != "initial_present" || reopened.Events[0].Epoch != 2 {
		t.Fatalf("same ID resumed as false regeneration: %+v", reopened)
	}
}

func TestSamplingGapDoesNotInterpolateOrCreateBirth(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	o, _ := NewObserver(DefaultOptions(.7))
	_, _ = o.Measure(0, x, g, p, f)
	f.Step = 5
	result, err := o.Measure(5, x, g, p, f)
	result = pairOnly(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Summaries) != 1 || result.Summaries[0].ActiveHolderRounds != 0 || result.Summaries[0].End != 0 || result.Events[0].Type != "initial_present" || result.Events[0].Epoch != 2 {
		t.Fatalf("sparse gap interpolated an event/exposure: %+v", result)
	}
}

func TestEpochFreezesMembersButInstantMeasurementUsesCurrentFrame(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	o, _ := NewObserver(detailOptions(.7))
	_, _ = o.Measure(0, x, g, p, f)
	f.Step = 1
	f.Groups[0].Core.Members = []int{0, 4}
	frozen, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	instant, err := Measure(1, x, g, p, f, detailOptions(.7))
	if err != nil {
		t.Fatal(err)
	}
	if !holderAt(t, frozen, 4).O[0].Status.Active || holderAt(t, instant, 4).O[0].Status.Active {
		t.Fatal("membership motion silently relabeled bridge reference")
	}
}

func TestExplicitContactStopDoesNotRepeatedlyReopenOrEmitLoss(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 2, 0)
	options := DefaultOptions(1.3)
	options.StopAtContact = true
	o, _ := NewObserver(options)
	first, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Summaries) != 1 || len(first.Events) != 0 {
		t.Fatal("contact should censor before qualification ledger")
	}
	f.Step = 1
	next, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Summaries) != 0 || len(next.Epochs) != 0 || len(next.Pairs) != 0 {
		t.Fatal("contact-closed pair repeatedly reopened")
	}
}

func TestGuardAndValidationAndOwnership(t *testing.T) {
	x, g, p, f := bridgeFixture()
	x[0] = -.7
	connectBridge(&g, 0, 4)
	options := detailOptions(.7)
	options.Guard = 1e-6
	result, err := Measure(0, x, g, p, f, options)
	if err != nil {
		t.Fatal(err)
	}
	if !holderAt(t, result, 4).O[0].Status.Uncertain {
		t.Fatal("inclusive epsilon boundary lost sensitivity flag")
	}
	bad := options
	bad.Guard = bad.Epsilon
	if _, err := NewObserver(bad); err == nil {
		t.Fatal("invalid options accepted")
	}
	o, _ := NewObserver(options)
	copyX := append([]float64(nil), x...)
	_, _ = o.Measure(0, x, g, p, f)
	if !reflect.DeepEqual(x, copyX) {
		t.Fatal("opinions mutated")
	}
	f.Step = 1
	x[0] = math.NaN()
	if _, err := o.Measure(1, x, g, p, f); err == nil {
		t.Fatal("nonfinite opinion accepted")
	}
	x[0] = -.7
	if _, err := o.Measure(1, x, g, p, f); err != nil {
		t.Fatalf("invalid input advanced the ledger: %v", err)
	}
}

func BenchmarkObserver500(b *testing.B) {
	n := 500
	x := make([]float64, n)
	g := utils.NetworkXGraph{Directed: true, Nodes: map[int64]map[string]any{}, Adjacency: map[int64]map[int64]any{}}
	p := map[int64][]model.PostRecord[float64]{}
	f := groups.Frame{Groups: []groups.Group{{ID: 1, State: "observed"}, {ID: 2, State: "observed"}}}
	for i := 0; i < n; i++ {
		x[i] = -.6
		if i >= 250 {
			x[i] = .6
		}
		if i%50 == 0 {
			x[i] = 0
		} else {
			side := 0
			if i >= 250 {
				side = 1
			}
			f.Groups[side].Core.Members = append(f.Groups[side].Core.Members, i)
		}
		g.Nodes[int64(i)] = map[string]any{}
		g.Adjacency[int64(i)] = map[int64]any{}
		p[int64(i)] = []model.PostRecord[float64]{{AgentID: int64(i), Step: 0, Opinion: x[i]}}
		for d := 1; d <= 15; d++ {
			g.Adjacency[int64(i)][int64((i+d*31)%n)] = nil
		}
	}
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
