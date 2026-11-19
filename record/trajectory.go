package record

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"smp/trajectory"
)

func (r *Reader) validateManifest() error {
	m := r.manifest
	if (m.Version != 2 && m.Version != 3) || m.Agents <= 0 || m.NextStep <= 0 {
		return fmt.Errorf("invalid trajectory version or dimensions")
	}
	if m.LegacyChunks < 0 || m.LegacyChunks > len(m.Chunks) {
		return fmt.Errorf("invalid trajectory legacy chunk count")
	}
	if m.Version == 3 {
		if err := m.Precision.Validate(); err != nil {
			return err
		}
		if m.Precision.Opinions == "" || m.Precision.OpinionSums == "" {
			return fmt.Errorf("trajectory precision is missing")
		}
	}
	next := 0
	for _, chunk := range m.Chunks {
		if chunk.Start != next || chunk.End < chunk.Start || chunk.End >= m.NextStep || filepath.Base(chunk.File) != chunk.File || chunk.File == "." {
			return fmt.Errorf("invalid trajectory chunk at step %d", next)
		}
		if digest, err := hex.DecodeString(chunk.SHA256); err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("invalid trajectory checksum for %s", chunk.File)
		}
		next = chunk.End + 1
	}
	if next != m.NextStep {
		return fmt.Errorf("non-contiguous trajectory: ends at %d, expected %d", next, m.NextStep)
	}
	return nil
}

func (r *Reader) opinionRow(step int) ([]float64, string, error) {
	if r.legacy != nil {
		row := r.legacy[step]
		if err := finiteOpinions(row, step); err != nil {
			return nil, "", err
		}
		return row, "history_f32", nil
	}
	index := sort.Search(len(r.manifest.Chunks), func(i int) bool { return r.manifest.Chunks[i].End >= step })
	if r.blockAt != index {
		chunk := r.manifest.Chunks[index]
		// Release the previous decoded block before allocating the next one.
		r.block, r.blockAt = nil, -1
		data, err := os.ReadFile(filepath.Join(r.dir, chunk.File))
		if err != nil {
			return nil, "", fmt.Errorf("read trajectory %s: %w", chunk.File, err)
		}
		digest := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), chunk.SHA256) {
			return nil, "", fmt.Errorf("trajectory checksum mismatch: %s", chunk.File)
		}
		if len(data) < 16 || uint64(binary.LittleEndian.Uint32(data[8:12])) != uint64(chunk.End-chunk.Start+1) || uint64(binary.LittleEndian.Uint32(data[12:16])) != uint64(r.agents) {
			return nil, "", fmt.Errorf("trajectory dimensions differ from manifest: %s", chunk.File)
		}
		block, err := trajectory.DecodeChunkOpinions(data)
		if err != nil {
			return nil, "", fmt.Errorf("decode trajectory %s: %w", chunk.File, err)
		}
		opinions, sums := "float32", "float32"
		if string(data[:8]) == "SMPTRJ03" {
			opinions, sums = fmt.Sprintf("float%d", int(data[16])*8), fmt.Sprintf("float%d", int(data[17])*8)
		}
		expected := trajectory.Precision{Opinions: "float32", OpinionSums: "float32"}
		if r.manifest.Version == 3 && index >= r.manifest.LegacyChunks {
			expected = r.manifest.Precision
		}
		if opinions != expected.Opinions || sums != expected.OpinionSums {
			return nil, "", fmt.Errorf("trajectory precision differs from manifest: %s", chunk.File)
		}
		r.block, r.blockAt = block, index
		r.precision = "history_" + strings.Replace(opinions, "float", "f", 1)
	}
	row := r.block[step-r.manifest.Chunks[index].Start]
	if err := finiteOpinions(row, step); err != nil {
		return nil, "", err
	}
	return row, r.precision, nil
}

func finiteOpinions(opinions []float64, step int) error {
	for id, opinion := range opinions {
		if math.IsNaN(opinion) || math.IsInf(opinion, 0) {
			return fmt.Errorf("non-finite opinion for agent %d at step %d", id, step)
		}
	}
	return nil
}
