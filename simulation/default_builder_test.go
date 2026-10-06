package simulation_test

import (
	"reflect"
	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
	"testing"
)

// The reference uses the existing low-level constructors and original ER/RNG
// initialization sequence, independently of the extracted NewModel builder.
func TestDefaultScenarioMatchesOriginalInitialization(t *testing.T) {
	for _, kind := range []string{simulation.DynamicsTypeHK, simulation.DynamicsTypeDeffuant, simulation.DynamicsTypeGalam, simulation.DynamicsTypeVoter} {
		t.Run(kind, func(t *testing.T) {
			meta := regressionMetadata(kind, "Random")
			got := inMemory(meta)
			if err := got.InitError(); err != nil {
				t.Fatal(err)
			}
			pool := smprng.MustNewPool(meta.RNG)
			graph := utils.CreateRandomNetwork(meta.NodeCount,
				float64(meta.NodeFollowCount)/float64(meta.NodeCount-1), pool.Stream(smprng.StreamNetwork))
			var raw simulation.IModel
			switch kind {
			case simulation.DynamicsTypeHK:
				p := model.SMPModelParams[float64, dynamics.HKParams]{SMPModelPureParams: meta.SMPModelPureParams,
					RecsysFactory: simulation.GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](meta.RecSysParams)["Random"]}
				raw = &simulation.Float64ModelWrapper[dynamics.HKParams]{M: model.NewSMPModelFloat64(graph, nil, &p, &meta.HKParams, &dynamics.HK{}, &meta.CollectItemOptions, nil, pool)}
			case simulation.DynamicsTypeDeffuant:
				p := model.SMPModelParams[float64, dynamics.DeffuantParams]{SMPModelPureParams: meta.SMPModelPureParams,
					RecsysFactory: simulation.GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](meta.RecSysParams)["Random"]}
				raw = &simulation.Float64ModelWrapper[dynamics.DeffuantParams]{M: model.NewSMPModelFloat64(graph, nil, &p, &meta.DeffuantParams, &dynamics.Deffuant{}, &meta.CollectItemOptions, nil, pool)}
			case simulation.DynamicsTypeGalam:
				ops := make([]bool, meta.NodeCount)
				for i := range ops {
					ops[i] = pool.Stream(smprng.StreamOpinion).IntN(2) == 1
				}
				p := model.SMPModelParams[bool, dynamics.GalamParams]{SMPModelPureParams: meta.SMPModelPureParams,
					RecsysFactory: simulation.GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](meta.RecSysParams)["Random"]}
				raw = &simulation.BoolModelWrapper[dynamics.GalamParams]{M: model.NewSMPModel(graph, &ops, &p, &meta.GalamParams, &dynamics.Galam{}, &meta.CollectItemOptions, nil, pool)}
			case simulation.DynamicsTypeVoter:
				ops := make([]bool, meta.NodeCount)
				for i := range ops {
					ops[i] = pool.Stream(smprng.StreamOpinion).IntN(2) == 1
				}
				p := model.SMPModelParams[bool, dynamics.VoterParams]{SMPModelPureParams: meta.SMPModelPureParams,
					RecsysFactory: simulation.GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](meta.RecSysParams)["Random"]}
				raw = &simulation.BoolModelWrapper[dynamics.VoterParams]{M: model.NewSMPModel(graph, &ops, &p, &meta.VoterParams, &dynamics.Voter{}, &meta.CollectItemOptions, nil, pool)}
			}
			raw.InitPosts()
			raw.SetCurStep(1)
			want := &simulation.Scenario{Metadata: meta, Model: raw, RNG: pool}
			for i := 0; i < 8; i++ {
				assertPhysicalEqual(t, got, want)
				got.Step()
				want.Step()
			}
			assertPhysicalEqual(t, got, want)
		})
	}
}

func TestPreparedCheckpointConcurrentBranches(t *testing.T) {
	parent := inMemory(regressionMetadata(simulation.DynamicsTypeHK, "StructureRandom"))
	if err := parent.InitError(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		parent.Step()
	}
	raw, err := parent.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := simulation.PrepareCheckpoint(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			t.Parallel()
			leftMeta, rightMeta := *parent.Metadata, *parent.Metadata
			leftMeta.RNG, rightMeta.RNG = smprng.FixedSpec(201, 202), smprng.FixedSpec(201, 202)
			left, right := inMemory(&leftMeta), inMemory(&rightMeta)
			if err := left.InitFromPreparedCheckpoint(prepared, true); err != nil {
				t.Fatal(err)
			}
			if err := right.InitFromCheckpoint(raw, true); err != nil {
				t.Fatal(err)
			}
			for j := 0; j < 5; j++ {
				left.Step()
				right.Step()
				assertPhysicalEqual(t, left, right)
			}
		})
	}
}

func regressionMetadata(kind, rec string) *simulation.ScenarioMetadata {
	m := makeValidMetadata()
	m.NodeCount, m.NodeFollowCount = 40, 5
	m.RecsysCount, m.PostRetainCount = 3, 3
	m.DynamicsType, m.RecsysFactoryType = kind, rec
	m.HKParams = dynamics.HKParams{Tolerance: .45, Influence: .08, RewiringRate: .2, RepostRate: .25}
	m.DeffuantParams = dynamics.DeffuantParams{Tolerance: .45, Influence: .08, RewiringRate: .2, RepostRate: .25}
	m.GalamParams = *dynamics.DefaultGalamParams()
	m.VoterParams = *dynamics.DefaultVoterParams()
	m.RecSysParams = map[string]any{"NoiseStd": 0., "OpRandomNoiseStd": 0., "LogRecommendations": false}
	return m
}

func inMemory(m *simulation.ScenarioMetadata) *simulation.Scenario {
	return simulation.NewScenarioWithOptions("", m, simulation.ScenarioOptions{Quiet: true})
}

func assertPhysicalEqual(t *testing.T, a, b *simulation.Scenario) {
	t.Helper()
	if a.Model.GetCurStep() != b.Model.GetCurStep() ||
		!reflect.DeepEqual(a.Model.GetOpinions(), b.Model.GetOpinions()) ||
		!reflect.DeepEqual(observedCounts(t, a.Model), observedCounts(t, b.Model)) ||
		!utils.CompareGraphs(a.Model.GetGraph(), b.Model.GetGraph()) {
		t.Fatal("microscopic trajectories differ")
	}
	as, err := a.RNG.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	bs, err := b.RNG.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(as, bs) {
		t.Fatal("RNG stream positions differ")
	}
	if a.Metadata.DynamicsType == simulation.DynamicsTypeHK || a.Metadata.DynamicsType == simulation.DynamicsTypeDeffuant {
		ar, err := a.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		br, err := b.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		ap, err := simulation.InspectFloat64Checkpoint(ar)
		if err != nil {
			t.Fatal(err)
		}
		bp, err := simulation.InspectFloat64Checkpoint(br)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ap.Posts, bp.Posts) {
			t.Fatal("post contents or timestamps differ")
		}
	}
}

func observedCounts(t *testing.T, m simulation.IModel) []model.AgentNumberRecord {
	t.Helper()
	counts, err := simulation.ObserveAgentNumbers(m)
	if err != nil {
		t.Fatal(err)
	}
	return counts
}
