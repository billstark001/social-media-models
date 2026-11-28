package bridges

import (
	"fmt"
	"smp/groups"
	"smp/model"
	"smp/utils"
)

// FixedGroupsFrame is an explicit experimental reference, not a claim that a
// dynamic detector has confirmed these cores. IDs are 1..K; members are copied.
func FixedGroupsFrame(step int, cores [][]int) groups.Frame {
	f := groups.Frame{Step: step, Status: "fixed_cohorts", Groups: make([]groups.Group, len(cores))}
	for k, ids := range cores {
		f.Groups[k] = groups.Group{ID: int64(k + 1), State: "observed", Core: groups.Core{ID: k, Members: append([]int(nil), ids...), Mass: len(ids)}, FirstObservedStep: step, ConfirmedStep: step, LastObservedStep: step}
	}
	return f
}

// NativeMeasure adapts an existing live model without advancing it, touching
// RNG, or substituting retained history for the actual latest displayed post.
// step is explicit: historical experiments can label native model states using
// their own documented completed-round convention.
func NativeMeasure[P any](step int, m *model.SMPModel[float64, P], observer *Observer, frame groups.Frame) (Frame, error) {
	if m == nil || m.Graph == nil || m.Grid == nil || m.Grid.Graph != m.Graph || m.Schedule == nil || observer == nil || observer.pairs == nil || observer.closed {
		return Frame{}, fmt.Errorf("bridges: invalid native model or observer")
	}
	n := len(m.Schedule.Agents)
	if n == 0 || len(m.Grid.AgentMap) != n || m.Graph.Nodes().Len() != n || observer.started && observer.n != n {
		return Frame{}, fmt.Errorf("bridges: native population, schedule and graph coverage differ")
	}
	if len(observer.nativeOpinions) != n {
		observer.nativeOpinions = make([]float64, n)
		observer.nativeRecords = make([]model.PostRecord[float64], n)
		observer.nativeSeen = make([]bool, n)
		observer.nativePosts = make(map[int64][]model.PostRecord[float64], n)
		observer.nativeGraph = utils.NetworkXGraph{Directed: true, Nodes: make(map[int64]map[string]any, n), Adjacency: make(map[int64]map[int64]any, n)}
		for i := 0; i < n; i++ {
			observer.nativeGraph.Nodes[int64(i)] = nil
			observer.nativeGraph.Adjacency[int64(i)] = make(map[int64]any)
		}
	}
	clear(observer.nativeSeen)
	clear(observer.nativePosts)
	for _, agent := range m.Schedule.Agents {
		if agent == nil || agent.ID < 0 || agent.ID >= int64(n) || observer.nativeSeen[agent.ID] || m.Grid.AgentMap[agent.ID] != agent || m.Graph.Node(agent.ID) == nil {
			return Frame{}, fmt.Errorf("bridges: invalid or inconsistent native agent identity")
		}
		holder := agent.ID
		observer.nativeSeen[holder] = true
		observer.nativeOpinions[holder] = agent.CurOpinion
		if agent.CurPost != nil {
			observer.nativeRecords[holder] = *agent.CurPost
			observer.nativePosts[holder] = observer.nativeRecords[holder : holder+1]
		}
	}
	// Bridge inputs only need structural adjacency. Reuse row buckets and copy
	// neighbor IDs directly instead of allocating per-edge attribute maps and
	// a complete serialized graph. This workspace never escapes a result.
	for from := 0; from < n; from++ {
		row := observer.nativeGraph.Adjacency[int64(from)]
		clear(row)
		neighbors := m.Graph.From(int64(from))
		for neighbors.Next() {
			row[neighbors.Node().ID()] = nil
		}
	}
	return observer.Measure(step, observer.nativeOpinions, observer.nativeGraph, observer.nativePosts, frame)
}
