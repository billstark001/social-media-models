package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smp/dynamics"
	smprng "smp/rng"
	"smp/simulation"
)

func fixtureJob(t *testing.T) (string, *persistentJob) {
	t.Helper()
	root := t.TempDir()
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "fixture"
	metadata.DynamicsType = simulation.DynamicsTypeHK
	metadata.HKParams = dynamics.HKParams{Influence: .05, Tolerance: .45, RewiringRate: .1, RepostRate: .1}
	metadata.MaxSimulationStep = 6
	metadata.NodeCount, metadata.NodeFollowCount = 12, 3
	metadata.RNG = smprng.FixedSpec(10, 20)
	metadata.RecSysParams = map[string]any{"LogRecommendations": false}
	return filepath.Join(root, "input.json"), &persistentJob{
		BasePath: root, Metadata: metadata, ObserveAt: []int{2, 4, 6}, FixedHorizon: true,
		SummaryPath: filepath.Join(root, "fixture", "observations.json"),
	}
}

func writeJob(t *testing.T, path string, job *persistentJob) {
	t.Helper()
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentJobFreezesNativeConfigurationAndRebuildsSummary(t *testing.T) {
	path, job := fixtureJob(t)
	writeJob(t, path, job)
	if code, err := runJob(context.Background(), path); code != 0 || err != nil {
		t.Fatalf("first run: code=%d err=%v", code, err)
	}
	first, err := os.ReadFile(job.SummaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(job.SummaryPath); err != nil {
		t.Fatal(err)
	}
	if code, err := runJob(context.Background(), path); code != 0 || err != nil {
		t.Fatalf("restore: code=%d err=%v", code, err)
	}
	second, err := os.ReadFile(job.SummaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("finished-run summary changed after recovery")
	}
	job.Metadata.HKParams.Influence = .3
	writeJob(t, path, job)
	if code, err := runJob(context.Background(), path); code == 0 || err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("native job accepted changed dynamics: code=%d err=%v", code, err)
	}
}

func TestCancelledPersistentJobHasRecoverySnapshotAndNoFinishMark(t *testing.T) {
	path, job := fixtureJob(t)
	writeJob(t, path, job)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code, err := runJob(ctx, path); code != 130 || err != nil {
		t.Fatalf("cancel: code=%d err=%v", code, err)
	}
	files, err := filepath.Glob(filepath.Join(job.BasePath, job.Metadata.UniqueName, "snapshot-*.msgpack"))
	if err != nil || len(files) == 0 {
		t.Fatalf("missing initial recovery snapshot: %v", err)
	}
	finished, _ := filepath.Glob(filepath.Join(job.BasePath, job.Metadata.UniqueName, "finished-*"))
	if len(finished) != 0 {
		t.Fatal("cancelled run was marked finished")
	}
	if code, err := runJob(context.Background(), path); code != 0 || err != nil {
		t.Fatalf("resume: code=%d err=%v", code, err)
	}
}

func TestPersistentJobRejectsInvalidVersionsAndPaths(t *testing.T) {
	for _, version := range []int{-1, simulation.CurrentDataVersion + 1} {
		path, job := fixtureJob(t)
		job.Metadata.DataVersion = version
		writeJob(t, path, job)
		if _, err := readJob(path); err == nil {
			t.Fatalf("accepted DataVersion %d", version)
		}
	}
	path, job := fixtureJob(t)
	job.SummaryPath = filepath.Join(job.BasePath, "fixture", "metadata.json")
	writeJob(t, path, job)
	if _, err := readJob(path); err == nil {
		t.Fatal("accepted summary overwriting metadata")
	}
}

func TestPositionalEntryRefusesResearchHistoryWithoutOptions(t *testing.T) {
	path, job := fixtureJob(t)
	writeJob(t, path, job)
	if code, err := runJob(context.Background(), path); code != 0 || err != nil {
		t.Fatalf("first run: %d %v", code, err)
	}
	metadataPath := filepath.Join(job.BasePath, job.Metadata.UniqueName, "metadata.json")
	before, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	scenario := simulation.NewScenarioWithOptions(job.BasePath, job.Metadata, simulation.ScenarioOptions{EnableDumps: true, Quiet: true})
	defer scenario.Close()
	if _, err := initializeScenario(scenario); err == nil {
		t.Fatal("positional entry accepted a research run without its recovery options")
	}
	after, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed positional load changed metadata")
	}
}
