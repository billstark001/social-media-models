package simulation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/utils"

	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
)

func ReadCheckpoint(path string) (*RawSnapshotData, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	fileBytes := data
	if strings.HasSuffix(path, ".lz4") {
		data, err = io.ReadAll(lz4.NewReader(bytes.NewReader(fileBytes)))
		if err != nil {
			return nil, "", err
		}
	}
	var snapshot RawSnapshotData
	if err := msgpack.Unmarshal(data, &snapshot); err != nil {
		return nil, "", err
	}
	if len(snapshot.Data) == 0 {
		return nil, "", fmt.Errorf("checkpoint contains no model data")
	}
	sum := sha256.Sum256(fileBytes)
	return &snapshot, hex.EncodeToString(sum[:]), nil
}

// Snapshot captures full microscopic state without filesystem I/O or advancing RNG.
// The returned bytes and metadata maps/slices are independent of the scenario.
func (s *Scenario) Snapshot() (*RawSnapshotData, error) {
	if s.Model == nil || s.Metadata == nil {
		return nil, fmt.Errorf("scenario is not initialized")
	}
	data, err := s.Model.RawDump()
	if err != nil {
		return nil, err
	}
	metadata := *s.Metadata
	metadata.RecSysParams = maps.Clone(s.Metadata.RecSysParams)
	metadata.CheckpointSteps = append([]int(nil), s.Metadata.CheckpointSteps...)
	return &RawSnapshotData{
		DynamicsType:  s.Metadata.DynamicsType,
		Data:          data,
		Metadata:      &metadata,
		CompletedStep: s.Model.GetCurStep() - 1,
		StableSteps:   s.StableSteps,
	}, nil
}

func (s *Scenario) SaveResearchCheckpoint() error {
	if !s.EnableDumps {
		return fmt.Errorf("research checkpoint requires persistence")
	}
	if s.DB != nil {
		if err := s.DB.Flush(); err != nil {
			return err
		}
	}
	if s.Trajectory != nil && s.AccState != nil {
		if err := s.Trajectory.Flush(s.AccState.Packed); err != nil {
			return err
		}
	}
	path := filepath.Join(s.BaseDir, s.Metadata.UniqueName, fmt.Sprintf("checkpoint-%09d.msgpack.lz4", s.Model.GetCurStep()-1))
	return s.SaveCheckpointTo(path)
}

type Float64CheckpointState struct {
	CompletedStep int
	Graph         utils.NetworkXGraph
	Opinions      []float64
	Posts         map[int64][]model.PostRecord[float64]
}

// InspectFloat64Checkpoint reads the physical state without constructing a
// simulator or consuming any RNG stream.
func InspectFloat64Checkpoint(snapshot *RawSnapshotData) (Float64CheckpointState, error) {
	var out Float64CheckpointState
	if snapshot == nil {
		return out, fmt.Errorf("nil checkpoint")
	}
	switch snapshot.DynamicsType {
	case DynamicsTypeHK:
		var dump model.SMPModelDumpData[float64, dynamics.HKParams]
		if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
			return out, err
		}
		out = Float64CheckpointState{dump.CurStep - 1, dump.Graph, dump.Opinions, dump.Posts}
	case DynamicsTypeDeffuant:
		var dump model.SMPModelDumpData[float64, dynamics.DeffuantParams]
		if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
			return out, err
		}
		out = Float64CheckpointState{dump.CurStep - 1, dump.Graph, dump.Opinions, dump.Posts}
	default:
		return out, fmt.Errorf("checkpoint dynamics %q has no float64 opinions", snapshot.DynamicsType)
	}
	if out.CompletedStep != snapshot.CompletedStep {
		return Float64CheckpointState{}, fmt.Errorf("checkpoint step mismatch")
	}
	return out, nil
}

func (s *Scenario) SaveCheckpointTo(path string) error {
	snapshot, err := s.Snapshot()
	if err != nil {
		return err
	}
	data, err := msgpack.Marshal(snapshot)
	if err != nil {
		return err
	}
	if strings.HasSuffix(path, ".lz4") {
		var compressed bytes.Buffer
		w := lz4.NewWriter(&compressed)
		if _, err := w.Write(data); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		data = compressed.Bytes()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Scenario) checkpointRequested(step int) bool {
	for _, requested := range s.Metadata.CheckpointSteps {
		if requested == step {
			return true
		}
	}
	return false
}

func (s *Scenario) InitFromCheckpoint(snapshot *RawSnapshotData, branch bool) error {
	prepared, err := PrepareCheckpoint(snapshot)
	if err != nil {
		return err
	}
	return s.InitFromPreparedCheckpoint(prepared, branch)
}

// InitFromPreparedCheckpoint reuses a single decoded checkpoint for multiple
// independent branches. Every restored model owns its mutable microscopic state.
func (s *Scenario) InitFromPreparedCheckpoint(snapshot *PreparedCheckpoint, branch bool) error {
	if snapshot == nil {
		return fmt.Errorf("nil checkpoint")
	}
	if s.EnableDumps {
		return fmt.Errorf("batch checkpoint initialization must be in-memory")
	}
	if s.Metadata == nil {
		return fmt.Errorf("metadata is nil")
	}
	if err := s.Metadata.PrepareForNewRun(); err != nil {
		return err
	}
	if err := s.Metadata.Validate(); err != nil {
		return err
	}
	if err := ValidateCheckpointMetadata(snapshot.metadata, s.Metadata); err != nil {
		return err
	}
	pool, err := smprng.NewPool(s.Metadata.RNG)
	if err != nil {
		return err
	}
	loaded, err := snapshot.restore(s.Metadata, pool, nil, branch)
	if err != nil {
		return err
	}
	if snapshot.completedStep != loaded.GetCurStep()-1 {
		return fmt.Errorf("checkpoint completed step does not match model state")
	}
	s.RNG, s.Model, s.StableSteps = pool, loaded, snapshot.stableSteps
	return nil
}

// ValidateCheckpointMetadata prevents loading a physical state under different
// dynamics or recommender parameters. Run names, horizons, RNG and output
// collection choices may differ between a parent and its branches.
func ValidateCheckpointMetadata(saved, requested *ScenarioMetadata) error {
	if saved == nil {
		return fmt.Errorf("checkpoint has no metadata; exact branch validation is unavailable")
	}
	physical := func(m *ScenarioMetadata) any {
		recsParams := maps.Clone(m.RecSysParams)
		delete(recsParams, "LogRecommendations")
		if len(recsParams) == 0 {
			recsParams = nil
		}
		return struct {
			DynamicsType string
			HK           dynamics.HKParams
			Deffuant     dynamics.DeffuantParams
			Galam        dynamics.GalamParams
			Voter        dynamics.VoterParams
			Pure         model.SMPModelPureParams
			Recsys       string
			RecSysParams map[string]any
			Network      string
			Nodes        int
			Follows      int
		}{m.DynamicsType, m.HKParams, m.DeffuantParams, m.GalamParams, m.VoterParams, m.SMPModelPureParams, m.RecsysFactoryType, recsParams, m.NetworkType, m.NodeCount, m.NodeFollowCount}
	}
	left, _ := json.Marshal(physical(saved))
	right, _ := json.Marshal(physical(requested))
	if !reflect.DeepEqual(left, right) {
		return fmt.Errorf("checkpoint physical metadata differs from request")
	}
	return nil
}

// RestoreRawModel restores a serialized microscopic checkpoint. For repeated
// branches, PrepareCheckpoint decodes it once and uses the same typed loader.
func RestoreRawModel(snapshot *RawSnapshotData, metadata *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), branch bool) (IModel, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("nil checkpoint")
	}
	tagged := *snapshot
	if tagged.DynamicsType == "" {
		tagged.DynamicsType = metadata.DynamicsType
	}
	prepared, err := PrepareCheckpoint(&tagged)
	if err != nil {
		return nil, err
	}
	return prepared.restore(metadata, pool, logger, branch)
}
