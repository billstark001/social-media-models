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

func (s *Scenario) snapshotData() (*RawSnapshotData, error) {
	data, err := s.Model.RawDump()
	if err != nil {
		return nil, err
	}
	return &RawSnapshotData{
		DynamicsType:  s.Metadata.DynamicsType,
		Data:          data,
		Metadata:      s.Metadata,
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
	snapshot, err := s.snapshotData()
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
	if s.EnableDumps {
		return fmt.Errorf("batch checkpoint initialization must be in-memory")
	}
	if err := s.Metadata.PrepareForNewRun(); err != nil {
		return err
	}
	if err := s.Metadata.Validate(); err != nil {
		return err
	}
	if err := ValidateCheckpointMetadata(snapshot.Metadata, s.Metadata); err != nil {
		return err
	}
	pool, err := smprng.NewPool(s.Metadata.RNG)
	if err != nil {
		return err
	}
	loaded, err := RestoreRawModel(snapshot, s.Metadata, pool, nil, branch)
	if err != nil {
		return err
	}
	s.RNG = pool
	s.Model = loaded
	s.StableSteps = snapshot.StableSteps
	if snapshot.CompletedStep != s.Model.GetCurStep()-1 {
		return fmt.Errorf("checkpoint completed step does not match model state")
	}
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

// RestoreRawModel is the single typed snapshot loader used by smp and batch.
// branch=true discards saved RNG positions and restores the caller's fresh
// stream positions after constructors and recommender initialization.
func RestoreRawModel(snapshot *RawSnapshotData, metadata *ScenarioMetadata, pool *smprng.Pool, logger func(*model.EventRecord), branch bool) (IModel, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("nil checkpoint")
	}
	dynamicsType := snapshot.DynamicsType
	if dynamicsType == "" {
		dynamicsType = metadata.DynamicsType
	}
	if dynamicsType != metadata.DynamicsType {
		return nil, fmt.Errorf("checkpoint dynamics %q differs from request %q", dynamicsType, metadata.DynamicsType)
	}
	initialStates, err := pool.Snapshot()
	if err != nil {
		return nil, err
	}
	finish := func(w IModel) (IModel, error) {
		if branch {
			if err := pool.Restore(initialStates); err != nil {
				return nil, err
			}
		}
		return w, nil
	}
	switch dynamicsType {
	case DynamicsTypeHK:
		var dump model.SMPModelDumpData[float64, dynamics.HKParams]
		if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
			return nil, err
		}
		if branch {
			dump.RNGStates = nil
		}
		factory := GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](metadata.RecSysParams)[metadata.RecsysFactoryType]
		params := model.SMPModelParams[float64, dynamics.HKParams]{SMPModelPureParams: metadata.SMPModelPureParams, RecsysFactory: factory}
		return finish(&Float64ModelWrapper[dynamics.HKParams]{M: dump.Load(&params, &metadata.HKParams, &dynamics.HK{}, &metadata.CollectItemOptions, logger, pool)})
	case DynamicsTypeDeffuant:
		var dump model.SMPModelDumpData[float64, dynamics.DeffuantParams]
		if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
			return nil, err
		}
		if branch {
			dump.RNGStates = nil
		}
		factory := GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](metadata.RecSysParams)[metadata.RecsysFactoryType]
		params := model.SMPModelParams[float64, dynamics.DeffuantParams]{SMPModelPureParams: metadata.SMPModelPureParams, RecsysFactory: factory}
		return finish(&Float64ModelWrapper[dynamics.DeffuantParams]{M: dump.Load(&params, &metadata.DeffuantParams, &dynamics.Deffuant{}, &metadata.CollectItemOptions, logger, pool)})
	case DynamicsTypeGalam:
		var dump model.SMPModelDumpData[bool, dynamics.GalamParams]
		if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
			return nil, err
		}
		if branch {
			dump.RNGStates = nil
		}
		factory := GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](metadata.RecSysParams)[metadata.RecsysFactoryType]
		params := model.SMPModelParams[bool, dynamics.GalamParams]{SMPModelPureParams: metadata.SMPModelPureParams, RecsysFactory: factory}
		return finish(&BoolModelWrapper[dynamics.GalamParams]{M: dump.Load(&params, &metadata.GalamParams, &dynamics.Galam{}, &metadata.CollectItemOptions, logger, pool)})
	case DynamicsTypeVoter:
		var dump model.SMPModelDumpData[bool, dynamics.VoterParams]
		if err := msgpack.Unmarshal(snapshot.Data, &dump); err != nil {
			return nil, err
		}
		if branch {
			dump.RNGStates = nil
		}
		factory := GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](metadata.RecSysParams)[metadata.RecsysFactoryType]
		params := model.SMPModelParams[bool, dynamics.VoterParams]{SMPModelPureParams: metadata.SMPModelPureParams, RecsysFactory: factory}
		return finish(&BoolModelWrapper[dynamics.VoterParams]{M: dump.Load(&params, &metadata.VoterParams, &dynamics.Voter{}, &metadata.CollectItemOptions, logger, pool)})
	default:
		return nil, fmt.Errorf("unknown checkpoint dynamics %q", dynamicsType)
	}
}
