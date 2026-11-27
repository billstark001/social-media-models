package recsys

import (
	"fmt"
	"reflect"
	"testing"

	"gonum.org/v1/gonum/graph/simple"
	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
)

func structureWorkspaceModel(random, cache bool) *model.SMPModel[float64, dynamics.HKParams] {
	graph := simple.NewDirectedGraph()
	opinions := make([]float64, 8)
	for id := range opinions {
		graph.AddNode(simple.Node(id))
		opinions[id] = float64(id) / 10
	}
	for _, edge := range [][2]int64{{0, 1}, {1, 0}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 0}, {6, 2}} {
		graph.SetEdge(graph.NewEdge(graph.Node(edge[0]), graph.Node(edge[1])))
	}
	params := &model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: model.SMPModelPureParams{RecsysCount: 3, PostRetainCount: 3},
		RecsysFactory: func(m *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
			if random {
				return NewStructureRandom(m, nil, 3, 0, .2, cache, nil)
			}
			return NewStructure(m, 0, nil, cache, nil)
		},
	}
	m := model.NewSMPModel(graph, &opinions, params, &dynamics.HKParams{}, &dynamics.HK{},
		&model.CollectItemOptions{}, nil, smprng.MustNewPool(smprng.FixedSpec(61, 97)))
	m.CurStep = 3
	for id := range opinions {
		for step := 0; step < 3; step++ {
			m.Grid.AddPost(int64(id), &model.PostRecord[float64]{AgentID: int64(id), Step: step, Opinion: float64(id)/10 + float64(step)/100}, 3)
		}
	}
	m.SetAgentCurPosts()
	m.Recsys.PostInit(nil)
	return m
}

func structureWorkspaceBase(m *model.SMPModel[float64, dynamics.HKParams]) *Structure[float64, dynamics.HKParams] {
	if random, ok := m.Recsys.(*StructureRandom[float64, dynamics.HKParams]); ok {
		return &random.Structure
	}
	return m.Recsys.(*Structure[float64, dynamics.HKParams])
}

// Count intersections directly from the adjacency relation, independently of
// the optimized traversal, epoch marks, scratch buffers, and caches.
func referenceStructureRows(graph *simple.DirectedGraph, n int) []map[int64]float64 {
	linked := func(a, b int64) bool { return graph.HasEdgeFromTo(a, b) || graph.HasEdgeFromTo(b, a) }
	rows := make([]map[int64]float64, n)
	for anchor := range rows {
		rows[anchor] = make(map[int64]float64)
		for candidate := range n {
			if candidate == anchor {
				continue
			}
			for common := range n {
				if linked(int64(anchor), int64(common)) && linked(int64(candidate), int64(common)) {
					rows[anchor][int64(candidate)]++
				}
			}
		}
	}
	return rows
}

func TestStructureWorkspaceCacheAndCheckpointAfterRewirings(t *testing.T) {
	for _, random := range []bool{false, true} {
		t.Run(fmt.Sprintf("weighted-%t", random), func(t *testing.T) {
			cached := structureWorkspaceModel(random, true)
			models := []*model.SMPModel[float64, dynamics.HKParams]{cached, structureWorkspaceModel(random, false)}
			changes := []model.RewiringEventBody{
				{AgentID: 0, Unfollow: 1, Follow: 6}, {AgentID: 1, Unfollow: 0, Follow: 7},
				{AgentID: 2, Unfollow: 3, Follow: 5}, {AgentID: 5, Unfollow: 0, Follow: 6},
				{AgentID: 6, Unfollow: 2, Follow: 0}, {AgentID: 0, Unfollow: 6, Follow: 1},
			}
			for phase := 0; phase <= len(changes); phase++ {
				if phase > 0 {
					change := &changes[phase-1]
					for _, m := range models {
						m.Graph.RemoveEdge(change.AgentID, change.Unfollow)
						m.Graph.SetEdge(m.Graph.NewEdge(m.Graph.Node(change.AgentID), m.Graph.Node(change.Follow)))
						m.Recsys.PostStep([]*model.RewiringEventBody{change})
					}
				}
				want := referenceStructureRows(cached.Graph, len(cached.Schedule.Agents))
				for round := 0; round < 2; round++ {
					for anchor := range want {
						var expectedPosts []*model.PostRecord[float64]
						var expectedRNG map[string][]byte
						for index, m := range models {
							agent := m.Schedule.Agents[anchor]
							posts := m.GetRecommendation(agent, m.Grid.GetNeighbors(agent.ID, false))
							rngState, err := m.RNG.Snapshot()
							if err != nil {
								t.Fatal(err)
							}
							if index == 0 {
								expectedPosts, expectedRNG = posts, rngState
							} else if !reflect.DeepEqual(posts, expectedPosts) || !reflect.DeepEqual(rngState, expectedRNG) {
								t.Fatalf("phase=%d round=%d anchor=%d model=%d: cache/restore changed posts or RNG", phase, round, anchor, index)
							}
						}
						if got := structureWorkspaceBase(cached).rawScoreCache[int64(anchor)]; !reflect.DeepEqual(got, want[anchor]) {
							t.Fatalf("phase=%d anchor=%d: stale cached scores %v, want %v", phase, anchor, got, want[anchor])
						}
					}
				}
				if phase == 2 {
					dump := cached.Dump()
					models = append(models, dump.Load(cached.ModelParams, cached.AgentParams, cached.Dynamics,
						cached.CollectItems, nil, smprng.MustNewPool(smprng.FixedSpec(61, 97))))
				}
				base := structureWorkspaceBase(cached)
				base.neighborEpoch, base.candidateEpoch = ^uint32(0), ^uint32(0)
				for anchor, row := range want {
					if got := base.computeRawScores(int64(anchor)); !reflect.DeepEqual(got, row) {
						t.Fatalf("phase=%d anchor=%d: scratch/epoch rollover changed scores: %v, want %v", phase, anchor, got, row)
					}
				}
			}
		})
	}
}
