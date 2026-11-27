package simulation_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/trajectory"

	"github.com/vmihailenco/msgpack/v5"
)

// Exercise actual disk recovery, including event and trajectory data flushed
// after the recovery snapshot. Compare every continued step, not only the end.
func TestPersistentRecoveryPreservesFeedsRNGAndTrajectory(t *testing.T) {
	for _, dynamicsType := range []string{"HK", "Deffuant", "Galam", "Voter"} {
		for index, recommender := range []string{"Random", "Opinion", "Structure", "OpinionRandom", "StructureRandom", "OpinionM9", "StructureM9"} {
			t.Run(dynamicsType+"/"+recommender, func(t *testing.T) {
				newScenario := func(base string) *simulation.Scenario {
					metadata := simulation.DefaultScenarioMetadata()
					metadata.UniqueName, metadata.DynamicsType = "recovery", dynamicsType
					metadata.NodeCount, metadata.NodeFollowCount = 18, 3
					metadata.RecsysCount, metadata.PostRetainCount = 4, 3*(index%2)
					metadata.MaxSimulationStep = 12
					metadata.RNG = smprng.FixedSpec(731, 997)
					metadata.RecsysFactoryType = recommender
					metadata.RecSysParams = map[string]any{
						"NoiseStd": .12, "OpRandomNoiseStd": .08, "MixRate": .3,
						"RandomRatio": .2, "UseCache": index%2 == 0,
						"LogRecommendations": false,
					}
					metadata.HKParams = dynamics.HKParams{Tolerance: .5, Influence: .15, RewiringRate: .4, RepostRate: .4}
					metadata.DeffuantParams = dynamics.DeffuantParams{Tolerance: .5, Influence: .15, RewiringRate: .4, RepostRate: .4}
					metadata.GalamParams = dynamics.GalamParams{Influence: .3, RewiringRate: .4, RepostRate: .4}
					metadata.VoterParams = dynamics.VoterParams{Influence: .3, RewiringRate: .4, RepostRate: .4}
					metadata.CollectItemOptions = model.CollectItemOptions{
						AgentNumber: true, OpinionSum: true, PostEvent: true, RewiringEvent: true,
						ViewPostsEvent: true, ViewPostsDetails: true,
					}
					metadata.TrajectoryPrecision = trajectory.Precision{Opinions: "float64", OpinionSums: "float64"}
					return simulation.NewScenarioWithOptions(base, metadata, simulation.ScenarioOptions{EnableDumps: true, Quiet: true})
				}
				full, interrupted := newScenario(t.TempDir()), newScenario(t.TempDir())
				for _, scenario := range []*simulation.Scenario{full, interrupted} {
					if err := scenario.InitError(); err != nil {
						t.Fatal(err)
					}
					for range 4 {
						scenario.Step()
					}
					if err := scenario.DumpError(); err != nil {
						t.Fatal(err)
					}
				}
				t.Cleanup(func() { full.Close() })
				// Persist an uncheckpointed tail to require rollback on both paths.
				for range 2 {
					interrupted.Step()
				}
				if err := interrupted.Trajectory.Flush(interrupted.AccState.Packed); err != nil {
					t.Fatal(err)
				}
				if err := interrupted.Close(); err != nil {
					t.Fatal(err)
				}
				resumed := newScenario(interrupted.BaseDir)
				if !resumed.Load() {
					t.Fatal("failed to load the disk recovery point")
				}
				t.Cleanup(func() { resumed.Close() })
				if resumed.Trajectory.Manifest.NextStep != 5 || resumed.AccState.Len() != 0 {
					t.Fatal("uncheckpointed samples were retained or recovery duplicated the checkpoint row")
				}
				check := func() {
					t.Helper()
					if full.StableSteps != resumed.StableSteps || !bytes.Equal(persistentReviewState(t, full), persistentReviewState(t, resumed)) {
						t.Fatalf("state, posts, statistics or RNG differ at completed step %d", full.Model.GetCurStep()-1)
					}
				}
				check()
				for range 8 {
					fullChanged, fullDelta := full.Step()
					resumedChanged, resumedDelta := resumed.Step()
					if fullChanged != resumedChanged || fullDelta != resumedDelta {
						t.Fatal("step convergence outputs changed after recovery")
					}
					check()
				}
				for _, scenario := range []*simulation.Scenario{full, resumed} {
					if err := scenario.DumpError(); err != nil {
						t.Fatal(err)
					}
					if err := scenario.Trajectory.Verify(); err != nil {
						t.Fatal(err)
					}
				}
				fullEvents, err := full.DB.GetEvents()
				if err != nil {
					t.Fatal(err)
				}
				resumedEvents, err := resumed.DB.GetEvents()
				if err != nil {
					t.Fatal(err)
				}
				if len(fullEvents) == 0 || !reflect.DeepEqual(fullEvents, resumedEvents) {
					t.Fatal("ordered persisted feeds, placements or events changed across recovery")
				}
				if !reflect.DeepEqual(full.Trajectory.Manifest, resumed.Trajectory.Manifest) {
					t.Fatal("trajectory index or encoded-channel checksums differ after recovery")
				}
				for _, chunk := range full.Trajectory.Manifest.Chunks {
					left, err := trajectory.LoadChunk(filepath.Join(full.BaseDir, full.Metadata.UniqueName, chunk.File))
					if err != nil {
						t.Fatal(err)
					}
					right, err := trajectory.LoadChunk(filepath.Join(resumed.BaseDir, resumed.Metadata.UniqueName, chunk.File))
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(left, right) {
						t.Fatal("decoded packed channels differ after recovery")
					}
				}
			})
		}
	}
}

func persistentReviewState(t *testing.T, scenario *simulation.Scenario) []byte {
	t.Helper()
	raw, err := scenario.Model.RawDump()
	if err != nil {
		t.Fatal(err)
	}
	var state any
	if scenario.Metadata.DynamicsType == "Galam" || scenario.Metadata.DynamicsType == "Voter" {
		var dump model.SMPModelDumpData[bool, struct{}]
		if err := msgpack.Unmarshal(raw, &dump); err != nil {
			t.Fatal(err)
		}
		dump.RecsysDumpData = nil
		state = dump
	} else {
		var dump model.SMPModelDumpData[float64, struct{}]
		if err := msgpack.Unmarshal(raw, &dump); err != nil {
			t.Fatal(err)
		}
		dump.RecsysDumpData = nil
		state = dump
	}
	canonical, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
