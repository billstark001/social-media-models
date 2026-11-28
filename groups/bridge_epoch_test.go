package groups_test

import (
	"testing"

	"smp/bridges"
	"smp/groups"
	"smp/model"
	"smp/utils"
)

func TestObservedGroupRecoveryAndSamplingGapCensorBridgeEpochs(t *testing.T) {
	for _, gap := range []bool{false, true} {
		tracker, err := groups.NewTracker(groups.DefaultOptions(.65))
		if err != nil {
			t.Fatal(err)
		}
		observer, err := bridges.NewObserver(bridges.DefaultOptions(.65))
		if err != nil {
			t.Fatal(err)
		}
		x := make([]float64, 101)
		for i := 0; i < 100; i++ {
			if i < 50 {
				x[i] = -.6
			} else {
				x[i] = .6
			}
		}
		graph := utils.NetworkXGraph{Directed: true, Nodes: make(map[int64]map[string]any), Adjacency: make(map[int64]map[int64]any)}
		for i := int64(0); i < 101; i++ {
			graph.Nodes[i] = map[string]any{}
			graph.Adjacency[i] = make(map[int64]any)
			if i < 100 {
				graph.Adjacency[i][100] = nil
			}
		}
		graph.Adjacency[100][0] = nil
		observe := func(step int, opinions []float64) (groups.Frame, bridges.Frame) {
			t.Helper()
			gf, err := tracker.Observe(step, opinions)
			if err != nil {
				t.Fatal(err)
			}
			posts := make(map[int64][]model.PostRecord[float64])
			for i, v := range opinions {
				origin := step
				if step == 0 {
					origin = -1
				}
				posts[int64(i)] = []model.PostRecord[float64]{{AgentID: int64(i), Step: origin, Opinion: v}}
			}
			bf, err := observer.Measure(step, opinions, graph, posts, gf)
			if err != nil {
				t.Fatal(err)
			}
			return gf, bf
		}
		for step := 0; step < 2; step++ {
			observe(step, x)
		}
		initialGroups, initial := observe(2, x)
		if len(initial.Pairs) != 1 || initial.Pairs[0].Counts.Active != 1 {
			t.Fatal("fixture did not form the initial bridge epoch")
		}
		var recoveredGroups groups.Frame
		var recovered, closure bridges.Frame
		if gap {
			recoveredGroups, recovered = observe(7, x)
			closure = recovered
		} else {
			merged := append([]float64(nil), x...)
			for i := 50; i < 100; i++ {
				merged[i] = -.6
			}
			_, closure = observe(3, merged)
			recoveredGroups, recovered = observe(4, x)
		}
		if len(recoveredGroups.Groups) != 2 || recoveredGroups.Groups[0].ID != initialGroups.Groups[0].ID || recoveredGroups.Groups[1].ID != initialGroups.Groups[1].ID {
			t.Fatal("temporary ambiguity/gap recycled persistent group IDs")
		}
		closed := 0
		for _, epoch := range closure.Epochs {
			if epoch.Scope == "pair" && epoch.State == "closed" {
				closed++
				if gap && (epoch.End != 2 || epoch.ClosedAt != 7 || epoch.Reason != "sampling_gap") {
					t.Fatalf("gap extrapolated the epoch: %+v", epoch)
				}
			}
		}
		if closed != 1 || len(recovered.Pairs) != 1 || recovered.Pairs[0].Epoch != 2 {
			t.Fatal("same group-ID recovery failed to close and replace bridge epoch")
		}
		for _, event := range append(closure.Events, recovered.Events...) {
			if event.Type == "loss" || event.Type == "regeneration" {
				t.Fatalf("censoring invented a holder transition: %+v", event)
			}
		}
		initialPresent := false
		for _, event := range recovered.Events {
			if event.Scope == "pair" && event.Holder == 100 && event.Type == "initial_present" && event.LeftCensored {
				initialPresent = true
			}
		}
		if !initialPresent {
			t.Fatal("recovered bridge did not reopen as left-censored initial stock")
		}
	}
}
