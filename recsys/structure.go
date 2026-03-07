package recsys

import (
	"log"
	"math/rand/v2"
	"sort"

	"smp/model"

	"github.com/vmihailenco/msgpack/v5"
)

// StructureDump is the serialisable snapshot of Structure's cache state.
type StructureDump struct {
	CandidateCache map[int64][]int64           // sorted candidate IDs (for Structure.Recommend)
	RawScoreCache  map[int64]map[int64]float64 // raw 2-hop counts (for StructureRandom.Recommend)
	CacheValid     map[int64]bool
}

// Structure implements a recommendation system based on network structure.
// Scores are computed on demand per Recommend call by traversing the 2-hop
// neighborhood, so no pre-built matrix is maintained. This makes the system
// efficient even when the graph rewires very frequently (e.g. voter model).
//
// When useCache is true, row-level lazy caching is enabled: each agent's
// candidate list is reused across steps and only invalidated when its 1-hop
// or 2-hop neighborhood actually changes (via PostStep).
type Structure[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	Model               *model.SMPModel[O, P]
	HistoricalPostCount int
	AgentCount          int

	LogFunc func(string)

	NumNodes   int
	AllIndices []int
	AgentMap   map[int64]*model.SMPAgent[O, P]

	noiseStd float64
	rng      *rand.Rand

	useCache       bool
	candidateCache map[int64][]int64           // sorted candidate IDs per agent (Structure.Recommend)
	rawScoreCache  map[int64]map[int64]float64 // raw 2-hop counts per agent (shared with StructureRandom)
	cacheValid     map[int64]bool
}

// NewStructure creates a structure-based recommendation system.
// useCache enables row-level lazy caching: a cache line is only invalidated
// when the agent's 1-hop or 2-hop neighborhood actually changes.
func NewStructure[O any, P any](
	m *model.SMPModel[O, P],
	noiseStd float64,
	historicalPostCount *int,
	useCache bool,
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
		LogFunc:             logFunc,
		noiseStd:            noiseStd,
		useCache:            useCache,
	}
	ret.rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	return ret
}

// PostInit implements model.SMPModelRecommendationSystem.
// When dumpData is provided the cache is restored from the snapshot.
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

	if s.useCache {
		if dumpData != nil {
			var dump StructureDump
			if err := msgpack.Unmarshal(dumpData, &dump); err == nil {
				s.candidateCache = dump.CandidateCache
				s.rawScoreCache = dump.RawScoreCache
				s.cacheValid = dump.CacheValid
				return
			}
		}
		s.candidateCache = make(map[int64][]int64, s.NumNodes)
		s.rawScoreCache = make(map[int64]map[int64]float64, s.NumNodes)
		s.cacheValid = make(map[int64]bool, s.NumNodes)
	}
}

// PreStep implements model.SMPModelRecommendationSystem (no-op: scores are on-demand)
func (s *Structure[O, P]) PreStep() {}

// invalidateAffected marks cacheValid false for each given node and
// all its undirected 1-hop neighbors (i.e. the nodes whose 2-hop
// neighborhood could have changed due to a rewiring touching that node).
func (s *Structure[O, P]) invalidateAffected(nodes []int64) {
	g := s.Model.Graph
	toInvalidate := make(map[int64]struct{}, len(nodes)*8)
	for _, v := range nodes {
		toInvalidate[v] = struct{}{}
		it := g.From(v)
		for it.Next() {
			toInvalidate[it.Node().ID()] = struct{}{}
		}
		it = g.To(v)
		for it.Next() {
			toInvalidate[it.Node().ID()] = struct{}{}
		}
	}
	for id := range toInvalidate {
		s.cacheValid[id] = false
	}
}

// PostStep implements model.SMPModelRecommendationSystem.
// For each rewiring event it invalidates the cache of every agent whose
// 1-hop or 2-hop neighborhood could have been affected.
func (s *Structure[O, P]) PostStep(changed []*model.RewiringEventBody) {
	if !s.useCache || len(changed) == 0 {
		return
	}
	affected := make([]int64, 0, len(changed)*3)
	for _, ev := range changed {
		affected = append(affected, ev.AgentID, ev.Unfollow, ev.Follow)
	}
	s.invalidateAffected(affected)
}

// Recommend implements model.SMPModelRecommendationSystem.
//
// Cache enabled (useCache=true):
//   - Cache hit:  O(count) – iterate cached sorted candidate IDs, call selectPost.
//   - Cache miss: O(D²) graph traversal + sort, result cached for future calls.
//
// Cache disabled (useCache=false): always O(D²) on-demand computation.
func (s *Structure[O, P]) Recommend(agent *model.SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*model.PostRecord[O] {
	visiblePosts := s.Model.Grid.PostMap

	// Cache hit path.
	if s.useCache && s.cacheValid[agent.ID] {
		if candidates, ok := s.candidateCache[agent.ID]; ok {
			result := make([]*model.PostRecord[O], 0, count)
			for _, cid := range candidates {
				if len(result) >= count {
					break
				}
				if agentPicked, ok2 := s.AgentMap[cid]; ok2 {
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
	}

	g := s.Model.Graph

	// Collect undirected 1-hop neighborhood of agent (union of From and To).
	agentNeighbors := make(map[int64]struct{})
	it := g.From(agent.ID)
	for it.Next() {
		agentNeighbors[it.Node().ID()] = struct{}{}
	}
	it = g.To(agent.ID)
	for it.Next() {
		agentNeighbors[it.Node().ID()] = struct{}{}
	}
	delete(agentNeighbors, agent.ID)

	// For each 1-hop neighbor w, count how many times each 2-hop node
	// appears – that count equals the number of common neighbors with agent.
	rawScores := make(map[int64]float64, len(agentNeighbors)*8)
	for w := range agentNeighbors {
		wNeighbors := make(map[int64]struct{})
		it := g.From(w)
		for it.Next() {
			wNeighbors[it.Node().ID()] = struct{}{}
		}
		it = g.To(w)
		for it.Next() {
			wNeighbors[it.Node().ID()] = struct{}{}
		}
		for v := range wNeighbors {
			if v != agent.ID {
				rawScores[v]++
			}
		}
	}

	// Persist raw scores so StructureRandom.Recommend can reuse them.
	if s.useCache {
		s.rawScoreCache[agent.ID] = rawScores
	}

	// Sort candidates by descending score (optionally perturbed by noise).
	type scored struct {
		id    int64
		score float64
	}
	candidates := make([]scored, 0, len(rawScores))
	for id, sc := range rawScores {
		if s.noiseStd > 0 {
			noise := s.rng.NormFloat64() * s.noiseStd
			sc = max(sc*(1-2*noise)+noise, 0)
		}
		candidates = append(candidates, scored{id, sc})
	}
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].score > candidates[b].score
	})

	// Cache the sorted candidate ID list.
	if s.useCache {
		ids := make([]int64, len(candidates))
		for i, c := range candidates {
			ids[i] = c.id
		}
		s.candidateCache[agent.ID] = ids
		s.cacheValid[agent.ID] = true
	}

	result := make([]*model.PostRecord[O], 0, count)
	for _, c := range candidates {
		if len(result) >= count {
			break
		}
		if agentPicked, ok := s.AgentMap[c.id]; ok {
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

// Dump implements model.SMPModelRecommendationSystem.
// When useCache is true the full cache state is serialized with msgpack so
// it can be restored by PostInit after a snapshot load.
func (s *Structure[O, P]) Dump() []byte {
	if !s.useCache {
		return nil
	}
	dump := StructureDump{
		CandidateCache: s.candidateCache,
		RawScoreCache:  s.rawScoreCache,
		CacheValid:     s.cacheValid,
	}
	data, err := msgpack.Marshal(dump)
	if err != nil {
		log.Printf("Failed to marshal Structure cache dump: %v", err)
		return nil
	}
	return data
}
