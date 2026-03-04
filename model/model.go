package model

import (
	"math"

	"gonum.org/v1/gonum/graph/simple"
)

type SMPModelPureParams struct {
	RecsysCount      int
	TweetRetainCount int
}

type RecsysFactory func(*SMPModel) SMPModelRecommendationSystem

type SMPModelParams struct {
	SMPModelPureParams
	RecsysFactory RecsysFactory
}

type CollectItemOptions struct {
	AgentNumber     bool
	OpinionSum      bool
	RewiringEvent   bool
	ViewTweetsEvent bool
	TweetEvent      bool
}

func DefaultSMPModelParams() *SMPModelParams {
	ret := &SMPModelParams{
		SMPModelPureParams: SMPModelPureParams{
			RecsysCount:      10,
			TweetRetainCount: 3,
		},
	}
	ret.RecsysCount = 10
	ret.TweetRetainCount = 3
	return ret
}

// SMPModel represents the social media platform model
type SMPModel struct {
	// params
	AgentParams  *SMPAgentParams
	ModelParams  *SMPModelParams
	CollectItems *CollectItemOptions
	// state
	Graph *simple.DirectedGraph
	Grid  *NetworkGrid

	// the smallest step that the data is not intact
	// equates the current step during step
	// will be incremented after the simulation step ends
	// starts from 1
	CurStep int

	// utils(?)
	Recsys      SMPModelRecommendationSystem
	Schedule    *RandomActivation
	EventLogger func(*EventRecord)
}

// NewSMPModel creates a new social media platform model
func NewSMPModel(
	graph *simple.DirectedGraph,
	opinions *[]float64,
	modelParams *SMPModelParams,
	agentParams *SMPAgentParams,
	collectItems *CollectItemOptions,
	eventLogger func(*EventRecord),
) *SMPModel {
	// Use default params if none provided
	if modelParams == nil {
		modelParams = DefaultSMPModelParams()
	}
	if agentParams == nil {
		agentParams = DefaultSMPAgentParams()
	}

	// Initialize struct
	model := &SMPModel{
		Graph:        graph,
		ModelParams:  modelParams,
		AgentParams:  agentParams,
		CollectItems: collectItems,
		EventLogger:  eventLogger,
		CurStep:      0,
	}

	// Initialize grid and scheduler
	model.Grid = NewNetworkGrid(graph)
	model.Schedule = NewRandomActivation(model)

	// Initialize recommendation system if factory is provided
	if modelParams.RecsysFactory != nil {
		model.Recsys = modelParams.RecsysFactory(model)
	}

	// Initialize agents
	nodes := graph.Nodes()
	opinionsVal := make([]float64, 0)
	if opinions != nil {
		opinionsVal = *opinions
	}
	i := 0
	for nodes.Next() {
		nodeID := nodes.Node().ID()

		var opinion *float64
		if i < len(opinionsVal) {
			i2 := opinionsVal[nodeID]
			opinion = &i2
		}

		agent := NewSMPAgent(nodeID, model, opinion)
		model.Grid.PlaceAgent(agent, nodeID)
		model.Schedule.AddAgent(agent)
		i++
	}

	// Post-initialization for recommendation system
	return model
}

func (m *SMPModel) SetAgentCurTweets() {
	cntTweetRetain := max(m.ModelParams.TweetRetainCount, 1)
	for aid, a := range m.Grid.AgentMap {
		if a.CurTweet == nil {
			l := len(m.Grid.TweetMap[aid])
			if l == 0 {
				// if no existent tweets, create one
				m.Grid.AddTweet(aid, &TweetRecord{
					AgentID: aid,
					Opinion: a.CurOpinion,
					Step:    -1,
				}, cntTweetRetain)
				l = 1
			}
			// apply the latest one
			a.CurTweet = m.Grid.TweetMap[aid][l-1]
		}
	}
}

// Step advances the model by one time step
func (m *SMPModel) Step(doIncrementCurStep bool) (int, float64) {
	// Pre-step actions for recommendation system
	if m.Recsys != nil {
		m.Recsys.PreStep()
	}

	// Execute agent steps
	m.Schedule.Step()

	// Pre-commit actions for recommendation system
	if m.Recsys != nil {
		m.Recsys.PreCommit()
	}

	// Collect changed nodes
	changed := make([]*RewiringEventBody, 0)
	changedCount := 0
	changedOpinionMax := 0.0

	// Apply changes from agents
	cntTweetRetain := max(m.ModelParams.TweetRetainCount, 1)
	for _, a := range m.Schedule.Agents {
		// Opinion change
		changedOpinion := a.NextOpinion - a.CurOpinion
		a.CurOpinion = a.NextOpinion
		changedOpinionMax = math.Max(changedOpinionMax, math.Abs(changedOpinion))

		// Add tweet if there is one
		if a.NextTweet != nil {
			m.Grid.AddTweet(a.ID, a.NextTweet, cntTweetRetain)
			a.CurTweet = a.NextTweet
		}

		// Rewiring
		if a.NextFollow != nil {
			if !m.Graph.HasEdgeFromTo(a.ID, a.NextFollow.Unfollow) {
				panic("BUG: wrong rewiring parameters")
			}
			// only rewires if the edge is inexistent
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

	// Post-step actions for recommendation system
	if m.Recsys != nil {
		m.Recsys.PostStep(changed)
	}

	// lastly, increment step counter
	if doIncrementCurStep {
		m.CurStep++
	}

	return changedCount, changedOpinionMax
}

func makeSelfAndNeighborIDsMap(agentID int64, neighbors []*SMPAgent) map[int64]bool {
	neighborIDs := make(map[int64]bool)
	neighborIDs[agentID] = true
	for _, n := range neighbors {
		neighborIDs[n.ID] = true
	}
	return neighborIDs
}

// GetRecommendation gets recommendations for an agent
func (m *SMPModel) GetRecommendation(agent *SMPAgent, neighbors []*SMPAgent) []*TweetRecord {
	if m.Recsys == nil {
		return []*TweetRecord{}
	}

	neighborIDs := makeSelfAndNeighborIDsMap(agent.ID, neighbors)

	return m.Recsys.Recommend(agent, neighborIDs, m.ModelParams.RecsysCount)
}

func (m *SMPModel) CollectOpinions() []float64 {
	ret := make([]float64, len(m.Schedule.Agents))
	for _, agent := range m.Schedule.Agents {
		ret[agent.ID] = agent.CurOpinion
	}
	return ret
}

func (m *SMPModel) CollectAgentNumbers() []AgentNumberRecord {
	ret := make([]AgentNumberRecord, len(m.Schedule.Agents))
	for _, agent := range m.Schedule.Agents {
		ret[agent.ID] = agent.AgentNumber
	}
	return ret
}

func (m *SMPModel) CollectAgentOpinions() []AgentOpinionSumRecord {
	ret := make([]AgentOpinionSumRecord, len(m.Schedule.Agents))
	for _, agent := range m.Schedule.Agents {
		ret[agent.ID] = agent.OpinionSum
	}
	return ret
}

// CollectTweets collects all current tweets
func (m *SMPModel) CollectTweets() map[int64][]TweetRecord {
	tweets := make(map[int64][]TweetRecord)
	for agent, value := range m.Grid.TweetMap {
		tweets[agent] = []TweetRecord{}
		for _, ptr := range value {
			tweets[agent] = append(tweets[agent], *ptr)
		}
	}
	return tweets
}
