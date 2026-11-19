package batch

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"smp/model"
)

type TraceReceipt struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	Updates int    `json:"updates"`
	Slots   int    `json:"slots"`
}

type traceFeed struct {
	Step      int   `json:"step"`
	Recipient int64 `json:"recipient"`
	*model.FeedDetails[float64]
}

type exposureTrace struct {
	file       *os.File
	compressed *gzip.Writer
	encoder    *json.Encoder
	receipt    TraceReceipt
	err        error
}

func tracePathIdentity(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// For a new destination, resolve its existing parent if available.
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(parent, filepath.Base(path))
	}
	return path, nil
}

func validateTracePath(path string, spec *ExperimentSpec, baseStep int) error {
	if !strings.HasSuffix(path, ".jsonl.gz") {
		return errors.New("exposure trace path must end in .jsonl.gz")
	}
	target, err := tracePathIdentity(path)
	if err != nil {
		return err
	}
	checkpoints := make([]string, 0, len(spec.CheckpointAt)+1)
	if spec.Checkpoint != "" {
		checkpoints = append(checkpoints, spec.Checkpoint)
	}
	for step, path := range spec.CheckpointAt {
		checkpoints = append(checkpoints, checkpointPath(path, 0, &Endpoint{Step: baseStep + step}))
	}
	for _, path := range checkpoints {
		source, err := tracePathIdentity(path)
		if err != nil {
			return err
		}
		if source == target {
			return fmt.Errorf("exposure trace cannot overwrite checkpoint %q", path)
		}
	}
	return nil
}

func openExposureTrace(path string) (*exposureTrace, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".exposure-*.tmp")
	if err != nil {
		return nil, err
	}
	z, err := gzip.NewWriterLevel(file, gzip.BestSpeed)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return &exposureTrace{file: file, compressed: z, encoder: json.NewEncoder(z), receipt: TraceReceipt{Version: 1, Path: path}}, nil
}

func (t *exposureTrace) record(step int, recipient int64, feed *model.FeedDetails[float64]) {
	if t.err != nil {
		return
	}
	if feed == nil {
		t.err = errors.New("missing feed details for exposure trace")
		return
	}
	if t.err = t.encoder.Encode(traceFeed{Step: step, Recipient: recipient, FeedDetails: feed}); t.err != nil {
		return
	}
	t.receipt.Updates++
	t.receipt.Slots += len(feed.Recommendations)
}

func (t *exposureTrace) finish() (*TraceReceipt, error) {
	if t.err != nil {
		return nil, t.err
	}
	if err := t.compressed.Close(); err != nil {
		return nil, err
	}
	if err := t.file.Sync(); err != nil {
		return nil, err
	}
	if err := t.file.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(t.file.Name(), t.receipt.Path); err != nil {
		return nil, err
	}
	return &t.receipt, nil
}

func (t *exposureTrace) abort() {
	_ = t.file.Close()
	_ = os.Remove(t.file.Name())
}
