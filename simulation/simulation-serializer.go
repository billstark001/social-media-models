package simulation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	utils "smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

// SimulationSerializer 用于处理模拟数据的序列化和反序列化
type SimulationSerializer struct {
	baseDir          string
	simulationID     string
	maxSnapshotCount int
}

// NewSimulationSerializer 创建一个新的SimulationSerializer实例
func NewSimulationSerializer(baseDir string, simulationID string, maxSnapshotCount int) *SimulationSerializer {
	return &SimulationSerializer{
		baseDir:          baseDir,
		simulationID:     simulationID,
		maxSnapshotCount: maxSnapshotCount,
	}
}

// 获取模拟的目录路径
func (s *SimulationSerializer) getSimulationDir() string {
	return filepath.Join(s.baseDir, s.simulationID)
}

// Exists 检查一个模拟ID是否存在
func (s *SimulationSerializer) Exists() bool {
	_, err := os.Stat(s.getSimulationDir())
	return !os.IsNotExist(err)
}

// ensureSimulationDir 确保模拟目录存在
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

	// 按文件名排序，由于使用ISO 8601时间格式，这也将按时间排序
	sort.Strings(snapshotFiles)
	return snapshotFiles, nil
}

type anyPtr interface{}

// 读取快照文件
func (s *SimulationSerializer) _read(filePath string, snapshot any) (anyPtr, error) {
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

func (s *SimulationSerializer) _write(fileType string, snapshot anyPtr) error {
	if err := s.ensureSimulationDir(); err != nil {
		return err
	}

	// serialize
	data, err := msgpack.Marshal(snapshot)
	if err != nil {
		return err
	}

	// save
	filePath := s._getFilePath(fileType, ".msgpack")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return err
	}

	// clean old ones
	return s._clean(fileType, false, ".msgpack")
}

// 清理旧的快照文件
func (s *SimulationSerializer) _clean(fileType string, all bool, suffixName string) error {
	if !all && s.maxSnapshotCount <= 0 {
		return nil // 不限制快照数量
	}

	files, err := s._list(fileType, suffixName)
	if err != nil {
		return err
	}

	toDelete := len(files)
	if !all {
		// 如果快照数量超过最大值，删除最旧的
		if len(files) > s.maxSnapshotCount {
			toDelete -= s.maxSnapshotCount
		} else {
			toDelete = 0
		}
	}

	// 需要删除的文件数量
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
	DynamicsType string
	Data         []byte
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

func (s *SimulationSerializer) SaveRawSnapshot(dynamicsType string, data []byte) error {
	return s._write("snapshot", &RawSnapshotData{DynamicsType: dynamicsType, Data: data})
}

// #endregion

// #region finished mark

type FinishMark struct {
}

func (s *SimulationSerializer) MarkFinished() error {
	return s._write("finished", &FinishMark{})
}

func (s *SimulationSerializer) IsFinished() (bool, error) {
	files, err := s._list("finished", ".msgpack")
	if err != nil {
		return false, err
	}
	finished := len(files) > 0
	return finished, nil
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

// SaveGraph 存储模拟的图结构
func (s *SimulationSerializer) SaveGraph(graph *utils.NetworkXGraph, step int) error {
	if err := s.ensureSimulationDir(); err != nil {
		return err
	}

	// 创建图文件名
	filename := fmt.Sprintf("graph-%d.msgpack", step)
	filePath := filepath.Join(s.getSimulationDir(), filename)

	// 序列化并保存图
	data, err := msgpack.Marshal(graph)
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

// LoadGraph 加载特定步骤的图
func (s *SimulationSerializer) LoadGraph(step int) (*utils.NetworkXGraph, error) {
	if !s.Exists() {
		return nil, nil
	}

	// 构建图文件路径
	filename := fmt.Sprintf("graph-%d.msgpack", step)
	filePath := filepath.Join(s.getSimulationDir(), filename)

	// 检查文件是否存在
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, nil
	}

	// 读取并解析图数据
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var graph utils.NetworkXGraph
	err = msgpack.Unmarshal(data, &graph)
	if err != nil {
		return nil, err
	}

	return &graph, nil
}

func (s *SimulationSerializer) DeleteGraphsAfterStep(step int, ignoreStepParsingErrors bool) error {

	reGraphName := regexp.MustCompile(`graph-(\d+)\.msgpack`)

	// list all graphs
	files, err := s._list("graph", ".msgpack")
	if err != nil {
		return err
	}

	for _, file := range files {
		matches := reGraphName.FindStringSubmatch(file)
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

// SaveMetadata 存储模拟元数据
func (s *SimulationSerializer) SaveMetadata(metadata *ScenarioMetadata) error {
	if err := s.ensureSimulationDir(); err != nil {
		return err
	}

	// 创建元数据文件路径
	filePath := filepath.Join(s.getSimulationDir(), "metadata.json")

	// 序列化并保存元数据
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

// LoadMetadata 加载模拟元数据
func (s *SimulationSerializer) LoadMetadata() (*ScenarioMetadata, error) {
	if !s.Exists() {
		return nil, nil
	}

	// 构建元数据文件路径
	filePath := filepath.Join(s.getSimulationDir(), "metadata.json")

	// 读取并解析元数据
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var metadata ScenarioMetadata
	err = json.Unmarshal(data, &metadata)
	if err != nil {
		return nil, err
	}

	return &metadata, nil
}
