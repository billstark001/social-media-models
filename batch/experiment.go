package batch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	smprng "smp/rng"
	"smp/simulation"
	"smp/terminal"
)

func deriveBranchSpec(base smprng.Spec, sourceSHA string, index int) smprng.Spec {
	payload, _ := json.Marshal(struct {
		Base   smprng.Spec
		Source string
		Index  int
	}{base, sourceSHA, index})
	sum := sha256.Sum256(payload)
	return smprng.FixedSpec(binary.BigEndian.Uint64(sum[:8]), binary.BigEndian.Uint64(sum[8:16]))
}

func validateExperiment(spec *ExperimentSpec, metadata *simulation.ScenarioMetadata) error {
	if spec == nil {
		return errors.New("missing experiment")
	}
	if spec.Checkpoint == "" {
		return errors.New("experiment.checkpoint is required")
	}
	if spec.Mode != "resume" && spec.Mode != "branch" {
		return errors.New("experiment.mode must be resume or branch")
	}
	if spec.Replicates < 1 || spec.Replicates > 10000 || (spec.Mode == "resume" && spec.Replicates != 1) {
		return errors.New("invalid experiment.replicates")
	}
	if len(spec.ObserveAt) == 0 {
		return errors.New("experiment.observe_at is empty")
	}
	last := -1
	for _, h := range spec.ObserveAt {
		if h <= last {
			return errors.New("experiment.observe_at must be non-negative and increasing")
		}
		last = h
	}
	for step, path := range spec.CheckpointAt {
		pos := sort.SearchInts(spec.ObserveAt, step)
		if path == "" || pos >= len(spec.ObserveAt) || spec.ObserveAt[pos] != step {
			return fmt.Errorf("checkpoint_at step %d is not observed", step)
		}
		if spec.Replicates > 1 && !strings.Contains(path, "{replicate}") {
			return errors.New("checkpoint_at needs {replicate} for multiple branches")
		}
	}
	if spec.Mode == "branch" && metadata.RNG.IsZero() {
		return errors.New("branch requires an explicit base RNG")
	}
	return nil
}

func runExperiment(ctx context.Context, metadata *simulation.ScenarioMetadata, request Request) (*Result, error) {
	spec := request.Experiment
	if err := validateExperiment(spec, metadata); err != nil {
		return nil, err
	}
	snapshot, sourceSHA, err := simulation.ReadCheckpoint(spec.Checkpoint)
	if err != nil {
		return nil, err
	}
	if err := simulation.ValidateCheckpointMetadata(snapshot.Metadata, metadata); err != nil {
		return nil, err
	}
	digest, err := protocolSHA256(metadata, request.Output, spec, sourceSHA)
	if err != nil {
		return nil, err
	}
	result := &Result{ProtocolSHA256: digest, RNG: metadata.RNG, SourceSHA256: sourceSHA, StopReason: "horizon", Replicates: make([]ReplicateResult, 0, spec.Replicates)}
	for index := 0; index < spec.Replicates; index++ {
		rep, err := runReplicate(ctx, metadata, snapshot, sourceSHA, spec, request.Output, index)
		if err != nil {
			return nil, err
		}
		result.Replicates = append(result.Replicates, rep)
		if rep.StoppedAt > result.Steps {
			result.Steps = rep.StoppedAt
		}
	}
	halted := 0
	for _, rep := range result.Replicates {
		if rep.StopReason == "halt" {
			halted++
		}
	}
	if halted == len(result.Replicates) {
		result.StopReason = "halt"
	} else if halted > 0 {
		result.StopReason = "mixed"
	}
	return result, nil
}

func runReplicate(ctx context.Context, metadata *simulation.ScenarioMetadata, snapshot *simulation.RawSnapshotData, sourceSHA string, spec *ExperimentSpec, output OutputOptions, index int) (ReplicateResult, error) {
	rep := ReplicateResult{Index: index, Endpoints: make([]Endpoint, 0, len(spec.ObserveAt)), StopReason: "horizon"}
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	copyMetadata := *metadata
	if spec.Mode == "branch" {
		copyMetadata.RNG = deriveBranchSpec(metadata.RNG, sourceSHA, index)
	}
	rep.RNG = copyMetadata.RNG
	scenario := simulation.NewScenarioWithOptions("", &copyMetadata, simulation.ScenarioOptions{EnableDumps: false, Quiet: true})
	if err := scenario.InitFromCheckpoint(snapshot, spec.Mode == "branch"); err != nil {
		return rep, err
	}
	if err := observeReplicate(ctx, scenario, spec, output, index, &rep); err != nil {
		return rep, err
	}
	rep.StoppedAt = scenario.Model.GetCurStep() - 1
	if output.Terminal != nil {
		epsilon, err := confidenceTolerance(&copyMetadata)
		if err != nil {
			return rep, err
		}
		classified, err := terminal.ClassifyOpinions(scenario.Model.GetOpinions(), epsilon, output.Terminal.MajorMass, output.Terminal.PositionResolution, output.Terminal.MassResolution)
		if err != nil {
			return rep, err
		}
		rep.Terminal = &classified
	}
	return rep, nil
}

func observeReplicate(ctx context.Context, scenario *simulation.Scenario, spec *ExperimentSpec, output OutputOptions, index int, rep *ReplicateResult) error {
	baseStep := scenario.Model.GetCurStep() - 1
	for _, h := range spec.ObserveAt {
		for scenario.Model.GetCurStep()-1 < baseStep+h {
			if err := ctx.Err(); err != nil {
				return err
			}
			scenario.Step()
			if spec.StopOnHalt && scenario.StableSteps > simulation.STOP_SIM_STEPS {
				rep.StopReason = "halt"
				return nil
			}
		}
		point, err := endpoint(scenario, h, output)
		if err != nil {
			return err
		}
		if err := saveEndpointCheckpoint(scenario, spec.CheckpointAt, h, index, &point); err != nil {
			return err
		}
		rep.Endpoints = append(rep.Endpoints, point)
	}
	return nil
}

func saveEndpointCheckpoint(scenario *simulation.Scenario, paths map[int]string, relativeStep, index int, point *Endpoint) error {
	path, ok := paths[relativeStep]
	if !ok {
		return nil
	}
	path = strings.ReplaceAll(path, "{replicate}", fmt.Sprintf("%04d", index))
	path = strings.ReplaceAll(path, "{step}", fmt.Sprintf("%09d", point.Step))
	if err := scenario.SaveCheckpointTo(path); err != nil {
		return err
	}
	point.Checkpoint = path
	return nil
}
