package recsys

import (
	"fmt"
	"log"
	"math/rand"
	"sort"
	"time"

	"smp/model"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

// Structure implements a recommendation system based on network structure.
type Structure[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	Model               *model.SMPModel[O, P]
	HistoricalPostCount int
	AgentCount          int

	NoiseStd   float64
	MatrixInit bool
	LogFunc    func(string)

	NumNodes   int
	ConnMat    [][]int
	RateMat    [][]float64
	AllIndices []int
	AgentMap   map[int64]*model.SMPAgent[O, P]

	topKFinder *utils.TopKFinder
}

// NewStructure creates a structure-based recommendation system.
func NewStructure[O any, P any](
	m *model.SMPModel[O, P],
	noiseStd float64,
	historicalPostCount *int,
	matrixInit bool,
	logFunc func(string),
) *Structure[O, P] {
	h := m.ModelParams.PostRetainCount
	if historicalPostCount != nil {
		h = *historicalPostCount
	}
	ret := &Structure[O, P]{
		Model:               m,
		AgentCount:          m.Graph.Nodes().Len(),
		HistoricalPostCount: h,
		NoiseStd:            noiseStd,
		MatrixInit:          matrixInit,
		LogFunc:             logFunc,
	}
	ret.topKFinder = utils.NewTopKFinder(50)
	return ret
}

func (s *Structure[O, P]) calcConnMatMult() {
	s.ConnMat = makeRawMat[int](s.NumNodes, s.NumNodes)
	adjMat := makeRawMat[int](s.NumNodes, s.NumNodes)

	for i := range s.NumNodes {
		nodes := s.Model.Graph.From(int64(i))
		for nodes.Next() {
			j := int(nodes.Node().ID())
			adjMat[i][j] = 1
			adjMat[j][i] = 1
		}
	}

	for i := range s.NumNodes {
		for j := range s.NumNodes {
			for k := range s.NumNodes {
				s.ConnMat[i][j] += adjMat[i][k] * adjMat[k][j]
			}
		}
	}

	for i := range s.NumNodes {
		for j := 0; j <= i; j++ {
			s.ConnMat[i][j] = 0
		}
	}
}

func (s *Structure[O, P]) calcConnMatGraph() {
	s.ConnMat = makeRawMat[int](s.NumNodes, s.NumNodes)
	for u := range s.NumNodes {
		for v := u + 1; v < s.NumNodes; v++ {
			s.ConnMat[u][v] = commonNeighborsCount(s.Model.Graph, u, v)
		}
	}
}

// PostInit implements model.SMPModelRecommendationSystem
func (s *Structure[O, P]) PostInit(dumpData []byte) {
	s.NumNodes = s.Model.Graph.Nodes().Len()
	s.AllIndices = make([]int, s.NumNodes)
	for i := range s.NumNodes {
		s.AllIndices[i] = i
	}

	s.AgentMap = make(map[int64]*model.SMPAgent[O, P], s.NumNodes)
	for _, a := range s.Model.Schedule.Agents {
		s.AgentMap[a.ID] = a
	}

	if dumpData != nil {
		s.ConnMat = make([][]int, 0)
		err := msgpack.Unmarshal(dumpData, &s.ConnMat)
		if err == nil {
			if s.LogFunc != nil {
				s.LogFunc("Connection matrix loaded from dump data.")
			}
			return
		} else {
			panic("test")
		}
	}

	tstart := time.Now()
	if s.MatrixInit {
		s.calcConnMatMult()
	} else {
		s.calcConnMatGraph()
	}
	tend := time.Now()
	if s.LogFunc != nil {
		s.LogFunc(fmt.Sprintf("Connection matrix generation costs %v", tend.Sub(tstart)))
	}

	s.RateMat = makeRawMat[float64](s.NumNodes, s.NumNodes)
}

// PreStep implements model.SMPModelRecommendationSystem
func (s *Structure[O, P]) PreStep() {
	rawRateMat := make([][]float64, s.NumNodes)
	for i := range rawRateMat {
		rawRateMat[i] = make([]float64, s.NumNodes)
		for j := range s.NumNodes {
			rawRateMat[i][j] = float64(s.ConnMat[i][j] + s.ConnMat[j][i])
			if s.NoiseStd > 0 {
				noise := rand.NormFloat64() * s.NoiseStd
				rawRateMat[i][j] = max(rawRateMat[i][j]*(1-2*noise)+noise, 0)
			}
		}
		rawRateMat[i][i] = 0
	}
	s.RateMat = rawRateMat
}

// PostStep implements model.SMPModelRecommendationSystem
func (s *Structure[O, P]) PostStep(changed []*model.RewiringEventBody) {
	if len(changed) == 0 {
		return
	}

	changedNodes := make(map[int64]bool)
	for _, event := range changed {
		changedNodes[event.Unfollow] = true
		changedNodes[event.Follow] = true
	}

	nodeSlice := make([]int, 0, len(changedNodes))
	for node := range changedNodes {
		nodeSlice = append(nodeSlice, int(node))
	}
	sort.Ints(nodeSlice)

	for i := range nodeSlice {
		for j := i + 1; j < len(nodeSlice); j++ {
			u := nodeSlice[i]
			v := nodeSlice[j]
			if u < v {
				s.ConnMat[u][v] = commonNeighborsCount(s.Model.Graph, u, v)
			} else {
				s.ConnMat[v][u] = commonNeighborsCount(s.Model.Graph, v, u)
			}
		}
	}
}

// Recommend implements model.SMPModelRecommendationSystem
func (s *Structure[O, P]) Recommend(agent *model.SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*model.PostRecord[O] {
	visiblePosts := s.Model.Grid.PostMap
	rateVec := s.RateMat[agent.ID]
	candidates := s.topKFinder.FindTopK(rateVec, len(neighborIDs)+count)

	result := make([]*model.PostRecord[O], 0, count)
	for i := range len(candidates) {
		if len(result) >= count {
			break
		}
		if agentPicked, ok := s.AgentMap[int64(candidates[i])]; ok {
			post := selectPost(
				s.HistoricalPostCount,
				neighborIDs,
				agentPicked.ID,
				s.Model.Grid.AgentMap,
				visiblePosts,
			)
			if post != nil {
				result = append(result, post)
			}
		}
	}

	return result
}

// Dump implements model.SMPModelRecommendationSystem
func (s *Structure[O, P]) Dump() []byte {
	data, err := msgpack.Marshal(s.ConnMat)
	if err != nil {
		log.Fatalf("Failed to marshal structure recsys dump data: %v", err)
	}
	return data
}
