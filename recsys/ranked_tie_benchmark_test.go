package recsys_test

import (
	"fmt"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
)

// These cases separate the usual continuous-score path from states where
// different authors genuinely tie. A frozen copy is also run against the
// pre-fix source for the one-time performance comparison.
type rankedTieBenchmarkCase struct {
	policy        string
	history       int
	equalOpinions bool
	noise         float64
	cache         bool
}

func rankedTieBenchmarkModel(c rankedTieBenchmarkCase) *model.SMPModel[float64, dynamics.HKParams] {
	const n = 500
	pool := smprng.MustNewPool(smprng.FixedSpec(0xabc, 0xdef))
	graph := utils.CreateRandomNetwork(n, 15.0/(n-1), pool.Stream(smprng.StreamNetwork))
	opinions := make([]float64, n)
	for id := range opinions {
		if !c.equalOpinions {
			opinions[id] = -0.9 + 1.8*float64(id)/n
		}
	}
	factories := simulation.GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](map[string]any{
		"NoiseStd": c.noise, "UseCache": c.cache,
	})
	params := &model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: model.SMPModelPureParams{RecsysCount: 10, PostRetainCount: c.history},
		RecsysFactory:      factories[c.policy],
	}
	m := model.NewSMPModelFloat64(graph, &opinions, params, dynamics.DefaultHKParams(), &dynamics.HK{}, &model.CollectItemOptions{}, nil, pool)
	m.CurStep = 6
	for id, opinion := range opinions {
		for step := range c.history {
			x := opinion
			if !c.equalOpinions {
				x += float64(step+1) * 1e-5
			}
			m.Grid.AddPost(int64(id), &model.PostRecord[float64]{AgentID: int64(id), Step: step, Opinion: x}, c.history)
		}
	}
	m.SetAgentCurPosts()
	m.Recsys.PostInit(nil)
	m.Recsys.PreStep()
	return m
}

func BenchmarkRankedTie(b *testing.B) {
	for _, history := range []int{0, 6} {
		for _, equal := range []bool{false, true} {
			c := rankedTieBenchmarkCase{policy: "Opinion", history: history, equalOpinions: equal, noise: .1}
			name := fmt.Sprintf("Opinion/kh%d/equal=%t", history, equal)
			b.Run(name+"/Query", func(b *testing.B) {
				m := rankedTieBenchmarkModel(c)
				agent := m.Schedule.Agents[0]
				neighbors := m.Grid.GetNeighbors(agent.ID, false)
				benchmarkPosts = m.GetRecommendation(agent, neighbors)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					benchmarkPosts = m.GetRecommendation(agent, neighbors)
				}
			})
			b.Run(name+"/PreStep", func(b *testing.B) {
				m := rankedTieBenchmarkModel(c)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					m.Recsys.PreStep()
				}
			})
		}
	}
	for _, cache := range []bool{true, false} {
		for _, noise := range []float64{.1, 0} {
			c := rankedTieBenchmarkCase{policy: "Structure", noise: noise, cache: cache}
			b.Run(fmt.Sprintf("Structure/cache=%t/noise=%g/Query", cache, noise), func(b *testing.B) {
				m := rankedTieBenchmarkModel(c)
				agent := m.Schedule.Agents[0]
				neighbors := m.Grid.GetNeighbors(agent.ID, false)
				benchmarkPosts = m.GetRecommendation(agent, neighbors)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					benchmarkPosts = m.GetRecommendation(agent, neighbors)
				}
			})
		}
	}
}

// BenchmarkRankedRound measures one recommender preparation plus a query for
// every anchor. Neighbors are prefetched; no physical dynamics or I/O runs.
func BenchmarkRankedRound(b *testing.B) {
	cases := []rankedTieBenchmarkCase{
		{policy: "Opinion", history: 6, equalOpinions: false, noise: .1},
		{policy: "Opinion", history: 6, equalOpinions: true, noise: .1},
		{policy: "Structure", noise: .1, cache: false},
		{policy: "Structure", noise: 0, cache: false},
	}
	for _, c := range cases {
		name := fmt.Sprintf("%s/kh%d/equal=%t/noise=%g", c.policy, c.history, c.equalOpinions, c.noise)
		b.Run(name, func(b *testing.B) {
			m := rankedTieBenchmarkModel(c)
			neighbors := make([][]*model.SMPAgent[float64, dynamics.HKParams], len(m.Schedule.Agents))
			for i, agent := range m.Schedule.Agents {
				neighbors[i] = m.Grid.GetNeighbors(agent.ID, false)
				benchmarkPosts = m.GetRecommendation(agent, neighbors[i])
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.Recsys.PreStep()
				for i, agent := range m.Schedule.Agents {
					benchmarkPosts = m.GetRecommendation(agent, neighbors[i])
				}
			}
		})
	}
}
