package bridges

import (
	"smp/groups"
	"testing"
)

func TestUnionDeduplicatesHoldersAndReferenceChangeCensors(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	connectBridge(&g, 2, 4)
	f.Groups = append(f.Groups, groups.Group{ID: 30, State: "observed", Core: groups.Core{Members: []int{5}}})
	o, _ := NewObserver(detailOptions(.7))
	first, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, pair := range first.Pairs {
		sum += pair.Counts.Active
	}
	if sum < 2 || first.Union == nil || first.Union.Counts.Active != 1 || len(first.Union.Holders) != len(x) {
		t.Fatalf("union summed pair counts: pair sum%d union%+v", sum, first.Union)
	}
	if first.Union.Counts.Bilateral != 1 {
		t.Fatal("union lost same-pair bilateral identity")
	}
	f.Step = 1
	f.Groups = f.Groups[:2]
	second, err := o.Measure(1, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if second.Union == nil || second.Union.Epoch != 2 || second.Union.Counts.Active != 1 {
		t.Fatal("reference-set change failed to open a new union epoch")
	}
	initial := 0
	for _, event := range second.Events {
		if event.Scope == "union" {
			if event.Type != "initial_present" || !event.LeftCensored {
				t.Fatalf("pair disappearance manufactured union event: %+v", event)
			}
			initial++
		}
	}
	if initial != 1 {
		t.Fatal("union reopened without its initial stock")
	}
	for _, summary := range second.Summaries {
		if summary.Scope == "union" && (summary.Losses != 0 || summary.Regenerations != 0 || summary.ActiveHolderRounds != 1) {
			t.Fatalf("reference-set removal manufactured loss: %+v", summary)
		}
	}
	if first.Union.Counts.Active != 1 || first.Union.Holders[4].Active.Active != true {
		t.Fatal("union result aliases next frame")
	}
}

func TestUnionKnownActiveDominatesAnotherPairsUnknown(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 1, 0)
	connectBridge(&g, 2, 0)
	p[0][0].AgentID = -1
	p[0][0].Opinion = -.1
	f.Groups = append(f.Groups, groups.Group{ID: 30, State: "observed", Core: groups.Core{Members: []int{5}}})
	o, _ := NewObserver(detailOptions(1.3))
	result, err := o.Measure(0, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	h := result.Union.Holders[0]
	if !h.Active.Known || !h.Active.Active || h.Post.Known {
		t.Fatalf("union tri-state aggregation incorrect: %+v", h)
	}
	if result.Union.Counts.UnknownByCategory["post"] != 1 {
		t.Fatal("unknown post category silently became zero")
	}
}

func TestUnionLedgerSummaryEmittedOnceAndNoGapInterpolation(t *testing.T) {
	x, g, p, f := bridgeFixture()
	connectBridge(&g, 0, 4)
	o, _ := NewObserver(DefaultOptions(.7))
	_, _ = o.Measure(0, x, g, p, f)
	f.Step = 3
	result, err := o.Measure(3, x, g, p, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range result.Summaries {
		if summary.Scope == "union" && (summary.ActiveHolderRounds != 0 || summary.End != 0) {
			t.Fatal("union interpolated sparse gap")
		}
	}
	final, err := o.FinalClose(3, "analysis_horizon")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range final.Summaries {
		if s.Scope == "union" {
			n++
			if s.Reason != "analysis_horizon" {
				t.Fatal("union final reason dropped")
			}
		}
	}
	if n != 1 {
		t.Fatal("missing final union summary")
	}
	again, _ := o.FinalClose(3)
	if len(again.Summaries) != 0 {
		t.Fatal("union final summary repeated")
	}
}
