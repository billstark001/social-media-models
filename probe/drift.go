package probe

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"

	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"
)

type runningSummary struct {
	n      int
	active int
	mean   float64
	m2     float64
}

func (s *runningSummary) add(value float64, active bool) {
	s.n++
	if active {
		s.active++
	}
	delta := value - s.mean
	s.mean += delta / float64(s.n)
	s.m2 += delta * (value - s.mean)
}

func (s runningSummary) result() Summary {
	variance := 0.0
	if s.n > 0 {
		variance = s.m2 / float64(s.n)
	}
	return Summary{
		Mean:     s.mean,
		Variance: variance,
		Samples:  s.n,
		Active:   s.active,
	}
}

type pointAccumulator struct {
	total                 runningSummary
	neighbor              runningSummary
	recommendation        runningSummary
	concordantNeighbor    float64
	concordantRecommended float64
	secondMoment          runningSummary
	noUpdate              runningSummary
}

func (a *pointAccumulator) add(total, neighbor, recommendation, second, noUpdate float64, nNeighbor, nRecommended int) {
	a.total.add(total, nNeighbor+nRecommended > 0)
	a.neighbor.add(neighbor, nNeighbor > 0)
	a.recommendation.add(recommendation, nRecommended > 0)
	a.secondMoment.add(second, nNeighbor+nRecommended > 0)
	a.noUpdate.add(noUpdate, false)
	a.concordantNeighbor += float64(nNeighbor)
	a.concordantRecommended += float64(nRecommended)
}

func (a pointAccumulator) point(x float64) PointResult {
	samples := max(a.total.n, 1)
	variance := a.secondMoment.mean - a.total.mean*a.total.mean
	if variance < 0 && variance > -1e-12 {
		variance = 0
	}
	return PointResult{X: x, FProbe: a.total.result(), FNeighbor: a.neighbor.result(), FRecommendation: a.recommendation.result(), MeanConcordantNeighbor: a.concordantNeighbor / float64(samples), MeanConcordantRecommendation: a.concordantRecommended / float64(samples), UpdateSecondMoment: a.secondMoment.result(), UpdateVariance: variance, NoUpdateProbability: a.noUpdate.mean}
}

func conditionalDeffuantMoments(x float64, neighborPosts, recommended []*model.PostRecord[float64], params driftParams) (second, noUpdate float64) {
	sum := 0.0
	count := 0
	zeros := 0
	for _, list := range [][]*model.PostRecord[float64]{neighborPosts, recommended} {
		for _, post := range list {
			if post != nil && math.Abs(post.Opinion-x) <= params.tolerance {
				delta := params.influence * (post.Opinion - x)
				sum += delta * delta
				count++
				if delta == 0 {
					zeros++
				}
			}
		}
	}
	if count == 0 {
		return 0, 1
	}
	return sum / float64(count), float64(zeros) / float64(count)
}

func makeModel[P any](
	request *Request,
	state *FrozenState,
	pool *smprng.Pool,
	agentParams *P,
	dynamicsImpl model.Dynamics[float64, P],
) (*model.SMPModel[float64, P], error) {
	graph := utils.DeserializeGraph(&state.Graph)
	nodeCount := graph.Nodes().Len()
	if len(state.Opinions) != nodeCount {
		return nil, fmt.Errorf(
			"state %d has %d opinions for %d graph nodes",
			state.Step,
			len(state.Opinions),
			nodeCount,
		)
	}
	factories := simulation.GetFloat64RecsysFactoriesWithParams[P](request.RecSysParams)
	factory, ok := factories[request.RecsysFactoryType]
	if !ok {
		return nil, fmt.Errorf("unknown float64 recommender %q", request.RecsysFactoryType)
	}
	params := &model.SMPModelParams[float64, P]{
		SMPModelPureParams: model.SMPModelPureParams{
			RecsysCount:     request.RecsysCount,
			PostRetainCount: request.PostRetainCount,
		},
		RecsysFactory: factory,
	}
	opinions := append([]float64(nil), state.Opinions...)
	m := model.NewSMPModel(
		graph,
		&opinions,
		params,
		agentParams,
		dynamicsImpl,
		&model.CollectItemOptions{},
		nil,
		pool,
	)
	m.CurStep = state.Step + 1
	for agentID := range m.Grid.PostMap {
		delete(m.Grid.PostMap, agentID)
	}
	for agentID, records := range state.Posts {
		if m.Grid.AgentMap[agentID] == nil {
			return nil, fmt.Errorf("state %d contains posts for unknown agent %d", state.Step, agentID)
		}
		posts := make([]*model.PostRecord[float64], 0, len(records))
		for i := range records {
			record := records[i]
			posts = append(posts, &record)
		}
		m.Grid.PostMap[agentID] = posts
	}
	m.SetAgentCurPosts()
	if m.Recsys != nil {
		m.Recsys.PostInit(nil)
		if preparer, ok := m.Recsys.(model.SMPModelProbePreparer); ok {
			preparer.PrepareProbe()
		}
	}
	return m, nil
}

func selectedAgents[O any, P any](
	m *model.SMPModel[O, P],
	anchorIDs []int64,
) ([]*model.SMPAgent[O, P], error) {
	if len(anchorIDs) == 0 {
		return m.Schedule.Agents, nil
	}
	ids := append([]int64(nil), anchorIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	agents := make([]*model.SMPAgent[O, P], 0, len(ids))
	for _, id := range ids {
		agent := m.Grid.AgentMap[id]
		if agent == nil {
			return nil, fmt.Errorf("unknown anchor agent %d", id)
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

func driftAt(
	x float64,
	neighborPosts []*model.PostRecord[float64],
	recommended []*model.PostRecord[float64],
	params driftParams,
) (total, neighbor, recommendation float64, nNeighbor, nRecommended int) {
	var neighborSum, recommendationSum float64
	for _, post := range neighborPosts {
		if post != nil && math.Abs(post.Opinion-x) <= params.tolerance {
			neighborSum += post.Opinion - x
			nNeighbor++
		}
	}
	for _, post := range recommended {
		if post != nil && math.Abs(post.Opinion-x) <= params.tolerance {
			recommendationSum += post.Opinion - x
			nRecommended++
		}
	}
	if count := nNeighbor + nRecommended; count > 0 {
		neighbor = params.influence * neighborSum / float64(count)
		recommendation = params.influence * recommendationSum / float64(count)
		total = neighbor + recommendation
	}
	return
}

func opinionIndependent(recsysName string) bool {
	switch recsysName {
	case "Random", "Structure", "StructureRandom", "StructureM9":
		return true
	default:
		return false
	}
}

func evaluateDriftDynamics[P any](
	request *Request,
	xValues []float64,
	params driftParams,
	pool *smprng.Pool,
	probeRNG *rand.Rand,
	agentParams *P,
	dynamicsImpl model.Dynamics[float64, P],
) ([]StateResult, error) {
	results := make([]StateResult, len(request.States))
	for stateIndex := range request.States {
		state := &request.States[stateIndex]
		accumulators := make([]pointAccumulator, len(xValues))
		anchorAccumulators := map[int64][]pointAccumulator{}
		for range request.Replicates {
			m, buildErr := makeModel(request, state, pool, agentParams, dynamicsImpl)
			if buildErr != nil {
				return nil, buildErr
			}
			agents, selectErr := selectedAgents(m, request.AnchorIDs)
			if selectErr != nil {
				return nil, selectErr
			}
			for _, agent := range agents {
				var anchorAcc []pointAccumulator
				if request.PerAnchor {
					if _, ok := anchorAccumulators[agent.ID]; !ok {
						anchorAccumulators[agent.ID] = make([]pointAccumulator, len(xValues))
					}
					anchorAcc = anchorAccumulators[agent.ID]
				}
				neighbors := m.Grid.GetNeighbors(agent.ID, false)
				neighborPosts := make([]*model.PostRecord[float64], 0, len(neighbors))
				for _, neighbor := range neighbors {
					if neighbor.CurPost != nil {
						neighborPosts = append(neighborPosts, neighbor.CurPost)
					}
				}

				var sharedRecommendations []*model.PostRecord[float64]
				if opinionIndependent(request.RecsysFactoryType) {
					var recommendationErr error
					sharedRecommendations, recommendationErr = m.GetRecommendationAt(
						agent,
						agent.CurOpinion,
						neighbors,
						probeRNG,
					)
					if recommendationErr != nil {
						return nil, recommendationErr
					}
				}

				for pointIndex, x := range xValues {
					recommended := sharedRecommendations
					if !opinionIndependent(request.RecsysFactoryType) {
						var recommendationErr error
						recommended, recommendationErr = m.GetRecommendationAt(agent, x, neighbors, probeRNG)
						if recommendationErr != nil {
							return nil, recommendationErr
						}
					}
					total, neighbor, recommendation, nNeighbor, nRecommended := driftAt(
						x,
						neighborPosts,
						recommended,
						params,
					)
					second := total * total
					noUpdate := 0.0
					if total == 0 {
						noUpdate = 1
					}
					if request.DynamicsType == simulation.DynamicsTypeDeffuant {
						second, noUpdate = conditionalDeffuantMoments(x, neighborPosts, recommended, params)
					}
					acc := &accumulators[pointIndex]
					acc.add(total, neighbor, recommendation, second, noUpdate, nNeighbor, nRecommended)
					if request.PerAnchor {
						anchorAcc[pointIndex].add(total, neighbor, recommendation, second, noUpdate, nNeighbor, nRecommended)
					}
				}
			}
		}

		result := StateResult{
			Step:   state.Step,
			Points: make([]PointResult, len(xValues)),
		}
		for i, x := range xValues {
			result.Points[i] = accumulators[i].point(x)
		}
		if request.PerAnchor {
			result.AnchorPoints = make(map[int64][]PointResult, len(anchorAccumulators))
			for id, accs := range anchorAccumulators {
				points := make([]PointResult, len(xValues))
				for i, x := range xValues {
					points[i] = accs[i].point(x)
				}
				result.AnchorPoints[id] = points
			}
		}
		results[stateIndex] = result
	}
	return results, nil
}

func measureDrift(request *Request, selected map[string]bool, grid []float64, params driftParams) ([]StateResult, smprng.Spec, error) {
	if !selected["force"] {
		results := make([]StateResult, len(request.States))
		for i := range results {
			results[i].Step = request.States[i].Step
		}
		return results, smprng.Spec{}, nil
	}
	resolved, err := smprng.Resolve(request.RNG)
	if err != nil {
		return nil, smprng.Spec{}, err
	}
	pool, err := smprng.NewPool(resolved)
	if err != nil {
		return nil, smprng.Spec{}, err
	}
	probeRNG := pool.Stream(smprng.StreamProbe)
	var results []StateResult
	switch request.DynamicsType {
	case simulation.DynamicsTypeHK:
		agentParams := request.HKParams
		results, err = evaluateDriftDynamics(request, grid, params, pool, probeRNG, &agentParams, &dynamics.HK{})
	case simulation.DynamicsTypeDeffuant:
		agentParams := request.DeffuantParams
		results, err = evaluateDriftDynamics(request, grid, params, pool, probeRNG, &agentParams, &dynamics.Deffuant{})
	}
	return results, resolved, err
}
