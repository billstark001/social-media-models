package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"smp/simulation"
	"smp/utils"
)

// persistentJob is the file protocol for reproducible full-history runs.
// Policy and observer state share the model's recovery snapshot.
type persistentJob struct {
	BasePath      string                       `json:"base_path"`
	Metadata      *simulation.ScenarioMetadata `json:"metadata"`
	PolicyChange  *simulation.PolicyChange     `json:"policy_change,omitempty"`
	FixedHorizon  bool                         `json:"fixed_horizon"`
	ObserveAt     []int                        `json:"observe_at"`
	SummaryPath   string                       `json:"summary_path"`
	ExposureTrace string                       `json:"exposure_trace,omitempty"`
	TraceStart    int                          `json:"trace_start,omitempty"`
}

func readJob(path string) (*persistentJob, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	job := &persistentJob{Metadata: simulation.DefaultScenarioMetadata()}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(job); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("job file must contain exactly one JSON object")
	}
	if job.BasePath == "" || job.Metadata == nil || job.SummaryPath == "" {
		return nil, fmt.Errorf("job requires base_path, metadata and summary_path")
	}
	if job.Metadata.RNG.IsZero() {
		return nil, fmt.Errorf("persistent jobs require explicit RNG seeds")
	}
	if job.Metadata.DataVersion < 0 || job.Metadata.DataVersion > simulation.CurrentDataVersion {
		return nil, fmt.Errorf("unsupported job DataVersion %d", job.Metadata.DataVersion)
	}
	job.Metadata.DataVersion = simulation.CurrentDataVersion
	if err := job.Metadata.PrepareForNewRun(); err != nil {
		return nil, err
	}
	if err := job.Metadata.Validate(); err != nil {
		return nil, err
	}
	directory := filepath.Join(job.BasePath, job.Metadata.UniqueName)
	summary, err := filepath.Abs(job.SummaryPath)
	if err != nil {
		return nil, err
	}
	expected, err := filepath.Abs(filepath.Join(directory, "observations.json"))
	if err != nil || summary != expected {
		return nil, fmt.Errorf("summary_path must be the run's observations.json")
	}
	if job.ExposureTrace != "" {
		trace, err := filepath.Abs(job.ExposureTrace)
		if err != nil {
			return nil, err
		}
		if filepath.Dir(trace) != filepath.Dir(expected) {
			return nil, fmt.Errorf("exposure_trace must be inside its run directory")
		}
	}
	return job, nil
}

func runJob(ctx context.Context, path string) (int, error) {
	job, err := readJob(path)
	if err != nil {
		return 2, err
	}
	directory := filepath.Join(job.BasePath, job.Metadata.UniqueName)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return 1, err
	}
	lock, err := os.OpenFile(filepath.Join(directory, "job.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return 1, err
	}
	defer lock.Close()
	if err := lockJob(lock); err != nil {
		return 1, fmt.Errorf("persistent run is already active: %w", err)
	}
	defer unlockJob(lock)
	if err := freezeJob(filepath.Join(directory, "job.json"), job); err != nil {
		return 1, err
	}
	scenario := simulation.NewScenarioWithOptions(job.BasePath, job.Metadata, simulation.ScenarioOptions{
		EnableDumps: true, Quiet: true, OutputJSONProgress: true,
		FixedHorizon: job.FixedHorizon,
		Research: &simulation.ResearchOptions{ObserveAt: job.ObserveAt, PolicyChange: job.PolicyChange,
			ExposureTrace: job.ExposureTrace, TraceStart: job.TraceStart},
	})
	defer scenario.Close()
	initialized, err := initializeScenario(scenario)
	if err != nil {
		return 1, err
	}
	if initialized {
		if err := scenario.DumpError(); err != nil {
			return 1, err
		}
	}

	run := scenario.StepTillEndResult(ctx)
	if !run.Completed {
		if run.StopReason != "cancelled" {
			return 1, fmt.Errorf("persistent run failed: %s", run.StopReason)
		}
		return 130, nil
	}
	if run.StopReason == "already_finished" {
		run.StopReason = "horizon"
	}
	if run.Step != job.Metadata.MaxSimulationStep {
		return 1, fmt.Errorf("research run ended before its fixed horizon")
	}
	result, err := scenario.ResearchResult(run)
	if err != nil {
		return 1, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return 1, err
	}
	if err := utils.AtomicWriteFile(job.SummaryPath, data, 0644); err != nil {
		return 1, err
	}
	return 0, nil
}

func freezeJob(path string, job *persistentJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	saved, err := os.ReadFile(path)
	if err == nil {
		if !bytes.Equal(saved, data) {
			return fmt.Errorf("persistent job differs from the existing run")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return utils.AtomicWriteFile(path, data, 0644)
}

func initializeScenario(scenario *simulation.Scenario) (bool, error) {
	if scenario.Load() {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(scenario.BaseDir, scenario.Metadata.UniqueName, "events.db")); err == nil {
		return false, fmt.Errorf("existing run could not be restored; refusing to overwrite its history")
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := scenario.InitError(); err != nil {
		return false, err
	}
	return true, nil
}
