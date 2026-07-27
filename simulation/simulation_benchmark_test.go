package simulation_test

import (
	"fmt"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
)

func newBenchmarkHKModel(recsysName string) *model.SMPModel[float64, dynamics.HKParams] {
	const (
		nodeCount   = 500
		followCount = 15
	)
	pool := smprng.MustNewPool(smprng.FixedSpec(0x1234, 0x5678))
	graph := utils.CreateRandomNetwork(
		nodeCount,
		float64(followCount)/float64(nodeCount-1),
		pool.Stream(smprng.StreamNetwork),
	)
	factories := simulation.GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](nil)
	params := &model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: model.SMPModelPureParams{
			RecsysCount:     10,
			PostRetainCount: 3,
		},
		RecsysFactory: factories[recsysName],
	}
	agentParams := &dynamics.HKParams{
		Tolerance:    0.45,
		Influence:    0.05,
		RewiringRate: 0.025,
		RepostRate:   0.1,
	}
	m := model.NewSMPModelFloat64(
		graph,
		nil,
		params,
		agentParams,
		&dynamics.HK{},
		&model.CollectItemOptions{},
		nil,
		pool,
	)
	m.SetAgentCurPosts()
	if m.Recsys != nil {
		m.Recsys.PostInit(nil)
	}
	return m
}

func BenchmarkSimulationStep(b *testing.B) {
	for _, recsysName := range []string{"Random", "OpinionM9", "StructureM9"} {
		b.Run(fmt.Sprintf("HK/%s", recsysName), func(b *testing.B) {
			m := newBenchmarkHKModel(recsysName)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.Step(true)
			}
		})
	}
}
