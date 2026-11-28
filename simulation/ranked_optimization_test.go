package simulation_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
	"smp/dynamics"
	"smp/model"
	"smp/recsys"
	"smp/simulation"
)

// The reference is captured from the validated uniform-tie implementation,
// before performance changes. Ordered feeds, all physical/RNG states and halt
// outputs must remain identical; this is not a comparison with the old ID rule.
func TestRankedOptimizationKeepsValidatedTrajectories(t *testing.T) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	encode := func(value any) {
		t.Helper()
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, policy := range []string{"Opinion", "OpinionM9", "Structure", "StructureM9", "OpinionRandom", "StructureRandom"} {
		for _, history := range []int{0, 2, 6} {
			for _, cache := range []bool{false, true} {
				for _, noise := range []float64{0, .1} {
					for _, continuous := range []bool{false, true} {
						source, err := rankedTieFixture(policy, history, cache, "")
						if err != nil {
							t.Fatal(err)
						}
						metadata := cloneCheckpointReviewMetadata(source.Metadata)
						metadata.RecSysParams["NoiseStd"] = noise
						opinions := source.Model.GetOpinions()
						if continuous {
							metadata.HKParams.Influence = .15
							for index := range opinions {
								opinions[index] = -.9 + 1.8*float64(index)/float64(len(opinions))
							}
						}
						scenario := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{Quiet: true})
						if err := scenario.InitFromState(&simulation.InitialState{Graph: source.Model.GetGraph(), Opinions: opinions}); err != nil {
							t.Fatal(err)
						}
						encode(fmt.Sprintf("%s/kh%d/cache%t/noise%g/continuous%t", policy, history, cache, noise, continuous))
						var feed bytes.Buffer
						checkpointReviewCapture(t, scenario.Model, &feed)
						for range 8 {
							step := checkpointReviewAdvance(t, scenario, &feed)
							encode(step.state)
							encode(step.feed)
							encode(step.changed)
							encode(step.delta)
							encode(step.stable)
							wrapper := scenario.Model.(*simulation.Float64ModelWrapper[dynamics.HKParams])
							encode(rankedOptimizationCache(t, wrapper.M.Recsys))
						}
					}
				}
			}
		}
	}
	got := hex.EncodeToString(hash.Sum(nil))
	const expected = "53ea02c41f2618409d94799297a0660e2063490a145adfc3a8ef43ae3b13408b"
	if got != expected {
		t.Fatalf("validated uniform-tie trajectory digest: got %s, want %s", got, expected)
	}
}

// Canonical JSON of typed cache fields compares scores and group boundaries
// without depending on msgpack map iteration order.
func rankedOptimizationCache(t *testing.T, recommender model.SMPModelRecommendationSystem[float64, dynamics.HKParams]) *recsys.StructureDump {
	t.Helper()
	if mixed, ok := recommender.(*recsys.Mix[float64, dynamics.HKParams]); ok {
		return rankedOptimizationCache(t, mixed.RecSys2)
	}
	switch recommender.(type) {
	case *recsys.Structure[float64, dynamics.HKParams], *recsys.StructureRandom[float64, dynamics.HKParams]:
		data := recommender.Dump()
		if len(data) != 0 {
			var dump recsys.StructureDump
			if err := msgpack.Unmarshal(data, &dump); err != nil {
				t.Fatal(err)
			}
			return &dump
		}
	}
	return nil
}
