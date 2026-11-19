package simulation_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"

	"github.com/vmihailenco/msgpack/v5"
)

func researchMetadata() *simulation.ScenarioMetadata {
	m := simulation.DefaultScenarioMetadata()
	m.UniqueName = "run"
	m.DynamicsType = simulation.DynamicsTypeHK
	m.HKParams = dynamics.HKParams{Influence: .08, Tolerance: .45, RewiringRate: .2, RepostRate: .3}
	m.NodeCount, m.NodeFollowCount, m.PostRetainCount = 24, 5, 2
	m.MaxSimulationStep = 12
	m.RecsysFactoryType = "OpinionM9"
	m.RecSysParams = map[string]any{"LogRecommendations": false}
	m.RNG = smprng.FixedSpec(17, 23)
	return m
}

func traceRows(t *testing.T, path string) []any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	z, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var rows []any
	decoder := json.NewDecoder(z)
	for {
		var row any
		if err := decoder.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestResearchResumesPolicyObservationsAndTrace(t *testing.T) {
	at, threshold := 3, 1.0
	for _, change := range []*simulation.PolicyChange{
		{Recommender: "StructureM9", At: &at, Duration: 5, Bridge: true, HideReposts: true},
		{Recommender: "StructureRandom", ExcessAtLeast: &threshold, Latest: 3, Duration: 5},
		{Recommender: "OpinionM9", At: &at, Duration: 5},
	} {
		for _, interruptedAt := range []int{2, 4, 8, 9} {
			t.Run(change.Recommender+string(rune('A'+interruptedAt)), func(t *testing.T) {
				run := func(dir string, resume bool) (*simulation.ResearchResult, model.SMPModelDumpData[float64, dynamics.HKParams]) {
					t.Helper()
					trace := filepath.Join(dir, "run", "feed.jsonl.gz")
					options := simulation.ScenarioOptions{EnableDumps: true, Quiet: true, FixedHorizon: true,
						Research: &simulation.ResearchOptions{ObserveAt: []int{2, 5, 12}, PolicyChange: change, ExposureTrace: trace, TraceStart: 2}}
					s := simulation.NewScenarioWithOptions(dir, researchMetadata(), options)
					if err := s.InitError(); err != nil {
						t.Fatal(err)
					}
					if resume {
						for i := 0; i < interruptedAt; i++ {
							s.Step()
						}
						s.Dump()
						for i := 0; i < 2 && s.Model.GetCurStep() <= 12; i++ {
							s.Step()
						}
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						s = simulation.NewScenarioWithOptions(dir, researchMetadata(), options)
						if !s.Load() {
							t.Fatal("could not restore interrupted research run")
						}
					}
					result, err := s.ResearchResult(s.StepTillEndResult(context.Background()))
					if err != nil {
						t.Fatal(err)
					}
					snap, err := s.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					if _, err := simulation.PrepareCheckpoint(snap); err == nil {
						t.Fatal("batch-style restore ignored research recovery state")
					}
					var dump model.SMPModelDumpData[float64, dynamics.HKParams]
					if err := msgpack.Unmarshal(snap.Data, &dump); err != nil {
						t.Fatal(err)
					}
					dump.RecsysDumpData = nil
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					return result, dump
				}
				fullDir, resumedDir := t.TempDir(), t.TempDir()
				full, fullDump := run(fullDir, false)
				resumed, resumedDump := run(resumedDir, true)
				if !reflect.DeepEqual(fullDump, resumedDump) {
					t.Fatal("physical state or RNG changed across research resume")
				}
				if !reflect.DeepEqual(traceRows(t, full.ExposureTrace.Path), traceRows(t, resumed.ExposureTrace.Path)) {
					t.Fatal("exposure trace changed across resume")
				}
				full.ExposureTrace.Path, resumed.ExposureTrace.Path = "", ""
				if !reflect.DeepEqual(full, resumed) {
					t.Fatalf("research summary changed across resume:\n%+v\n%+v", full, resumed)
				}
				if full.Endpoints[0].Exposure.Updates != 0 || full.Endpoints[1].Exposure.Updates != 3*24 || full.Endpoints[2].Exposure.Updates != 7*24 {
					t.Fatal("prefix exposure leaked into paired observation window")
				}
				if full.ExposureTrace.Updates != 10*24 {
					t.Fatal("trace retained prefix updates")
				}
			})
		}
	}
}

func TestResearchOptionsCannotChangeOnResume(t *testing.T) {
	dir := t.TempDir()
	options := simulation.ScenarioOptions{EnableDumps: true, Quiet: true, FixedHorizon: true, Research: &simulation.ResearchOptions{ObserveAt: []int{0, 12}}}
	s := simulation.NewScenarioWithOptions(dir, researchMetadata(), options)
	if err := s.InitError(); err != nil {
		t.Fatal(err)
	}
	s.Step()
	s.Dump()
	s.Close()
	options.Research = &simulation.ResearchOptions{ObserveAt: []int{0, 6, 12}}
	s = simulation.NewScenarioWithOptions(dir, researchMetadata(), options)
	defer s.Close()
	if s.Load() {
		t.Fatal("resume accepted a changed observation plan")
	}
}

func TestFixedHorizonKeepsCollectingAfterStableState(t *testing.T) {
	m := researchMetadata()
	m.MaxSimulationStep = 100
	m.HKParams.Influence, m.HKParams.RewiringRate = 0, 0
	s := simulation.NewScenarioWithOptions("", m, simulation.ScenarioOptions{Quiet: true, FixedHorizon: true})
	if err := s.InitError(); err != nil {
		t.Fatal(err)
	}
	if run := s.StepTillEndResult(context.Background()); run.Step != 100 || run.StopReason != "horizon" {
		t.Fatalf("fixed horizon stopped early: %+v", run)
	}
}

func TestResearchRejectsUnknownVersionBeforeUpgrading(t *testing.T) {
	for _, version := range []int{-1, simulation.CurrentDataVersion + 1} {
		metadata := researchMetadata()
		metadata.DataVersion = version
		s := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{Quiet: true, Research: &simulation.ResearchOptions{ObserveAt: []int{0, 12}}})
		if err := s.InitError(); err == nil {
			t.Fatalf("accepted unknown research version %d", version)
		}
	}
}

func TestOrdinarySnapshotRetainsItsVersionTwoWireShape(t *testing.T) {
	old := struct {
		DynamicsType  string
		Data          []byte
		Metadata      *simulation.ScenarioMetadata
		CompletedStep int
		StableSteps   int
	}{DynamicsType: "HK", Data: []byte{1, 2}, CompletedStep: 4, StableSteps: 2}
	current := simulation.RawSnapshotData{DynamicsType: old.DynamicsType, Data: old.Data, CompletedStep: old.CompletedStep, StableSteps: old.StableSteps}
	before, err := msgpack.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	after, err := msgpack.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("disabled research extension changed legacy snapshot wire bytes")
	}
}
