package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smp/probe"
	smprng "smp/rng"
	"smp/simulation"
)

func TestReadJSONLineDrainsOversizedInput(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 80)+"\n{}\ntail"), 16)
	if _, err := readJSONLine(reader, 32); !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("oversized request was not rejected: %v", err)
	}
	for _, want := range []string{"{}\n", "tail"} {
		line, err := readJSONLine(reader, 32)
		if err != nil || string(line) != want {
			t.Fatalf("lost later request after oversized line: %q, %v", line, err)
		}
	}
	if _, err := readJSONLine(reader, 32); !errors.Is(err, io.EOF) {
		t.Fatalf("missing end of stream: %v", err)
	}
}

func TestJSONLIndependentJobs(t *testing.T) {
	const state = `"states":[{"step":0,"graph":{"directed":true,"nodes":{"0":{},"1":{}},"adjacency":{"0":{"1":{}},"1":{}},"graph":{}},"opinions":[-0.5,0.5]}]`
	input := `{"request_id":"bad","version":1,"old_alias":true}` + "\n" +
		`{"request_id":"overflow","version":1,"measurements":["terminal"],"terminal":{"epsilon":0.45,"major_mass":0.02},"states":[{"opinions":[-1e308,1e308]}]}` + "\n" +
		`{"request_id":"static","version":1,"measurements":["pathway","energy","bins","terminal"],"epsilon":0.45,"bins":[-1,0,1],` + state + "}\n"
	var output, diagnostic bytes.Buffer
	if err := runCLI([]string{"--format", "jsonl"}, strings.NewReader(input), &output, &diagnostic); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var invalid, overflow, valid jsonReply
	if err := decoder.Decode(&invalid); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&overflow); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&valid); err != nil {
		t.Fatal(err)
	}
	if invalid.Status != "error" || invalid.RequestID != "bad" || !strings.Contains(invalid.Error, "old_alias") {
		t.Fatalf("obsolete/unknown fields were not rejected: %+v", invalid)
	}
	if overflow.Status != "error" || overflow.RequestID != "overflow" || overflow.Result != nil {
		t.Fatalf("non-serializable numeric output terminated the worker: %+v", overflow)
	}
	if valid.Status != "ok" || valid.RequestID != "static" || valid.Line != 3 || valid.Result == nil {
		t.Fatalf("valid job after invalid input failed: %+v", valid)
	}
	point := valid.Result.Results[0]
	if point.Pathway == nil || point.Energy == nil || point.Binned == nil || point.Terminal == nil || len(point.Points) != 0 {
		t.Fatalf("incomplete native static result: %+v", point)
	}
}

func TestDirectCheckpointCLI(t *testing.T) {
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "cli-checkpoint"
	metadata.DynamicsType = simulation.DynamicsTypeHK
	metadata.HKParams.Tolerance = 0.45
	metadata.NodeCount, metadata.NodeFollowCount = 8, 2
	metadata.RNG = smprng.FixedSpec(1, 2)
	scenario := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{Quiet: true})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "checkpoint.msgpack.lz4")
	if err := scenario.SaveCheckpointTo(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostic bytes.Buffer
	args := []string{"--checkpoint", path, "--measurements", "pathway,energy,bins,terminal", "--bins=-1,0,1"}
	if err := runCLI(args, strings.NewReader(""), &output, &diagnostic); err != nil {
		t.Fatal(err)
	}
	var response probe.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Precision != "checkpoint_f64" || response.Results[0].Pathway == nil {
		t.Fatalf("unexpected checkpoint response: %+v", response)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("offline checkpoint measurement modified its source")
	}
}

func TestDirectRunTerminalNeedsNoGraphOrPostReplay(t *testing.T) {
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "cli-run"
	metadata.DynamicsType = simulation.DynamicsTypeHK
	metadata.HKParams.Tolerance = 0.45
	metadata.NodeCount, metadata.NodeFollowCount = 8, 2
	metadata.RNG = smprng.FixedSpec(1, 2)
	metadata.PostEvent, metadata.RewiringEvent = false, false
	metadata.HKParams.RepostRate, metadata.HKParams.RewiringRate = 0.5, 0.5
	dir := t.TempDir()
	scenario := simulation.NewScenarioWithOptions(dir, metadata, simulation.ScenarioOptions{EnableDumps: true, Quiet: true})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	defer scenario.DB.Close()
	scenario.Step()
	scenario.Step()
	scenario.Dump()
	var output, diagnostic bytes.Buffer
	args := []string{"--run-directory", filepath.Join(dir, metadata.UniqueName), "--steps", "0,2", "--measurements", "terminal"}
	if err := runCLI(args, strings.NewReader(""), &output, &diagnostic); err != nil {
		t.Fatal(err)
	}
	var response probe.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 2 || response.Results[1].Step != 2 || response.Results[1].Terminal == nil {
		t.Fatalf("terminal-only run analysis was blocked by unavailable replay logs: %+v", response)
	}
}

func TestCLIRejectsUnusedOrConflictingSources(t *testing.T) {
	for _, args := range [][]string{
		{"--epsilon", "0.45"},
		{"--checkpoint", "a", "--run-directory", "b"},
		{"--checkpoint", "a", "--steps", "0"},
		{"--run-directory", "a", "--format", "jsonl"},
		{"--checkpoint", "a", "--archive-native"},
		{"--collection-profile", "plot"},
		{"--checkpoint", "a", "--channels="},
	} {
		if err := runCLI(args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("ambiguous or unused flags accepted: %v", args)
		}
	}
}

func TestDirectCollectionCLIWritesSmallReceiptAndOptionalArchive(t *testing.T) {
	metadata := simulation.DefaultScenarioMetadata()
	metadata.UniqueName = "cli-collection"
	metadata.DynamicsType = simulation.DynamicsTypeHK
	metadata.HKParams.Tolerance = .45
	metadata.NodeCount, metadata.NodeFollowCount = 8, 2
	metadata.RNG = smprng.FixedSpec(1, 2)
	scenario := simulation.NewScenarioWithOptions("", metadata, simulation.ScenarioOptions{Quiet: true})
	if err := scenario.InitError(); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(t.TempDir(), "state.msgpack.lz4")
	if err := scenario.SaveCheckpointTo(checkpoint); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "collected")
	var output, diagnostic bytes.Buffer
	args := []string{"--checkpoint", checkpoint, "--measurements", "groups,bridges", "--collection-profile", "plot", "--channels", "O,S", "--scopes", "union", "--products", "summaries,series", "--output-directory", directory, "--archive-native"}
	if err := runCLI(args, strings.NewReader(""), &output, &diagnostic); err != nil {
		t.Fatal(err)
	}
	var response probe.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Output == nil || !response.Output.Completed || response.Output.ArchiveNative == "" || len(response.Results) != 0 || response.Analysis == nil || response.Analysis.Observations != 1 || response.Analysis.Population != 8 {
		t.Fatalf("missing compact response: %+v", response)
	}
	if len(response.Analysis.Collection.Channels) != 2 || response.Analysis.Collection.Channels[0] != "O" || response.Output.Rows["snapshots"] != 0 || output.Len() > 32*1024 {
		t.Fatal("CLI lost selectors or returned full products")
	}
	for _, name := range []string{"results.sqlite", "metadata.json", "native.msgpack.zst"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "native.json")); !os.IsNotExist(err) {
		t.Fatal("native JSON was persisted")
	}
}

func TestJSONLHugeGridDoesNotPanicOrDiscardLaterJobs(t *testing.T) {
	input := `{"request_id":"huge","measurements":["landscape"],"epsilon":0.4,"grid":{"min":0,"max":1,"step":4.336808689942018e-19},"states":[{"step":0,"opinions":[0]}]}` + "\n" +
		`{"request_id":"next","measurements":["terminal"],"epsilon":0.4,"states":[{"step":0,"opinions":[0]}]}` + "\n"
	var output bytes.Buffer
	if err := runJSONL(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var first, next jsonReply
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&next); err != nil {
		t.Fatal(err)
	}
	if first.Status != "error" || next.Status != "ok" || next.RequestID != "next" {
		t.Fatalf("huge grid disrupted independent jobs: %+v, %+v", first, next)
	}
}
