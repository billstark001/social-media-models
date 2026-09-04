// Package batch implements the no-file JSONL simulation protocol used by
// smp-batch and Python scan orchestration.
package batch

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"smp/progress"
	smprng "smp/rng"
	"smp/simulation"
	"smp/terminal"
)

const SchemaVersion = 1

type Request struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Metadata      json.RawMessage `json:"metadata"`
	Output        OutputOptions   `json:"output"`
}

type OutputOptions struct {
	FinalOpinions bool             `json:"final_opinions"`
	Terminal      *TerminalOptions `json:"terminal,omitempty"`
}

type TerminalOptions struct {
	MajorMass          float64 `json:"major_mass"`
	PositionResolution float64 `json:"position_resolution"`
	MassResolution     float64 `json:"mass_resolution"`
}

type OpinionSummary struct {
	Count    int     `json:"count"`
	Mean     float64 `json:"mean"`
	Variance float64 `json:"variance"`
	Minimum  float64 `json:"minimum"`
	Maximum  float64 `json:"maximum"`
}

type Result struct {
	ProtocolSHA256 string           `json:"protocol_sha256"`
	RNG            smprng.Spec      `json:"resolved_rng"`
	Steps          int              `json:"steps"`
	StopReason     string           `json:"stop_reason"`
	Opinions       OpinionSummary   `json:"opinions"`
	Terminal       *terminal.Result `json:"terminal,omitempty"`
	FinalOpinions  []float64        `json:"final_opinions,omitempty"`
}

type ResponseError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type Response struct {
	SchemaVersion int            `json:"schema_version"`
	RequestID     string         `json:"request_id,omitempty"`
	Status        string         `json:"status"`
	Result        *Result        `json:"result,omitempty"`
	Error         *ResponseError `json:"error,omitempty"`
}

type ProgressEvent = progress.Event

type Options struct {
	Progress             io.Writer
	ProgressMode         string
	ProgressStepInterval int
}

func decodeRequest(line string) (Request, error) {
	var request Request
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return request, errors.New("request contains trailing JSON")
		}
		return request, err
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return request, errors.New("request_id must not be empty")
	}
	if request.SchemaVersion != SchemaVersion {
		return request, fmt.Errorf(
			"unsupported schema_version %d (expected %d)",
			request.SchemaVersion,
			SchemaVersion,
		)
	}
	if len(request.Metadata) == 0 || string(request.Metadata) == "null" {
		return request, errors.New("metadata must be a JSON object")
	}
	return request, nil
}

func decodeMetadata(raw json.RawMessage) (*simulation.ScenarioMetadata, error) {
	metadata := simulation.DefaultScenarioMetadata()
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(metadata); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("metadata contains trailing JSON")
		}
		return nil, err
	}
	// The batch protocol never serializes histories or events. Collection flags
	// are observational only and can be disabled without changing RNG streams.
	metadata.AgentNumber = false
	metadata.OpinionSum = false
	metadata.RewiringEvent = false
	metadata.ViewPostsEvent = false
	metadata.PostEvent = false
	return metadata, nil
}

func normalizeOutput(output OutputOptions) (OutputOptions, error) {
	if output.Terminal == nil {
		return output, nil
	}
	if output.Terminal.MajorMass == 0 {
		output.Terminal.MajorMass = 0.02
	}
	if !isFinite(output.Terminal.MajorMass) || output.Terminal.MajorMass <= 0 || output.Terminal.MajorMass > 1 {
		return output, errors.New("output.terminal.major_mass must be in (0,1]")
	}
	if !isFinite(output.Terminal.PositionResolution) || output.Terminal.PositionResolution < 0 {
		return output, errors.New("output.terminal.position_resolution must be finite and non-negative")
	}
	if !isFinite(output.Terminal.MassResolution) || output.Terminal.MassResolution < 0 {
		return output, errors.New("output.terminal.mass_resolution must be finite and non-negative")
	}
	return output, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func confidenceTolerance(metadata *simulation.ScenarioMetadata) (float64, error) {
	switch metadata.DynamicsType {
	case simulation.DynamicsTypeHK:
		return metadata.HKParams.Tolerance, nil
	case simulation.DynamicsTypeDeffuant:
		return metadata.DeffuantParams.Tolerance, nil
	default:
		return 0, fmt.Errorf("terminal component classification is not supported for %s", metadata.DynamicsType)
	}
}

func protocolSHA256(metadata *simulation.ScenarioMetadata, output OutputOptions) (string, error) {
	payload := struct {
		SchemaVersion int                          `json:"schema_version"`
		Metadata      *simulation.ScenarioMetadata `json:"metadata"`
		Output        OutputOptions                `json:"output"`
	}{SchemaVersion: SchemaVersion, Metadata: metadata, Output: output}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func summarizeOpinions(values []float64) OpinionSummary {
	result := OpinionSummary{Count: len(values)}
	if len(values) == 0 {
		return result
	}
	result.Minimum = math.Inf(1)
	result.Maximum = math.Inf(-1)
	mean := 0.0
	m2 := 0.0
	for index, value := range values {
		delta := value - mean
		mean += delta / float64(index+1)
		m2 += delta * (value - mean)
		result.Minimum = math.Min(result.Minimum, value)
		result.Maximum = math.Max(result.Maximum, value)
	}
	result.Mean = mean
	result.Variance = m2 / float64(len(values))
	return result
}

func emitProgress(writer io.Writer, mode string, event ProgressEvent) error {
	if writer == nil || mode == "" || mode == "none" {
		return nil
	}
	switch mode {
	case "jsonl":
		return progress.WriteJSONL(writer, event)
	case "human":
		if event.Type == "progress" {
			_, err := fmt.Fprintf(
				writer,
				"[%s] step %d/%d\n",
				event.RequestID,
				event.Step,
				event.MaxStep,
			)
			return err
		}
		_, err := fmt.Fprintf(writer, "[%s] %s\n", event.RequestID, event.Type)
		return err
	default:
		return fmt.Errorf("unsupported progress mode %q", mode)
	}
}

func errorResponse(requestID, kind string, err error) Response {
	return Response{
		SchemaVersion: SchemaVersion,
		RequestID:     requestID,
		Status:        "error",
		Error: &ResponseError{
			Kind:    kind,
			Message: err.Error(),
		},
	}
}

// Run reads JSONL requests and writes one response per non-empty input line.
// Request failures are recoverable and do not stop later batch items.
func Run(ctx context.Context, input io.Reader, output io.Writer, options Options) error {
	if options.ProgressMode == "" {
		options.ProgressMode = "none"
	}
	if options.ProgressMode != "none" && options.ProgressMode != "human" && options.ProgressMode != "jsonl" {
		return fmt.Errorf("unsupported progress mode %q", options.ProgressMode)
	}
	if options.ProgressStepInterval < 0 {
		return errors.New("progress step interval must be non-negative")
	}

	writer := bufio.NewWriter(output)
	writeResponse := func(response Response) error {
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			return err
		}
		return writer.Flush()
	}

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		request, err := decodeRequest(line)
		if err != nil {
			if writeErr := writeResponse(errorResponse(request.RequestID, "invalid_request", err)); writeErr != nil {
				return writeErr
			}
			continue
		}
		metadata, err := decodeMetadata(request.Metadata)
		if err != nil {
			if writeErr := writeResponse(errorResponse(request.RequestID, "invalid_metadata", err)); writeErr != nil {
				return writeErr
			}
			continue
		}
		request.Output, err = normalizeOutput(request.Output)
		if err != nil {
			if writeErr := writeResponse(errorResponse(request.RequestID, "invalid_output", err)); writeErr != nil {
				return writeErr
			}
			continue
		}

		if err := emitProgress(options.Progress, options.ProgressMode, ProgressEvent{
			RequestID: request.RequestID,
			Type:      progress.TypeStart,
			MaxStep:   metadata.MaxSimulationStep,
		}); err != nil {
			return err
		}
		scenario := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{
			EnableDumps:          false,
			Quiet:                true,
			ProgressStepInterval: options.ProgressStepInterval,
			ProgressCallback: func(scenarioProgress simulation.ScenarioProgress) {
				_ = emitProgress(options.Progress, options.ProgressMode, ProgressEvent{
					RequestID: request.RequestID,
					Type:      progress.TypeProgress,
					Step:      scenarioProgress.Step,
					MaxStep:   scenarioProgress.MaxStep,
				})
			},
		})
		if err := scenario.InitError(); err != nil {
			if writeErr := writeResponse(errorResponse(request.RequestID, "initialization", err)); writeErr != nil {
				return writeErr
			}
			continue
		}
		digest, err := protocolSHA256(metadata, request.Output)
		if err != nil {
			return err
		}
		run := scenario.StepTillEndResult(ctx)
		opinions := scenario.Model.GetOpinions()
		result := &Result{
			ProtocolSHA256: digest,
			RNG:            metadata.RNG,
			Steps:          run.Step,
			StopReason:     run.StopReason,
			Opinions:       summarizeOpinions(opinions),
		}
		if request.Output.Terminal != nil {
			epsilon, toleranceErr := confidenceTolerance(metadata)
			if toleranceErr != nil {
				if writeErr := writeResponse(errorResponse(request.RequestID, "terminal_classification", toleranceErr)); writeErr != nil {
					return writeErr
				}
				continue
			}
			classification, classifyErr := terminal.ClassifyOpinions(
				opinions,
				epsilon,
				request.Output.Terminal.MajorMass,
				request.Output.Terminal.PositionResolution,
				request.Output.Terminal.MassResolution,
			)
			if classifyErr != nil {
				if writeErr := writeResponse(errorResponse(request.RequestID, "terminal_classification", classifyErr)); writeErr != nil {
					return writeErr
				}
				continue
			}
			result.Terminal = &classification
		}
		if request.Output.FinalOpinions {
			result.FinalOpinions = opinions
		}
		status := "ok"
		if !run.Completed {
			status = "cancelled"
		}
		if err := writeResponse(Response{
			SchemaVersion: SchemaVersion,
			RequestID:     request.RequestID,
			Status:        status,
			Result:        result,
		}); err != nil {
			return err
		}
		if err := emitProgress(options.Progress, options.ProgressMode, ProgressEvent{
			RequestID:  request.RequestID,
			Type:       progress.TypeDone,
			Step:       run.Step,
			MaxStep:    metadata.MaxSimulationStep,
			StopReason: run.StopReason,
		}); err != nil {
			return err
		}
		if !run.Completed {
			return ctx.Err()
		}
	}
	return scanner.Err()
}
