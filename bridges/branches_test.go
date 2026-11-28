package bridges

import (
	"encoding/json"
	"fmt"
	"reflect"
	"smp/groups"
	"smp/model"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func branchKey(scope string, ids [2]int64, epoch int, channel string, side *int) string {
	s := -1
	if side != nil {
		s = *side
	}
	return fmt.Sprintf("%s/%v/%d/%s/%d", scope, ids, epoch, channel, s)
}

func assertBranchConservation(t *testing.T, frames []Frame) {
	t.Helper()
	events := map[string]map[string]int{}
	episodes := map[string][]BranchEpisode{}
	summaries := map[string]bool{}
	for _, f := range frames {
		if f.BranchLedgerVersion != BranchLedgerVersion {
			t.Fatal("missing branch capability version")
		}
		for _, e := range f.BranchEvents {
			k := branchKey(e.Scope, e.GroupIDs, e.Epoch, e.Channel, e.Side)
			if events[k] == nil {
				events[k] = map[string]int{}
			}
			events[k][e.Type]++
		}
		for _, e := range f.BranchEpisodes {
			k := branchKey(e.Scope, e.GroupIDs, e.Epoch, e.Channel, e.Side)
			episodes[k] = append(episodes[k], e)
		}
	}
	for _, f := range frames {
		for _, s := range f.BranchSummaries {
			k := branchKey(s.Scope, s.GroupIDs, s.Epoch, s.Channel, s.Side)
			if summaries[k] {
				t.Fatalf("duplicate branch summary: %s", k)
			}
			summaries[k] = true
			ec := events[k]
			if ec["initial_present"] != s.InitialPresent || ec["formation"] != s.Formations || ec["loss"] != s.Losses || ec["regeneration"] != s.Regenerations || ec["conversion"] != s.Conversions {
				t.Fatalf("branch events do not match summary %s: %+v %+v", k, ec, s)
			}
			var integral int64
			censored := 0
			for _, e := range episodes[k] {
				if e.ObservationEnd < e.ObservedStart {
					t.Fatal("negative branch duration")
				}
				integral += int64(e.ObservationEnd - e.ObservedStart)
				if e.RightCensored {
					censored++
					if e.End != nil {
						t.Fatal("censored episode has an observed death")
					}
				} else if e.End == nil {
					t.Fatal("closed loss episode lacks end")
				}
			}
			window := int64(s.Population) * int64(s.End-s.Start)
			if s.InitialPresent+s.Formations+s.Regenerations != s.Losses+s.CensoredEpisodes || censored != s.CensoredEpisodes || integral != s.ActiveHolderRounds {
				t.Fatalf("branch episode conservation failed %s: %+v integral=%d censored=%d", k, s, integral, censored)
			}
			if s.KnownHolderRounds+s.UnknownHolderRounds != window || s.ActiveHolderRounds+s.NeverSeenHolderRounds+s.SeenInactiveHolderRounds != s.KnownHolderRounds || s.UnknownHolderRounds < 0 {
				t.Fatalf("branch risk partition failed: %+v", s)
			}
			if s.Scope == "union" && s.Side != nil {
				t.Fatal("union invented global sides")
			}
			if s.Channel == ChannelStructural && s.UnknownHolderRounds != 0 {
				t.Fatal("physical support became unknown on a validated graph")
			}
			for _, main := range f.Summaries {
				if main.Scope != s.Scope || main.GroupIDs != s.GroupIDs || main.Epoch != s.Epoch {
					continue
				}
				if s.ActiveHolderRounds > main.ActiveHolderRounds && s.Channel != ChannelStructural {
					t.Fatalf("qualified channel exposure exceeds U: %+v / %+v", s, main)
				}
				if s.Side == nil && s.ActiveHolderRounds < main.ActiveHolderRounds && s.Channel == ChannelStructural {
					t.Fatalf("physical support exposure is below U: %+v / %+v", s, main)
				}
			}
		}
	}
}

func findBranchSummary(t *testing.T, frames []Frame, scope, channel string, side int, epoch int) BranchSummary {
	t.Helper()
	for _, f := range frames {
		for _, s := range f.BranchSummaries {
			v := -1
			if s.Side != nil {
				v = *s.Side
			}
			if s.Scope == scope && s.Channel == channel && v == side && s.Epoch == epoch {
				return s
			}
		}
	}
	t.Fatalf("missing %s/%s side=%d epoch=%d", scope, channel, side, epoch)
	return BranchSummary{}
}

func channelTransitionHistory(t *testing.T, sides bool) []Frame {
	t.Helper()
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	x[4] = -.2
	p[4][0] = model.PostRecord[float64]{AgentID: 4, Step: 0, Opinion: .95}
	options := detailOptions(.7)
	options.BranchSides = sides
	o, _ := NewObserver(options)
	frames := []Frame{}
	for step := 0; step < 4; step++ {
		if step == 1 {
			p[4][0].Opinion = .2
		}
		if step == 2 {
			x[4] = .95
		}
		if step == 3 {
			x[4] = -.2
		}
		f.Step = step
		got, err := o.Measure(step, x, g, p, f)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, got)
	}
	end, err := o.FinalClose(3, "analysis_horizon")
	if err != nil {
		t.Fatal(err)
	}
	return append(frames, end)
}

func TestIndependentChannelEpisodesAcrossSignalTypeTransitions(t *testing.T) {
	frames := channelTransitionHistory(t, false)
	assertBranchConservation(t, frames)
	for _, scope := range []string{"pair", "union"} {
		o := findBranchSummary(t, frames, scope, ChannelOpinion, -1, 1)
		p := findBranchSummary(t, frames, scope, ChannelPost, -1, 1)
		s := findBranchSummary(t, frames, scope, ChannelStructural, -1, 1)
		if o.InitialPresent != 1 || o.Formations != 0 || o.Losses != 1 || o.Regenerations != 1 || o.Conversions != 0 || o.ActiveHolderRounds != 2 || o.SeenInactiveHolderRounds != 1 {
			t.Fatalf("bad O-only→both→P-only→both ledger: %+v", o)
		}
		if p.InitialPresent != 0 || p.Formations != 1 || p.Losses != 0 || p.Regenerations != 0 || p.Conversions != 0 || p.ActiveHolderRounds != 2 {
			t.Fatalf("P formation was hidden by existing U: %+v", p)
		}
		if s.InitialPresent != 1 || s.Formations+s.Losses+s.Regenerations+s.Conversions != 0 || s.ActiveHolderRounds != 3 {
			t.Fatalf("S was confused with structural_only: %+v", s)
		}
	}
	final := frames[len(frames)-1]
	for _, s := range final.Summaries {
		if s.InitialPresent != 1 || s.Losses+s.Regenerations+s.Formations != 0 || s.ActiveHolderRounds != 3 {
			t.Fatalf("channels interrupted main U episode: %+v", s)
		}
	}
	for _, f := range frames {
		for _, e := range f.BranchEvents {
			if e.Side != nil {
				t.Fatal("default tracking unexpectedly created side ledgers")
			}
		}
	}
}

func TestUnknownPostHasIndependentCensorAndObservedSeenRisk(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	o, _ := NewObserver(detailOptions(.7))
	frames := []Frame{}
	for step := 10; step <= 14; step++ {
		if step == 11 {
			delete(p, 4)
		}
		if step == 13 {
			p[4] = []model.PostRecord[float64]{{AgentID: 4, Step: 0, Opinion: .95}}
		}
		if step == 14 {
			p[4][0].Opinion = 0
		}
		f.Step = step
		got, err := o.Measure(step, x, g, p, f)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, got)
	}
	f.Step = 17
	got, err := o.Measure(17, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	frames = append(frames, got)
	end, err := o.FinalClose(19, "analysis_horizon")
	if err != nil {
		t.Fatal(err)
	}
	frames = append(frames, end)
	assertBranchConservation(t, frames)
	for _, scope := range []string{"pair", "union"} {
		p := findBranchSummary(t, frames, scope, ChannelPost, -1, 1)
		op := findBranchSummary(t, frames, scope, ChannelOpinion, -1, 1)
		if p.Start != 10 || p.End != 14 || p.InitialPresent != 1 || p.Regenerations != 1 || p.Losses != 0 || p.CensoredEpisodes != 2 || p.UnknownHolderRounds != 2 || p.KnownHolderRounds != 22 || p.ActiveHolderRounds != 1 || p.SeenInactiveHolderRounds != 1 {
			t.Fatalf("bad independent P unknown/seen exposure: %+v", p)
		}
		if op.ActiveHolderRounds != 4 || op.UnknownHolderRounds != 0 || op.CensoredEpisodes != 1 || op.Losses != 0 {
			t.Fatalf("P unknown censored O: %+v", op)
		}
	}
	for _, f := range frames {
		for _, e := range f.Events {
			if e.Type == "loss" || e.Type == "regeneration" {
				t.Fatalf("P unknown changed main U lifecycle: %+v", e)
			}
		}
	}
}

func TestOwnChannelDirectionConversionAndOptionalSideLifecycles(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	x[4] = -.2
	p[4][0].Opinion = .95
	opts := detailOptions(.7)
	opts.BranchSides = true
	o, _ := NewObserver(opts)
	first, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	x[4] = .2
	f.Step = 1
	second, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	end, err := o.FinalClose(2)
	if err != nil {
		t.Fatal(err)
	}
	frames := []Frame{first, second, end}
	assertBranchConservation(t, frames)
	any := findBranchSummary(t, frames, "pair", ChannelOpinion, -1, 1)
	left := findBranchSummary(t, frames, "pair", ChannelOpinion, 0, 1)
	right := findBranchSummary(t, frames, "pair", ChannelOpinion, 1, 1)
	if any.Conversions != 1 || any.Losses+any.Formations+any.Regenerations != 0 || left.Losses != 1 || right.Formations != 1 {
		t.Fatalf("wrong any/side history: any=%+v left=%+v right=%+v", any, left, right)
	}
	for _, e := range second.BranchEvents {
		if e.Channel == ChannelStructural || e.Channel == ChannelPost {
			t.Fatalf("another channel's role change caused branch event: %+v", e)
		}
		if e.Scope == "pair" && e.Side == nil && (e.Type != "conversion" || e.FromRole != 1 || e.ToRole != 2 || e.FromKnownBits != 3 || e.ToKnownBits != 3) {
			t.Fatalf("branch mask included other channels: %+v", e)
		}
	}
	union := findBranchSummary(t, frames, "union", ChannelOpinion, -1, 1)
	if union.Conversions != 0 {
		t.Fatal("union invented an L/R conversion")
	}
	if len(end.BranchSummaries) != 12 {
		t.Fatalf("expected pair 9 + union 3 summaries, got %d", len(end.BranchSummaries))
	}
}

func TestUnknownOriginalAuthorCensorsOnlyPostChannel(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	p[4][0] = model.PostRecord[float64]{AgentID: 2, Step: 0, Opinion: .4}
	o, _ := NewObserver(detailOptions(.7))
	frames := []Frame{}
	for step := 0; step < 3; step++ {
		if step == 1 {
			p[4][0].AgentID = -1
			p[4][0].Opinion = .8
		}
		if step == 2 {
			p[4][0].AgentID = 0
		}
		f.Step = step
		got, err := o.Measure(step, x, g, p, f)
		if err != nil {
			t.Fatal(err)
		}
		if step == 1 {
			h := holderAt(t, got, 4)
			if h.P[1].Status.Known || !h.P[0].Status.Known || !h.Active.Known || !h.Active.Active {
				t.Fatal("fixture did not isolate source-unknown P with stable O")
			}
		}
		frames = append(frames, got)
	}
	end, err := o.FinalClose(2)
	if err != nil {
		t.Fatal(err)
	}
	frames = append(frames, end)
	assertBranchConservation(t, frames)
	post := findBranchSummary(t, frames, "pair", ChannelPost, -1, 1)
	if post.InitialPresent != 2 || post.Losses+post.Regenerations+post.Formations != 0 || post.UnknownHolderRounds != 1 || post.ActiveHolderRounds != 1 {
		t.Fatalf("unknown author inferred a post loss/regeneration: %+v", post)
	}
	for _, f := range frames {
		for _, e := range f.Events {
			if e.Type == "loss" || e.Type == "regeneration" {
				t.Fatal("source-unknown P interrupted U")
			}
		}
	}
}

func TestChannelUnionDeduplicatesAndReferenceChangesCensor(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	f.Groups = append(f.Groups, groups.Group{ID: 30, State: "observed", Core: groups.Core{Members: []int{5}}})
	opts := detailOptions(.7)
	opts.BranchSides = true
	o, _ := NewObserver(opts)
	first, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, e := range first.BranchEvents {
		if e.Type == "initial_present" && e.Side == nil {
			counts[e.Scope+"/"+e.Channel]++
		}
	}
	if counts["union/opinion"] != 1 || counts["union/post"] != 1 || counts["union/structural"] != 1 || counts["pair/opinion"] < 2 || counts["pair/structural"] != 3 {
		t.Fatalf("union summed pair-holder incidences: %+v", counts)
	}
	f.Step = 1
	f.Groups[0].State = "unobserved"
	second, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range second.BranchEvents {
		if e.Type == "loss" || e.Type == "regeneration" {
			t.Fatalf("reference removal manufactured channel event: %+v", e)
		}
	}
	end, err := o.FinalClose(1)
	if err != nil {
		t.Fatal(err)
	}
	assertBranchConservation(t, []Frame{first, second, end})
}

func TestStructuralLedgerTracksPhysicalSupportWhileUInactive(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	x[4] = .95
	p[4][0].Opinion = .95
	o, _ := NewObserver(DefaultOptions(.7))
	first, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 0 {
		t.Fatal("expected no initial main U")
	}
	f.Step = 1
	p[4][0] = model.PostRecord[float64]{AgentID: 2, Step: 0, Opinion: -.2}
	second, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	end, err := o.FinalClose(1)
	if err != nil {
		t.Fatal(err)
	}
	frames := []Frame{first, second, end}
	assertBranchConservation(t, frames)
	s := findBranchSummary(t, frames, "pair", ChannelStructural, -1, 1)
	post := findBranchSummary(t, frames, "pair", ChannelPost, -1, 1)
	if s.InitialPresent != 1 || s.ActiveHolderRounds != 1 || s.Conversions != 0 || post.Formations != 1 {
		t.Fatalf("S was mistaken for U or structural_only: S=%+v P=%+v", s, post)
	}
}

func TestBranchClosePoliciesMatchTheirMainEpoch(t *testing.T) {
	for _, policy := range []string{"unknown", "epoch", "gap", "contact"} {
		t.Run(policy, func(t *testing.T) {
			x, g, p, f := bridgeFixture()
			connectBridge(&g, 0, 4)
			opts := DefaultOptions(.7)
			opts.StopAtContact = policy == "contact"
			o, _ := NewObserver(opts)
			first, err := o.Measure(0, x, g, p, f)
			if err != nil {
				t.Fatal(err)
			}
			f.Step = 1
			switch policy {
			case "unknown":
				f.Groups[0].State = "unobserved"
			case "epoch":
				f.Groups[0].Epoch++
			case "gap":
				f.Step = 3
			case "contact":
				x[2] = -.6
				x[3] = -.6
			}
			second, err := o.Measure(f.Step, x, g, p, f)
			if err != nil {
				t.Fatal(err)
			}
			end, err := o.FinalClose(f.Step, "analysis_horizon")
			if err != nil {
				t.Fatal(err)
			}
			frames := []Frame{first, second, end}
			assertBranchConservation(t, frames)
			for _, frame := range frames {
				for _, branch := range frame.BranchSummaries {
					matched := false
					for _, main := range frame.Summaries {
						if branch.Scope == main.Scope && branch.GroupIDs == main.GroupIDs && branch.Epoch == main.Epoch {
							matched = true
							if branch.Start != main.Start || branch.End != main.End || branch.Reason != main.Reason {
								t.Fatalf("branch epoch differs from main: %+v / %+v", branch, main)
							}
						}
					}
					if !matched {
						t.Fatal("branch summary repeated or invented an epoch")
					}
				}
			}
		})
	}
}

func TestBranchReturnedSideAndEndpointOwnership(t *testing.T) {
	frames := channelTransitionHistory(t, true)
	var prior *BranchEvent
	for i := range frames[0].BranchEvents {
		e := &frames[0].BranchEvents[i]
		if e.Scope == "pair" && e.Channel == ChannelOpinion && e.Side != nil && *e.Side == 0 {
			prior = e
			break
		}
	}
	if prior == nil {
		t.Fatal("missing side-0 initial record")
	}
	*prior.Side = 99
	for i, event := range frames[0].BranchEvents {
		if &frames[0].BranchEvents[i] != prior && event.Side != nil && *event.Side == 99 {
			t.Fatal("two records in one frame shared Side storage")
		}
	}
	for _, f := range frames[1:] {
		for _, e := range f.BranchEvents {
			if e.Side != nil && (*e.Side < 0 || *e.Side > 1) {
				t.Fatal("returned Side mutated a future event")
			}
		}
		for _, s := range f.BranchSummaries {
			if s.Side != nil && (*s.Side < 0 || *s.Side > 1) {
				t.Fatal("returned Side mutated a descriptor")
			}
		}
	}
	var closed *BranchEpisode
	for i := range frames[2].BranchEpisodes {
		e := &frames[2].BranchEpisodes[i]
		if e.Scope == "pair" && e.Channel == ChannelOpinion && e.Side != nil && *e.Side == 0 {
			closed = e
			break
		}
	}
	if closed == nil || closed.End == nil {
		t.Fatal("missing side loss episode")
	}
	*closed.Side = 98
	*closed.End = 100
	for _, event := range frames[2].BranchEvents {
		if event.Side != nil && *event.Side == 98 {
			t.Fatal("episode/event shared Side pointer")
		}
	}
	for _, episode := range frames[4].BranchEpisodes {
		if episode.Channel == ChannelOpinion && episode.Side != nil && *episode.Side == 0 {
			if episode.Start == nil || *episode.Start != 3 || episode.ObservationEnd != 3 {
				t.Fatal("closed endpoint mutation affected a later episode")
			}
		}
	}
}

func TestBranchWireFieldsAreFlatAndSideZeroIsExplicit(t *testing.T) {
	zero := 0
	value := BranchSummary{Summary: Summary{Scope: "pair", Epoch: 2, KnownHolderRounds: 4}, Channel: ChannelPost, Side: &zero, Population: 6, UnknownHolderRounds: 2}
	for _, wire := range []string{"json", "msgpack"} {
		var data []byte
		var err error
		var fields map[string]any
		if wire == "json" {
			data, err = json.Marshal(value)
			if err == nil {
				err = json.Unmarshal(data, &fields)
			}
		} else {
			data, err = msgpack.Marshal(value)
			if err == nil {
				err = msgpack.Unmarshal(data, &fields)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if fields["scope"] != "pair" || fields["channel"] != "post" || fields["side"] == nil || fields["population"] == nil || fields["unknown_holder_rounds"] == nil {
			t.Fatalf("non-flat or missing fields in %s: %+v", wire, fields)
		}
		if _, ok := fields["Summary"]; ok {
			t.Fatal("embedded summary was nested on wire")
		}
	}
	value.Side = nil
	data, _ := json.Marshal(value)
	var fields map[string]any
	json.Unmarshal(data, &fields)
	if _, ok := fields["side"]; ok {
		t.Fatal("any-side should omit side")
	}
}

func TestBranchSidesDoNotChangeExistingUOrAnyChannelRecords(t *testing.T) {
	without, with := channelTransitionHistory(t, false), channelTransitionHistory(t, true)
	for i := range without {
		a, b := without[i], with[i]
		if !reflect.DeepEqual(a.Events, b.Events) || !reflect.DeepEqual(a.Episodes, b.Episodes) || !reflect.DeepEqual(a.Summaries, b.Summaries) || !reflect.DeepEqual(a.Epochs, b.Epochs) {
			t.Fatal("side ledgers changed U")
		}
		anyEvents := []BranchEvent{}
		for _, e := range b.BranchEvents {
			if e.Side == nil {
				anyEvents = append(anyEvents, e)
			}
		}
		if len(a.BranchEvents) != len(anyEvents) {
			t.Fatal("side ledgers changed any-channel events")
		}
		for j := range a.BranchEvents {
			if !reflect.DeepEqual(a.BranchEvents[j], anyEvents[j]) {
				t.Fatal("side ledgers altered any-channel event")
			}
		}
	}
}

func BenchmarkObserverBranchSides500(b *testing.B) {
	x, g, p, f := auditBenchmarkFixture()
	opts := DefaultOptions(.7)
	opts.BranchSides = true
	o, _ := NewObserver(opts)
	b.ReportAllocs()
	b.ResetTimer()
	for step := 0; step < b.N; step++ {
		f.Step = step
		if _, err := o.Measure(step, x, g, p, f); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObserverChannelEpoch500(b *testing.B) {
	x, g, p, f := auditBenchmarkFixture()
	for _, sides := range []bool{false, true} {
		name := "Any"
		if sides {
			name = "Sides"
		}
		b.Run(name, func(b *testing.B) {
			opts := DefaultOptions(.7)
			opts.BranchSides = sides
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				o, _ := NewObserver(opts)
				if _, err := o.Measure(0, x, g, p, f); err != nil {
					b.Fatal(err)
				}
				if _, err := o.FinalClose(0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
