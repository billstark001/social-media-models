package model

import (
	"math/rand/v2"
	"testing"

	smprng "smp/rng"

	"gonum.org/v1/gonum/graph/simple"
)

type feedTestParams struct{}

func (*feedTestParams) GetRepostRate() float64   { return 0 }
func (*feedTestParams) GetRewiringRate() float64 { return 1 }

type feedTestDynamics struct{}

func (feedTestDynamics) Concordant(x, y float64, _ *feedTestParams) bool { return x == y }
func (feedTestDynamics) Step(x float64, _, _, _, _ []float64, _ *feedTestParams, _ *rand.Rand) (float64, AgentOpinionSumRecord) {
	return x, AgentOpinionSumRecord{}
}

func feedTestAgent() (*SMPAgent[float64, feedTestParams], []*SMPAgent[float64, feedTestParams], []*PostRecord[float64]) {
	m := &SMPModel[float64, feedTestParams]{
		Graph:       simple.NewDirectedGraph(),
		ModelParams: &SMPModelParams[float64, feedTestParams]{SMPModelPureParams: SMPModelPureParams{RecsysCount: 10}},
		CurStep:     8,
		placements:  make([]PostPlacement, 10),
	}
	posts := make([]*PostRecord[float64], 10)
	for i := range posts {
		id := int64(i + 1)
		posts[i] = &PostRecord[float64]{AgentID: id, Step: 7}
		m.placements[i] = PostPlacement{Holder: id, Round: 7}
	}
	neighbors := []*SMPAgent[float64, feedTestParams]{
		{ID: 11},
		{ID: 12, CurPost: &PostRecord[float64]{AgentID: 7, Step: 2, Opinion: 1}},
	}
	return &SMPAgent[float64, feedTestParams]{Model: m}, neighbors, posts
}

var benchmarkFeed *FeedDetails[float64]

func BenchmarkFeedDetails(b *testing.B) {
	agent, neighbors, posts := feedTestAgent()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkFeed = agent.feedDetails(neighbors, posts, RecommendationLayout{Exploration: 1, Bridge: 9, HasBridge: true})
	}
}

func TestFeedDetailsOwnsPlacementValues(t *testing.T) {
	agent, neighbors, posts := feedTestAgent()
	layout := RecommendationLayout{Exploration: 1, Bridge: 9, HasBridge: true}
	first := agent.feedDetails(neighbors, posts, layout)
	if first.Recommendations[0].Placement.Holder != 1 ||
		first.Recommendations[0].Source != ExplorationSlot || first.Recommendations[9].Source != BridgeSlot {
		t.Fatal("unexpected displayed feed provenance")
	}
	if len(first.Neighbors) != len(neighbors) || first.Neighbors[0].Post != nil ||
		first.Neighbors[0].Placement.Holder != 11 || first.Neighbors[0].Placement.Round != 7 ||
		first.Neighbors[0].Source != NeighborSlot ||
		first.Neighbors[1].Placement.Holder != 12 || first.Neighbors[1].Post.AgentID != 7 {
		t.Fatal("followee placements must retain missing posts and distinguish holder from author")
	}
	for i := range agent.Model.placements {
		agent.Model.placements[i] = PostPlacement{Holder: 99, Round: 8}
	}
	neighbors[0].ID = 90
	second := agent.feedDetails(neighbors, posts, layout)
	if first.Recommendations[0].Placement.Holder != 1 || second.Recommendations[0].Placement.Holder != 99 ||
		first.Neighbors[0].Placement.Holder != 11 || second.Neighbors[0].Placement.Holder != 90 {
		t.Fatal("queued feed details share mutable placement workspace")
	}
	agent.Model.placements = nil
	unknown := agent.feedDetails(neighbors, posts, RecommendationLayout{Exploration: 1})
	for _, slot := range unknown.Recommendations {
		if slot.Placement != nil {
			t.Fatal("unknown recommendation placement was fabricated")
		}
	}
	for _, slot := range unknown.Neighbors {
		if slot.Placement == nil {
			t.Fatal("followee placement must remain available")
		}
	}
}

func TestRecommendationLayoutZeroValueIsPolicy(t *testing.T) {
	var layout RecommendationLayout
	if layout.Source(0) != PolicySlot {
		t.Fatal("zero layout must not invent a bridge")
	}
	layout.Exploration = 1
	if layout.Source(0) != ExplorationSlot || layout.Source(1) != PolicySlot {
		t.Fatal("exploration prefix is not represented correctly")
	}
	layout.HasBridge = true
	if layout.Source(0) != BridgeSlot {
		t.Fatal("explicit bridge slot must take precedence")
	}
}

type feedTestRecommender struct {
	BaseRecommendationSystem[float64, feedTestParams]
	posts    []*PostRecord[float64]
	excluded map[int64]bool
}

func (r *feedTestRecommender) Recommend(_ *SMPAgent[float64, feedTestParams], excluded map[int64]bool, _ int) []*PostRecord[float64] {
	r.excluded = make(map[int64]bool, len(excluded))
	for id, value := range excluded {
		r.excluded[id] = value
	}
	return r.posts
}

func TestAgentKeepsPostlessFolloweesAlignedDuringRewiring(t *testing.T) {
	g := simple.NewDirectedGraph()
	for id := range 4 {
		g.AddNode(simple.Node(id))
	}
	g.SetEdge(g.NewEdge(g.Node(0), g.Node(1)))
	g.SetEdge(g.NewEdge(g.Node(0), g.Node(2)))
	opinions := []float64{0, 0, 1, 0}
	m := NewSMPModel(
		g, &opinions, DefaultSMPModelParams[float64, feedTestParams](), &feedTestParams{},
		feedTestDynamics{}, &CollectItemOptions{}, nil, smprng.MustNewPool(smprng.FixedSpec(1, 2)),
	)
	m.CurStep = 1
	m.Grid.AgentMap[2].CurPost = &PostRecord[float64]{AgentID: 2, Step: 0, Opinion: 1}
	r := &feedTestRecommender{posts: []*PostRecord[float64]{{AgentID: 3, Step: 0, Opinion: 0}}}
	m.Recsys = r
	var details *FeedDetails[float64]
	m.CaptureFeedDetails = true
	m.ObserveFeed = func(_ *SMPAgent[float64, feedTestParams], feed FeedObservation[float64]) {
		details = feed.Details
	}
	agent := m.Grid.AgentMap[0]
	agent.Step()
	if agent.NextFollow == nil || agent.NextFollow.Unfollow != 2 || agent.NextFollow.Follow != 3 {
		t.Fatal("a missing neighbor post shifted rewiring to the wrong followee")
	}
	if !r.excluded[1] || !r.excluded[2] {
		t.Fatal("postless followees must remain excluded from recommendations")
	}
	if details == nil || len(details.Neighbors) != 2 || details.Neighbors[0].Placement.Holder != 1 ||
		details.Neighbors[0].Post != nil || details.Neighbors[1].Placement.Holder != 2 ||
		details.Neighbors[1].Post.Opinion != 1 {
		t.Fatal("detailed feed lost its original followee positions")
	}
}
