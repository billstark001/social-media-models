package simulation

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"smp/model"
)

type TraceReceipt struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	Updates int    `json:"updates"`
	Slots   int    `json:"slots"`
}

type TracePosition struct {
	Offset  int64 `json:"offset"`
	Updates int   `json:"updates"`
	Slots   int   `json:"slots"`
}

// FeedTrace seals a gzip member at each microscopic checkpoint. The saved byte
// offset permits truncating an interrupted write without rescanning old feeds.
type FeedTrace struct {
	file       *os.File
	compressed *gzip.Writer
	encoder    *json.Encoder
	path       string
	position   TracePosition
	finalized  bool
	err        error
}

func OpenFeedTrace(path string, position TracePosition) (*FeedTrace, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	partial := path + ".partial"
	if info, err := os.Stat(path); err == nil {
		if info.Size() != position.Offset {
			return nil, fmt.Errorf("finished exposure trace differs from snapshot")
		}
		return &FeedTrace{path: path, position: position, finalized: true}, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || position.Offset < 0 || info.Size() < position.Offset {
		_ = file.Close()
		return nil, fmt.Errorf("exposure trace is shorter than its saved offset")
	}
	if err := file.Truncate(position.Offset); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Seek(position.Offset, 0); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &FeedTrace{file: file, path: path, position: position}, nil
}

func (t *FeedTrace) Record(step int, recipient int64, feed *model.FeedDetails[float64]) {
	if t.err != nil {
		return
	}
	if feed == nil || t.finalized {
		t.err = fmt.Errorf("missing feed details or finalized exposure trace")
		return
	}
	if t.compressed == nil {
		t.compressed, t.err = gzip.NewWriterLevel(t.file, gzip.BestSpeed)
		if t.err != nil {
			return
		}
		t.encoder = json.NewEncoder(t.compressed)
	}
	t.err = t.encoder.Encode(struct {
		Step      int   `json:"step"`
		Recipient int64 `json:"recipient"`
		*model.FeedDetails[float64]
	}{step, recipient, feed})
	if t.err == nil {
		t.position.Updates++
		t.position.Slots += len(feed.Recommendations)
	}
}

func (t *FeedTrace) Checkpoint() (TracePosition, error) {
	if t.err != nil || t.finalized {
		return t.position, t.err
	}
	if t.compressed != nil {
		if t.err = t.compressed.Close(); t.err != nil {
			return t.position, t.err
		}
		t.compressed, t.encoder = nil, nil
	}
	if t.err = t.file.Sync(); t.err != nil {
		return t.position, t.err
	}
	t.position.Offset, t.err = t.file.Seek(0, 1)
	return t.position, t.err
}

func (t *FeedTrace) Finish() (*TraceReceipt, error) {
	position, err := t.Checkpoint()
	if err != nil {
		return nil, err
	}
	if !t.finalized {
		if err := t.file.Close(); err != nil {
			return nil, err
		}
		if err := os.Rename(t.path+".partial", t.path); err != nil {
			return nil, err
		}
		t.finalized = true
	}
	return &TraceReceipt{Version: 1, Path: t.path, Updates: position.Updates, Slots: position.Slots}, nil
}

func (t *FeedTrace) Close() error {
	if t.file != nil && !t.finalized {
		return t.file.Close()
	}
	return nil
}
