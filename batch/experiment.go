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

	"smp/dynamics"
	"smp/observables"
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
	switch spec.Mode {
	case "fresh":
		if spec.Checkpoint != "" {
			return errors.New("fresh experiment cannot take a checkpoint")
		}
	case "resume", "branch":
		if spec.Checkpoint == "" {
			return errors.New("experiment.checkpoint is required")
		}
	default:
		return errors.New("experiment.mode must be fresh, resume or branch")
	}
	if spec.Replicates < 1 || spec.Replicates > 10000 || (spec.Mode != "branch" && spec.Replicates != 1) {
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
	if spec.PolicyChange != nil && (spec.StopOnHalt || len(spec.CheckpointAt) > 0) {
		return errors.New("policy_change requires a fixed window without intermediate checkpoints")
	}
	if err := spec.PolicyChange.validate(metadata, last); err != nil {
		return err
	}
	if spec.Mode == "branch" && metadata.RNG.IsZero() {
		return errors.New("branch requires an explicit base RNG")
	}
	return nil
}

func runExperiment(ctx context.Context, metadata *simulation.ScenarioMetadata, request Request) (*Result, error) {
	spec := request.Experiment
	if path := request.Output.ExposureTrace; path != "" {
		if !request.Output.Pathway || spec == nil || spec.Replicates != 1 {
			return nil, errors.New("exposure_trace requires pathway observations and one replicate")
		}
	}
	if err := validateExperiment(spec, metadata); err != nil {
		return nil, err
	}
	if (request.Output.Pathway || spec.PolicyChange != nil) && metadata.DynamicsType != simulation.DynamicsTypeHK {
		return nil, errors.New("pathway observations and policy changes require HK")
	}
	var prepared *simulation.PreparedCheckpoint
	var sourceSHA string
	baseStep := 0
	if spec.Mode == "fresh" {
		if err := metadata.PrepareForNewRun(); err != nil {
			return nil, err
		}
	} else {
		snapshot, digest, err := simulation.ReadCheckpoint(spec.Checkpoint)
		if err != nil {
			return nil, err
		}
		sourceSHA = digest
		baseStep = snapshot.CompletedStep
		if err := simulation.ValidateCheckpointMetadata(snapshot.Metadata, metadata); err != nil {
			return nil, err
		}
		if spec.Mode == "resume" {
			saved, err := resumeSpec(snapshot.Metadata.RNG, metadata.RNG)
			if err != nil {
				return nil, err
			}
			copyMetadata := *metadata
			copyMetadata.RNG = saved
			metadata = &copyMetadata
		}
		prepared, err = simulation.PrepareCheckpoint(snapshot)
		if err != nil {
			return nil, err
		}
	}
	if path := request.Output.ExposureTrace; path != "" {
		if err := validateTracePath(path, spec, baseStep); err != nil {
			return nil, err
		}
	}
	digest, err := protocolSHA256(metadata, request.Output, spec, sourceSHA)
	if err != nil {
		return nil, err
	}
	result := &Result{ProtocolSHA256: digest, RNG: metadata.RNG, SourceSHA256: sourceSHA, StopReason: "horizon", Replicates: make([]ReplicateResult, 0, spec.Replicates)}
	for index := 0; index < spec.Replicates; index++ {
		rep, err := runReplicate(ctx, metadata, prepared, sourceSHA, spec, request.Output, index)
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

// A resumed run inherits the checkpoint root as well as its stream positions.
func resumeSpec(saved, requested smprng.Spec) (smprng.Spec, error) {
	if saved.IsZero() {
		return saved, errors.New("resume requires the checkpoint's RNG specification")
	}
	saved, err := smprng.Resolve(saved)
	if err != nil || requested.IsZero() {
		return saved, err
	}
	requested, err = smprng.Resolve(requested)
	if err != nil {
		return saved, err
	}
	if requested != saved {
		return saved, errors.New("resume RNG does not match the checkpoint; use branch for independent future streams")
	}
	return saved, nil
}

func runReplicate(ctx context.Context, metadata *simulation.ScenarioMetadata, prepared *simulation.PreparedCheckpoint, sourceSHA string, spec *ExperimentSpec, output OutputOptions, index int) (ReplicateResult, error) {
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
	var err error
	if spec.Mode == "fresh" {
		err = scenario.InitError()
	} else {
		err = scenario.InitFromPreparedCheckpoint(prepared, spec.Mode == "branch")
	}
	if err != nil {
		return rep, err
	}
	var trace *exposureTrace
	if output.ExposureTrace != "" {
		trace, err = openExposureTrace(output.ExposureTrace)
		if err != nil {
			return rep, err
		}
		defer trace.abort()
	}
	if err := observeReplicate(ctx, scenario, spec, output, index, &rep, trace); err != nil {
		return rep, err
	}
	rep.StoppedAt = scenario.Model.GetCurStep() - 1
	var finalOpinions []float64
	if output.FinalOpinions || output.Terminal != nil {
		finalOpinions = scenario.Model.GetOpinions()
	}
	if output.FinalOpinions {
		rep.FinalOpinions = finalOpinions
	}
	if output.Terminal != nil {
		epsilon, err := confidenceTolerance(&copyMetadata)
		if err != nil {
			return rep, err
		}
		classified, err := terminal.ClassifyOpinions(finalOpinions, epsilon, output.Terminal.MajorMass, output.Terminal.PositionResolution, output.Terminal.MassResolution)
		if err != nil {
			return rep, err
		}
		rep.Terminal = &classified
	}
	if trace != nil {
		rep.ExposureTrace, err = trace.finish()
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func observeReplicate(ctx context.Context, scenario *simulation.Scenario, spec *ExperimentSpec, output OutputOptions, index int, rep *ReplicateResult, trace *exposureTrace) error {
	baseStep := scenario.Model.GetCurStep() - 1
	change := spec.PolicyChange
	policy := scenario.Metadata.RecsysFactoryType
	var m *hkModel
	var exposure Exposure
	var measure observables.PathwayMeasurer
	var opinions []float64
	if output.Pathway || change != nil {
		m = scenario.Model.(*simulation.Float64ModelWrapper[dynamics.HKParams]).M
	}
	if output.Pathway {
		measureFeed(m, &exposure, trace)
	}
	if change != nil && change.ExcessAtLeast != nil {
		opinions = make([]float64, len(m.Schedule.Agents))
	}
	for _, h := range spec.ObserveAt {
		for scenario.Model.GetCurStep()-1 < baseStep+h {
			if err := ctx.Err(); err != nil {
				return err
			}
			elapsed := scenario.Model.GetCurStep() - 1 - baseStep
			if change != nil {
				if rep.SwitchStep == nil {
					trigger := false
					if change.At != nil {
						trigger = elapsed == *change.At
					} else {
						for _, agent := range m.Schedule.Agents {
							opinions[agent.ID] = agent.CurOpinion
						}
						hit, err := measure.ExcessAtLeast(opinions, m.Graph, m.AgentParams.Tolerance, *change.ExcessAtLeast)
						if err != nil {
							return err
						}
						trigger = hit
						if !trigger && elapsed == change.Latest {
							trigger = true
							rep.SwitchFallback = true
						}
					}
					if trigger {
						step := baseStep + elapsed
						rep.SwitchStep = &step
						if err := setPolicy(m, scenario.Metadata, policy, change.Recommender, change.Bridge, change.HideReposts); err != nil {
							return err
						}
						policy = change.Recommender
					}
				} else if change.Duration > 0 && baseStep+elapsed == *rep.SwitchStep+change.Duration {
					if err := setPolicy(m, scenario.Metadata, policy, scenario.Metadata.RecsysFactoryType, false, false); err != nil {
						return err
					}
				}
			}
			rewires, _ := scenario.Step()
			if trace != nil && trace.err != nil {
				return trace.err
			}
			if output.Pathway {
				exposure.Rewires += rewires
			}
			if spec.StopOnHalt && scenario.StableSteps > simulation.STOP_SIM_STEPS {
				rep.StopReason = "halt"
				return nil
			}
		}
		point, err := endpoint(scenario, h, output, &measure)
		if err != nil {
			return err
		}
		if err := saveEndpointCheckpoint(scenario, spec.CheckpointAt, h, index, &point); err != nil {
			return err
		}
		if output.Pathway {
			counts := exposure
			point.Exposure = &counts
			exposure = Exposure{}
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
	path = checkpointPath(path, index, point)
	if err := scenario.SaveCheckpointTo(path); err != nil {
		return err
	}
	point.Checkpoint = path
	return nil
}

func checkpointPath(path string, index int, point *Endpoint) string {
	path = strings.ReplaceAll(path, "{replicate}", fmt.Sprintf("%04d", index))
	return strings.ReplaceAll(path, "{step}", fmt.Sprintf("%09d", point.Step))
}
