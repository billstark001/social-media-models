package simulation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	smprng "smp/rng"
	utils "smp/utils"

	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
)

// SimulationSerializer persists and reloads simulation records.
type SimulationSerializer struct {
	baseDir          string
	simulationID     string
	maxSnapshotCount int
}

// NewSimulationSerializer configures the output directory and snapshot retention.
func NewSimulationSerializer(baseDir string, simulationID string, maxSnapshotCount int) *SimulationSerializer {
	return &SimulationSerializer{
		baseDir:          baseDir,
		simulationID:     simulationID,
		maxSnapshotCount: maxSnapshotCount,
	}
}

func (s *SimulationSerializer) getSimulationDir() string {
	return filepath.Join(s.baseDir, s.simulationID)
}

// Exists reports whether the simulation directory exists.
func (s *SimulationSerializer) Exists() bool {
	_, err := os.Stat(s.getSimulationDir())
	return !os.IsNotExist(err)
}

func (s *SimulationSerializer) ensureSimulationDir() error {
	dir := s.getSimulationDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0755)
	}
	return nil
}

// #region serialize

func (s *SimulationSerializer) _list(fileType string, suffixName string) ([]string, error) {
	dir := s.getSimulationDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var snapshotFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), fileType+"-") && strings.HasSuffix(entry.Name(), suffixName) {
			snapshotFiles = append(snapshotFiles, filepath.Join(dir, entry.Name()))
		}
	}

	// ISO timestamps sort chronologically.
	sort.Strings(snapshotFiles)
	return snapshotFiles, nil
}

func (s *SimulationSerializer) _read(filePath string, snapshot any) (any, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	err = msgpack.Unmarshal(data, &snapshot)
	if err != nil {
		return nil, err
	}

	return snapshot, nil
}

func (s *SimulationSerializer) _getFilePath(fileType string, suffixName string) string {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	timestamp = strings.ReplaceAll(timestamp, ":", "") // change to ISO 8601 format
	filename := fmt.Sprintf("%s-%s%s", fileType, timestamp, suffixName)
	filePath := filepath.Join(s.getSimulationDir(), filename)
	return filePath
}

func (s *SimulationSerializer) _write(fileType string, snapshot any) error {
	if err := s.ensureSimulationDir(); err != nil {
		return err
	}

	// serialize
	data, err := msgpack.Marshal(snapshot)
	if err != nil {
		return err
	}

	// Commit by rename even when two dumps share the same timestamp. A failed
	// write must not replace the last complete recovery snapshot with a prefix.
	filePath := s._getFilePath(fileType, ".msgpack")
	if err := utils.AtomicWriteFile(filePath, data, 0644); err != nil {
		return err
	}

	// clean old ones
	return s._clean(fileType, false, ".msgpack")
}

func (s *SimulationSerializer) _clean(fileType string, all bool, suffixName string) error {
	if !all && s.maxSnapshotCount <= 0 {
		return nil
	}

	files, err := s._list(fileType, suffixName)
	if err != nil {
		return err
	}

	toDelete := len(files)
	if !all {

		if len(files) > s.maxSnapshotCount {
			toDelete -= s.maxSnapshotCount
		} else {
			toDelete = 0
		}
	}

	for i := range toDelete {
		if err := os.Remove(files[i]); err != nil {
			return err
		}
	}

	return nil
}

// #endregion

// #region snapshot

// RawSnapshotData wraps a dynamics-type tag and the msgpack-encoded model dump.
type RawSnapshotData struct {
	DynamicsType  string
	Data          []byte
	Metadata      *ScenarioMetadata
	CompletedStep int
	StableSteps   int
	Research      *ResearchState `msgpack:",omitempty" json:",omitempty"`
}

func (s *SimulationSerializer) GetLatestRawSnapshot() (*RawSnapshotData, error) {
	if !s.Exists() {
		return nil, nil
	}
	files, err := s._list("snapshot", ".msgpack")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	latestFile := files[len(files)-1]
	ret, err := s._read(latestFile, &RawSnapshotData{})
	return ret.(*RawSnapshotData), err
}

func (s *SimulationSerializer) SaveRawSnapshot(snapshot *RawSnapshotData) error {
	return s._write("snapshot", snapshot)
}

// #endregion

// #region finished mark

type FinishMark struct {
	DataVersion int
	RNG         smprng.Spec
}

func (s *SimulationSerializer) MarkFinished(mark FinishMark) error {
	return s._write("finished", &mark)
}

func (s *SimulationSerializer) IsFinished() (bool, error) {
	files, err := s._list("finished", ".msgpack")
	if err != nil {
		return false, err
	}
	finished := len(files) > 0
	return finished, nil
}

// GetFinishMark loads the newest finish mark.
func (s *SimulationSerializer) GetFinishMark() (*FinishMark, error) {
	files, err := s._list("finished", ".msgpack")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	ret, err := s._read(files[len(files)-1], &FinishMark{})
	if err != nil {
		return nil, err
	}
	return ret.(*FinishMark), nil
}

// #endregion

// #region acc-state

func (s *SimulationSerializer) GetLatestAccumulativeState() (*AccumulativeModelState, error) {
	if !s.Exists() {
		return nil, nil
	}
	files, err := s._list("acc-state", ".lz4")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	latestFile := files[len(files)-1]
	ret, err := LoadAccumulativeModelState(latestFile)
	return ret, err
}

func (s *SimulationSerializer) SaveAccumulativeState(snapshot *AccumulativeModelState) error {

	// declare
	fileType := "acc-state"
	filePath := s._getFilePath(fileType, ".lz4")

	// save
	err := SaveAccumulativeModelState(filePath, snapshot)
	if err != nil {
		return err
	}

	// clean old ones
	return s._clean(fileType, false, ".lz4")
}

// #endregion

// #region graph

// SaveGraph persists the directed graph at a completed step.
func (s *SimulationSerializer) SaveGraph(graph *utils.NetworkXGraph, step int) error {
	if err := s.ensureSimulationDir(); err != nil {
		return err
	}

	filename := fmt.Sprintf("graph-%d.msgpack.lz4", step)
	filePath := filepath.Join(s.getSimulationDir(), filename)

	data, err := msgpack.Marshal(graph)
	if err != nil {
		return err
	}

	var compressed bytes.Buffer
	w := lz4.NewWriter(&compressed)
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, compressed.Bytes(), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filePath)
}

// LoadGraph reads a step snapshot, including legacy uncompressed graph files.
func (s *SimulationSerializer) LoadGraph(step int) (*utils.NetworkXGraph, error) {
	if !s.Exists() {
		return nil, nil
	}

	filename := fmt.Sprintf("graph-%d.msgpack.lz4", step)
	filePath := filepath.Join(s.getSimulationDir(), filename)

	compressed := true
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		compressed = false
		filePath = filepath.Join(s.getSimulationDir(), fmt.Sprintf("graph-%d.msgpack", step))
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return nil, nil
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if compressed {
		reader := lz4.NewReader(bytes.NewReader(data))
		data, err = io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
	}

	var graph utils.NetworkXGraph
	err = msgpack.Unmarshal(data, &graph)
	if err != nil {
		return nil, err
	}

	return &graph, nil
}

func (s *SimulationSerializer) DeleteGraphsAfterStep(step int, ignoreStepParsingErrors bool) error {

	reGraphName := regexp.MustCompile(`graph-(\d+)\.msgpack(?:\.lz4)?$`)

	// list all graphs
	files, err := os.ReadDir(s.getSimulationDir())
	if err != nil {
		return err
	}

	for _, entry := range files {
		file := filepath.Join(s.getSimulationDir(), entry.Name())
		matches := reGraphName.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		stepInt, err := strconv.Atoi(matches[1])
		if err != nil && !ignoreStepParsingErrors {
			return err
		}
		if err == nil && stepInt >= step {
			err := os.Remove(file)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// #endregion

// SaveMetadata persists the resolved simulation configuration.
func (s *SimulationSerializer) SaveMetadata(metadata *ScenarioMetadata) error {
	if err := s.ensureSimulationDir(); err != nil {
		return err
	}

	filePath := filepath.Join(s.getSimulationDir(), "metadata.json")

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

// LoadMetadata reads the resolved configuration, if present.
func (s *SimulationSerializer) LoadMetadata() (*ScenarioMetadata, error) {
	if !s.Exists() {
		return nil, nil
	}

	filePath := filepath.Join(s.getSimulationDir(), "metadata.json")

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// DataVersion 0 runs predate metadata.json. The caller can still
			// recover them from the CLI metadata and the stored snapshot.
			return nil, nil
		}
		return nil, err
	}

	var metadata ScenarioMetadata
	err = json.Unmarshal(data, &metadata)
	if err != nil {
		return nil, err
	}

	return &metadata, nil
}
