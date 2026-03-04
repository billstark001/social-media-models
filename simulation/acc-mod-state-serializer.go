package simulation

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/pierrec/lz4/v4"
)

// 存储数据
func SaveAccumulativeModelState(path string, state *AccumulativeModelState) error {
	var buf bytes.Buffer

	// this `steps` variable is larger than the real step number by 1
	// since it includes step 0
	steps := int32(len(state.Opinions))
	if steps == 0 {
		return fmt.Errorf("empty opinions")
	}
	agents := int32(len(state.Opinions[0]))

	// 写入维度
	binary.Write(&buf, binary.LittleEndian, steps)
	binary.Write(&buf, binary.LittleEndian, agents)

	// 写 Opinions
	for _, agentsSlice := range state.Opinions {
		binary.Write(&buf, binary.LittleEndian, agentsSlice)
	}

	// 写 AgentNumbers
	for _, agSlice := range state.AgentNumbers {
		for _, arr := range agSlice {
			binary.Write(&buf, binary.LittleEndian, arr)
		}
	}

	// 写 AgentOpinionSums
	for _, agSlice := range state.AgentOpinionSums {
		for _, arr := range agSlice {
			binary.Write(&buf, binary.LittleEndian, arr)
		}
	}

	// LZ4 压缩
	var out bytes.Buffer
	w := lz4.NewWriter(&out)
	_, err := w.Write(buf.Bytes())
	if err != nil {
		return err
	}
	w.Close()

	return os.WriteFile(path, out.Bytes(), 0644)
}

// 读取数据
func LoadAccumulativeModelState(path string) (*AccumulativeModelState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	r := lz4.NewReader(bytes.NewReader(raw))
	var buf bytes.Buffer
	io.Copy(&buf, r)

	data := buf.Bytes()
	reader := bytes.NewReader(data)

	var steps, agents int32
	binary.Read(reader, binary.LittleEndian, &steps)
	binary.Read(reader, binary.LittleEndian, &agents)

	// Opinions
	opinions := make([][]float32, steps)
	for i := range opinions {
		opinions[i] = make([]float32, agents)
		binary.Read(reader, binary.LittleEndian, opinions[i])
	}

	// AgentNumbers
	agentNumbers := make([][][4]int16, steps)
	for i := range agentNumbers {
		agentNumbers[i] = make([][4]int16, agents)
		for j := range agentNumbers[i] {
			binary.Read(reader, binary.LittleEndian, &agentNumbers[i][j])
		}
	}

	// AgentOpinionSums
	agentOpinionSums := make([][][4]float32, steps)
	for i := range agentOpinionSums {
		agentOpinionSums[i] = make([][4]float32, agents)
		for j := range agentOpinionSums[i] {
			binary.Read(reader, binary.LittleEndian, &agentOpinionSums[i][j])
		}
	}

	return &AccumulativeModelState{
		Opinions:         opinions,
		AgentNumbers:     agentNumbers,
		AgentOpinionSums: agentOpinionSums,
	}, nil
}
