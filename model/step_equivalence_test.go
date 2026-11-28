package model_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"runtime"
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/recsys"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
)

// These hashes were captured from 902fada before optimizing the step path.
// Every completed step includes opinions, graph, posts, collected statistics,
// RNG states, convergence outputs and (when enabled) ordered feed/events.
// Cases with effective Opinion ties now have a deliberately different sampling
// law: those are checked by tie-RNG evidence and repeated full trajectories,
// rather than replacing their old ID-dependent hashes with new golden values.
func TestStepMatchesPreOptimizationTrajectories(t *testing.T) {
	want := map[string]string{
		"HK/Random":                "eedb84002d8b5559a72a5ce64faf936619f357a39338d36a665f0e578b1c4bf8",
		"Deffuant/Random":          "c037ec8385598b91c40706edd2bc872f7bc0b9ed978a84e8d8009b0b632356f2",
		"Galam/Random":             "538bb930796c31bf6d1f61cde8e0050e943552c612c56c8341c0e322487699d3",
		"Voter/Random":             "fb15ba7933a15712b342ba9343ebf0a8f5fd5fbba4c83fbc54ecd79eabec2c1e",
		"HK/Opinion":               "ca9fb2f480b3d30c9eea29271ae6656da9e4dab53e60ca72ef48fcdc94652b46",
		"HK/Structure":             "486211f0611817b4ae9249c8ec4554466548d3ec2b9f841b41079a3b27858aa3",
		"Deffuant/Structure":       "8043fe881c775037b0c3b322215ef77a8967eee4b60bafe8832ba5505f86a875",
		"Galam/Structure":          "5c0849996bc7d3d576808d710d612a8da6912b3dee2a3b3ae566a527aac868cb",
		"Voter/Structure":          "57657ea4419eae3439151dc525af34f6ab53cfc364292033723c78194dc5db45",
		"HK/OpinionRandom":         "e1e559aa9aedfa535f170b40faa8861701d25ab84604238f4d4b3913d37a3d0e",
		"Deffuant/OpinionRandom":   "66103e401ec6b5bf9eb3a6b36fc782b75d827dcd9aa8173b071b8351bd140010",
		"Galam/OpinionRandom":      "debbe7faab8f404a87fb8b1863ba3a59e54fd17fba3628636fc6c2a94627d376",
		"Voter/OpinionRandom":      "f2069814f6314ff1fc0498014d66d9514d0d4ba595296ccb9a8d2ede8dd343f1",
		"HK/StructureRandom":       "9dc71abb0bbbb2ec26976cf597ccbd6d94ce57f48268941eba1ff8150a73c431",
		"Deffuant/StructureRandom": "f174bdcff79a440ab6138a113f429edc44511eb48e95a3a4256cc1bc171a1932",
		"Galam/StructureRandom":    "9559f9f311e32b0af0f3c6048cf775f92cb9f746f99feefbcca4003b68071c08",
		"Voter/StructureRandom":    "938d11ed9e3bf2c5e4074c6cac51c460f93cfabc9317a855aac8c3311cc406ff",
		"HK/StructureM9":           "51df7b4f78cfac32c5e1a2eaf8218de5b9c8145b9041a75be7dae49e6defc4a6",
		"Deffuant/StructureM9":     "1b1c4a6168633b3ec04a57e6986c9ffc406d22fd804e6f6671b7434a107d8ff2",
		"Galam/StructureM9":        "27030bc04b682954f141e23dc05e8281ef5c9be21a8cab73d0e2c07efb9f57c9",
		"Voter/StructureM9":        "ff8b89d4d2fc193e782f5e3ea5c1741202204d4bb03497b1d96f35feb5a0539f",
	}
	recParams := map[string]any{"MixRate": .25, "RandomRatio": .2}
	for _, name := range []string{"Random", "Opinion", "Structure", "OpinionRandom", "StructureRandom", "OpinionM9", "StructureM9"} {
		t.Run("HK/"+name, func(t *testing.T) {
			checkStepDigest(t, want, "HK/"+name, name,
				func() model.Dynamics[float64, dynamics.HKParams] { return &dynamics.HK{} },
				dynamics.HKParams{Tolerance: .45, Influence: .15, RewiringRate: .4, RepostRate: .3},
				simulation.GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](recParams),
				func(r *rand.Rand) float64 { return r.Float64()*2 - 1 })
		})
		t.Run("Deffuant/"+name, func(t *testing.T) {
			checkStepDigest(t, want, "Deffuant/"+name, name,
				func() model.Dynamics[float64, dynamics.DeffuantParams] { return &dynamics.Deffuant{} },
				dynamics.DeffuantParams{Tolerance: .45, Influence: .15, RewiringRate: .4, RepostRate: .3},
				simulation.GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](recParams),
				func(r *rand.Rand) float64 { return r.Float64()*2 - 1 })
		})
		t.Run("Galam/"+name, func(t *testing.T) {
			checkStepDigest(t, want, "Galam/"+name, name,
				func() model.Dynamics[bool, dynamics.GalamParams] { return &dynamics.Galam{} },
				dynamics.GalamParams{Influence: .3, RewiringRate: .4, RepostRate: .3},
				simulation.GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](recParams),
				func(r *rand.Rand) bool { return r.Float64() > .5 })
		})
		t.Run("Voter/"+name, func(t *testing.T) {
			checkStepDigest(t, want, "Voter/"+name, name,
				func() model.Dynamics[bool, dynamics.VoterParams] { return &dynamics.Voter{} },
				dynamics.VoterParams{Influence: .3, RewiringRate: .4, RepostRate: .3},
				simulation.GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](recParams),
				func(r *rand.Rand) bool { return r.Float64() > .5 })
		})
	}
}

func checkStepDigest[O, P any](t *testing.T, want map[string]string, key, recsys string,
	newDynamics func() model.Dynamics[O, P], params P, factories map[string]model.RecsysFactory[O, P],
	opinion func(*rand.Rand) O,
) {
	t.Helper()
	if runtime.GOARCH != "amd64" {
		t.Log("exact pre-optimization hashes were captured on amd64; checking observation invariants on this architecture")
		checkStepObservationModes(t, recsys, newDynamics, params, factories, opinion)
		return
	}
	got, tieQueries := stepTrajectoryDigest(t, recsys, newDynamics, params, factories, opinion)
	if stepHasRevisedOpinionTies(key) {
		if tieQueries == 0 {
			t.Fatal("revised-law trajectory did not exercise Opinion tie sampling")
		}
		repeated, repeatedTieQueries := stepTrajectoryDigest(t, recsys, newDynamics, params, factories, opinion)
		if got != repeated || tieQueries != repeatedTieQueries {
			t.Fatal("same-seed revised tie trajectories or tie RNG consumption differ")
		}
		t.Logf("%s: %d recommendation queries drew tie randomness; complete trajectories reproduce", key, tieQueries)
	} else {
		expected, ok := want[key]
		if !ok {
			t.Fatalf("missing pre-optimization reference for %s", key)
		}
		if tieQueries != 0 {
			t.Fatalf("protected pre-optimization case %s unexpectedly exercised %d revised tie queries", key, tieQueries)
		}
		if got != expected {
			t.Fatalf("%q: %q,", key, got)
		}
	}
	checkStepObservationModes(t, recsys, newDynamics, params, factories, opinion)
}

func stepHasRevisedOpinionTies(key string) bool {
	switch key {
	case "Deffuant/Opinion", "Galam/Opinion", "Voter/Opinion",
		"HK/OpinionM9", "Deffuant/OpinionM9", "Galam/OpinionM9", "Voter/OpinionM9":
		return true
	default:
		return false
	}
}

type opinionTieConsumption[O, P any] struct {
	model.SMPModelRecommendationSystem[O, P]
	pool    *smprng.Pool
	queries *int
}

func (r *opinionTieConsumption[O, P]) Recommend(a *model.SMPAgent[O, P], excluded map[int64]bool, count int) []*model.PostRecord[O] {
	before, err := r.pool.Snapshot()
	if err != nil {
		panic(err)
	}
	posts := r.SMPModelRecommendationSystem.Recommend(a, excluded, count)
	after, err := r.pool.Snapshot()
	if err != nil {
		panic(err)
	}
	// Opinion generates query noise in PreStep; Recommend itself only draws
	// randomness for an effective tie. In M9 wrap only this component, so its
	// uniform exploration slot cannot be mistaken for evidence of a tie.
	if !bytes.Equal(before[smprng.StreamRecommendation], after[smprng.StreamRecommendation]) {
		(*r.queries)++
	}
	return posts
}

func traceOpinionTieConsumption[O, P any](m *model.SMPModel[O, P], queries *int) {
	switch recommender := m.Recsys.(type) {
	case *recsys.Opinion[O, P]:
		m.Recsys = &opinionTieConsumption[O, P]{recommender, m.RNG, queries}
	case *recsys.Mix[O, P]:
		if opinion, ok := recommender.RecSys2.(*recsys.Opinion[O, P]); ok {
			recommender.RecSys2 = &opinionTieConsumption[O, P]{opinion, m.RNG, queries}
		}
	}
}

func stepTrajectoryDigest[O, P any](t *testing.T, recsys string,
	newDynamics func() model.Dynamics[O, P], params P, factories map[string]model.RecsysFactory[O, P],
	opinion func(*rand.Rand) O,
) (string, int) {
	t.Helper()
	tieQueries := 0
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	encode := func(value any) {
		t.Helper()
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, seed := range []uint64{7, 193} {
		for _, retain := range []int{0, 3} {
			for _, observe := range []bool{false, true} {
				pool := smprng.MustNewPool(smprng.FixedSpec(seed, 29))
				graph := utils.CreateRandomNetwork(24, .13, pool.Stream(smprng.StreamNetwork))
				ops := make([]O, 24)
				for i := range ops {
					ops[i] = opinion(pool.Stream(smprng.StreamOpinion))
				}
				var logger func(*model.EventRecord)
				if observe {
					logger = func(event *model.EventRecord) { encode(event) }
				}
				m := model.NewSMPModel(graph, &ops, &model.SMPModelParams[O, P]{
					SMPModelPureParams: model.SMPModelPureParams{RecsysCount: 4, PostRetainCount: retain},
					RecsysFactory:      factories[recsys],
				}, &params, newDynamics(), &model.CollectItemOptions{
					AgentNumber: true, OpinionSum: true, PostEvent: true, RewiringEvent: true,
					ViewPostsEvent: true, ViewPostsDetails: observe,
				}, logger, pool)
				m.SetAgentCurPosts()
				m.Recsys.PostInit(nil)
				traceOpinionTieConsumption(m, &tieQueries)
				if observe {
					m.ObserveFeed = func(a *model.SMPAgent[O, P], feed model.FeedObservation[O]) {
						encode(a.ID)
						encode(feed)
					}
				}
				for range 16 {
					changed, delta := m.Step(true)
					encode(changed)
					encode(delta)
					dump := m.Dump()
					// Recommender cache dumps contain unordered msgpack maps; the
					// resulting feeds and all model/RNG state are checked instead.
					dump.RecsysDumpData = nil
					encode(dump)
				}
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), tieQueries
}
