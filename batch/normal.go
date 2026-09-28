package batch

import (
	"context"

	"smp/observables"
	"smp/progress"
	"smp/simulation"
	"smp/terminal"
)

func runNormal(ctx context.Context, metadata *simulation.ScenarioMetadata, request Request, options Options) (Response, error) {
	if err := emitProgress(options.Progress, options.ProgressMode, ProgressEvent{RequestID: request.RequestID, Type: progress.TypeStart, MaxStep: metadata.MaxSimulationStep}); err != nil {
		return Response{}, err
	}
	scenario := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{
		EnableDumps:          false,
		Quiet:                true,
		ProgressStepInterval: options.ProgressStepInterval,
		ProgressCallback: func(event simulation.ScenarioProgress) {
			_ = emitProgress(options.Progress, options.ProgressMode, ProgressEvent{RequestID: request.RequestID, Type: progress.TypeProgress, Step: event.Step, MaxStep: event.MaxStep})
		},
	})
	if err := scenario.InitError(); err != nil {
		return errorResponse(request.RequestID, "initialization", err), nil
	}
	digest, err := protocolSHA256(metadata, request.Output, nil, "")
	if err != nil {
		return Response{}, err
	}
	run := scenario.StepTillEndResult(ctx)
	opinions := scenario.Model.GetOpinions()
	summary := summarizeOpinions(opinions)
	result := &Result{ProtocolSHA256: digest, RNG: metadata.RNG, Steps: run.Step, StopReason: run.StopReason, Opinions: &summary}
	if kind, err := measureNormalResult(metadata, request.Output, scenario, result); err != nil {
		return errorResponse(request.RequestID, kind, err), nil
	}
	status := "ok"
	if !run.Completed {
		status = "cancelled"
	}
	return Response{SchemaVersion: SchemaVersion, RequestID: request.RequestID, Status: status, Result: result}, nil
}

func measureNormalResult(metadata *simulation.ScenarioMetadata, output OutputOptions, scenario *simulation.Scenario, result *Result) (string, error) {
	opinions := scenario.Model.GetOpinions()
	graph := scenario.Model.GetGraph()
	if output.Energy {
		epsilon, err := confidenceTolerance(metadata)
		if err != nil {
			return "measurement", err
		}
		value, err := observables.MeasureEnergy(opinions, graph, epsilon, 1)
		if err != nil {
			return "measurement", err
		}
		result.Energy = &value
	}
	if len(output.Bins) > 0 {
		value, err := observables.MeasureBinnedState(opinions, graph, output.Bins)
		if err != nil {
			return "measurement", err
		}
		result.Binned = &value
	}
	if output.Terminal != nil {
		epsilon, err := confidenceTolerance(metadata)
		if err != nil {
			return "terminal_classification", err
		}
		classification, err := terminal.ClassifyOpinions(opinions, epsilon, output.Terminal.MajorMass, output.Terminal.PositionResolution, output.Terminal.MassResolution)
		if err != nil {
			return "terminal_classification", err
		}
		result.Terminal = &classification
	}
	if output.FinalOpinions {
		result.FinalOpinions = opinions
	}
	return "", nil
}
