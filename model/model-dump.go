package model

import utils "smp/utils"

type SMPModelDumpData struct {
	CurStep          int
	Graph            utils.NetworkXGraph
	Opinions         []float64
	AgentNumbers     []AgentNumberRecord
	AgentOpinionSums []AgentOpinionSumRecord
	Tweets           map[int64][]TweetRecord
	RecsysDumpData   []byte // no pointer
}

func (m *SMPModel) Dump() *SMPModelDumpData {
	ret := &SMPModelDumpData{
		CurStep:          m.CurStep,
		Graph:            *utils.SerializeGraph(m.Graph),
		Opinions:         m.CollectOpinions(),
		AgentNumbers:     m.CollectAgentNumbers(),
		AgentOpinionSums: m.CollectAgentOpinions(),
		Tweets:           m.CollectTweets(),
	}
	if m.Recsys != nil {
		ret.RecsysDumpData = m.Recsys.Dump()
	}
	return ret
}

func (d *SMPModelDumpData) Load(
	modelParams *SMPModelParams,
	agentParams *SMPAgentParams,
	collectItems *CollectItemOptions,
	eventLogger func(*EventRecord),
) *SMPModel {
	model := NewSMPModel(
		utils.DeserializeGraph(&d.Graph),
		&d.Opinions,
		modelParams,
		agentParams,
		collectItems,
		eventLogger,
	)

	// recover agent numbers and opinion sums
	for _, agent := range model.Schedule.Agents {
		agent.AgentNumber = d.AgentNumbers[int(agent.ID)]
		agent.OpinionSum = d.AgentOpinionSums[int(agent.ID)]
	}

	// recover step
	model.CurStep = d.CurStep

	// recover tweets
	g := model.Grid
	for agent, value := range d.Tweets {
		g.TweetMap[agent] = []*TweetRecord{}
		for _, ptr := range value {
			g.TweetMap[agent] = append(g.TweetMap[agent], &ptr)
		}
	}

	// recover tweets
	model.SetAgentCurTweets()

	// recover dump data
	if model.Recsys != nil {
		// pointer is passed
		model.Recsys.PostInit(d.RecsysDumpData)
	}

	return model
}
