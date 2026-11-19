package batch

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"smp/model"
)

func TestFailedTracePreservesPublishedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feeds.jsonl.gz")
	if err := os.WriteFile(path, []byte("previous completed trace"), 0600); err != nil {
		t.Fatal(err)
	}
	trace, err := openExposureTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.abort()
	trace.record(1, 0, &model.FeedDetails[float64]{Opinion: math.NaN()})
	if trace.err == nil || trace.receipt.Updates != 0 {
		t.Fatal("failed JSON encoding counted an update")
	}
	if _, err := trace.finish(); err == nil {
		t.Fatal("failed trace was published")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous completed trace" {
		t.Fatal("failed trace replaced a completed file")
	}
}

func TestTraceRejectsAliasedCheckpointPaths(t *testing.T) {
	dir := t.TempDir()
	checkpoint := filepath.Join(dir, "source.jsonl.gz")
	if err := os.WriteFile(checkpoint, []byte("checkpoint bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.jsonl.gz")
	if err := os.Symlink(checkpoint, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateTracePath(alias, &ExperimentSpec{Checkpoint: checkpoint}, 0); err == nil {
		t.Fatal("trace aliases its source checkpoint")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTracePath(checkpoint, &ExperimentSpec{Checkpoint: relative}, 0); err == nil {
		t.Fatal("relative and absolute source paths were considered different")
	}
	if err := validateTracePath(checkpoint, &ExperimentSpec{CheckpointAt: map[int]string{2: checkpoint}}, 0); err == nil {
		t.Fatal("trace aliases an output checkpoint")
	}
}
