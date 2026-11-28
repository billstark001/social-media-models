package recsys

import (
	"math/rand/v2"
	"reflect"
	"testing"

	"gonum.org/v1/gonum/graph/simple"
	"smp/dynamics"
	"smp/model"
	smprng "smp/rng"
)

func TestTieSamplerResetPermutationAndEpochWrap(t *testing.T) {
	source := rand.NewPCG(37, 91)
	rng := rand.New(source)
	var sampler tieSampler
	for _, size := range []int{50, 3, 1, 0, 70, 2, 50} {
		sampler.reset(size)
		seen := make([]bool, size)
		for sampler.remaining > 0 {
			index := sampler.draw(rng)
			if index < 0 || index >= size || seen[index] {
				t.Fatalf("size=%d: repeated or out-of-range index %d", size, index)
			}
			seen[index] = true
		}
	}
	sampler.epoch = ^uint32(0)
	sampler.reset(50)
	if sampler.epoch != 1 {
		t.Fatal("epoch overflow was not reset")
	}
	for _, mark := range sampler.marks {
		if mark != 0 {
			t.Fatal("overflow retained stale permutation marks")
		}
	}
	before, _ := source.MarshalBinary()
	sampler.reset(1)
	if sampler.draw(rng) != 0 {
		t.Fatal("singleton index")
	}
	after, _ := source.MarshalBinary()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("singleton consumed randomness")
	}
}

func TestTieSamplerOrderedPairsAreUniform(t *testing.T) {
	rng := rand.New(rand.NewPCG(91, 37))
	var sampler tieSampler
	var counts [3][3]int
	for trial := 0; trial < 60000; trial++ {
		sampler.reset(3)
		first, second := sampler.draw(rng), sampler.draw(rng)
		counts[first][second]++
	}
	for first := range 3 {
		for second := range 3 {
			if first == second {
				if counts[first][second] != 0 {
					t.Fatal("sampled with replacement")
				}
			} else if counts[first][second] < 9500 || counts[first][second] > 10500 {
				t.Fatalf("ordered pair (%d,%d) occurred %d/60000 times", first, second, counts[first][second])
			}
		}
	}
}

func opinionTieFixture(opinions []float64, history int) *Opinion[float64, dynamics.HKParams] {
	graph := simple.NewDirectedGraph()
	for id := range opinions {
		graph.AddNode(simple.Node(id))
	}
	params := &model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: model.SMPModelPureParams{PostRetainCount: history},
	}
	m := model.NewSMPModel(graph, &opinions, params, &dynamics.HKParams{}, &dynamics.HK{},
		&model.CollectItemOptions{}, nil, smprng.MustNewPool(smprng.FixedSpec(37, 91)))
	o := NewOpinion(m, 0, nil)
	m.Recsys = o
	m.SetAgentCurPosts()
	m.CurStep = 1
	o.PostInit(nil)
	return o
}

func TestOpinionTieAcrossUnequalSidesIsUniformAndFiltered(t *testing.T) {
	// One valid slot on the left and four on the right. A left/right coin
	// would allocate the singleton 1/2 rather than its correct probability 1/5.
	o := opinionTieFixture([]float64{0, -.5, .5, .5, .5, .5, .5}, 0)
	o.PreStep()
	agent := o.AgentMap[0]
	excluded := map[int64]bool{0: true, 6: true}
	counts := make([]int, 7)
	for trial := 0; trial < 25000; trial++ {
		posts := o.Recommend(agent, excluded, 1)
		if len(posts) != 1 {
			t.Fatal("missing tied recommendation")
		}
		counts[posts[0].AgentID]++
	}
	for id := 1; id <= 5; id++ {
		if counts[id] < 4700 || counts[id] > 5300 {
			t.Fatalf("candidate %d: %d selections; counts=%v", id, counts[id], counts)
		}
	}
	if counts[0] != 0 || counts[6] != 0 {
		t.Fatal("self or followee entered the sample")
	}
}

func TestOpinionHistoryTieRetainsSlotMultiplicity(t *testing.T) {
	o := opinionTieFixture([]float64{0, 0, 0}, 2)
	o.Model.Grid.PostMap[1], o.Model.Grid.PostMap[2] = nil, nil
	for _, slot := range []struct {
		author int64
		step   int
	}{{1, 0}, {1, 1}, {2, 1}} {
		o.Model.Grid.AddPost(slot.author, &model.PostRecord[float64]{AgentID: slot.author, Step: slot.step, Opinion: 0}, 2)
	}
	o.PreStep()
	excluded := map[int64]bool{0: true}
	counts := [3]int{}
	for trial := 0; trial < 18000; trial++ {
		posts := o.Recommend(o.AgentMap[0], excluded, 1)
		counts[posts[0].AgentID]++
	}
	if counts[1] < 11600 || counts[1] > 12400 {
		t.Fatalf("two history slots lost their 2/3 weight: %v", counts)
	}
	all := o.Recommend(o.AgentMap[0], excluded, 10)
	if len(all) != 3 || all[0] == all[1] || all[0] == all[2] || all[1] == all[2] {
		t.Fatal("history sample changed multiplicity or sampled a slot twice")
	}
}

func TestOpinionNonTieKeepsMarkerTraversalAndRNG(t *testing.T) {
	o := opinionTieFixture([]float64{0, .1, .8, -.5}, 0)
	o.PreStep()
	o.Epsilon[0] = .9
	before, _ := o.Model.RNG.Snapshot()
	posts := o.Recommend(o.AgentMap[0], map[int64]bool{0: true}, 2)
	after, _ := o.Model.RNG.Snapshot()
	if len(posts) != 2 || posts[0].AgentID != 1 || posts[1].AgentID != 2 {
		t.Fatal("real recommendation changed to a global noisy-query search")
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("non-tied recommendations consumed RNG")
	}
	// Markers or excluded neighbors with the same score do not create a
	// genuine tie when exactly one eligible placement remains.
	o = opinionTieFixture([]float64{0, 0, 0}, 0)
	o.PreStep()
	before, _ = o.Model.RNG.Snapshot()
	posts = o.Recommend(o.AgentMap[0], map[int64]bool{0: true, 1: true}, 1)
	after, _ = o.Model.RNG.Snapshot()
	if len(posts) != 1 || posts[0].AgentID != 2 || !reflect.DeepEqual(before, after) {
		t.Fatal("a unique eligible slot was treated as a random tie")
	}
}
