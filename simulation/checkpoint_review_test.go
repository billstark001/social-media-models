package simulation_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
)

type checkpointReviewStep struct {
	state   []byte
	feed    []byte
	changed int
	delta   float64
	stable  int
}

func checkpointReviewMetadata(kind, recommender string, retain int, cache bool) *simulation.ScenarioMetadata {
	m := regressionMetadata(kind, recommender)
	m.NodeCount, m.NodeFollowCount = 18, 3
	m.PostRetainCount, m.RecsysCount = retain, 5
	m.RNG = smprng.FixedSpec(193, 29)
	m.RecSysParams = map[string]any{
		"UseCache": cache, "NoiseStd": .12, "OpRandomNoiseStd": .08,
		"MixRate": .3, "RandomRatio": .2, "LogRecommendations": false,
	}
	m.HKParams.RewiringRate, m.HKParams.RepostRate = .4, .3
	m.DeffuantParams.RewiringRate, m.DeffuantParams.RepostRate = .4, .3
	m.GalamParams.RewiringRate, m.GalamParams.RepostRate = .4, .3
	m.VoterParams.RewiringRate, m.VoterParams.RepostRate = .4, .3
	m.CollectItemOptions = model.CollectItemOptions{
		AgentNumber: true, OpinionSum: true, PostEvent: true, RewiringEvent: true,
		ViewPostsEvent: true, ViewPostsDetails: true,
	}
	return m
}

func cloneCheckpointReviewMetadata(m *simulation.ScenarioMetadata) *simulation.ScenarioMetadata {
	copy := *m
	copy.RecSysParams = maps.Clone(m.RecSysParams)
	copy.CheckpointSteps = append([]int(nil), m.CheckpointSteps...)
	return &copy
}

func attachCheckpointReviewFeed[O, P any](t *testing.T, m *model.SMPModel[O, P], feed *bytes.Buffer) {
	t.Helper()
	encoder := json.NewEncoder(feed)
	encode := func(value any) {
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	// Encode in callback order while the pre-commit slices are still valid.
	m.CaptureFeedDetails = true
	m.ObserveFeed = func(a *model.SMPAgent[O, P], observation model.FeedObservation[O]) {
		encode(a.ID)
		encode(observation)
	}
	m.EventLogger = func(event *model.EventRecord) { encode(event) }
}

func checkpointReviewCapture(t *testing.T, m simulation.IModel, feed *bytes.Buffer) {
	t.Helper()
	switch w := m.(type) {
	case *simulation.Float64ModelWrapper[dynamics.HKParams]:
		attachCheckpointReviewFeed(t, w.M, feed)
	case *simulation.Float64ModelWrapper[dynamics.DeffuantParams]:
		attachCheckpointReviewFeed(t, w.M, feed)
	case *simulation.BoolModelWrapper[dynamics.GalamParams]:
		attachCheckpointReviewFeed(t, w.M, feed)
	case *simulation.BoolModelWrapper[dynamics.VoterParams]:
		attachCheckpointReviewFeed(t, w.M, feed)
	default:
		t.Fatalf("unsupported review model %T", m)
	}
}

func checkpointReviewTypedState[O, P any](t *testing.T, m *model.SMPModel[O, P]) []byte {
	t.Helper()
	dump := m.Dump()
	// Msgpack map ordering is deliberately unspecified. Validate caches through
	// the resulting feeds and RNG positions; compare all physical dump fields.
	dump.RecsysDumpData = nil
	data, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func checkpointReviewState(t *testing.T, s *simulation.Scenario) []byte {
	t.Helper()
	switch w := s.Model.(type) {
	case *simulation.Float64ModelWrapper[dynamics.HKParams]:
		return checkpointReviewTypedState(t, w.M)
	case *simulation.Float64ModelWrapper[dynamics.DeffuantParams]:
		return checkpointReviewTypedState(t, w.M)
	case *simulation.BoolModelWrapper[dynamics.GalamParams]:
		return checkpointReviewTypedState(t, w.M)
	case *simulation.BoolModelWrapper[dynamics.VoterParams]:
		return checkpointReviewTypedState(t, w.M)
	default:
		t.Fatalf("unsupported review model %T", s.Model)
		return nil
	}
}

func checkpointReviewAdvance(t *testing.T, s *simulation.Scenario, feed *bytes.Buffer) checkpointReviewStep {
	t.Helper()
	feed.Reset()
	changed, delta := s.Step()
	return checkpointReviewStep{
		state: checkpointReviewState(t, s), feed: bytes.Clone(feed.Bytes()),
		changed: changed, delta: delta, stable: s.StableSteps,
	}
}

func checkpointReviewEqual(t *testing.T, got, want checkpointReviewStep, step int) {
	t.Helper()
	if !bytes.Equal(got.state, want.state) {
		t.Fatalf("step %d: physical state or an RNG stream differs after recovery", step)
	}
	if !bytes.Equal(got.feed, want.feed) {
		t.Fatalf("step %d: ordered feed, placement details or events differ after recovery", step)
	}
	if got.changed != want.changed || got.delta != want.delta || got.stable != want.stable {
		t.Fatalf("step %d: convergence output differs: changed=%d/%d delta=%g/%g stable=%d/%d", step,
			got.changed, want.changed, got.delta, want.delta, got.stable, want.stable)
	}
}

func TestCheckpointReviewExactContinuationMatrix(t *testing.T) {
	for _, kind := range []string{"HK", "Deffuant", "Galam", "Voter"} {
		for _, recommender := range []string{"Random", "Opinion", "Structure", "OpinionRandom", "StructureRandom", "OpinionM9", "StructureM9"} {
			for _, retain := range []int{0, 3} {
				for _, cache := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/retain%d/cache%t", kind, recommender, retain, cache), func(t *testing.T) {
						const horizon = 10
						metadata := checkpointReviewMetadata(kind, recommender, retain, cache)
						parent := inMemory(metadata)
						if err := parent.InitError(); err != nil {
							t.Fatal(err)
						}
						var parentFeed bytes.Buffer
						checkpointReviewCapture(t, parent.Model, &parentFeed)
						expected := make([]checkpointReviewStep, horizon+1)
						expected[0] = checkpointReviewStep{state: checkpointReviewState(t, parent)}
						snapshots := make(map[int]*simulation.RawSnapshotData)
						for step := 0; step <= horizon; step++ {
							if step > 0 {
								expected[step] = checkpointReviewAdvance(t, parent, &parentFeed)
							}
							if step == 0 || step == 1 || step == 4 || step == 7 {
								snapshot, err := parent.Snapshot()
								if err != nil {
									t.Fatal(err)
								}
								snapshots[step] = snapshot
							}
						}
						for _, cut := range []int{0, 1, 4, 7} {
							prepared, err := simulation.PrepareCheckpoint(snapshots[cut])
							if err != nil {
								t.Fatal(err)
							}
							for _, reuse := range []bool{false, true} {
								t.Run(fmt.Sprintf("cut%d/prepared%t", cut, reuse), func(t *testing.T) {
									resumed := inMemory(cloneCheckpointReviewMetadata(metadata))
									if reuse {
										err = resumed.InitFromPreparedCheckpoint(prepared, false)
									} else {
										err = resumed.InitFromCheckpoint(snapshots[cut], false)
									}
									if err != nil {
										t.Fatal(err)
									}
									if !bytes.Equal(checkpointReviewState(t, resumed), expected[cut].state) || resumed.StableSteps != expected[cut].stable {
										t.Fatalf("cut %d: restored state or halt counter differs before continuation", cut)
									}
									var feed bytes.Buffer
									checkpointReviewCapture(t, resumed.Model, &feed)
									for step := cut + 1; step <= horizon; step++ {
										checkpointReviewEqual(t, checkpointReviewAdvance(t, resumed, &feed), expected[step], step)
									}
								})
							}
							t.Run(fmt.Sprintf("cut%d/isolatedBranches", cut), func(t *testing.T) {
								checkpointReviewBranchIsolation(t, metadata, snapshots[cut], prepared)
							})
						}
					})
				}
			}
		}
	}
}

func checkpointReviewBranchIsolation(t *testing.T, metadata *simulation.ScenarioMetadata, raw *simulation.RawSnapshotData, prepared *simulation.PreparedCheckpoint) {
	t.Helper()
	restore := func(reuse bool) *simulation.Scenario {
		m := cloneCheckpointReviewMetadata(metadata)
		m.RNG = smprng.FixedSpec(817, 912)
		s := inMemory(m)
		var err error
		if reuse {
			err = s.InitFromPreparedCheckpoint(prepared, true)
		} else {
			err = s.InitFromCheckpoint(raw, true)
		}
		if err != nil {
			t.Fatal(err)
		}
		wantRNG, err := smprng.MustNewPool(m.RNG).Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		gotRNG, err := s.RNG.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotRNG, wantRNG) {
			t.Fatal("restoring an independent branch consumed its fresh RNG streams")
		}
		return s
	}
	left, waiting := restore(true), restore(true)
	before := checkpointReviewState(t, waiting)
	for range 3 {
		left.Step()
	}
	if !bytes.Equal(checkpointReviewState(t, waiting), before) {
		t.Fatal("advancing one prepared branch mutated an idle sibling")
	}
	fresh, reference := restore(true), restore(false)
	if !bytes.Equal(checkpointReviewState(t, fresh), before) {
		t.Fatal("an advanced branch mutated the reusable prepared checkpoint")
	}
	var waitingFeed, freshFeed, referenceFeed bytes.Buffer
	checkpointReviewCapture(t, waiting.Model, &waitingFeed)
	checkpointReviewCapture(t, fresh.Model, &freshFeed)
	checkpointReviewCapture(t, reference.Model, &referenceFeed)
	for step := raw.CompletedStep + 1; step <= raw.CompletedStep+3; step++ {
		want := checkpointReviewAdvance(t, reference, &referenceFeed)
		checkpointReviewEqual(t, checkpointReviewAdvance(t, waiting, &waitingFeed), want, step)
		checkpointReviewEqual(t, checkpointReviewAdvance(t, fresh, &freshFeed), want, step)
	}
}

// Workers return errors and observations through a channel; testing.T is used
// only by the parent goroutine after every worker has finished.
type concurrentCheckpointReviewRecorder struct {
	state   func() ([]byte, error)
	advance func() (checkpointReviewStep, error)
}

func newTypedConcurrentCheckpointReviewRecorder[O, P any](s *simulation.Scenario, m *model.SMPModel[O, P]) concurrentCheckpointReviewRecorder {
	var feed bytes.Buffer
	var encodeErr error
	encoder := json.NewEncoder(&feed)
	encode := func(value any) {
		if encodeErr == nil {
			encodeErr = encoder.Encode(value)
		}
	}
	m.CaptureFeedDetails = true
	m.ObserveFeed = func(a *model.SMPAgent[O, P], observation model.FeedObservation[O]) {
		encode(a.ID)
		encode(observation)
	}
	m.EventLogger = func(event *model.EventRecord) { encode(event) }
	state := func() ([]byte, error) {
		dump := m.Dump()
		dump.RecsysDumpData = nil
		return json.Marshal(dump)
	}
	return concurrentCheckpointReviewRecorder{
		state: state,
		advance: func() (checkpointReviewStep, error) {
			feed.Reset()
			encodeErr = nil
			changed, delta := s.Step()
			if encodeErr != nil {
				return checkpointReviewStep{}, encodeErr
			}
			data, err := state()
			return checkpointReviewStep{
				state: data, feed: bytes.Clone(feed.Bytes()),
				changed: changed, delta: delta, stable: s.StableSteps,
			}, err
		},
	}
}

func newConcurrentCheckpointReviewRecorder(s *simulation.Scenario) (concurrentCheckpointReviewRecorder, error) {
	switch w := s.Model.(type) {
	case *simulation.Float64ModelWrapper[dynamics.HKParams]:
		return newTypedConcurrentCheckpointReviewRecorder(s, w.M), nil
	case *simulation.Float64ModelWrapper[dynamics.DeffuantParams]:
		return newTypedConcurrentCheckpointReviewRecorder(s, w.M), nil
	case *simulation.BoolModelWrapper[dynamics.GalamParams]:
		return newTypedConcurrentCheckpointReviewRecorder(s, w.M), nil
	case *simulation.BoolModelWrapper[dynamics.VoterParams]:
		return newTypedConcurrentCheckpointReviewRecorder(s, w.M), nil
	default:
		return concurrentCheckpointReviewRecorder{}, fmt.Errorf("unsupported concurrent review model %T", s.Model)
	}
}

func TestCheckpointReviewConcurrentPreparedBranches(t *testing.T) {
	for _, kind := range []string{"HK", "Deffuant", "Galam", "Voter"} {
		for _, recommender := range []string{"Structure", "StructureRandom", "StructureM9"} {
			t.Run(kind+"/"+recommender, func(t *testing.T) {
				const workers, steps = 4, 4
				metadata := checkpointReviewMetadata(kind, recommender, 3, true)
				parent := inMemory(metadata)
				if err := parent.InitError(); err != nil {
					t.Fatal(err)
				}
				for range 6 {
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
				branchMetadata := func(worker int) *simulation.ScenarioMetadata {
					m := cloneCheckpointReviewMetadata(metadata)
					m.RNG = smprng.FixedSpec(uint64(817+worker*97), 912)
					return m
				}
				// Independently decode the raw checkpoint for each reference before
				// concurrently exercising the shared prepared instance's first use.
				expected := make([][]checkpointReviewStep, workers)
				for worker := range workers {
					reference := inMemory(branchMetadata(worker))
					if err := reference.InitFromCheckpoint(raw, true); err != nil {
						t.Fatal(err)
					}
					expected[worker] = make([]checkpointReviewStep, steps+1)
					expected[worker][0] = checkpointReviewStep{state: checkpointReviewState(t, reference)}
					var feed bytes.Buffer
					checkpointReviewCapture(t, reference.Model, &feed)
					for step := 1; step <= steps; step++ {
						expected[worker][step] = checkpointReviewAdvance(t, reference, &feed)
					}
				}
				type result struct {
					worker int
					states []checkpointReviewStep
					err    error
				}
				start, ready, completed := make(chan struct{}), make(chan struct{}, workers), make(chan result, workers)
				for worker := range workers {
					go func() {
						out := result{worker: worker, states: make([]checkpointReviewStep, steps+1)}
						defer func() { completed <- out }()
						branch := inMemory(branchMetadata(worker))
						ready <- struct{}{}
						<-start
						if out.err = branch.InitFromPreparedCheckpoint(prepared, true); out.err != nil {
							return
						}
						var recorder concurrentCheckpointReviewRecorder
						if recorder, out.err = newConcurrentCheckpointReviewRecorder(branch); out.err != nil {
							return
						}
						if out.states[0].state, out.err = recorder.state(); out.err != nil {
							return
						}
						for step := 1; step <= steps; step++ {
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
				results := make([]result, workers)
				for range workers {
					out := <-completed
					results[out.worker] = out
				}
				for worker, out := range results {
					if out.err != nil {
						t.Errorf("worker %d: %v", worker, out.err)
						continue
					}
					for step, state := range out.states {
						checkpointReviewEqual(t, state, expected[worker][step], raw.CompletedStep+step)
					}
				}
			})
		}
	}
}
