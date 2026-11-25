package recsys_test

import (
	"fmt"
	"reflect"
	"testing"

	"gonum.org/v1/gonum/graph/simple"
	"smp/dynamics"
	"smp/model"
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
