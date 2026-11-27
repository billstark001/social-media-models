package simulation_test

import (
	"testing"

	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/trajectory"
)

func benchmarkAccumulationModel(b *testing.B, dynamicsType string) simulation.IModel {
	b.Helper()
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "benchmark"
	metadata.DynamicsType = dynamicsType
	metadata.NodeCount, metadata.NodeFollowCount = 500, 15
	metadata.RecsysFactoryType = "Random"
	metadata.RNG = smprng.FixedSpec(0x1234, 0x5678)
	metadata.CollectItemOptions = model.CollectItemOptions{AgentNumber: true, OpinionSum: true}
	s := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{Quiet: true})
	if err := s.InitError(); err != nil {
		b.Fatal(err)
	}
	s.Step()
	return s.Model
}

func BenchmarkPackedAccumulate(b *testing.B) {
	for _, dynamicsType := range []string{"HK", "Deffuant", "Galam", "Voter"} {
		b.Run(dynamicsType, func(b *testing.B) {
			m := benchmarkAccumulationModel(b, dynamicsType)
			acc := simulation.NewTrajectoryAccumulativeState(500, trajectory.Precision{})
			m.Accumulate(acc)
			acc.Packed.Reset()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.Accumulate(acc)
				acc.Packed.Reset()
			}
		})
	}
}
