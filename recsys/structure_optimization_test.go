package recsys

import (
	"math/rand/v2"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/model"
)

func TestStructureUncachedWorkspaceLeavesCountsAndCachedRowsUntouched(t *testing.T) {
	m := structureWorkspaceModel(false, true)
	base := structureWorkspaceBase(m)
	for _, agent := range m.Schedule.Agents {
		m.GetRecommendation(agent, m.Grid.GetNeighbors(agent.ID, false))
	}
	before, err := decodeStructureDump(base.Dump())
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := referenceStructureRows(m.Graph, len(m.Schedule.Agents))
	for round := range 4 {
		for _, agent := range m.Schedule.Agents {
			base.RecommendAt(agent, -.3, map[int64]bool{agent.ID: true}, 3,
				rand.New(rand.NewPCG(uint64(round+1), uint64(agent.ID+1))))
			for id, count := range base.rawCounts {
				if count != 0 {
					t.Fatalf("probe left raw count %v at ID %d", count, id)
				}
			}
			if got := base.computeRawScores(agent.ID); !reflect.DeepEqual(got, wantCounts[agent.ID]) {
				t.Fatalf("probe changed subsequent raw counts at ID %d: got %v want %v", agent.ID, got, wantCounts[agent.ID])
			}
		}
	}
	after, err := decodeStructureDump(base.Dump())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("uncached candidate workspace aliased an immutable cached row")
	}
}

func TestStructureDenseQueryWorkspaceDoesNotChangeWeightedSampling(t *testing.T) {
	for _, cache := range []bool{false, true} {
		first := structureWorkspaceModel(true, cache)
		second := structureWorkspaceModel(true, cache)
		base := structureWorkspaceBase(first)
		for round := range 3 {
			for id, agent := range first.Schedule.Agents {
				// Exercise the ranked probe on the embedded Structure, then the
				// weighted wrapper that shares its two-hop count workspace.
				base.RecommendAt(agent, -.3, map[int64]bool{agent.ID: true}, 3,
					rand.New(rand.NewPCG(uint64(round+1), uint64(id+1))))
				var expected []*model.PostRecord[float64]
				var expectedRNG map[string][]byte
				for index, m := range []*model.SMPModel[float64, dynamics.HKParams]{first, second} {
					current := m.Schedule.Agents[id]
					posts := m.GetRecommendation(current, m.Grid.GetNeighbors(current.ID, false))
					rng, err := m.RNG.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					if index == 0 {
						expected, expectedRNG = posts, rng
					} else if !reflect.DeepEqual(expected, posts) || !reflect.DeepEqual(expectedRNG, rng) {
						t.Fatalf("cache=%t round=%d ID=%d: ranked scratch changed weighted posts/RNG", cache, round, id)
					}
				}
			}
		}
	}
}
