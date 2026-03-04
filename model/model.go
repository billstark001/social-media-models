package model

import (
	"math"
	"math/rand"

	"gonum.org/v1/gonum/graph/simple"
)

type SMPModelPureParams struct {
	RecsysCount     int
	PostRetainCount int
}

type RecsysFactory[O any, P any] func(*SMPModel[O, P]) SMPModelRecommendationSystem[O, P]

type SMPModelParams[O any, P any] struct {
	SMPModelPureParams
	RecsysFactory RecsysFactory[O, P]
}

type CollectItemOptions struct {
	AgentNumber    bool
	OpinionSum     bool
	RewiringEvent  bool
	ViewPostsEvent bool
	PostEvent      bool
}

func DefaultSMPModelParams[O any, P any]() *SMPModelParams[O, P] {
	return &SMPModelParams[O, P]{
		SMPModelPureParams: SMPModelPureParams{
			RecsysCount:     10,
			PostRetainCount: 3,
		},
	}
}

// SMPModel represents the social media platform model.
type SMPModel[O any, P any] struct {
	AgentParams  *P
	ModelParams  *SMPModelParams[O, P]
	CollectItems *CollectItemOptions
	Dynamics     Dynamics[O, P]

	Graph *simple.DirectedGraph
	Grid  *NetworkGrid[O, P]

	// CurStep starts from 1; incremented after each simulation step.
	CurStep int

	Recsys      SMPModelRecommendationSystem[O, P]
	Schedule    *RandomActivation[O, P]
	EventLogger func(*EventRecord)
}

// NewSMPModel creates a new social media platform model.
func NewSMPModel[O any, P any](
	graph *simple.DirectedGraph,
	opinions *[]O,
	modelParams *SMPModelParams[O, P],
	agentParams *P,
	dynamics Dynamics[O, P],
	collectItems *CollectItemOptions,
	eventLogger func(*EventRecord),
) *SMPModel[O, P] {
	if modelParams == nil {
		modelParams = DefaultSMPModelParams[O, P]()
	}

	m := &SMPModel[O, P]{
		Graph:        graph,
		ModelParams:  modelParams,
		AgentParams:  agentParams,
		Dynamics:     dynamics,
		CollectItems: collectItems,
		EventLogger:  eventLogger,
		CurStep:      0,
	}

	m.Grid = NewNetworkGrid[O, P](graph)
	m.Schedule = NewRandomActivation(m)

	if modelParams.RecsysFactory != nil {
		m.Recsys = modelParams.RecsysFactory(m)
	}

	opinionsVal := make([]O, 0)
	if opinions != nil {
		opinionsVal = *opinions
	}

	nodes := graph.Nodes()
	i := 0
	for nodes.Next() {
		nodeID := nodes.Node().ID()
		var opinion O
		if int(nodeID) < len(opinionsVal) {
			opinion = opinionsVal[nodeID]
		}
		agent := NewSMPAgent(nodeID, m, opinion)
		m.Grid.PlaceAgent(agent, nodeID)
		m.Schedule.AddAgent(agent)
		i++
	}

	return m
}

func (m *SMPModel[O, P]) SetAgentCurPosts() {
	cntPostRetain := max(m.ModelParams.PostRetainCount, 1)
	for aid, a := range m.Grid.AgentMap {
		if a.CurPost == nil {
			l := len(m.Grid.PostMap[aid])
			if l == 0 {
				m.Grid.AddPost(aid, &PostRecord[O]{
					AgentID: aid,
					Opinion: a.CurOpinion,
					Step:    -1,
				}, cntPostRetain)
				l = 1
			}
			a.CurPost = m.Grid.PostMap[aid][l-1]
		}
	}
}

// Step advances the model by one time step.
func (m *SMPModel[O, P]) Step(doIncrementCurStep bool) (int, float64) {
	if m.Recsys != nil {
		m.Recsys.PreStep()
	}

	m.Schedule.Step()

	if m.Recsys != nil {
		m.Recsys.PreCommit()
	}

	changed := make([]*RewiringEventBody, 0)
	changedCount := 0
	changedOpinionMax := 0.0

	cntPostRetain := max(m.ModelParams.PostRetainCount, 1)
	for _, a := range m.Schedule.Agents {
		// Convert opinion change to float64 for comparison - use interface trick
		// Since we can't subtract generic O values here, rely on Dynamics
		// We store float64 changedOpinion via type assertion if O=float64
		if f, ok := any(a.NextOpinion).(float64); ok {
			if cf, ok2 := any(a.CurOpinion).(float64); ok2 {
				diff := math.Abs(f - cf)
				if diff > changedOpinionMax {
					changedOpinionMax = diff
				}
			}
		}
		a.CurOpinion = a.NextOpinion

		if a.NextPost != nil {
			m.Grid.AddPost(a.ID, a.NextPost, cntPostRetain)
			a.CurPost = a.NextPost
		}

		if a.NextFollow != nil {
			if !m.Graph.HasEdgeFromTo(a.ID, a.NextFollow.Unfollow) {
				panic("BUG: wrong rewiring parameters")
			}
			if !m.Graph.HasEdgeFromTo(a.ID, a.NextFollow.Follow) {
				m.Graph.RemoveEdge(a.ID, a.NextFollow.Unfollow)
				m.Graph.SetEdge(m.Graph.NewEdge(
					m.Graph.Node(a.ID),
					m.Graph.Node(a.NextFollow.Follow),
				))
				changed = append(changed, a.NextFollow)
				changedCount++
			}
		}
	}

	if m.Recsys != nil {
		m.Recsys.PostStep(changed)
	}

	if doIncrementCurStep {
		m.CurStep++
	}

	return changedCount, changedOpinionMax
}

func makeSelfAndNeighborIDsMap[O any, P any](agentID int64, neighbors []*SMPAgent[O, P]) map[int64]bool {
	neighborIDs := make(map[int64]bool)
	neighborIDs[agentID] = true
	for _, n := range neighbors {
		neighborIDs[n.ID] = true
	}
	return neighborIDs
}

// GetRecommendation gets recommendations for an agent.
func (m *SMPModel[O, P]) GetRecommendation(agent *SMPAgent[O, P], neighbors []*SMPAgent[O, P]) []*PostRecord[O] {
	if m.Recsys == nil {
		return []*PostRecord[O]{}
	}
	neighborIDs := makeSelfAndNeighborIDsMap(agent.ID, neighbors)
	return m.Recsys.Recommend(agent, neighborIDs, m.ModelParams.RecsysCount)
}

func (m *SMPModel[O, P]) CollectOpinions() []O {
	ret := make([]O, len(m.Schedule.Agents))
	for _, agent := range m.Schedule.Agents {
		ret[agent.ID] = agent.CurOpinion
	}
	return ret
}

func (m *SMPModel[O, P]) CollectAgentNumbers() []AgentNumberRecord {
	ret := make([]AgentNumberRecord, len(m.Schedule.Agents))
	for _, agent := range m.Schedule.Agents {
		ret[agent.ID] = agent.AgentNumber
	}
	return ret
}

func (m *SMPModel[O, P]) CollectAgentOpinions() []AgentOpinionSumRecord {
	ret := make([]AgentOpinionSumRecord, len(m.Schedule.Agents))
	for _, agent := range m.Schedule.Agents {
		ret[agent.ID] = agent.OpinionSum
	}
	return ret
}

// CollectPosts collects all current posts.
func (m *SMPModel[O, P]) CollectPosts() map[int64][]PostRecord[O] {
	posts := make(map[int64][]PostRecord[O])
	for agentID, value := range m.Grid.PostMap {
		posts[agentID] = []PostRecord[O]{}
		for _, ptr := range value {
			posts[agentID] = append(posts[agentID], *ptr)
		}
	}
	return posts
}

// NewSMPModelFloat64 is a convenience constructor for the common float64 opinion case,
// generating random opinions in [-1, 1] when opinions is nil.
func NewSMPModelFloat64[P any](
	graph *simple.DirectedGraph,
	opinions *[]float64,
	modelParams *SMPModelParams[float64, P],
	agentParams *P,
	dynamics Dynamics[float64, P],
	collectItems *CollectItemOptions,
	eventLogger func(*EventRecord),
) *SMPModel[float64, P] {
	if opinions == nil {
		n := graph.Nodes().Len()
		ops := make([]float64, n)
		for i := range ops {
			ops[i] = rand.Float64()*2 - 1
		}
		opinions = &ops
	}
	return NewSMPModel(graph, opinions, modelParams, agentParams, dynamics, collectItems, eventLogger)
}
