package recsys

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
)

func structureTieModel(seed uint64, cache bool) *model.SMPModel[float64, dynamics.HKParams] {
	graph := simple.NewDirectedGraph()
	opinions := make([]float64, 6)
	for id := range opinions {
		graph.AddNode(simple.Node(id))
	}
	for _, edge := range [][2]int64{{0, 1}, {1, 2}, {1, 3}, {1, 4}, {1, 5}} {
		graph.SetEdge(graph.NewEdge(graph.Node(edge[0]), graph.Node(edge[1])))
	}
	params := &model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: model.SMPModelPureParams{RecsysCount: 2, PostRetainCount: 0},
		RecsysFactory: func(m *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
			return NewStructure(m, 0, nil, cache, nil)
		},
	}
	m := model.NewSMPModel(graph, &opinions, params, &dynamics.HKParams{}, &dynamics.HK{},
		&model.CollectItemOptions{}, nil, smprng.MustNewPool(smprng.FixedSpec(seed, 91)))
	m.CurStep = 1
	m.SetAgentCurPosts()
	m.Recsys.PostInit(nil)
	return m
}

func structureTieIDs(posts []*model.PostRecord[float64]) []int64 {
	ids := make([]int64, len(posts))
	for index, post := range posts {
		ids[index] = post.AgentID
	}
	return ids
}

func TestStructureTiesAreFreshUniformAndWithoutReplacement(t *testing.T) {
	const trials = 12000
	for _, cache := range []bool{false, true} {
		m := structureTieModel(73, cache)
		base := m.Recsys.(*Structure[float64, dynamics.HKParams])
		agent := m.Grid.AgentMap[0]
		neighbors := map[int64]bool{0: true, 1: true}
		firstCounts := [6]int{}
		for range trials {
			posts := base.Recommend(agent, neighbors, 2)
			if len(posts) != 2 || posts[0].AgentID == posts[1].AgentID {
				t.Fatalf("cache=%t: invalid sample %v", cache, structureTieIDs(posts))
			}
			for _, post := range posts {
				if post.AgentID < 2 || post.AgentID > 5 {
					t.Fatalf("cache=%t: selected ineligible author %d", cache, post.AgentID)
				}
			}
			firstCounts[posts[0].AgentID]++
		}
		for id := 2; id <= 5; id++ {
			if firstCounts[id] < 2750 || firstCounts[id] > 3250 {
				t.Fatalf("cache=%t: unequal exposure across exact ties: %v", cache, firstCounts)
			}
		}
		if cache {
			row := base.candidateCache[0]
			for index, candidate := range row {
				if candidate.ID != int64(index+2) || candidate.Score != 1 || candidate.GroupEnd != 4 {
					t.Fatalf("sampling mutated cached scores/order: %v", row)
				}
			}
		}
	}
}

func TestStructureTiesFilterAndPreserveScorePriority(t *testing.T) {
	m := structureTieModel(73, true)
	base := m.Recsys.(*Structure[float64, dynamics.HKParams])
	// Two unavailable authors share the highest score with two valid authors.
	// The lower-score candidate must never replace either valid high-score one.
	row := []StructureCandidate{{ID: 1, Score: 3, GroupEnd: 4}, {ID: 2, Score: 3, GroupEnd: 4}, {ID: 3, Score: 3, GroupEnd: 4}, {ID: 4, Score: 3, GroupEnd: 4}, {ID: 5, Score: 2, GroupEnd: 5}}
	m.Grid.AgentMap[2].CurPost = nil
	for range 100 {
		posts := base.selectRankedPosts(row, map[int64]bool{0: true, 1: true}, 2,
			m.RNG.Stream(smprng.StreamRecommendation))
		if len(posts) != 2 {
			t.Fatalf("expected two valid tied posts: %v", structureTieIDs(posts))
		}
		ids := structureTieIDs(posts)
		if !((ids[0] == 3 && ids[1] == 4) || (ids[0] == 4 && ids[1] == 3)) {
			t.Fatalf("tie selection changed score priority or eligibility: %v", ids)
		}
	}
}

func TestStructureDistinctScoresPreserveOrderAndRNG(t *testing.T) {
	m := structureTieModel(73, true)
	base := m.Recsys.(*Structure[float64, dynamics.HKParams])
	rngBefore, err := m.RNG.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	posts := base.selectRankedPosts([]StructureCandidate{{ID: 4, Score: 3, GroupEnd: 1}, {ID: 2, Score: 2, GroupEnd: 2}, {ID: 3, Score: 1, GroupEnd: 3}},
		map[int64]bool{0: true, 1: true}, 2, m.RNG.Stream(smprng.StreamRecommendation))
	if !reflect.DeepEqual(structureTieIDs(posts), []int64{4, 2}) {
		t.Fatalf("non-tied score order changed: %v", structureTieIDs(posts))
	}
	rngAfter, err := m.RNG.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rngBefore, rngAfter) {
		t.Fatal("non-tied current-post selection consumed RNG")
	}
}

func TestStructureTieKeepsRepostsHeldByFollowees(t *testing.T) {
	m := structureTieModel(73, true)
	base := m.Recsys.(*Structure[float64, dynamics.HKParams])
	// Eligibility is based on the displayed author, not the candidate holder.
	// Holder 1 is followed, but its repost by author 3 is still recommendable.
	m.Grid.AgentMap[1].CurPost = &model.PostRecord[float64]{AgentID: 3, Step: 0}
	row := []StructureCandidate{{ID: 1, Score: 1, GroupEnd: 2}, {ID: 2, Score: 1, GroupEnd: 2}}
	counts := [2]int{}
	for range 2000 {
		posts := base.selectRankedPosts(row, map[int64]bool{0: true, 1: true}, 1,
			m.RNG.Stream(smprng.StreamRecommendation))
		if len(posts) != 1 {
			t.Fatalf("expected a valid repost/current post: %v", structureTieIDs(posts))
		}
		switch posts[0].AgentID {
		case 2:
			counts[0]++
		case 3:
			counts[1]++
		default:
			t.Fatalf("unexpected author %d", posts[0].AgentID)
		}
	}
	if counts[0] < 850 || counts[1] < 850 {
		t.Fatalf("holder filtering biased author eligibility: %v", counts)
	}
}

func TestStructureTiesReplayCheckpointAndProbeIsolation(t *testing.T) {
	first := structureTieModel(501, true)
	second := structureTieModel(501, true)
	models := []*model.SMPModel[float64, dynamics.HKParams]{first, second}
	neighbors := map[int64]bool{0: true, 1: true}
	for round := 0; round < 60; round++ {
		if round == 20 {
			dump := first.Dump()
			prepared, err := first.Recsys.(model.RecommendationStatePreparer).PrepareState(dump.RecsysDumpData)
			if err != nil {
				t.Fatal(err)
			}
			models = append(models,
				dump.Load(first.ModelParams, first.AgentParams, first.Dynamics, first.CollectItems, nil,
					smprng.MustNewPool(smprng.FixedSpec(501, 91))),
				dump.LoadWithRecommenderState(first.ModelParams, first.AgentParams, first.Dynamics, first.CollectItems, nil,
					smprng.MustNewPool(smprng.FixedSpec(501, 91)),
					func(model.SMPModelRecommendationSystem[float64, dynamics.HKParams]) model.PreparedRecommendationState {
						return prepared
					}),
			)
		}
		var expected []*model.PostRecord[float64]
		var expectedRNG map[string][]byte
		for index, m := range models {
			base := m.Recsys.(*Structure[float64, dynamics.HKParams])
			if index == 1 {
				before := m.Dump()
				for probe := 0; probe < 3; probe++ {
					base.RecommendAt(m.Grid.AgentMap[0], .3, neighbors, 2,
						rand.New(rand.NewPCG(uint64(round), uint64(probe))))
				}
				after := m.Dump()
				beforeCache, err := decodeStructureDump(before.RecsysDumpData)
				if err != nil {
					t.Fatal(err)
				}
				afterCache, err := decodeStructureDump(after.RecsysDumpData)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(beforeCache, afterCache) || !reflect.DeepEqual(before.RNGStates, after.RNGStates) {
					t.Fatal("probe changed cached scores or simulation RNG")
				}
			}
			posts := base.Recommend(m.Grid.AgentMap[0], neighbors, 2)
			rngState, err := m.RNG.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if index == 0 {
				expected, expectedRNG = posts, rngState
			} else if !reflect.DeepEqual(posts, expected) || !reflect.DeepEqual(rngState, expectedRNG) {
				t.Fatalf("round=%d branch=%d: replay or probe isolation changed posts/RNG", round, index)
			}
		}
	}
}

func TestStructureRejectsCacheWithoutNoisyTieScores(t *testing.T) {
	legacy := struct {
		CandidateCache map[int64][]int64
		RawScoreCache  map[int64]map[int64]float64
		CacheValid     map[int64]bool
	}{CandidateCache: map[int64][]int64{}, RawScoreCache: map[int64]map[int64]float64{}, CacheValid: map[int64]bool{}}
	data, err := msgpack.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	m := structureTieModel(73, true)
	base := m.Recsys.(*Structure[float64, dynamics.HKParams])
	if _, err := base.PrepareState(data); err == nil || !strings.Contains(err.Error(), "cache version") {
		t.Fatalf("old cache must require an explicit new run, got %v", err)
	}
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("PostInit silently accepted an old cache")
		}
	}()
	base.PostInit(data)
}

func TestStructureRejectsInvalidCachedTieBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		end  int
	}{
		{name: "missing", end: 0},
		{name: "past-row", end: 5},
		{name: "split-tie", end: 1},
		{name: "cross-score", end: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			dump := StructureDump{
				Version: structureDumpVersion,
				CandidateCache: map[int64][]StructureCandidate{
					0: {{ID: 2, Score: 2, GroupEnd: test.end}, {ID: 3, Score: 2, GroupEnd: 2}, {ID: 4, Score: 1, GroupEnd: 3}},
				},
				RawScoreCache: map[int64]map[int64]float64{}, CacheValid: map[int64]bool{},
			}
			data, err := msgpack.Marshal(dump)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeStructureDump(data); err == nil {
				t.Fatal("invalid tie boundary was accepted")
			}
		})
	}
}
