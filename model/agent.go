package model

import smprng "smp/rng"

// events & records

type EventRecord struct {
	Type    string
	AgentID int64
	Step    int
	Body    any
}

type RewiringEventBody struct {
	AgentID  int64
	Unfollow int64
	Follow   int64
}

type PostEventBody[O any] struct {
	Record   *PostRecord[O]
	IsRepost bool
}

type ViewPostsEventBody[O any] struct {
	NeighborConcordant    []*PostRecord[O]
	NeighborDiscordant    []*PostRecord[O]
	RecommendedConcordant []*PostRecord[O]
	RecommendedDiscordant []*PostRecord[O]
	Details               *FeedDetails[O] `json:"Details,omitempty" msgpack:"Details,omitempty"`
}

// AgentNumberRecord holds counts of (concordant neighbor, concordant recommended,
// discordant neighbor, discordant recommended) agents.
type AgentNumberRecord = [4]int

// AgentSumRecord is a generic 4-slot statistics record.
type AgentSumRecord[T any] [4]T

// AgentOpinionSumRecord holds sum statistics of opinion differences.
// [0]: concordant-neighbor sum, [1]: concordant-recommended sum,
// [2]: discordant-neighbor sum, [3]: discordant-recommended sum.
type AgentOpinionSumRecord = AgentSumRecord[float64]

// AgentBehaviorParams is implemented by parameter types that support agent behavior.
type AgentBehaviorParams interface {
	GetRepostRate() float64
	GetRewiringRate() float64
}

// SMPAgent represents an agent in the social media platform model.
type SMPAgent[O any, P any] struct {
	ID int64 // to align with gonum/graph

	Model          *SMPModel[O, P]
	hasEventLogger bool
	Params         *P
	CollectOptions *CollectItemOptions

	CurOpinion O
	CurPost    *PostRecord[O]

	NextOpinion O
	NextPost    *PostRecord[O]
	NextFollow  *RewiringEventBody

	AgentNumber AgentNumberRecord
	OpinionSum  AgentOpinionSumRecord
}

// NewSMPAgent creates a new SMPAgent with the given opinion.
func NewSMPAgent[O any, P any](uniqueID int64, m *SMPModel[O, P], opinion O) *SMPAgent[O, P] {
	agent := &SMPAgent[O, P]{
		ID:          uniqueID,
		Model:       m,
		CurOpinion:  opinion,
		NextOpinion: opinion,
	}
	agent.hasEventLogger = m.EventLogger != nil
	agent.Params = m.AgentParams
	agent.CollectOptions = m.CollectItems
	return agent
}

// PartitionPosts divides posts into concordant and discordant groups.
func PartitionPosts[O any, P any](
	opinion O,
	neighbors []*SMPAgent[O, P],
	neighborPosts []*PostRecord[O],
	recommended []*PostRecord[O],
	dynamics Dynamics[O, P],
	params *P,
) (
	concordantNeighborAgents []*SMPAgent[O, P],
	concordantNeighborPosts []*PostRecord[O],
	concordantRecommended []*PostRecord[O],
	discordantNeighborAgents []*SMPAgent[O, P],
	discordantNeighborPosts []*PostRecord[O],
	discordantRecommended []*PostRecord[O],
) {
	for i, np := range neighborPosts {
		if np == nil {
			continue
		}
		a := neighbors[i]
		if dynamics.Concordant(opinion, np.Opinion, params) {
			concordantNeighborAgents = append(concordantNeighborAgents, a)
			concordantNeighborPosts = append(concordantNeighborPosts, np)
		} else {
			discordantNeighborAgents = append(discordantNeighborAgents, a)
			discordantNeighborPosts = append(discordantNeighborPosts, np)
		}
	}
	for _, rp := range recommended {
		if dynamics.Concordant(opinion, rp.Opinion, params) {
			concordantRecommended = append(concordantRecommended, rp)
		} else {
			discordantRecommended = append(discordantRecommended, rp)
		}
	}
	return
}

func extractOpinions[O any](posts []*PostRecord[O]) []O {
	ops := make([]O, len(posts))
	for i, p := range posts {
		ops[i] = p.Opinion
	}
	return ops
}

// Step performs a single step for this agent.
func (a *SMPAgent[O, P]) Step() {
	// Reset next-state fields each step.
	a.NextFollow = nil
	a.NextPost = nil

	neighbors := a.Model.Grid.GetNeighbors(a.ID, false)

	neighborPosts := make([]*PostRecord[O], len(neighbors))
	for i, neighbor := range neighbors {
		neighborPosts[i] = neighbor.CurPost
	}

	observeFeed := a.Model.ObserveFeed
	var recommended []*PostRecord[O]
	layout := RecommendationLayout{}
	logViews := (a.CollectOptions.ViewPostsEvent || a.CollectOptions.ViewPostsDetails) && a.Model.EventLogger != nil
	detail := (logViews && a.CollectOptions.ViewPostsDetails) || a.Model.CaptureFeedDetails
	if observeFeed == nil && !detail {
		recommended = a.Model.GetRecommendation(a, neighbors)
	} else {
		recommended, layout = a.Model.observedRecommendations(a, neighbors, detail)
	}

	_, cNP, cR, dNA, dNP, dR := PartitionPosts(
		a.CurOpinion,
		neighbors,
		neighborPosts,
		recommended,
		a.Model.Dynamics,
		a.Params,
	)

	cNOps := extractOpinions(cNP)
	cROps := extractOpinions(cR)
	dNOps := extractOpinions(dNP)
	dROps := extractOpinions(dR)

	nextOpinion, opSum := a.Model.Dynamics.Step(
		a.CurOpinion,
		cNOps,
		cROps,
		dNOps,
		dROps,
		a.Params,
		a.Model.RNG.Stream(smprng.StreamDynamics),
	)
	a.NextOpinion = nextOpinion

	nNeighbor := len(cNP)
	nRecommended := len(cR)
	nConcordant := nNeighbor + nRecommended

	if a.CollectOptions.AgentNumber {
		a.AgentNumber = [4]int{nNeighbor, nRecommended, len(dNP), len(dR)}
	}
	if a.CollectOptions.OpinionSum {
		a.OpinionSum = opSum
	}

	var views ViewPostsEventBody[O]
	if logViews || observeFeed != nil {
		views = ViewPostsEventBody[O]{
			NeighborConcordant:    cNP,
			NeighborDiscordant:    dNP,
			RecommendedConcordant: cR,
			RecommendedDiscordant: dR,
		}
		if detail {
			views.Details = a.feedDetails(neighbors, recommended, layout)
		}
		if observeFeed != nil {
			observeFeed(a, FeedObservation[O]{ViewPostsEventBody: views, Displayed: recommended, Layout: layout})
		}
	}

	behaviorRNG := a.Model.RNG.Stream(smprng.StreamBehavior)
	rndRepost := behaviorRNG.Float64()
	rndRewiring := behaviorRNG.Float64()

	var behaviorParams AgentBehaviorParams
	if bp, ok := any(a.Params).(AgentBehaviorParams); ok {
		behaviorParams = bp
	}

	var ePost *PostEventBody[O]
	repostRate := 0.0
	if behaviorParams != nil {
		repostRate = behaviorParams.GetRepostRate()
	}
	if nNeighbor > 0 && rndRepost < repostRate {
		// Repost a concordant post
		repostIndex := int(float64(nConcordant)*rndRepost/repostRate) % nConcordant
		var repostRecord *PostRecord[O]
		if repostIndex < nNeighbor {
			repostRecord = cNP[repostIndex]
		} else {
			repostRecord = cR[repostIndex-nNeighbor]
		}
		a.NextPost = repostRecord
		if a.CollectOptions.PostEvent {
			ePost = &PostEventBody[O]{Record: a.NextPost, IsRepost: true}
		}
	} else {
		postRecord := PostRecord[O]{AgentID: a.ID, Step: a.Model.CurStep, Opinion: nextOpinion}
		a.NextPost = &postRecord
		if a.CollectOptions.PostEvent {
			ePost = &PostEventBody[O]{Record: a.NextPost, IsRepost: false}
		}
	}

	// Handle rewiring
	rewiringRate := 0.0
	if behaviorParams != nil {
		rewiringRate = behaviorParams.GetRewiringRate()
	}
	var eRewiring *RewiringEventBody
	if rewiringRate > 0 &&
		len(dNP) > 0 && len(cR) > 0 &&
		rndRewiring < rewiringRate {
		idx1 := behaviorRNG.IntN(len(cR))
		idx2 := behaviorRNG.IntN(len(dNP))
		follow := cR[idx1].AgentID
		unfollow := dNA[idx2].ID
		a.NextFollow = &RewiringEventBody{AgentID: a.ID, Unfollow: unfollow, Follow: follow}
		if a.CollectOptions.RewiringEvent {
			eRewiring = a.NextFollow
		}
	}

	if a.Model.EventLogger != nil {
		if logViews {
			a.Model.EventLogger(&EventRecord{
				Type:    "ViewPosts",
				AgentID: a.ID,
				Step:    a.Model.CurStep,
				Body:    views,
			})
		}
		if ePost != nil {
			a.Model.EventLogger(&EventRecord{
				Type:    "Post",
				AgentID: a.ID,
				Step:    a.Model.CurStep,
				Body:    *ePost,
			})
		}
		if eRewiring != nil {
			a.Model.EventLogger(&EventRecord{
				Type:    "Rewiring",
				AgentID: a.ID,
				Step:    a.Model.CurStep,
				Body:    *eRewiring,
			})
		}
	}
}
