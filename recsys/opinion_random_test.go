package recsys_test

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"gonum.org/v1/gonum/graph/simple"
	"smp/dynamics"
	"smp/model"
	"smp/recsys"
	smprng "smp/rng"
	"smp/simulation"
)

func opinionRandomModel(steepness, noise float64) *model.SMPModel[float64, dynamics.HKParams] {
	graph := simple.NewDirectedGraph()
	opinions := []float64{0, .1, .2}
	for i := range opinions {
		graph.AddNode(simple.Node(i))
	}
	params := &model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: model.SMPModelPureParams{RecsysCount: 2},
		RecsysFactory: simulation.GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](map[string]any{
			"Tolerance": .4, "Steepness": steepness, "OpRandomNoiseStd": noise,
		})["OpinionRandom"],
	}
	m := model.NewSMPModelFloat64(
		graph, &opinions, params, dynamics.DefaultHKParams(), &dynamics.HK{},
		&model.CollectItemOptions{}, nil, smprng.MustNewPool(smprng.FixedSpec(123, 456)),
	)
	m.SetAgentCurPosts()
	m.Recsys.PostInit(nil)
	return m
}

// Retain the separate raw-matrix formulation as an oracle for repeated in-place
// rebuilds, including changes between positive support and all-zero rows.
func referenceOpinionRandomRates(o *recsys.OpinionRandom[float64, dynamics.HKParams], pool *smprng.Pool) [][]float64 {
	raw := make([][]float64, o.NumNodes)
	result := make([][]float64, o.NumNodes)
	for i := range raw {
		raw[i] = make([]float64, o.NumNodes)
		result[i] = make([]float64, o.NumNodes)
	}
	rng := pool.Stream(smprng.StreamRecommendation)
	for i := range o.NumNodes {
		for j := i + 1; j < o.NumNodes; j++ {
			diff := math.Abs(o.Agents[i].CurOpinion - o.Agents[j].CurOpinion)
			rate := max(1-diff/o.Tolerance, 0)
			if o.NoiseStd > 0 {
				noise := rng.NormFloat64() * o.NoiseStd
				rate = max(rate*(1-2*noise)+noise, 0)
			}
			if o.Steepness != 1 {
				rate = math.Pow(rate, o.Steepness)
			}
			raw[i][j], raw[j][i] = rate, rate
		}
	}
	for i, row := range raw {
		sum := 0.0
		for _, rate := range row {
			sum += rate
		}
		if sum > 0 {
			for j, rate := range row {
				if o.RandomRatio > 0 {
					result[i][j] = (1-o.RandomRatio)*rate/sum + o.RandomRatio/(float64(o.NumNodes)-1)
				} else {
					result[i][j] = rate / sum
				}
			}
		}
		result[i][i] = 0
	}
	return result
}

func TestOpinionRandomRebuildMatchesSeparateRawMatrix(t *testing.T) {
	for _, steepness := range []float64{1, 4} {
		for _, noise := range []float64{0, .1, 2} {
			for _, randomRatio := range []float64{0, .3, 1} {
				m := opinionRandomModel(steepness, noise)
				o := m.Recsys.(*recsys.OpinionRandom[float64, dynamics.HKParams])
				o.RandomRatio = randomRatio
				expectedPool := smprng.MustNewPool(smprng.FixedSpec(123, 456))
				states, err := m.RNG.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if err := expectedPool.Restore(states); err != nil {
					t.Fatal(err)
				}
				for _, opinions := range [][]float64{{0, .1, .2}, {-.8, .1, .8}, {.1, .1, .1}, {-.7, .15, .3}} {
					for i, opinion := range opinions {
						o.Agents[i].CurOpinion = opinion
					}
					expected := referenceOpinionRandomRates(o, expectedPool)
					o.PreStep()
					if !reflect.DeepEqual(o.RateMat, expected) {
						t.Fatalf("steepness=%g noise=%g ratio=%g opinions=%v: got %v, want %v", steepness, noise, randomRatio, opinions, o.RateMat, expected)
					}
				}
				actualStates, _ := m.RNG.Snapshot()
				expectedStates, _ := expectedPool.Snapshot()
				if !reflect.DeepEqual(actualStates, expectedStates) {
					t.Fatal("in-place score rebuild changed RNG state")
				}
			}
		}
	}
}

func TestOpinionRandomClearsZeroSupportAndResumesIdentically(t *testing.T) {
	for _, steepness := range []float64{1, 4} {
		t.Run(fmt.Sprintf("zeta%g", steepness), func(t *testing.T) {
			m := opinionRandomModel(steepness, 0)
			agent := m.Schedule.Agents[0]
			m.Recsys.PreStep()
			if posts := m.Recsys.Recommend(agent, nil, 2); len(posts) != 2 {
				t.Fatalf("expected two positive-score candidates, got %d", len(posts))
			}

			agent.CurOpinion, agent.NextOpinion = -.8, -.8
			checkpoint := m.Dump()
			m.Recsys.PreStep()
			if posts := m.Recsys.Recommend(agent, nil, 2); len(posts) != 0 {
				t.Fatalf("zero-score row retained %d stale recommendations", len(posts))
			}

			restored := checkpoint.Load(
				m.ModelParams, m.AgentParams, m.Dynamics, m.CollectItems, nil,
				smprng.MustNewPool(smprng.FixedSpec(123, 456)),
			)
			for range 3 {
				m.Step(true)
				restored.Step(true)
				if !reflect.DeepEqual(m.Dump(), restored.Dump()) {
					t.Fatal("zero-score recommendations differ after checkpoint restoration")
				}
			}
		})
	}
}

func TestOpinionRandomPreStepPreservesRNGDrawContract(t *testing.T) {
	for _, steepness := range []float64{1, 4} {
		for _, noise := range []float64{0, 2} {
			t.Run(fmt.Sprintf("zeta%g/noise%g", steepness, noise), func(t *testing.T) {
				m := opinionRandomModel(steepness, noise)
				before, err := m.RNG.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				expected := smprng.MustNewPool(smprng.FixedSpec(123, 456))
				if err := expected.Restore(before); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					m.Recsys.PreStep()
					if noise > 0 {
						for range 3 {
							expected.Stream(smprng.StreamRecommendation).NormFloat64()
						}
					}
				}
				actualStates, err := m.RNG.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				expectedStates, err := expected.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actualStates, expectedStates) {
					t.Fatal("score preparation changed recommendation or other RNG streams")
				}
			})
		}
	}
}
