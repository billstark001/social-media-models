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
	"sort"
	"strings"

	"smp/observables"
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
	Experiment    *ExperimentSpec `json:"experiment,omitempty"`
}

type ExperimentSpec struct {
	Checkpoint   string         `json:"checkpoint"`
	Mode         string         `json:"mode"`
	Replicates   int            `json:"replicates"`
	ObserveAt    []int          `json:"observe_at"`
	CheckpointAt map[int]string `json:"checkpoint_at,omitempty"`
	StopOnHalt   bool           `json:"stop_on_halt,omitempty"`
}

type OutputOptions struct {
	FinalOpinions bool             `json:"final_opinions"`
	Terminal      *TerminalOptions `json:"terminal,omitempty"`
	Energy        bool             `json:"energy,omitempty"`
	Bins          []float64        `json:"bins,omitempty"`
	FullState     bool             `json:"full_state,omitempty"`
}

type EdgePair [2]int64
type Endpoint struct {
	Step         int                      `json:"step"`
	RelativeStep int                      `json:"relative_step"`
	Energy       *observables.Energy      `json:"energy,omitempty"`
	Binned       *observables.BinnedState `json:"binned,omitempty"`
	Opinions     []float64                `json:"opinions,omitempty"`
	Edges        []EdgePair               `json:"edges,omitempty"`
	Checkpoint   string                   `json:"checkpoint,omitempty"`
}
type ReplicateResult struct {
	Index      int              `json:"index"`
	RNG        smprng.Spec      `json:"rng"`
	Endpoints  []Endpoint       `json:"endpoints"`
	Terminal   *terminal.Result `json:"terminal,omitempty"`
	StopReason string           `json:"stop_reason"`
	StoppedAt  int              `json:"stopped_at"`
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
	ProtocolSHA256 string                   `json:"protocol_sha256"`
	RNG            smprng.Spec              `json:"resolved_rng"`
	Steps          int                      `json:"steps"`
	StopReason     string                   `json:"stop_reason"`
	Opinions       *OpinionSummary          `json:"opinions,omitempty"`
	Terminal       *terminal.Result         `json:"terminal,omitempty"`
	FinalOpinions  []float64                `json:"final_opinions,omitempty"`
	SourceSHA256   string                   `json:"source_sha256,omitempty"`
	Replicates     []ReplicateResult        `json:"replicates,omitempty"`
	Energy         *observables.Energy      `json:"energy,omitempty"`
	Binned         *observables.BinnedState `json:"binned,omitempty"`
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
	if len(output.Bins) > 0 {
		if len(output.Bins) < 2 {
			return output, errors.New("output.bins needs at least two boundaries")
		}
		for i, x := range output.Bins {
			if !isFinite(x) || (i > 0 && x <= output.Bins[i-1]) {
				return output, errors.New("output.bins must be finite and increasing")
			}
		}
	}
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

func protocolSHA256(metadata *simulation.ScenarioMetadata, output OutputOptions, experiment *ExperimentSpec, sourceSHA string) (string, error) {
	payload := struct {
		SchemaVersion int                          `json:"schema_version"`
		Metadata      *simulation.ScenarioMetadata `json:"metadata"`
		Output        OutputOptions                `json:"output"`
		Experiment    *ExperimentSpec              `json:"experiment,omitempty"`
		SourceSHA256  string                       `json:"source_sha256,omitempty"`
	}{SchemaVersion: SchemaVersion, Metadata: metadata, Output: output, Experiment: experiment, SourceSHA256: sourceSHA}
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

func endpoint(scenario *simulation.Scenario, relative int, output OutputOptions) (Endpoint, error) {
	step := scenario.Model.GetCurStep() - 1
	result := Endpoint{Step: step, RelativeStep: relative}
	opinions := scenario.Model.GetOpinions()
	graph := scenario.Model.GetGraph()
	if output.Energy {
		epsilon, err := confidenceTolerance(scenario.Metadata)
		if err != nil {
			return result, err
		}
		energy, err := observables.MeasureEnergy(opinions, graph, epsilon, 1)
		if err != nil {
			return result, err
		}
		result.Energy = &energy
	}
	if len(output.Bins) > 0 {
		binned, err := observables.MeasureBinnedState(opinions, graph, output.Bins)
		if err != nil {
			return result, err
		}
		result.Binned = &binned
	}
	if output.FullState {
		result.Opinions = opinions
		nodes := graph.Nodes()
		for nodes.Next() {
			i := nodes.Node().ID()
			neighbors := graph.From(i)
			for neighbors.Next() {
				result.Edges = append(result.Edges, EdgePair{i, neighbors.Node().ID()})
			}
		}
		sort.Slice(result.Edges, func(i, j int) bool {
			if result.Edges[i][0] != result.Edges[j][0] {
				return result.Edges[i][0] < result.Edges[j][0]
			}
			return result.Edges[i][1] < result.Edges[j][1]
		})
	}
	return result, nil
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

func processRequest(ctx context.Context, line string, options Options) (Response, *ProgressEvent, error) {
	request, err := decodeRequest(line)
	if err != nil {
		return errorResponse(request.RequestID, "invalid_request", err), nil, nil
	}
	metadata, err := decodeMetadata(request.Metadata)
	if err != nil {
		return errorResponse(request.RequestID, "invalid_metadata", err), nil, nil
	}
	request.Output, err = normalizeOutput(request.Output)
	if err != nil {
		return errorResponse(request.RequestID, "invalid_output", err), nil, nil
	}
	if request.Experiment == nil {
		response, err := runNormal(ctx, metadata, request, options)
		if err != nil || response.Result == nil {
			return response, nil, err
		}
		done := &ProgressEvent{RequestID: request.RequestID, Type: progress.TypeDone, Step: response.Result.Steps, MaxStep: metadata.MaxSimulationStep, StopReason: response.Result.StopReason}
		return response, done, nil
	}
	result, err := runExperiment(ctx, metadata, request)
	if err != nil {
		return errorResponse(request.RequestID, "experiment", err), nil, nil
	}
	return Response{SchemaVersion: SchemaVersion, RequestID: request.RequestID, Status: "ok", Result: result}, nil, nil
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
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		response, done, err := processRequest(ctx, line, options)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
		if done != nil {
			if err := emitProgress(options.Progress, options.ProgressMode, *done); err != nil {
				return err
			}
		}
		if response.Status == "cancelled" {
			return ctx.Err()
		}
	}
	return scanner.Err()
}
