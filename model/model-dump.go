package model

import (
	smprng "smp/rng"
	utils "smp/utils"
)

type SMPModelDumpData[O any, P any] struct {
	CurStep          int
	Graph            utils.NetworkXGraph
	Opinions         []O
	AgentNumbers     []AgentNumberRecord
	AgentOpinionSums []AgentOpinionSumRecord
	Posts            map[int64][]PostRecord[O]
	RecsysDumpData   []byte
	RNGStates        map[string][]byte
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
	rngStates, err := m.RNG.Snapshot()
	if err != nil {
		panic(err)
	}
	ret.RNGStates = rngStates
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
	rngPool *smprng.Pool,
) *SMPModel[O, P] {
	return d.LoadWithRecommenderState(modelParams, agentParams, dynamics, collectItems, eventLogger, rngPool, nil)
}

// LoadWithRecommenderState uses the same full-state loader as Load, with optional
// reusable decoding of a recommender snapshot. It never shares mutable agents,
// graph, posts or RNG with the source checkpoint.
func (d *SMPModelDumpData[O, P]) LoadWithRecommenderState(
	modelParams *SMPModelParams[O, P],
	agentParams *P,
	dynamics Dynamics[O, P],
	collectItems *CollectItemOptions,
	eventLogger func(*EventRecord),
	rngPool *smprng.Pool,
	prepare func(SMPModelRecommendationSystem[O, P]) PreparedRecommendationState,
) *SMPModel[O, P] {
	m := NewSMPModel(
		utils.DeserializeGraph(&d.Graph),
		&d.Opinions,
		modelParams,
		agentParams,
		dynamics,
		collectItems,
		eventLogger,
		rngPool,
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
		var state PreparedRecommendationState
		if prepare != nil {
			state = prepare(m.Recsys)
		}
		if state != nil {
			if err := state.Restore(m.Recsys); err != nil {
				panic(err)
			}
		} else {
			m.Recsys.PostInit(d.RecsysDumpData)
		}
	}
	// Constructors and PostInit may derive temporary state. Restore last so the
	// next simulated action starts at exactly the snapshotted RNG position.
	if len(d.RNGStates) > 0 {
		if err := rngPool.Restore(d.RNGStates); err != nil {
			panic(err)
		}
	}

	return m
}
