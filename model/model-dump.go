package model

import utils "smp/utils"

type SMPModelDumpData[O any, P any] struct {
	CurStep          int
	Graph            utils.NetworkXGraph
	Opinions         []O
	AgentNumbers     []AgentNumberRecord
	AgentOpinionSums []AgentOpinionSumRecord
	Posts            map[int64][]PostRecord[O]
	RecsysDumpData   []byte
}

func (m *SMPModel[O, P]) Dump() *SMPModelDumpData[O, P] {
	ret := &SMPModelDumpData[O, P]{
		CurStep:          m.CurStep,
		Graph:            *utils.SerializeGraph(m.Graph),
		Opinions:         m.CollectOpinions(),
		AgentNumbers:     m.CollectAgentNumbers(),
		AgentOpinionSums: m.CollectAgentOpinions(),
		Posts:            m.CollectPosts(),
	}
	if m.Recsys != nil {
		ret.RecsysDumpData = m.Recsys.Dump()
	}
	return ret
}

func (d *SMPModelDumpData[O, P]) Load(
	modelParams *SMPModelParams[O, P],
	agentParams *P,
	dynamics Dynamics[O, P],
	collectItems *CollectItemOptions,
	eventLogger func(*EventRecord),
) *SMPModel[O, P] {
	m := NewSMPModel(
		utils.DeserializeGraph(&d.Graph),
		&d.Opinions,
		modelParams,
		agentParams,
		dynamics,
		collectItems,
		eventLogger,
	)

	for _, agent := range m.Schedule.Agents {
		agent.AgentNumber = d.AgentNumbers[int(agent.ID)]
		agent.OpinionSum = d.AgentOpinionSums[int(agent.ID)]
	}

	m.CurStep = d.CurStep

	g := m.Grid
	for agentID, value := range d.Posts {
		g.PostMap[agentID] = []*PostRecord[O]{}
		for _, ptr := range value {
			p := ptr
			g.PostMap[agentID] = append(g.PostMap[agentID], &p)
		}
	}

	m.SetAgentCurPosts()

	if m.Recsys != nil {
		m.Recsys.PostInit(d.RecsysDumpData)
	}

	return m
}
