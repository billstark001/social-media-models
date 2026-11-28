package recsys

import (
	"fmt"
	"log"
	"maps"
	"math"
	"math/rand/v2"
	"slices"

	"smp/model"
	smprng "smp/rng"

	"github.com/vmihailenco/msgpack/v5"
)

const structureDumpVersion = 2

// StructureCandidate retains the perturbed score, so a cached ranking can draw
// a fresh order for exact ties on every query without redrawing its noise.
type StructureCandidate struct {
	ID       int64
	Score    float64
	GroupEnd int // exclusive end of this exact-score group in the immutable row
}

// StructureDump is the serialisable snapshot of Structure's cache state.
type StructureDump struct {
	Version        int
	CandidateCache map[int64][]StructureCandidate // immutable score-ranked rows
	RawScoreCache  map[int64]map[int64]float64    // raw 2-hop counts (for StructureRandom.Recommend)
	CacheValid     map[int64]bool
}

func (d StructureDump) validate() error {
	if d.Version != structureDumpVersion {
		return fmt.Errorf("unsupported Structure cache version %d: require %d; start a new run with the current tie rule", d.Version, structureDumpVersion)
	}
	if d.CandidateCache == nil || d.RawScoreCache == nil || d.CacheValid == nil {
		return fmt.Errorf("Structure cache is missing a required index")
	}
	for _, row := range d.CandidateCache {
		for index, candidate := range row {
			if candidate.ID < 0 || candidate.Score < 0 || math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) {
				return fmt.Errorf("Structure cache contains an invalid scored candidate")
			}
			if index > 0 && (row[index-1].Score < candidate.Score ||
				(row[index-1].Score == candidate.Score && row[index-1].ID >= candidate.ID)) {
				return fmt.Errorf("Structure cache candidates are not in canonical score order")
			}
			if candidate.GroupEnd <= index || candidate.GroupEnd > len(row) ||
				row[candidate.GroupEnd-1].Score != candidate.Score ||
				(candidate.GroupEnd < len(row) && row[candidate.GroupEnd].Score == candidate.Score) ||
				(index > 0 && row[index-1].Score == candidate.Score && row[index-1].GroupEnd != candidate.GroupEnd) {
				return fmt.Errorf("Structure cache contains an invalid exact-score group boundary")
			}
		}
	}
	return nil
}

func decodeStructureDump(data []byte) (StructureDump, error) {
	var dump StructureDump
	if err := msgpack.Unmarshal(data, &dump); err != nil {
		return StructureDump{}, fmt.Errorf("decode Structure cache: %w", err)
	}
	if err := dump.validate(); err != nil {
		return StructureDump{}, err
	}
	return dump, nil
}

// Structure implements a recommendation system based on network structure.
// Scores are computed on demand by traversing the two-hop neighborhood.
//
// When useCache is true, row-level lazy caching is enabled: each agent's
// candidate list is reused across steps and only invalidated when its 1-hop
// or 2-hop neighborhood actually changes (via PostStep).
type Structure[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	postVisibility
	Model               *model.SMPModel[O, P]
	HistoricalPostCount int
	AgentCount          int

	LogFunc func(string)

	NumNodes   int
	AllIndices []int
	AgentMap   map[int64]*model.SMPAgent[O, P]

	noiseStd float64

	useCache       bool
	candidateCache map[int64][]StructureCandidate // immutable score-ranked rows
	rawScoreCache  map[int64]map[int64]float64    // raw 2-hop counts per agent (shared with StructureRandom)
	cacheValid     map[int64]bool

	neighborSeen   []uint32
	candidateSeen  []uint32
	rawCounts      []float64
	neighborIDs    []int64
	touchedIDs     []int64
	neighborEpoch  uint32
	candidateEpoch uint32
	ties           tieSampler
	rankedScratch  []StructureCandidate // private uncached/probe query workspace
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
	return ret
}

// PostInit implements model.SMPModelRecommendationSystem.
// When dumpData is provided the cache is restored from the snapshot.
func (s *Structure[O, P]) PostInit(dumpData []byte) {
	s.initWorkspace()
	if s.useCache {
		if dumpData != nil {
			dump, err := decodeStructureDump(dumpData)
			if err != nil {
				panic(err)
			}
			s.candidateCache = dump.CandidateCache
			s.rawScoreCache = dump.RawScoreCache
			s.cacheValid = dump.CacheValid
			return
		}
		s.candidateCache = make(map[int64][]StructureCandidate, s.NumNodes)
		s.rawScoreCache = make(map[int64]map[int64]float64, s.NumNodes)
		s.cacheValid = make(map[int64]bool, s.NumNodes)
	}
}

func (s *Structure[O, P]) initWorkspace() {
	s.NumNodes = s.Model.Graph.Nodes().Len()
	s.AllIndices = make([]int, s.NumNodes)
	for i := range s.NumNodes {
		s.AllIndices[i] = i
	}
	s.AgentMap = make(map[int64]*model.SMPAgent[O, P], s.NumNodes)
	for _, a := range s.Model.Schedule.Agents {
		s.AgentMap[a.ID] = a
	}
	s.neighborSeen = make([]uint32, s.NumNodes)
	s.candidateSeen = make([]uint32, s.NumNodes)
	s.rawCounts = make([]float64, s.NumNodes)
}

type preparedStructureState struct{ dump StructureDump }

// PrepareState decodes once. Cached rows are immutable: recommendation only
// reads them, and a cache miss replaces a whole row rather than changing it.
func (s *Structure[O, P]) PrepareState(data []byte) (model.PreparedRecommendationState, error) {
	dump, err := decodeStructureDump(data)
	if err != nil {
		return nil, err
	}
	return &preparedStructureState{dump: dump}, nil
}

func (p *preparedStructureState) Restore(recommender any) error {
	consumer, ok := recommender.(interface{ restoreStructureCache(StructureDump) })
	if !ok {
		return fmt.Errorf("structure state requires a structure recommender")
	}
	consumer.restoreStructureCache(p.dump)
	return nil
}

func (s *Structure[O, P]) restoreStructureCache(dump StructureDump) {
	s.initWorkspace()
	if s.useCache {
		// The outer indexes and validity flags are private to each branch; immutable
		// score/candidate rows may be shared until recomputation replaces them.
		s.candidateCache = maps.Clone(dump.CandidateCache)
		s.rawScoreCache = maps.Clone(dump.RawScoreCache)
		s.cacheValid = maps.Clone(dump.CacheValid)
	}
}

// PreStep implements model.SMPModelRecommendationSystem (no-op: scores are on-demand)
func (s *Structure[O, P]) PreStep() {}

// invalidateAffected marks cacheValid false for each given node and
// all its undirected 1-hop neighbors (i.e. the nodes whose 2-hop
// neighborhood could have changed due to a rewiring touching that node).
func (s *Structure[O, P]) invalidateAffected(nodes []int64) {
	g := s.Model.Graph
	for _, v := range nodes {
		s.cacheValid[v] = false
		it := g.From(v)
		for it.Next() {
			s.cacheValid[it.Node().ID()] = false
		}
		it = g.To(v)
		for it.Next() {
			s.cacheValid[it.Node().ID()] = false
		}
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

func nextEpoch(epoch *uint32, seen []uint32) uint32 {
	(*epoch)++
	if *epoch == 0 {
		clear(seen)
		*epoch = 1
	}
	return *epoch
}

// countTwoHopPaths fills dense query scratch and returns the touched IDs. The
// consumer must clear those counts after copying or reading them.
func (s *Structure[O, P]) countTwoHopPaths(agentID int64) []int64 {
	g := s.Model.Graph
	neighborEpoch := nextEpoch(&s.neighborEpoch, s.neighborSeen)
	neighbors := s.neighborIDs[:0]
	addNeighbor := func(id int64) {
		if id == agentID || s.neighborSeen[id] == neighborEpoch {
			return
		}
		s.neighborSeen[id] = neighborEpoch
		neighbors = append(neighbors, id)
	}
	it := g.From(agentID)
	for it.Next() {
		addNeighbor(it.Node().ID())
	}
	it = g.To(agentID)
	for it.Next() {
		addNeighbor(it.Node().ID())
	}

	touched := s.touchedIDs[:0]
	for _, neighborID := range neighbors {
		candidateEpoch := nextEpoch(&s.candidateEpoch, s.candidateSeen)
		addCandidate := func(id int64) {
			if id == agentID || s.candidateSeen[id] == candidateEpoch {
				return
			}
			s.candidateSeen[id] = candidateEpoch
			if s.rawCounts[id] == 0 {
				touched = append(touched, id)
			}
			s.rawCounts[id]++
		}
		it = g.From(neighborID)
		for it.Next() {
			addCandidate(it.Node().ID())
		}
		it = g.To(neighborID)
		for it.Next() {
			addCandidate(it.Node().ID())
		}
	}

	s.neighborIDs = neighbors
	s.touchedIDs = touched
	return touched
}

// computeRawScores materializes the immutable map needed by weighted sampling
// and cache snapshots. Ranked uncached queries consume the same dense counts
// directly instead of constructing a map only to read it back by ID.
func (s *Structure[O, P]) computeRawScores(agentID int64) map[int64]float64 {
	touched := s.countTwoHopPaths(agentID)
	result := make(map[int64]float64, len(touched))
	for _, id := range touched {
		result[id] = s.rawCounts[id]
		s.rawCounts[id] = 0
	}
	return result
}

// Recommend selects posts from candidates ranked by common-neighbor score.
// With caching enabled, noisy scores are reused until invalidated; exact ties
// are sampled afresh from the supplied RNG for every query.
func (s *Structure[O, P]) Recommend(agent *model.SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*model.PostRecord[O] {
	rng := s.Model.RNG.Stream(smprng.StreamRecommendation)
	return s.recommend(agent, neighborIDs, count, rng, s.useCache)
}

// RecommendAt implements counterfactual recommendation. Structural scores do
// not depend on opinion, but stochastic ties/history use the probe-owned RNG.
func (s *Structure[O, P]) RecommendAt(
	agent *model.SMPAgent[O, P],
	_ O,
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	return s.recommend(agent, neighborIDs, count, rng, false)
}

func (s *Structure[O, P]) recommend(
	agent *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
	useCache bool,
) []*model.PostRecord[O] {

	// Cache rows retain noisy scores, not one arbitrary order for their ties.
	if useCache && s.cacheValid[agent.ID] {
		if candidates, ok := s.candidateCache[agent.ID]; ok {
			return s.selectRankedPosts(candidates, neighborIDs, count, rng)
		}
	}

	var rawScores map[int64]float64
	var candidateCount int
	if useCache {
		// Persist immutable raw scores for cache snapshots.
		rawScores = s.computeRawScores(agent.ID)
		s.rawScoreCache[agent.ID] = rawScores
		candidateCount = len(rawScores)
	} else {
		candidateCount = len(s.countTwoHopPaths(agent.ID))
	}

	// Sort candidates by descending score (optionally perturbed by noise).
	candidates := s.rankedScratch[:0]
	if useCache {
		// Cached rows are immutable and may be shared by prepared branches.
		candidates = make([]StructureCandidate, 0, candidateCount)
	} else if cap(candidates) < candidateCount {
		candidates = make([]StructureCandidate, 0, candidateCount)
	}
	for index := range s.NumNodes {
		id := int64(index)
		var sc float64
		if useCache {
			sc = rawScores[id]
		} else {
			sc = s.rawCounts[index]
		}
		if sc == 0 {
			continue
		}
		if !useCache {
			s.rawCounts[index] = 0
		}
		if s.noiseStd > 0 {
			noise := rng.NormFloat64() * s.noiseStd
			sc = max(sc*(1-2*noise)+noise, 0)
		}
		candidates = append(candidates, StructureCandidate{ID: id, Score: sc})
	}
	slices.SortFunc(candidates, func(left, right StructureCandidate) int {
		if left.Score != right.Score {
			if left.Score > right.Score {
				return -1
			}
			if right.Score > left.Score {
				return 1
			}
			return 0
		}
		// Canonical enumeration makes RNG replay independent of map order.
		// This key never determines which member of an exact tie is returned.
		if left.ID < right.ID {
			return -1
		}
		if left.ID > right.ID {
			return 1
		}
		return 0
	})
	groupEnd := len(candidates)
	for index := len(candidates) - 1; index >= 0; index-- {
		if index == len(candidates)-1 || candidates[index].Score != candidates[index+1].Score {
			groupEnd = index + 1
		}
		candidates[index].GroupEnd = groupEnd
	}

	// Rows are immutable; per-query tie sampling only touches private scratch.
	if useCache {
		s.candidateCache[agent.ID] = candidates
		s.cacheValid[agent.ID] = true
	} else {
		s.rankedScratch = candidates
	}
	return s.selectRankedPosts(candidates, neighborIDs, count, rng)
}

func (s *Structure[O, P]) selectRankedPosts(
	candidates []StructureCandidate,
	neighborIDs map[int64]bool,
	count int,
	rng *rand.Rand,
) []*model.PostRecord[O] {
	result := make([]*model.PostRecord[O], 0, count)
	appendPost := func(candidate StructureCandidate) {
		if picked, ok := s.AgentMap[candidate.ID]; ok {
			if post := selectPost(s.HistoricalPostCount, neighborIDs, picked.ID, s.Model, s.hideReposts, rng); post != nil {
				result = append(result, post)
			}
		}
	}
	for start := 0; start < len(candidates) && len(result) < count; {
		end := candidates[start].GroupEnd
		if end == start+1 {
			// Preserve both candidate order and RNG consumption outside ties.
			appendPost(candidates[start])
		} else {
			s.ties.reset(end - start)
			for s.ties.remaining > 0 && len(result) < count {
				appendPost(candidates[start+s.ties.draw(rng)])
			}
		}
		start = end
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
		Version:        structureDumpVersion,
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
