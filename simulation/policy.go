package simulation

import (
	"errors"
	"math"
	"smp/dynamics"
	"smp/model"
)

type hkAgent = model.SMPAgent[float64, dynamics.HKParams]
type hkModel = model.SMPModel[float64, dynamics.HKParams]

func oppositeCamps(x, y, margin float64) bool {
	return (x < -margin && y > margin) || (y < -margin && x > margin)
}

type PolicyChange struct {
	Recommender   string   `json:"recommender"`
	At            *int     `json:"at,omitempty"`
	ExcessAtLeast *float64 `json:"excess_at_least,omitempty"`
	Latest        int      `json:"latest,omitempty"`
	Duration      int      `json:"duration,omitempty"`
	Bridge        bool     `json:"bridge,omitempty"`
	HideReposts   bool     `json:"hide_reposts,omitempty"`
}

func (p *PolicyChange) Validate(metadata *ScenarioMetadata, horizon int) error {
	if p == nil {
		return nil
	}
	if p.Recommender == "" || (p.At == nil) == (p.ExcessAtLeast == nil) {
		return errors.New("policy_change needs a recommender and exactly one trigger")
	}
	last := p.Latest
	if p.At != nil {
		if p.Latest != 0 {
			return errors.New("policy_change.latest applies only to a state trigger")
		}
		last = *p.At
	} else if (math.IsNaN(*p.ExcessAtLeast) || math.IsInf(*p.ExcessAtLeast, 0)) || math.Abs(*p.ExcessAtLeast) > 1 {
		return errors.New("policy_change excess_at_least must be in [-1, 1]")
	}
	if last < 0 || last >= horizon || p.Duration < 0 || p.Duration > horizon-last {
		return errors.New("policy_change lies outside the observation horizon")
	}
	target := *metadata
	target.RecsysFactoryType = p.Recommender
	return target.Validate()
}

type hkRecommender = model.SMPModelRecommendationSystem[float64, dynamics.HKParams]
type hkPost = model.PostRecord[float64]

// bridgeRecommender wraps a live recommender only during an intervention.
// Candidates are refreshed once per step and kept in deterministic agent order.
type bridgeRecommender struct {
	hkRecommender
	model   *hkModel
	camps   [2][]*hkPost
	closest [2]*hkPost
}

func (r *bridgeRecommender) PreStep() {
	r.hkRecommender.PreStep()
	r.closest = [2]*hkPost{}
	for i := range r.camps {
		clear(r.camps[i])
		r.camps[i] = r.camps[i][:0]
	}
	epsilon := r.model.AgentParams.Tolerance
	for _, agent := range r.model.Schedule.Agents {
		post := agent.CurPost
		if post == nil || !post.IsOriginalPlacement(agent.ID, r.model.CurStep-1) || math.Abs(post.Opinion) <= epsilon/4 || math.Abs(post.Opinion) > epsilon {
			continue
		}
		camp := 0
		if post.Opinion > 0 {
			camp = 1
		}
		r.camps[camp] = append(r.camps[camp], post)
		if r.closest[camp] == nil || math.Abs(post.Opinion) < math.Abs(r.closest[camp].Opinion) {
			r.closest[camp] = post
		}
	}
}

func (r *bridgeRecommender) Recommend(agent *hkAgent, excluded map[int64]bool, count int) []*hkPost {
	posts, _ := r.RecommendWithLayout(agent, excluded, count)
	return posts
}

func (r *bridgeRecommender) RecommendWithLayout(agent *hkAgent, excluded map[int64]bool, count int) ([]*hkPost, model.RecommendationLayout) {
	var posts []*hkPost
	var layout model.RecommendationLayout
	if mixed, ok := r.hkRecommender.(model.RecommendationLayoutSystem[float64, dynamics.HKParams]); ok {
		posts, layout = mixed.RecommendWithLayout(agent, excluded, count)
	} else {
		posts = r.hkRecommender.Recommend(agent, excluded, count)
	}
	epsilon := r.model.AgentParams.Tolerance
	x := agent.CurOpinion
	if count <= 0 || math.Abs(x) <= epsilon/4 {
		return posts, layout
	}
	camp := 0
	if x < 0 {
		camp = 1
	}
	nearest := r.closest[camp]
	if nearest == nil || math.Abs(x-nearest.Opinion) > epsilon {
		return posts, layout
	}

	eligible := func(post *hkPost) bool {
		return oppositeCamps(x, post.Opinion, epsilon/4) && math.Abs(x-post.Opinion) <= epsilon
	}
	for _, post := range posts {
		if eligible(post) {
			return posts, layout
		}
	}
	for _, post := range r.camps[camp] {
		if excluded[post.AgentID] || !eligible(post) {
			continue
		}
		if len(posts) < count {
			r.model.RecordRecommendationPlacement(post.AgentID, r.model.CurStep-1)
			layout.Bridge = len(posts)
			layout.HasBridge = true
			return append(posts, post), layout
		}
		// Preserve the exploration prefix, including an all-exploration mix.
		if len(posts) > layout.Exploration {
			index := len(posts) - 1
			r.model.ReplaceRecommendationPlacement(index, post.AgentID, r.model.CurStep-1)
			posts[index] = post
			layout.Bridge = index
			layout.HasBridge = true
			return posts, layout
		}
		break
	}
	return posts, layout
}

func SetHKPolicy(m *hkModel, metadata *ScenarioMetadata, current, name string, bridge, hideReposts bool) error {
	recommender := m.Recsys
	if wrapped, ok := recommender.(*bridgeRecommender); ok {
		recommender = wrapped.hkRecommender
	}
	if current != name {
		states, err := m.RNG.Snapshot()
		if err != nil {
			return err
		}
		factory := GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](metadata.RecSysParams)[name]
		recommender = factory(m)
		recommender.PostInit(nil)
		if err := m.RNG.Restore(states); err != nil {
			return err
		}
	}
	if configurable, ok := recommender.(interface{ SetHideReposts(bool) }); ok {
		configurable.SetHideReposts(hideReposts)
	}
	if bridge {
		recommender = &bridgeRecommender{hkRecommender: recommender, model: m}
	}
	m.Recsys = recommender
	return nil
}
