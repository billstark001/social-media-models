// Package probe evaluates counterfactual social forces on frozen simulation
// states without advancing or mutating the original simulation.
package probe

import (
	"errors"
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

// Grid specifies an inclusive, evenly-spaced opinion grid.
type Grid struct {
	Min  float64 `msgpack:"min"`
	Max  float64 `msgpack:"max"`
	Step float64 `msgpack:"step"`
}

// FrozenState contains only the state required to evaluate recommendations
// and continuous-opinion force at one completed simulation step.
type FrozenState struct {
	Step     int                                   `msgpack:"step"`
	Graph    utils.NetworkXGraph                   `msgpack:"graph"`
	Opinions []float64                             `msgpack:"opinions"`
	Posts    map[int64][]model.PostRecord[float64] `msgpack:"posts"`
}

// Request is the stdin protocol consumed by cmd/smp-probe.
type Request struct {
	RNG               smprng.Spec             `msgpack:"rng"`
	DynamicsType      string                  `msgpack:"dynamics_type"`
	HKParams          dynamics.HKParams       `msgpack:"hk_params"`
	DeffuantParams    dynamics.DeffuantParams `msgpack:"deffuant_params"`
	RecsysFactoryType string                  `msgpack:"recsys_factory_type"`
	RecSysParams      map[string]any          `msgpack:"recsys_params"`
	RecsysCount       int                     `msgpack:"recsys_count"`
	PostRetainCount   int                     `msgpack:"post_retain_count"`
	Grid              Grid                    `msgpack:"grid"`
	Replicates        int                     `msgpack:"replicates"`
	AnchorIDs         []int64                 `msgpack:"anchor_ids"`
	States            []FrozenState           `msgpack:"states"`
}

// Summary reports the distribution across anchor-agent/replicate samples.
// Variance is the population variance and Active is the number of samples with
// at least one concordant post for this force component.
type Summary struct {
	Mean     float64 `msgpack:"mean"`
	Variance float64 `msgpack:"variance"`
	Samples  int     `msgpack:"samples"`
	Active   int     `msgpack:"active"`
}

// PointResult reports the dynamics' expected force (FProbe) plus additive
// neighbor and recommendation components. Concordant counts expose the feed
// composition needed by node-only or node-edge analysis.
type PointResult struct {
	X                            float64 `msgpack:"x"`
	FProbe                       Summary `msgpack:"f_probe"`
	FNeighbor                    Summary `msgpack:"f_neighbor"`
	FRecommendation              Summary `msgpack:"f_recommendation"`
	MeanConcordantNeighbor       float64 `msgpack:"mean_concordant_neighbor"`
	MeanConcordantRecommendation float64 `msgpack:"mean_concordant_recommendation"`
}

type StateResult struct {
	Step   int           `msgpack:"step"`
	Points []PointResult `msgpack:"points"`
}

type Response struct {
	RNG          smprng.Spec   `msgpack:"rng"`
	DynamicsType string        `msgpack:"dynamics_type"`
	Results      []StateResult `msgpack:"results"`
}

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
}

type forceParams struct {
	tolerance float64
	influence float64
}

func gridValues(grid Grid) ([]float64, error) {
	if math.IsNaN(grid.Min) || math.IsNaN(grid.Max) || math.IsNaN(grid.Step) ||
		math.IsInf(grid.Min, 0) || math.IsInf(grid.Max, 0) || math.IsInf(grid.Step, 0) {
		return nil, errors.New("grid values must be finite")
	}
	if grid.Step <= 0 {
		return nil, fmt.Errorf("grid step must be > 0, got %v", grid.Step)
	}
	if grid.Max < grid.Min {
		return nil, fmt.Errorf("grid max %v is less than min %v", grid.Max, grid.Min)
	}
	span := grid.Max - grid.Min
	intervals := int(math.Round(span / grid.Step))
	if math.Abs(float64(intervals)*grid.Step-span) > 1e-9*max(1, math.Abs(span)) {
		return nil, fmt.Errorf("grid step %v does not evenly divide [%v, %v]", grid.Step, grid.Min, grid.Max)
	}
	values := make([]float64, intervals+1)
	for i := range values {
		values[i] = grid.Min + float64(i)*grid.Step
	}
	values[len(values)-1] = grid.Max
	return values, nil
}

func validateRequest(request *Request) ([]float64, error) {
	if request == nil {
		return nil, errors.New("nil probe request")
	}
	values, err := gridValues(request.Grid)
	if err != nil {
		return nil, err
	}
	if request.Replicates <= 0 {
		request.Replicates = 1
	}
	if request.RecsysCount <= 0 {
		return nil, fmt.Errorf("recsys_count must be > 0, got %d", request.RecsysCount)
	}
	if request.PostRetainCount < 0 {
		return nil, fmt.Errorf("post_retain_count must be >= 0, got %d", request.PostRetainCount)
	}
	if len(request.States) == 0 {
		return nil, errors.New("at least one frozen state is required")
	}
	return values, nil
}

func paramsForRequest(request *Request) (forceParams, error) {
	var params forceParams
	switch request.DynamicsType {
	case simulation.DynamicsTypeHK:
		params = forceParams{
			tolerance: request.HKParams.Tolerance,
			influence: request.HKParams.Influence,
		}
	case simulation.DynamicsTypeDeffuant:
		params = forceParams{
			tolerance: request.DeffuantParams.Tolerance,
			influence: request.DeffuantParams.Influence,
		}
	default:
		return params, fmt.Errorf(
			"unsupported dynamics_type %q (supported: HK, Deffuant)",
			request.DynamicsType,
		)
	}
	if math.IsNaN(params.tolerance) || math.IsInf(params.tolerance, 0) || params.tolerance < 0 {
		return params, fmt.Errorf("%s tolerance must be finite and >= 0", request.DynamicsType)
	}
	if math.IsNaN(params.influence) || math.IsInf(params.influence, 0) ||
		params.influence < 0 || params.influence > 1 {
		return params, fmt.Errorf("%s influence must be in [0, 1]", request.DynamicsType)
	}
	return params, nil
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

func forceAt(
	x float64,
	neighborPosts []*model.PostRecord[float64],
	recommended []*model.PostRecord[float64],
	params forceParams,
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

func evaluateDynamics[P any](
	request *Request,
	xValues []float64,
	params forceParams,
	pool *smprng.Pool,
	probeRNG *rand.Rand,
	agentParams *P,
	dynamicsImpl model.Dynamics[float64, P],
) ([]StateResult, error) {
	results := make([]StateResult, len(request.States))
	for stateIndex := range request.States {
		state := &request.States[stateIndex]
		accumulators := make([]pointAccumulator, len(xValues))
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
					total, neighbor, recommendation, nNeighbor, nRecommended := forceAt(
						x,
						neighborPosts,
						recommended,
						params,
					)
					acc := &accumulators[pointIndex]
					acc.total.add(total, nNeighbor+nRecommended > 0)
					acc.neighbor.add(neighbor, nNeighbor > 0)
					acc.recommendation.add(recommendation, nRecommended > 0)
					acc.concordantNeighbor += float64(nNeighbor)
					acc.concordantRecommended += float64(nRecommended)
				}
			}
		}

		result := StateResult{
			Step:   state.Step,
			Points: make([]PointResult, len(xValues)),
		}
		for i, x := range xValues {
			acc := accumulators[i]
			samples := max(acc.total.n, 1)
			result.Points[i] = PointResult{
				X:                            x,
				FProbe:                       acc.total.result(),
				FNeighbor:                    acc.neighbor.result(),
				FRecommendation:              acc.recommendation.result(),
				MeanConcordantNeighbor:       acc.concordantNeighbor / float64(samples),
				MeanConcordantRecommendation: acc.concordantRecommended / float64(samples),
			}
		}
		results[stateIndex] = result
	}
	return results, nil
}

// Evaluate measures F_probe over all requested states, anchors and replicates.
// For Deffuant, FProbe is the conditional expected drift of its uniformly
// selected concordant post; this is the exact expectation of the implemented
// stochastic update rule.
func Evaluate(request *Request) (*Response, error) {
	xValues, err := validateRequest(request)
	if err != nil {
		return nil, err
	}
	params, err := paramsForRequest(request)
	if err != nil {
		return nil, err
	}
	resolved, err := smprng.Resolve(request.RNG)
	if err != nil {
		return nil, err
	}
	pool, err := smprng.NewPool(resolved)
	if err != nil {
		return nil, err
	}
	probeRNG := pool.Stream(smprng.StreamProbe)

	var results []StateResult
	switch request.DynamicsType {
	case simulation.DynamicsTypeHK:
		agentParams := request.HKParams
		results, err = evaluateDynamics(
			request,
			xValues,
			params,
			pool,
			probeRNG,
			&agentParams,
			&dynamics.HK{},
		)
	case simulation.DynamicsTypeDeffuant:
		agentParams := request.DeffuantParams
		results, err = evaluateDynamics(
			request,
			xValues,
			params,
			pool,
			probeRNG,
			&agentParams,
			&dynamics.Deffuant{},
		)
	}
	if err != nil {
		return nil, err
	}
	return &Response{
		RNG:          resolved,
		DynamicsType: request.DynamicsType,
		Results:      results,
	}, nil
}
