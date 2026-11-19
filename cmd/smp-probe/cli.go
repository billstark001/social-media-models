package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"smp/probe"

	"github.com/vmihailenco/msgpack/v5"
)

func commaValues(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func integerValues(value string) ([]int, error) {
	var result []int
	for _, item := range commaValues(value) {
		n, err := strconv.Atoi(item)
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", item, err)
		}
		result = append(result, n)
	}
	return result, nil
}

func floatValues(value string) ([]float64, error) {
	var result []float64
	for _, item := range commaValues(value) {
		n, err := strconv.ParseFloat(item, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid numeric value %q: %w", item, err)
		}
		result = append(result, n)
	}
	return result, nil
}

func runMsgpack(input io.Reader, output io.Writer) error {
	var request probe.Request
	decoder := msgpack.NewDecoder(input)
	decoder.SetCustomStructTag("json")
	decoder.DisallowUnknownFields(true)
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode request: expected one MsgPack object")
	}
	response, err := probe.Evaluate(&request)
	if err != nil {
		return fmt.Errorf("evaluate request: %w", err)
	}
	encoder := msgpack.NewEncoder(output)
	encoder.SetCustomStructTag("json")
	if err := encoder.Encode(response); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

func decodeJSON(input io.Reader, value any) error {
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func runJSON(input io.Reader, output io.Writer) error {
	var request probe.Request
	if err := decodeJSON(input, &request); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	response, err := probe.Evaluate(&request)
	if err != nil {
		return fmt.Errorf("evaluate request: %w", err)
	}
	return json.NewEncoder(output).Encode(response)
}

type jsonJob struct {
	RequestID string `json:"request_id"`
	probe.Request
}

type jsonReply struct {
	Line      int             `json:"line"`
	RequestID string          `json:"request_id,omitempty"`
	Status    string          `json:"status"`
	Result    *probe.Response `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

var errRequestTooLarge = errors.New("JSONL request exceeds 64 MiB")

// A rejected line is drained through its delimiter so later jobs remain usable.
func readJSONLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	tooLarge := false
	for {
		part, err := reader.ReadSlice('\n')
		if !tooLarge {
			if len(part) > limit-len(line) {
				tooLarge, line = true, nil
			} else {
				line = append(line, part...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if tooLarge {
			return nil, errRequestTooLarge
		}
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, io.EOF
		}
		return line, nil
	}
}

// JSONL jobs are independent: one invalid job does not discard later jobs.
func runJSONL(input io.Reader, output io.Writer) error {
	reader := bufio.NewReaderSize(input, 64*1024)
	line := 0
	for {
		data, err := readJSONLine(reader, 64*1024*1024)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil && !errors.Is(err, errRequestTooLarge) {
			return err
		}
		line++
		var job jsonJob
		reply := jsonReply{Line: line, Status: "ok"}
		if err == nil {
			err = decodeJSON(bytes.NewReader(data), &job)
		}
		reply.RequestID = job.RequestID
		if err == nil {
			reply.Result, err = probe.Evaluate(&job.Request)
		}
		if err != nil {
			reply.Status, reply.Error = "error", err.Error()
		}
		encoded, err := json.Marshal(reply)
		if err != nil {
			reply.Status, reply.Error, reply.Result = "error", "encode result: "+err.Error(), nil
			encoded, err = json.Marshal(reply)
			if err != nil {
				return err
			}
		}
		encoded = append(encoded, '\n')
		written, err := output.Write(encoded)
		if err != nil {
			return err
		}
		if written != len(encoded) {
			return io.ErrShortWrite
		}
	}
}

func runCLI(args []string, input io.Reader, output, diagnostic io.Writer) error {
	flags := flag.NewFlagSet("smp-probe", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	format := flags.String("format", "", "Input/output encoding: msgpack, json, or jsonl")
	runDirectory := flags.String("run-directory", "", "Measure a persistent SMP run directory")
	var checkpoints []string
	flags.Func("checkpoint", "Measure a checkpoint; repeat for multiple checkpoints", func(value string) error {
		checkpoints = append(checkpoints, value)
		return nil
	})
	steps := flags.String("steps", "", "Comma-separated completed steps for a run; default: last recorded step")
	measurements := flags.String("measurements", "energy,pathway", "Comma-separated measurements")
	epsilon := flags.String("epsilon", "", "Confidence radius; default: source dynamics tolerance")
	bins := flags.String("bins", "", "Comma-separated bin edges")
	gridStep := flags.Float64("grid-step", 0.02, "Opinion grid spacing for force/landscape")
	replicates := flags.Int("replicates", 1, "Counterfactual recommendation replicates")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	direct := *runDirectory != "" || len(checkpoints) != 0
	if !direct {
		var unused string
		flags.Visit(func(f *flag.Flag) {
			if f.Name != "format" {
				unused = f.Name
			}
		})
		if unused != "" {
			return fmt.Errorf("--%s requires --run-directory or --checkpoint", unused)
		}
		switch *format {
		case "", "msgpack":
			return runMsgpack(input, output)
		case "json":
			return runJSON(input, output)
		case "jsonl":
			return runJSONL(input, output)
		default:
			return fmt.Errorf("unknown format %q", *format)
		}
	}
	if *format != "" && *format != "json" {
		return errors.New("direct source flags produce JSON; use --format jsonl for batched stdin requests")
	}
	if *runDirectory != "" && len(checkpoints) != 0 {
		return errors.New("choose run_directory or checkpoints")
	}
	if len(checkpoints) != 0 && *steps != "" {
		return errors.New("--steps applies only to --run-directory")
	}
	request := probe.Request{
		Version: probe.ProtocolVersion, RunDirectory: *runDirectory, CheckpointPaths: checkpoints,
		Measurements: commaValues(*measurements), Replicates: *replicates,
		Grid: probe.Grid{Min: -1, Max: 1, Step: *gridStep},
	}
	var err error
	if request.Steps, err = integerValues(*steps); err != nil {
		return err
	}
	if request.Bins, err = floatValues(*bins); err != nil {
		return err
	}
	if *epsilon != "" {
		value, err := strconv.ParseFloat(*epsilon, 64)
		if err != nil {
			return fmt.Errorf("invalid epsilon: %w", err)
		}
		request.Epsilon = &value
	}
	response, err := probe.Evaluate(&request)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(response)
}
