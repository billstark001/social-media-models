package trajectory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const ChunkSteps = 256

type Chunk struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version      int       `json:"version"`
	Agents       int       `json:"agents"`
	NextStep     int       `json:"next_step"`
	Precision    Precision `json:"precision,omitempty"`
	LegacyChunks int       `json:"legacy_chunks,omitempty"`
	Chunks       []Chunk   `json:"chunks"`
}

// Writer stores the same three per-agent channels as the legacy
// accumulated state, but commits independent compressed time blocks. The
// current block is the only history retained by Scenario in memory.
type Writer struct {
	dir      string
	Manifest Manifest
}

func Open(dir string, agents int, precision Precision) (*Writer, error) {
	if err := precision.Validate(); err != nil {
		return nil, err
	}
	precision = precision.Resolved()
	path := filepath.Join(dir, "trajectory.json")
	w := &Writer{dir: dir, Manifest: Manifest{Version: 3, Agents: agents, Precision: precision}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &w.Manifest); err != nil {
			return nil, err
		}
		if (w.Manifest.Version != 2 && w.Manifest.Version != 3) || w.Manifest.Agents != agents {
			return nil, fmt.Errorf("trajectory metadata mismatch")
		}
		if w.Manifest.Version == 3 && w.Manifest.Precision != precision {
			return nil, fmt.Errorf("trajectory precision differs from existing run")
		}
		// Version-2 blocks carry their original float32 precision in their
		// header. New blocks use the requested precision after the next flush.
		if w.Manifest.Version == 2 {
			w.Manifest.LegacyChunks = len(w.Manifest.Chunks)
		}
		w.Manifest.Version = 3
		w.Manifest.Precision = precision
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return w, nil
}

func (w *Writer) saveManifest() error {
	data, err := json.Marshal(w.Manifest)
	if err != nil {
		return err
	}
	tmp := filepath.Join(w.dir, "trajectory.json.tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(w.dir, "trajectory.json"))
}

func (w *Writer) Flush(buffer *Buffer) error {
	if buffer == nil {
		return fmt.Errorf("trajectory buffer is nil")
	}
	count := buffer.Steps
	if count == 0 {
		return nil
	}
	if buffer.Agents != w.Manifest.Agents || buffer.Precision != w.Manifest.Precision {
		return fmt.Errorf("trajectory buffer metadata mismatch")
	}
	start := w.Manifest.NextStep
	end := start + count - 1
	name := fmt.Sprintf("trajectory-%09d-%09d.smpc", start, end)
	path := filepath.Join(w.dir, name)
	tmp := path + ".tmp"
	data, err := EncodeChunk(buffer)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	oldNext := w.Manifest.NextStep
	oldCount := len(w.Manifest.Chunks)
	w.Manifest.Chunks = append(w.Manifest.Chunks, Chunk{Start: start, End: end, File: name, SHA256: hex.EncodeToString(sum[:])})
	w.Manifest.NextStep = end + 1
	if err := w.saveManifest(); err != nil {
		w.Manifest.Chunks = w.Manifest.Chunks[:oldCount]
		w.Manifest.NextStep = oldNext
		_ = os.Remove(path)
		return err
	}
	buffer.Reset()
	return nil
}

// Truncate discards complete chunks committed after the latest model snapshot.
// Snapshot writes flush the current block first, so a chunk never crosses the
// requested boundary.
func (w *Writer) Truncate(nextStep int) error {
	if nextStep < 0 || nextStep > w.Manifest.NextStep {
		return fmt.Errorf("trajectory cannot truncate to step %d", nextStep)
	}
	if nextStep == w.Manifest.NextStep {
		return nil
	}
	kept := make([]Chunk, 0, len(w.Manifest.Chunks))
	var removed []Chunk
	for _, chunk := range w.Manifest.Chunks {
		if chunk.End < nextStep {
			kept = append(kept, chunk)
			continue
		}
		if chunk.Start < nextStep {
			return fmt.Errorf("snapshot step %d splits trajectory chunk", nextStep)
		}
		removed = append(removed, chunk)
	}
	previous := w.Manifest
	w.Manifest.Chunks = kept
	w.Manifest.NextStep = nextStep
	if w.Manifest.LegacyChunks > len(kept) {
		w.Manifest.LegacyChunks = len(kept)
	}
	if err := w.saveManifest(); err != nil {
		w.Manifest = previous
		return err
	}
	// The committed index no longer refers to these files. A crash during
	// cleanup leaves only harmless orphan files, not a broken trajectory.
	for _, chunk := range removed {
		if err := os.Remove(filepath.Join(w.dir, chunk.File)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (w *Writer) Verify() error {
	if err := w.VerifyIndex(); err != nil {
		return err
	}
	for _, chunk := range w.Manifest.Chunks {
		data, err := os.ReadFile(filepath.Join(w.dir, chunk.File))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != chunk.SHA256 {
			return fmt.Errorf("trajectory chunk %s checksum mismatch", chunk.File)
		}
	}
	return nil
}

// VerifyIndex checks continuity and file presence without reading old blocks.
// Historical checksums are checked when a block is actually read.
func (w *Writer) VerifyIndex() error {
	next := 0
	for _, chunk := range w.Manifest.Chunks {
		if chunk.Start != next || chunk.End < chunk.Start {
			return fmt.Errorf("non-contiguous trajectory at step %d", next)
		}
		if _, err := os.Stat(filepath.Join(w.dir, chunk.File)); err != nil {
			return err
		}
		next = chunk.End + 1
	}
	if next != w.Manifest.NextStep {
		return fmt.Errorf("trajectory end mismatch")
	}
	return nil
}
