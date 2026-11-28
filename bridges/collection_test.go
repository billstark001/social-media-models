package bridges

import (
	"reflect"
	"smp/groups"
	"smp/model"
	"testing"
)

func TestPlotCollectionMatchesAuditAcrossUnknownGapsAndEpochChanges(t *testing.T) {
	x, g, posts, frame := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	full, _ := ResolveCollection(nil)
	full.Products = append(full.Products, "series")
	audit, err := NewObserverWithCollection(detailOptions(.7), &full)
	if err != nil {
		t.Fatal(err)
	}
	plot, err := NewObserverWithCollection(detailOptions(.7), &Collection{Profile: "plot"})
	if err != nil {
		t.Fatal(err)
	}
	var audits, plots []Frame
	for _, step := range []int{0, 1, 2, 3, 4, 5, 8, 9} {
		switch step {
		case 1:
			x[4] = .95
		case 2:
			delete(posts, 4)
		case 3:
			posts[4] = []model.PostRecord[float64]{{AgentID: 4, Step: 0, Opinion: .95}}
		case 4:
			x[4] = 0
			posts[4][0].Opinion = 0
		case 5:
			frame.Groups = append(frame.Groups, groups.Group{ID: 30, State: "observed", Core: groups.Core{Members: []int{5}}})
		case 9:
			frame.Groups = frame.Groups[:2]
		}
		frame.Step = step
		a, err := audit.Measure(step, x, g, posts, frame)
		if err != nil {
			t.Fatal(err)
		}
		p, err := plot.Measure(step, x, g, posts, frame)
		if err != nil {
			t.Fatal(err)
		}
		audits = append(audits, a)
		plots = append(plots, p)
		for _, track := range plot.pairs {
			if len(track.holders) != 0 || len(track.branches) != 0 {
				t.Fatal("union-only collector allocated independent pair histories")
			}
		}
		if plot.union != nil {
			for _, track := range append([]*pairTrack{plot.union}, plot.union.branches...) {
				for _, h := range track.holders {
					if h.open != nil {
						t.Fatal("plot collector constructed episode objects")
					}
				}
			}
		}
	}
	a, _ := audit.FinalClose(9)
	p, _ := plot.FinalClose(9)
	audits = append(audits, a)
	plots = append(plots, p)
	var expected, actual []StepStats
	var wantMain, gotMain []Summary
	var wantBranches, gotBranches []BranchSummary
	for i, a := range audits {
		p := plots[i]
		if len(p.Events)+len(p.Episodes)+len(p.BranchEvents)+len(p.BranchEpisodes) != 0 {
			t.Fatal("plot collector emitted entity records")
		}
		for _, s := range a.Series {
			if s.Scope == "union" {
				expected = append(expected, s)
			}
		}
		actual = append(actual, p.Series...)
		for _, s := range a.Summaries {
			if s.Scope == "union" {
				wantMain = append(wantMain, s)
			}
		}
		gotMain = append(gotMain, p.Summaries...)
		for _, s := range a.BranchSummaries {
			if s.Scope == "union" {
				wantBranches = append(wantBranches, s)
			}
		}
		gotBranches = append(gotBranches, p.BranchSummaries...)
	}
	if !reflect.DeepEqual(expected, actual) || !reflect.DeepEqual(wantMain, gotMain) || !reflect.DeepEqual(wantBranches, gotBranches) {
		t.Fatal("selective collection changed union observations")
	}
	// The aggregate stream independently preserves the event/episode oracle.
	type key struct {
		epoch   int
		channel string
	}
	totals := map[key]StepStats{}
	for _, s := range actual {
		k := key{s.Epoch, s.Channel}
		v := totals[k]
		v.InitialPresent += s.InitialPresent
		v.Formations += s.Formations
		v.Losses += s.Losses
		v.Regenerations += s.Regenerations
		v.Conversions += s.Conversions
		v.CensoredEpisodes += s.CensoredEpisodes
		v.ActiveHolderRounds += s.ActiveHolderRounds
		v.KnownHolderRounds += s.KnownHolderRounds
		v.NeverSeenHolderRounds += s.NeverSeenHolderRounds
		v.SeenInactiveHolderRounds += s.SeenInactiveHolderRounds
		v.UncertainFormations += s.UncertainFormations
		v.UncertainLosses += s.UncertainLosses
		v.UncertainRegenerations += s.UncertainRegenerations
		if s.Known+s.Unknown != len(x) || s.Active+s.NeverSeen+s.SeenInactive != s.Known || s.KnownHolderRounds+s.UnknownHolderRounds != int64(len(x))*int64(s.IntervalEnd-s.IntervalStart) {
			t.Fatalf("invalid aggregate risk partition: %+v", s)
		}
		totals[k] = v
	}
	for _, a := range audits {
		check := func(channel string, s Summary) {
			if s.Scope != "union" {
				return
			}
			v := totals[key{s.Epoch, channel}]
			if v.InitialPresent != s.InitialPresent || v.Formations != s.Formations || v.Losses != s.Losses || v.Regenerations != s.Regenerations || v.Conversions != s.Conversions || v.CensoredEpisodes != s.CensoredEpisodes || v.ActiveHolderRounds != s.ActiveHolderRounds || v.KnownHolderRounds != s.KnownHolderRounds || v.NeverSeenHolderRounds != s.NeverSeenHolderRounds || v.SeenInactiveHolderRounds != s.SeenInactiveHolderRounds {
				t.Fatalf("series lost lifecycle/exposure information: %+v vs %+v", v, s)
			}
		}
		for _, s := range a.Summaries {
			check("U", s)
		}
		for _, s := range a.BranchSummaries {
			check(channelCode(s.Channel), s.Summary)
		}
	}
	var unknown, closing, coincident bool
	for i, s := range actual {
		unknown = unknown || s.UnknownHolderRounds > 0
		closing = closing || s.Closing
		for _, other := range actual[i+1:] {
			if s.Channel == other.Channel && s.Step == other.Step && s.Epoch != other.Epoch && s.Closing != other.Closing {
				coincident = true
			}
		}
	}
	if !unknown || !closing || !coincident {
		t.Fatalf("fixture missed boundary cases: unknown=%v closing=%v coincident=%v", unknown, closing, coincident)
	}
}

func TestCollectionSelectsIndependentChannelAndOwnsInput(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	selection := Collection{Profile: "plot", Channels: []string{"O"}}
	o, err := NewObserverWithCollection(DefaultOptions(.7), &selection)
	if err != nil {
		t.Fatal(err)
	}
	selection.Channels[0] = "S"
	got, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.union.holders) != 0 || len(o.union.branches) != 1 || len(got.Series) != 1 || got.Series[0].Channel != "O" {
		t.Fatal("channel choice mutated or unnecessary histories retained")
	}
	if len(got.Events)+len(got.BranchEvents) != 0 {
		t.Fatal("entity records leaked")
	}
}

func TestCollectionRejectsUnknownDuplicateAndEmptyChoices(t *testing.T) {
	for _, c := range []Collection{{Profile: "unknown"}, {Channels: []string{}}, {Channels: []string{"U", "U"}}, {Scopes: []string{"global"}}, {Products: []string{"summaries", "typo"}}} {
		if _, err := ResolveCollection(&c); err == nil {
			t.Fatalf("invalid collection accepted: %+v", c)
		}
	}
}

func TestDetailsSelectionIsTheOnlyNewOutputSwitch(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	resolved, err := ResolveCollection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.IncludesProduct("details") {
		t.Fatal("default audit unexpectedly requested holder details")
	}
	o, err := NewObserverWithCollection(DefaultOptions(.7), &Collection{Profile: "plot", Products: []string{"details"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Union == nil || len(got.Union.Holders) != len(x) || len(got.Pairs) != 0 {
		t.Fatal("explicit details failed or leaked unrequested pair snapshots")
	}
	legacy, err := NewObserver(detailOptions(.7))
	if err != nil {
		t.Fatal(err)
	}
	got, err = legacy.Measure(0, x, g, p, f)
	if err != nil || got.Union == nil || len(got.Union.Holders) != len(x) {
		t.Fatal("legacy detail input was not translated into the new collector")
	}
}

func TestPairOnlyCollectionDoesNotBuildUnionWorkspace(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	o, err := NewObserverWithCollection(DefaultOptions(.7), &Collection{Profile: "plot", Scopes: []string{"pair"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if o.union != nil || len(o.unionStates) != 0 || len(got.Series) != 4 {
		t.Fatal("pair-only selection built a union or lost channels")
	}
	for _, e := range got.Epochs {
		if e.Scope != "pair" {
			t.Fatal("unrequested union epochs leaked")
		}
	}
}
