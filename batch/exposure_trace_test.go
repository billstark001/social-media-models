package batch

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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
	t.Chdir(dir)
	checkpoint := filepath.Join(dir, "source.jsonl.gz")
	if err := os.WriteFile(checkpoint, []byte("checkpoint bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("symlink", func(t *testing.T) {
		alias := filepath.Join(dir, "symlink.jsonl.gz")
		if err := os.Symlink(checkpoint, alias); err != nil {
			if runtime.GOOS == "windows" && (errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314))) {
				t.Skipf("creating symlinks requires Windows privileges: %v", err)
			}
			t.Fatal(err)
		}
		if err := validateTracePath(alias, &ExperimentSpec{Checkpoint: checkpoint}, 0); err == nil {
			t.Fatal("trace aliases its source checkpoint")
		}
	})
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
