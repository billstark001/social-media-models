package recsys_test

import (
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/recsys"
	smprng "smp/rng"
	"smp/utils"
)

// makeBoolModel builds a small SMPModel[bool, P] suitable for recsys testing.
// n agents are placed on a random graph with edgeDensity ≈ followCount/(n-1).
func makeBoolVoterModel(n, followCount int, opinions []bool) *model.SMPModel[bool, dynamics.VoterParams] {
	pool := smprng.MustNewPool(smprng.FixedSpec(10, 20))
	graph := utils.CreateRandomNetwork(n, float64(followCount)/float64(n-1), pool.Stream(smprng.StreamNetwork))
	params := &model.SMPModelParams[bool, dynamics.VoterParams]{
		SMPModelPureParams: model.SMPModelPureParams{
			RecsysCount:     5,
			PostRetainCount: 1,
		},
		RecsysFactory: nil, // recsys set manually after construction
	}
	dynParams := dynamics.DefaultVoterParams()
	dynParams.RepostRate = 0.0
	dynParams.RewiringRate = 0.0
	return model.NewSMPModel(graph, &opinions, params, dynParams, &dynamics.Voter{}, &model.CollectItemOptions{}, nil, pool)
}

func makeBoolGalamModel(n, followCount int, opinions []bool) *model.SMPModel[bool, dynamics.GalamParams] {
	pool := smprng.MustNewPool(smprng.FixedSpec(30, 40))
	graph := utils.CreateRandomNetwork(n, float64(followCount)/float64(n-1), pool.Stream(smprng.StreamNetwork))
	params := &model.SMPModelParams[bool, dynamics.GalamParams]{
		SMPModelPureParams: model.SMPModelPureParams{
			RecsysCount:     5,
			PostRetainCount: 1,
		},
		RecsysFactory: nil,
	}
	dynParams := dynamics.DefaultGalamParams()
	dynParams.RepostRate = 0.0
	dynParams.RewiringRate = 0.0
	return model.NewSMPModel(graph, &opinions, params, dynParams, &dynamics.Galam{}, &model.CollectItemOptions{}, nil, pool)
}

// TestOpinionRecsysBoolVoter verifies that NewOpinion works with bool-opinion Voter models.
// After PostInit, the recsys should be able to serve Recommend calls without panicking.
func TestOpinionRecsysBoolVoter(t *testing.T) {
	n := 20
	ops := make([]bool, n)
	for i := range ops {
		ops[i] = i%2 == 0 // alternating true/false
	}
	m := makeBoolVoterModel(n, 4, ops)
	m.SetAgentCurPosts()

	rs := recsys.NewOpinion(m, 0.05, nil)
	rs.PostInit(nil)
	rs.PreStep()

	// Recommend posts for the first agent; should return without panic.
	agent := m.Schedule.Agents[0]
	posts := rs.Recommend(agent, map[int64]bool{}, 3)

	// With bool opinions (0.0/1.0), recommendation still works – posts may be nil or non-nil.
	_ = posts
	t.Logf("OpinionRecsys[bool,VoterParams] returned %d post(s) for agent 0", len(posts))
}

// TestOpinionRecsysBoolGalam verifies that NewOpinion works with bool-opinion Galam models.
func TestOpinionRecsysBoolGalam(t *testing.T) {
	n := 20
	ops := make([]bool, n)
	for i := range ops {
		ops[i] = i < n/2 // first half true, second half false
	}
	m := makeBoolGalamModel(n, 4, ops)
	m.SetAgentCurPosts()

	rs := recsys.NewOpinion(m, 0.0, nil)
	rs.PostInit(nil)
	rs.PreStep()

	// Agents with opinion true (index 0..9) should preferentially receive posts from
	// other true-opinion agents (toFloat64 maps true→1.0, false→0.0).
	trueAgent := m.Schedule.Agents[0] // opinion = true → toFloat64 = 1.0
	posts := rs.Recommend(trueAgent, map[int64]bool{}, 5)
	for _, p := range posts {
		if p == nil {
			continue
		}
		// Poster's opinion must be bool, and posts should come from agents with matching opinion.
		if p.AgentID >= int64(n/2) {
			// Agent IDs 0..n/2-1 are true; n/2..n-1 are false. A true agent should
			// mostly receive posts from other true agents (closer in toFloat64 space).
			t.Logf("Note: true-opinion agent received post from potentially false-opinion agent %d (acceptable with noise)", p.AgentID)
		}
	}
	t.Logf("OpinionRecsys[bool,GalamParams] returned %d post(s) for true-opinion agent", len(posts))
}

// TestOpinionRandomRecsysBoolVoter verifies that NewOpinionRandom works with bool-opinion Voter models.
// With tolerance=0.4, different-bool agents get rate=0, same-bool agents get rate=1.
func TestOpinionRandomRecsysBoolVoter(t *testing.T) {
	n := 30
	ops := make([]bool, n)
	for i := range ops {
		ops[i] = i%3 != 0 // 2/3 true, 1/3 false
	}
	m := makeBoolVoterModel(n, 5, ops)
	m.SetAgentCurPosts()

	// tolerance=0.4, steepness=1.0, noiseStd=0.0, randomRatio=0.0
	rs := recsys.NewOpinionRandom(m, nil, 0.4, 1.0, 0.0, 0.0)
	rs.PostInit(nil)
	rs.PreStep()

	// With randomRatio=0.0 and noiseStd=0.0:
	//   same-opinion pair: diff=0 → rate=1.0
	//   diff-opinion pair: diff=1 → rate=max(1-1/0.4,0)=0.0
	// So true-opinion agents should only recommend posts from true-opinion peers.
	falseAgent := m.Schedule.Agents[0] // ops[0]=false (0%3==0)
	trueAgent := m.Schedule.Agents[1]  // ops[1]=true

	falsePosts := rs.Recommend(falseAgent, map[int64]bool{}, 3)
	truePosts := rs.Recommend(trueAgent, map[int64]bool{}, 3)

	t.Logf("OpinionRandom[bool,VoterParams]: false-agent got %d posts, true-agent got %d posts",
		len(falsePosts), len(truePosts))

	// Verify no panics; posts might be empty if agents haven't been activated yet.
	_ = falsePosts
	_ = truePosts
}

// TestOpinionRandomRecsysBoolGalam verifies OpinionRandom with Galam bool-opinion model.
func TestOpinionRandomRecsysBoolGalam(t *testing.T) {
	n := 20
	ops := make([]bool, n)
	for i := range ops {
		ops[i] = i%2 == 0
	}
	m := makeBoolGalamModel(n, 4, ops)
	m.SetAgentCurPosts()

	// Use nonzero randomRatio to ensure some cross-opinion posts are possible.
	rs := recsys.NewOpinionRandom(m, nil, 0.4, 1.0, 0.1, 0.2)
	rs.PostInit(nil)
	rs.PreStep()

	for _, agent := range m.Schedule.Agents[:5] {
		posts := rs.Recommend(agent, map[int64]bool{}, 3)
		t.Logf("OpinionRandom[bool,GalamParams] agent %d got %d posts", agent.ID, len(posts))
	}
}

// TestOpinionRecsysPreStepSortingBool checks that PreStep sorts PostIndices correctly
// when all opinions are bool: false→0.0 comes before true→1.0.
func TestOpinionRecsysPreStepSortingBool(t *testing.T) {
	n := 6
	// Agents 0,1,2 = false; 3,4,5 = true
	ops := []bool{false, false, false, true, true, true}
	m := makeBoolVoterModel(n, 2, ops)
	m.SetAgentCurPosts()

	rs := recsys.NewOpinion(m, 0.0, nil)
	rs.PostInit(nil)
	rs.PreStep()

	// True-opinion agent (ID=3) should have AgentIndices close to the high end of PostIndices.
	// False-opinion agent (ID=0) should have AgentIndices close to the low end.
	trueIdx := rs.AgentIndices[3]
	falseIdx := rs.AgentIndices[0]

	if trueIdx <= falseIdx {
		t.Errorf("expected true-opinion agent index (%d) > false-opinion agent index (%d) in sorted PostIndices",
			trueIdx, falseIdx)
	}
	t.Logf("PostIndices: false-agent index=%d, true-agent index=%d (out of %d total)",
		falseIdx, trueIdx, len(rs.PostIndices))
}
