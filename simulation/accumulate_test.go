package simulation_test

import (
	"bytes"
	"reflect"
	"testing"

	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/trajectory"
)

func TestPackedAccumulateMatchesDetachedLegacyRows(t *testing.T) {
	for _, dynamicsType := range []string{"HK", "Deffuant", "Galam", "Voter"} {
		for _, precision := range []trajectory.Precision{
			{}, {Opinions: "float64", OpinionSums: "float32"}, {Opinions: "float16", OpinionSums: "float64"},
		} {
			t.Run(dynamicsType+"/"+precision.Opinions+"/"+precision.OpinionSums, func(t *testing.T) {
				metadata := simulation.DefaultScenarioMetadata()
				metadata.UniqueName, metadata.DynamicsType = "accumulate", dynamicsType
				metadata.NodeCount, metadata.NodeFollowCount = 24, 5
				metadata.RecsysFactoryType = "Random"
				metadata.RNG = smprng.FixedSpec(17, 23)
				metadata.CollectItemOptions = model.CollectItemOptions{AgentNumber: true, OpinionSum: true}
				s := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{Quiet: true})
				if err := s.InitError(); err != nil {
					t.Fatal(err)
				}
				legacy := simulation.NewAccumulativeModelState()
				packed := simulation.NewTrajectoryAccumulativeState(metadata.NodeCount, precision)
				want := trajectory.NewBuffer(metadata.NodeCount, precision)
				var initial []float64
				for step := 0; step < 8; step++ {
					before, err := s.RNG.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					s.Model.Accumulate(legacy)
					s.Model.Accumulate(packed)
					counts := make([]model.AgentNumberRecord, metadata.NodeCount)
					sums := make([]model.AgentOpinionSumRecord, metadata.NodeCount)
					for i := range counts {
						for c := range counts[i] {
							counts[i][c] = int(legacy.AgentNumbers[step][i][c])
							sums[i][c] = legacy.AgentOpinionSums[step][i][c]
						}
					}
					if err := want.Append(legacy.Opinions[step], counts, sums); err != nil {
						t.Fatal(err)
					}
					for i := range want.Channels {
						if !bytes.Equal(want.Channels[i], packed.Packed.Channels[i]) {
							t.Fatalf("packed channel %d differs at step %d", i, step)
						}
					}
					after, err := s.RNG.Snapshot()
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("accumulating changed RNG: %v", err)
					}
					if step == 0 {
						initial = append([]float64(nil), legacy.Opinions[0]...)
					} else if !reflect.DeepEqual(initial, legacy.Opinions[0]) {
						t.Fatal("later accumulation changed a retained legacy opinion row")
					}
					if step%3 == 2 {
						packed.Packed.Reset()
						want.Reset()
					}
					s.Step()
				}
			})
		}
	}
}
