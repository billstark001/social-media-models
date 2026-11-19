package simulation_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"smp/simulation"
	"smp/utils"
)

func TestRecommendationNumericRepresentationsHaveIdenticalTrajectories(t *testing.T) {
	for _, number := range []any{int(0), int8(0), int16(0), int32(0), int64(0), uint(0), uint8(0), uint16(0), uint32(0), uint64(0), float32(0), json.Number("0")} {
		for _, kind := range []string{simulation.DynamicsTypeHK, simulation.DynamicsTypeVoter} {
			for _, policy := range []string{"Opinion", "OpinionRandom", "StructureRandom", "OpinionM9", "StructureM9"} {
				t.Run(fmt.Sprintf("%T/%s/%s", number, kind, policy), func(t *testing.T) {
					build := func(value any) *simulation.Scenario {
						meta := makeValidMetadata()
						meta.DynamicsType, meta.RecsysFactoryType = kind, policy
						meta.RecSysParams = map[string]any{"NoiseStd": value, "OpRandomNoiseStd": value, "MixRate": value, "RandomRatio": value, "LogRecommendations": false}
						scenario := simulation.NewScenarioWithOptions("", meta, simulation.ScenarioOptions{Quiet: true})
						if err := scenario.InitError(); err != nil {
							t.Fatal(err)
						}
						return scenario
					}
					want, got := build(float64(0)), build(number)
					for range 3 {
						want.Step()
						got.Step()
					}
					left, err := want.RNG.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					right, err := got.RNG.Snapshot()
					if err != nil || !reflect.DeepEqual(left, right) || !reflect.DeepEqual(want.Model.GetOpinions(), got.Model.GetOpinions()) || !utils.CompareGraphs(want.Model.GetGraph(), got.Model.GetGraph()) {
						t.Fatalf("equivalent numeric encodings changed the model or RNG: %v", err)
					}
				})
			}
		}
	}
}

func TestRecommendationParametersRejectInvalidValues(t *testing.T) {
	for _, params := range []map[string]any{
		{"NoiseStd": "0"}, {"NoiseStd": -0.1}, {"OpRandomNoiseStd": math.NaN()},
		{"UseCache": 1}, {"LogRecommendations": nil}, {"Steepness": math.Inf(1)},
		{"Tolerance": -1}, {"RandomRatio": 1.1}, {"MixRate": -0.1}, {"MixRate": true},
	} {
		if err := simulation.ValidateRecsysParams(params); err == nil {
			t.Fatalf("invalid recommendation parameter was silently replaced: %v", params)
		}
	}
}
