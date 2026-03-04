package model_test

import (
	"math"
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/utils"

	"gonum.org/v1/gonum/graph/simple"
)

// helper: build a small HK model with n agents in a ring graph
func makeHKModel(n int, opinions []float64) *model.SMPModel[float64, dynamics.HKParams] {
	g := simple.NewDirectedGraph()
	for i := 0; i < n; i++ {
		g.AddNode(simple.Node(int64(i)))
	}
	// ring: 0→1→2→…→(n-1)→0
	for i := 0; i < n; i++ {
		g.SetEdge(simple.Edge{F: simple.Node(int64(i)), T: simple.Node(int64((i + 1) % n))})
	}

	params := dynamics.DefaultHKParams()
	params.RepostRate = 0.0 // disable reposting to keep tests deterministic
	params.RewiringRate = 0.0

	mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
	mp.PostRetainCount = 1
	mp.RecsysCount = 0

	return model.NewSMPModel[float64, dynamics.HKParams](
		g,
		&opinions,
		mp,
		params,
		&dynamics.HK{},
		&model.CollectItemOptions{AgentNumber: true, OpinionSum: true},
		nil,
	)
}

// ---- PostRecord tests ----

func TestPostRecordFields(t *testing.T) {
	pr := model.PostRecord[float64]{AgentID: 7, Step: 3, Opinion: 0.42}
	if pr.AgentID != 7 || pr.Step != 3 || pr.Opinion != 0.42 {
		t.Errorf("unexpected PostRecord values: %+v", pr)
	}
}

func TestPostRecordGenericBool(t *testing.T) {
	pr := model.PostRecord[bool]{AgentID: 1, Step: 0, Opinion: true}
	if !pr.Opinion {
		t.Error("bool PostRecord opinion should be true")
	}
}

// ---- AgentSumRecord tests ----

func TestAgentSumRecordGeneric(t *testing.T) {
	var r model.AgentSumRecord[int]
	r[0] = 10
	r[1] = 20
	if r[0] != 10 || r[1] != 20 {
		t.Error("AgentSumRecord[int] failed")
	}

	var rf model.AgentOpinionSumRecord
	rf[2] = 0.5
	if rf[2] != 0.5 {
		t.Error("AgentOpinionSumRecord (float64) failed")
	}
}

// ---- PartitionPosts tests ----

func TestPartitionPostsAllConcordant(t *testing.T) {
	hk := &dynamics.HK{}
	p := dynamics.DefaultHKParams()
	p.Tolerance = 0.5

	g := simple.NewDirectedGraph()
	for i := 0; i < 3; i++ {
		g.AddNode(simple.Node(int64(i)))
	}
	mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
	m := model.NewSMPModel[float64, dynamics.HKParams](
		g, nil, mp, p, hk,
		&model.CollectItemOptions{}, nil,
	)

	neighbors := []*model.SMPAgent[float64, dynamics.HKParams]{
		{ID: 1},
		{ID: 2},
	}
	neighborPosts := []*model.PostRecord[float64]{
		{AgentID: 1, Opinion: 0.1},
		{AgentID: 2, Opinion: 0.2},
	}
	recommended := []*model.PostRecord[float64]{
		{AgentID: 3, Opinion: 0.3},
	}
	_ = m

	cNA, cNP, cR, dNA, dNP, dR := model.PartitionPosts(
		0.0, neighbors, neighborPosts, recommended, hk, p,
	)

	if len(cNP) != 2 {
		t.Errorf("expected 2 concordant neighbors, got %d", len(cNP))
	}
	if len(cR) != 1 {
		t.Errorf("expected 1 concordant recommended, got %d", len(cR))
	}
	if len(dNP) != 0 || len(dR) != 0 {
		t.Errorf("expected 0 discordant, got dNP=%d dR=%d", len(dNP), len(dR))
	}
	_ = cNA
	_ = dNA
}

func TestPartitionPostsMixed(t *testing.T) {
	hk := &dynamics.HK{}
	p := dynamics.DefaultHKParams()
	p.Tolerance = 0.3

	neighbors := []*model.SMPAgent[float64, dynamics.HKParams]{
		{ID: 1},
		{ID: 2},
	}
	neighborPosts := []*model.PostRecord[float64]{
		{AgentID: 1, Opinion: 0.2},  // concordant
		{AgentID: 2, Opinion: 0.8},  // discordant
	}
	recommended := []*model.PostRecord[float64]{
		{AgentID: 3, Opinion: -0.5}, // discordant
	}

	_, cNP, cR, _, dNP, dR := model.PartitionPosts(
		0.0, neighbors, neighborPosts, recommended, hk, p,
	)

	if len(cNP) != 1 || cNP[0].AgentID != 1 {
		t.Errorf("unexpected concordant neighbor posts: %v", cNP)
	}
	if len(cR) != 0 {
		t.Errorf("unexpected concordant recommended: %v", cR)
	}
	if len(dNP) != 1 || dNP[0].AgentID != 2 {
		t.Errorf("unexpected discordant neighbor posts: %v", dNP)
	}
	if len(dR) != 1 {
		t.Errorf("expected 1 discordant recommended, got %d", len(dR))
	}
}

// ---- SMPModel creation & step tests ----

func TestModelCreation(t *testing.T) {
	opinions := []float64{0.1, -0.2, 0.3, -0.4}
	m := makeHKModel(4, opinions)

	if len(m.Schedule.Agents) != 4 {
		t.Errorf("expected 4 agents, got %d", len(m.Schedule.Agents))
	}
	ops := m.CollectOpinions()
	for i, op := range ops {
		if math.Abs(op-opinions[i]) > 1e-9 {
			t.Errorf("agent %d: opinion %v, want %v", i, op, opinions[i])
		}
	}
}

func TestModelStepOpinionChanges(t *testing.T) {
	// All agents have opinion 0 except agent 1 (=0.5).
	// With ring connectivity 0→1→2→3→0 and tolerance=1.0 (all concordant),
	// opinions should shift.
	n := 4
	opinions := make([]float64, n)
	opinions[1] = 0.5

	params := dynamics.DefaultHKParams()
	params.Tolerance = 1.0
	params.Decay = 1.0
	params.RepostRate = 0
	params.RewiringRate = 0

	g := simple.NewDirectedGraph()
	for i := 0; i < n; i++ {
		g.AddNode(simple.Node(int64(i)))
	}
	// ring: each agent follows the next
	for i := 0; i < n; i++ {
		g.SetEdge(simple.Edge{F: simple.Node(int64(i)), T: simple.Node(int64((i + 1) % n))})
	}

	mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
	mp.PostRetainCount = 1
	mp.RecsysCount = 0

	m := model.NewSMPModelFloat64(
		g, &opinions, mp, params, &dynamics.HK{},
		&model.CollectItemOptions{OpinionSum: true}, nil,
	)
	m.SetAgentCurPosts()

	before := m.CollectOpinions()
	m.Step(true)
	after := m.CollectOpinions()

	changed := false
	for i := range before {
		if math.Abs(before[i]-after[i]) > 1e-12 {
			changed = true
			break
		}
	}
	if !changed {
		t.Error("expected at least one opinion to change after step, but none did")
	}
}

func TestModelCollectAgentNumbers(t *testing.T) {
	opinions := []float64{0.1, -0.2, 0.3}
	m := makeHKModel(3, opinions)
	m.SetAgentCurPosts()
	m.Step(true)

	nums := m.CollectAgentNumbers()
	if len(nums) != 3 {
		t.Errorf("expected 3 AgentNumberRecords, got %d", len(nums))
	}
}

func TestModelCollectPosts(t *testing.T) {
	opinions := []float64{0.1, -0.2, 0.3}
	m := makeHKModel(3, opinions)
	m.SetAgentCurPosts()
	m.Step(true)

	posts := m.CollectPosts()
	if len(posts) == 0 {
		t.Error("CollectPosts should return non-empty map after a step")
	}
}

// ---- NetworkGrid tests ----

func TestNetworkGridPlaceAndGet(t *testing.T) {
	g := utils.CreateRandomNetwork(5, 0.5)
	mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
	p := dynamics.DefaultHKParams()
	m := model.NewSMPModelFloat64(g, nil, mp, p, &dynamics.HK{}, &model.CollectItemOptions{}, nil)

	for i := 0; i < 5; i++ {
		a := m.Grid.GetAgent(int64(i))
		if a == nil {
			t.Errorf("expected agent at node %d, got nil", i)
		}
		if a.ID != int64(i) {
			t.Errorf("agent at node %d has ID %d", i, a.ID)
		}
	}
}

func TestNetworkGridAddPost(t *testing.T) {
	g := simple.NewDirectedGraph()
	g.AddNode(simple.Node(0))
	grid := model.NewNetworkGrid[float64, dynamics.HKParams](g)

	post := &model.PostRecord[float64]{AgentID: 0, Step: 1, Opinion: 0.5}
	grid.AddPost(0, post, 3)
	if len(grid.PostMap[0]) != 1 {
		t.Errorf("expected 1 post, got %d", len(grid.PostMap[0]))
	}
	if grid.PostMap[0][0].Opinion != 0.5 {
		t.Error("post opinion mismatch")
	}
}

func TestNetworkGridPostRetention(t *testing.T) {
	g := simple.NewDirectedGraph()
	g.AddNode(simple.Node(0))
	grid := model.NewNetworkGrid[float64, dynamics.HKParams](g)

	for i := 0; i < 5; i++ {
		grid.AddPost(0, &model.PostRecord[float64]{AgentID: 0, Step: i, Opinion: float64(i)}, 3)
	}
	if len(grid.PostMap[0]) != 3 {
		t.Errorf("expected 3 retained posts, got %d", len(grid.PostMap[0]))
	}
	// The retained posts should be the most recent (steps 2,3,4)
	for _, p := range grid.PostMap[0] {
		if p.Step < 2 {
			t.Errorf("retained old post with step %d", p.Step)
		}
	}
}

func TestNetworkGridGetNeighbors(t *testing.T) {
	n := 5
	g := simple.NewDirectedGraph()
	for i := 0; i < n; i++ {
		g.AddNode(simple.Node(int64(i)))
	}
	// node 0 follows nodes 1,2,3
	for _, j := range []int{1, 2, 3} {
		g.SetEdge(simple.Edge{F: simple.Node(0), T: simple.Node(int64(j))})
	}

	p := dynamics.DefaultHKParams()
	mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
	m := model.NewSMPModelFloat64(g, nil, mp, p, &dynamics.HK{}, &model.CollectItemOptions{}, nil)

	neighbors := m.Grid.GetNeighbors(0, false)
	if len(neighbors) != 3 {
		t.Errorf("expected 3 neighbors for node 0, got %d", len(neighbors))
	}
}

// ---- EventRecord / body types tests ----

func TestEventBodyTypes(t *testing.T) {
	pr := &model.PostRecord[float64]{AgentID: 1, Opinion: 0.5}
	body := model.PostEventBody[float64]{Record: pr, IsRepost: false}
	if body.IsRepost {
		t.Error("expected IsRepost=false")
	}
	if body.Record.Opinion != 0.5 {
		t.Errorf("wrong opinion in PostEventBody: %v", body.Record.Opinion)
	}

	vb := model.ViewPostsEventBody[float64]{
		NeighborConcordant: []*model.PostRecord[float64]{pr},
	}
	if len(vb.NeighborConcordant) != 1 {
		t.Error("ViewPostsEventBody: wrong NeighborConcordant length")
	}
}

// ---- DumpData round-trip test ----

func TestModelDumpLoad(t *testing.T) {
	n := 6
	opinions := make([]float64, n)
	for i := range opinions {
		opinions[i] = float64(i)/float64(n)*2 - 1
	}
	m := makeHKModel(n, opinions)
	m.SetAgentCurPosts()
	m.Step(true)
	m.Step(true)

	dump := m.Dump()
	if dump.CurStep != m.CurStep {
		t.Errorf("dump CurStep %d != model CurStep %d", dump.CurStep, m.CurStep)
	}
	if len(dump.Opinions) != n {
		t.Errorf("dump Opinions length %d, want %d", len(dump.Opinions), n)
	}

	// load back
	p := dynamics.DefaultHKParams()
	p.RepostRate = 0.0
	p.RewiringRate = 0.0
	mp := model.DefaultSMPModelParams[float64, dynamics.HKParams]()
	mp.PostRetainCount = 1
	ci := &model.CollectItemOptions{AgentNumber: true, OpinionSum: true}
	m2 := dump.Load(mp, p, &dynamics.HK{}, ci, nil)

	if m2.CurStep != m.CurStep {
		t.Errorf("loaded CurStep %d != original %d", m2.CurStep, m.CurStep)
	}
	op1 := m.CollectOpinions()
	op2 := m2.CollectOpinions()
	for i := range op1 {
		if math.Abs(op1[i]-op2[i]) > 1e-9 {
			t.Errorf("agent %d: opinion %v != %v after load", i, op2[i], op1[i])
		}
	}
}
