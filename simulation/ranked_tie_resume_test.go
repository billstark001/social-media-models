package simulation_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/recsys"
	smprng "smp/rng"
	"smp/simulation"
	"smp/trajectory"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
)

// Both camps contain multiple authors with exactly equal float64 opinions.
// The regular ring also gives Structure multiple equal common-neighbor scores.
// Zero influence keeps these ties present while rewiring and reposting exercise
// continued RNG use, changing cache rows, historical slots and placement data.
func rankedTieFixture(policy string, history int, cache bool, directory string) (*simulation.Scenario, error) {
	const nodes = 24
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName, metadata.DynamicsType = "ranked-ties", simulation.DynamicsTypeHK
	metadata.NodeCount, metadata.NodeFollowCount = nodes, 2
	metadata.RecsysCount, metadata.PostRetainCount = 4, history
	metadata.MaxSimulationStep = 12
	metadata.RecsysFactoryType = policy
	metadata.RNG = smprng.FixedSpec(193, 29)
	metadata.RecSysParams = map[string]any{
		"NoiseStd": 0., "UseCache": cache, "MixRate": .25,
		"LogRecommendations": false,
	}
	metadata.HKParams = dynamics.HKParams{Tolerance: .45, Influence: 0, RewiringRate: .6, RepostRate: .25}
	metadata.CollectItemOptions = model.CollectItemOptions{
		AgentNumber: true, OpinionSum: true, PostEvent: true, RewiringEvent: true,
		ViewPostsEvent: true, ViewPostsDetails: true,
	}
	metadata.TrajectoryPrecision = trajectory.Precision{Opinions: "float64", OpinionSums: "float64"}
	graph := simple.NewDirectedGraph()
	opinions := make([]float64, nodes)
	for i := range nodes {
		graph.AddNode(simple.Node(i))
		if i%2 == 1 {
			opinions[i] = .9
		}
	}
	for i := range nodes {
		for _, offset := range []int{1, 2} {
			graph.SetEdge(graph.NewEdge(simple.Node(i), simple.Node((i+offset)%nodes)))
		}
	}
	scenario := simulation.NewScenarioWithOptions(directory, metadata, simulation.ScenarioOptions{
		EnableDumps: directory != "", Quiet: true,
	})
	err := scenario.InitFromState(&simulation.InitialState{Graph: graph, Opinions: opinions})
	return scenario, err
}

func TestRankedTieReproducibilityAndCheckpointContinuation(t *testing.T) {
	for _, policy := range []string{"Opinion", "Structure", "OpinionM9", "StructureM9"} {
		for _, history := range []int{0, 3} {
			for _, cache := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/history%d/cache%t", policy, history, cache), func(t *testing.T) {
					const horizon, cut = 12, 4
					parent, err := rankedTieFixture(policy, history, cache, "")
					if err != nil {
						t.Fatal(err)
					}
					var feed bytes.Buffer
					checkpointReviewCapture(t, parent.Model, &feed)
					expected := make([]checkpointReviewStep, horizon+1)
					expected[0] = checkpointReviewStep{state: checkpointReviewState(t, parent)}
					checkpointPath := filepath.Join(t.TempDir(), "ties.msgpack.lz4")
					rewires := 0
					for step := 1; step <= horizon; step++ {
						expected[step] = checkpointReviewAdvance(t, parent, &feed)
						rewires += expected[step].changed
						if step == cut {
							if err := parent.SaveCheckpointTo(checkpointPath); err != nil {
								t.Fatal(err)
							}
						}
					}
					if rewires == 0 {
						t.Fatal("tie fixture did not exercise effective rewiring")
					}
					repeated, err := rankedTieFixture(policy, history, cache, "")
					if err != nil {
						t.Fatal(err)
					}
					var repeatedFeed bytes.Buffer
					checkpointReviewCapture(t, repeated.Model, &repeatedFeed)
					for step := 1; step <= horizon; step++ {
						checkpointReviewEqual(t, checkpointReviewAdvance(t, repeated, &repeatedFeed), expected[step], step)
					}
					raw, _, err := simulation.ReadCheckpoint(checkpointPath)
					if err != nil {
						t.Fatal(err)
					}
					prepared, err := simulation.PrepareCheckpoint(raw)
					if err != nil {
						t.Fatal(err)
					}
					for _, reuse := range []bool{false, true} {
						resumed := inMemory(cloneCheckpointReviewMetadata(parent.Metadata))
						if reuse {
							err = resumed.InitFromPreparedCheckpoint(prepared, false)
						} else {
							err = resumed.InitFromCheckpoint(raw, false)
						}
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(checkpointReviewState(t, resumed), expected[cut].state) {
							t.Fatal("serialized checkpoint did not restore physical state and RNG exactly")
						}
						var resumedFeed bytes.Buffer
						checkpointReviewCapture(t, resumed.Model, &resumedFeed)
						for step := cut + 1; step <= horizon; step++ {
							checkpointReviewEqual(t, checkpointReviewAdvance(t, resumed, &resumedFeed), expected[step], step)
						}
					}
					rankedTieParallelContinuation(t, parent.Metadata, prepared, cut, expected[cut+1:])
				})
			}
		}
	}
}

func rankedTieParallelContinuation(t *testing.T, metadata *simulation.ScenarioMetadata, prepared *simulation.PreparedCheckpoint, completedStep int, expected []checkpointReviewStep) {
	t.Helper()
	const workers = 4
	type result struct {
		states []checkpointReviewStep
		err    error
	}
	ready := make(chan struct{}, workers)
	start := make(chan struct{})
	results := make(chan result, workers)
	for range workers {
		go func() {
			out := result{states: make([]checkpointReviewStep, len(expected))}
			defer func() { results <- out }()
			branch := inMemory(cloneCheckpointReviewMetadata(metadata))
			ready <- struct{}{}
			<-start
			if out.err = branch.InitFromPreparedCheckpoint(prepared, false); out.err != nil {
				return
			}
			recorder, err := newConcurrentCheckpointReviewRecorder(branch)
			if err != nil {
				out.err = err
				return
			}
			for step := range expected {
				if out.states[step], out.err = recorder.advance(); out.err != nil {
					return
				}
			}
		}()
	}
	for range workers {
		<-ready
	}
	close(start)
	for range workers {
		out := <-results
		if out.err != nil {
			t.Fatal(out.err)
		}
		for step, got := range out.states {
			checkpointReviewEqual(t, got, expected[step], completedStep+step+1)
		}
	}
}

func TestRankedTieProbeIsolation(t *testing.T) {
	for _, policy := range []string{"Opinion", "Structure"} {
		for _, warm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/warm%t", policy, warm), func(t *testing.T) {
				probed, err := rankedTieFixture(policy, 0, true, "")
				if err != nil {
					t.Fatal(err)
				}
				m := probed.Model.(*simulation.Float64ModelWrapper[dynamics.HKParams]).M
				agent := m.Grid.AgentMap[0]
				neighbors := m.Grid.GetNeighbors(agent.ID, false)
				if warm {
					m.Recsys.PreStep()
					m.GetRecommendation(agent, neighbors)
				}
				raw, err := probed.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				control := inMemory(cloneCheckpointReviewMetadata(probed.Metadata))
				if err := control.InitFromCheckpoint(raw, false); err != nil {
					t.Fatal(err)
				}
				before := checkpointReviewState(t, probed)
				cacheBefore := rankedTieStructureState(t, m)
				m.Recsys.(model.SMPModelProbePreparer).PrepareProbe()
				var want []*model.PostRecord[float64]
				for repeat := range 3 {
					source := rand.NewPCG(123, 456)
					rngBefore, _ := source.MarshalBinary()
					posts, err := m.GetRecommendationAt(agent, agent.CurOpinion, neighbors, rand.New(source))
					if err != nil {
						t.Fatal(err)
					}
					rngAfter, _ := source.MarshalBinary()
					if bytes.Equal(rngBefore, rngAfter) {
						t.Fatal("zero-noise probe did not draw tie randomness; fixture missed the repaired path")
					}
					if len(posts) != probed.Metadata.RecsysCount {
						t.Fatal("tie probe did not fill its requested slots")
					}
					if repeat == 0 {
						want = append([]*model.PostRecord[float64](nil), posts...)
					} else if !reflect.DeepEqual(posts, want) {
						t.Fatal("same probe RNG produced different tie choices")
					}
				}
				if !bytes.Equal(before, checkpointReviewState(t, probed)) {
					t.Fatal("probe changed physical state or advanced a simulation RNG stream")
				}
				if !reflect.DeepEqual(cacheBefore, rankedTieStructureState(t, m)) {
					t.Fatal("probe changed a cached Structure row or its tie groups")
				}
				var probedFeed, controlFeed bytes.Buffer
				checkpointReviewCapture(t, probed.Model, &probedFeed)
				checkpointReviewCapture(t, control.Model, &controlFeed)
				for step := 1; step <= 8; step++ {
					checkpointReviewEqual(t, checkpointReviewAdvance(t, probed, &probedFeed), checkpointReviewAdvance(t, control, &controlFeed), step)
				}
			})
		}
	}
}

func rankedTieStructureState(t *testing.T, m *model.SMPModel[float64, dynamics.HKParams]) recsys.StructureDump {
	t.Helper()
	var state recsys.StructureDump
	if data := m.Recsys.Dump(); len(data) > 0 {
		if err := msgpack.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

// Exercise the persistent runner's actual disk-load/rollback path as well as
// the standalone checkpoint API above. The incomplete tail must not duplicate
// displayed ties, advance their RNG twice, or retain events past the snapshot.
func TestRankedTiePersistentRecovery(t *testing.T) {
	for _, policy := range []string{"Opinion", "Structure"} {
		t.Run(policy, func(t *testing.T) {
			full, err := rankedTieFixture(policy, 3, true, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { full.Close() })
			interrupted, err := rankedTieFixture(policy, 3, true, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, scenario := range []*simulation.Scenario{full, interrupted} {
				for range 4 {
					scenario.Step()
				}
				if err := scenario.DumpError(); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				interrupted.Step()
			}
			if err := interrupted.Trajectory.Flush(interrupted.AccState.Packed); err != nil {
				t.Fatal(err)
			}
			if err := interrupted.Close(); err != nil {
				t.Fatal(err)
			}
			resumed := simulation.NewScenarioWithOptions(interrupted.BaseDir,
				cloneCheckpointReviewMetadata(interrupted.Metadata),
				simulation.ScenarioOptions{EnableDumps: true, Quiet: true})
			if !resumed.Load() {
				t.Fatal("persistent runner could not restore a ranked-tie checkpoint")
			}
			t.Cleanup(func() { resumed.Close() })
			if resumed.Trajectory.Manifest.NextStep != 5 {
				t.Fatal("recovery retained the uncheckpointed tail")
			}
			for step := 4; step <= 12; step++ {
				if full.StableSteps != resumed.StableSteps || !bytes.Equal(persistentReviewState(t, full), persistentReviewState(t, resumed)) {
					t.Fatalf("step %d: persistent tie recovery changed physical state or RNG", step)
				}
				if step < 12 {
					fullChanged, fullDelta := full.Step()
					resumedChanged, resumedDelta := resumed.Step()
					if fullChanged != resumedChanged || fullDelta != resumedDelta {
						t.Fatal("persistent tie recovery changed convergence outputs")
					}
				}
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
				t.Fatal("persistent recovery changed ordered feed, placement or rewiring events")
			}
			if !reflect.DeepEqual(full.Trajectory.Manifest, resumed.Trajectory.Manifest) {
				t.Fatal("persistent recovery changed trajectory chunks or channel checksums")
			}
		})
	}
}
