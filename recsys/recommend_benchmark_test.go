package recsys_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
)

var benchmarkPosts []*model.PostRecord[float64]

func benchmarkRecommendationModel(recsysName string) *model.SMPModel[float64, dynamics.HKParams] {
	const (
		nodeCount   = 500
		followCount = 15
	)
	pool := smprng.MustNewPool(smprng.FixedSpec(0xabc, 0xdef))
	graph := utils.CreateRandomNetwork(
		nodeCount,
		float64(followCount)/float64(nodeCount-1),
		pool.Stream(smprng.StreamNetwork),
	)
	factories := simulation.GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](
		map[string]any{"NoiseStd": 0.1, "OpRandomNoiseStd": 0.1},
	)
	params := &model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: model.SMPModelPureParams{
			RecsysCount:     10,
			PostRetainCount: 3,
		},
		RecsysFactory: factories[recsysName],
	}
	m := model.NewSMPModelFloat64(
		graph,
		nil,
		params,
		dynamics.DefaultHKParams(),
		&dynamics.HK{},
		&model.CollectItemOptions{},
		nil,
		pool,
	)
	m.SetAgentCurPosts()
	m.Recsys.PostInit(nil)
	m.Recsys.PreStep()
	return m
}

func BenchmarkRecommend(b *testing.B) {
	for _, recsysName := range []string{"Random", "Opinion", "OpinionRandom", "Structure"} {
		b.Run(fmt.Sprintf("%s/Recommend", recsysName), func(b *testing.B) {
			m := benchmarkRecommendationModel(recsysName)
			agent := m.Schedule.Agents[0]
			neighbors := m.Grid.GetNeighbors(agent.ID, false)
			// Warm lazy simulation caches before measuring.
			benchmarkPosts = m.GetRecommendation(agent, neighbors)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkPosts = m.GetRecommendation(agent, neighbors)
			}
		})
		b.Run(fmt.Sprintf("%s/RecommendAt", recsysName), func(b *testing.B) {
			m := benchmarkRecommendationModel(recsysName)
			agent := m.Schedule.Agents[0]
			neighbors := m.Grid.GetNeighbors(agent.ID, false)
			probeRNG := rand.New(rand.NewPCG(123, 456))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				posts, err := m.GetRecommendationAt(agent, 0.25, neighbors, probeRNG)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkPosts = posts
			}
		})
	}
}
