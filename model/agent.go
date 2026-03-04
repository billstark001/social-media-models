package model

import (
	"math"
	"math/rand"
)

// events & records

type EventRecord struct {
	Type    string
	AgentID int64
	Step    int
	Body    any
}

type RewiringEventBody struct {
	Unfollow int64
	Follow   int64
}

type TweetEventBody struct {
	Record    *TweetRecord
	IsRetweet bool
	// RetweetFrom *int64
}

type ViewTweetsEventBody struct {
	NeighborConcordant    []*TweetRecord
	NeighborDiscordant    []*TweetRecord
	RecommendedConcordant []*TweetRecord
	RecommendedDiscordant []*TweetRecord
}

// (concordant neighbor, concordant recommend, d.n., d.r.)
type AgentNumberRecord = [4]int
type AgentOpinionSumRecord = [4]float64

// agent params

type SMPAgentParams struct {
	Tolerance    float64
	Decay        float64
	RewiringRate float64
	RetweetRate  float64
}

func DefaultSMPAgentParams() *SMPAgentParams {
	return &SMPAgentParams{
		Tolerance:    0.25,
		Decay:        1.0,
		RewiringRate: 0.1,
		RetweetRate:  0.3,
	}
}

// sim options

// SMPAgent represents an agent in the social media platform model
type SMPAgent struct {
	ID int64 // to align with gonum/graph

	Model          *SMPModel
	hasEventLogger bool
	Params         *SMPAgentParams
	CollectOptions *CollectItemOptions

	CurOpinion float64
	CurTweet   *TweetRecord

	NextOpinion float64
	NextTweet   *TweetRecord
	NextFollow  *RewiringEventBody

	AgentNumber AgentNumberRecord
	OpinionSum  AgentOpinionSumRecord
}

// NewSMPAgent creates a new SMPAgent
func NewSMPAgent(uniqueID int64, model *SMPModel, opinion *float64) *SMPAgent {
	// Initialize opinion if not provided
	var curOpinion float64
	if opinion != nil {
		curOpinion = *opinion
	} else {
		curOpinion = rand.Float64()*2 - 1 // Random between -1 and 1
	}

	agent := &SMPAgent{
		ID:          uniqueID,
		Model:       model,
		CurOpinion:  curOpinion,
		NextOpinion: curOpinion,
	}

	// Setup parameters
	agent.hasEventLogger = model.EventLogger != nil
	agent.Params = model.AgentParams
	agent.CollectOptions = model.CollectItems

	return agent
}

type PartitionTweetsReturn struct {
	concordantNeighbor      []*SMPAgent
	concordantNeighborTweet []*TweetRecord
	concordantRecommended   []*TweetRecord
	discordantNeighbor      []*SMPAgent
	discordantNeighborTweet []*TweetRecord
	discordantRecommended   []*TweetRecord
	sumN                    float64
	sumR                    float64
	sumND                   float64
	sumRD                   float64
}

// PartitionTweets divides tweets into concordant and discordant groups
func PartitionTweets(
	opinion float64,
	neighbors []*SMPAgent,
	neighbor_tweets []*TweetRecord,
	recommended []*TweetRecord,
	epsilon float64,
) PartitionTweetsReturn {

	// return value
	r := PartitionTweetsReturn{}

	// Process neighbors
	for i, at := range neighbor_tweets {
		a := neighbors[i]
		o := at.Opinion
		if math.Abs(opinion-o) <= epsilon {
			r.concordantNeighbor = append(r.concordantNeighbor, a)
			r.concordantNeighborTweet = append(r.concordantNeighborTweet, at)
			r.sumN += o - opinion
		} else {
			r.discordantNeighbor = append(r.discordantNeighbor, a)
			r.discordantNeighborTweet = append(r.discordantNeighborTweet, at)
			r.sumND += o - opinion
		}
	}
	if neighbors != nil {
	}

	// Process recommended
	for _, a := range recommended {
		o := a.Opinion
		if math.Abs(opinion-o) <= epsilon {
			r.concordantRecommended = append(r.concordantRecommended, a)
			r.sumR += o - opinion
		} else {
			r.discordantRecommended = append(r.discordantRecommended, a)
			r.sumRD += o - opinion
		}
	}

	return r
}

// SMPAgentStep implements the agent's step function
func SMPAgentStep(
	agentID int64,
	curOpinion float64,
	curStep int,
	neighbors []*SMPAgent,
	neighbor_tweets []*TweetRecord,
	recommended []*TweetRecord,
	params *SMPAgentParams,
	options *CollectItemOptions,
) (
	nextOpinion float64,
	nextTweet *TweetRecord,
	nextFollow *RewiringEventBody,

	nrAgents AgentNumberRecord,
	opSumAgents AgentOpinionSumRecord,

	eViewTweets *ViewTweetsEventBody,
	eRetweet *TweetEventBody,
	eRewiring *RewiringEventBody,

) {
	// Default return values
	nextOpinion = curOpinion
	var zeroAgents [4]int
	var zeroOpSum [4]float64
	nrAgents = zeroAgents
	opSumAgents = zeroOpSum

	// Calculate tweet sets
	t := PartitionTweets(
		curOpinion, neighbors, neighbor_tweets, recommended, params.Tolerance,
	)

	nNeighbor := len(t.concordantNeighborTweet)
	nRecommended := len(t.concordantRecommended)
	nConcordant := nNeighbor + nRecommended

	// Collect agent counts if requested
	if options.AgentNumber {
		nrAgents = [4]int{
			nNeighbor,
			nRecommended,
			len(t.discordantNeighborTweet),
			len(t.discordantRecommended),
		}
	}

	// Record viewed tweets if requested
	if options.ViewTweetsEvent {
		eViewTweets = &ViewTweetsEventBody{
			NeighborConcordant:    t.concordantNeighborTweet,
			NeighborDiscordant:    t.discordantNeighborTweet,
			RecommendedConcordant: t.concordantRecommended,
			RecommendedDiscordant: t.discordantRecommended,
		}
	}

	// Calculate influence
	if nConcordant > 0 {
		nextOpinion += ((t.sumR + t.sumN) / float64(nConcordant)) * params.Decay
	}

	// Collect opinion sums if requested
	if options.OpinionSum {
		opSumAgents = [4]float64{t.sumN, t.sumR, t.sumND, t.sumRD}
	}

	// Generate random numbers for retweet and rewiring decisions
	rndRetweet := rand.Float64()
	rndRewiring := rand.Float64()

	// Handle tweet or retweet
	rRetweet := params.RetweetRate
	if nNeighbor > 0 && rndRetweet < rRetweet {
		// Retweet
		retweetIndex := int(float64(nConcordant)*rndRetweet/rRetweet) % nConcordant
		var retweetRecord *TweetRecord
		if retweetIndex < nNeighbor {
			retweetRecord = t.concordantNeighborTweet[retweetIndex]
		} else {
			retweetRecord = t.concordantRecommended[retweetIndex-nNeighbor]
		}
		nextTweet = retweetRecord

		// report event
		if options.TweetEvent {
			eRetweet = &TweetEventBody{
				Record:    nextTweet,
				IsRetweet: true,
			}
		}
	} else { // Post new tweet
		tweetRecord := TweetRecord{agentID, curStep, nextOpinion}
		nextTweet = &tweetRecord

		if options.TweetEvent {
			eRetweet = &TweetEventBody{
				Record:    nextTweet,
				IsRetweet: false,
			}
		}
	}

	// Handle rewiring
	gamma := params.RewiringRate
	if gamma > 0 &&
		len(t.discordantNeighborTweet) > 0 && len(t.concordantRecommended) > 0 &&
		rndRewiring < gamma {
		idx1 := rand.Intn(len(t.concordantRecommended))
		idx2 := rand.Intn(len(t.discordantNeighborTweet))
		follow := t.concordantRecommended[idx1].AgentID
		unfollow := t.discordantNeighbor[idx2].ID

		nextFollow = &RewiringEventBody{
			Unfollow: unfollow,
			Follow:   follow,
		}

		// report event
		if options.RewiringEvent {
			eRewiring = nextFollow
		}
	}

	return
}

// Step performs a single step for this agent
func (a *SMPAgent) Step() {
	// Get the neighbors
	neighbors := a.Model.Grid.GetNeighbors(a.ID, false)

	// latest 1 tweet
	neighbor_tweets := make([]*TweetRecord, 0)
	for _, a := range neighbors {
		t := a.CurTweet
		if t != nil {
			neighbor_tweets = append(neighbor_tweets, t)
		}
	}

	recommended := a.Model.GetRecommendation(a, neighbors)

	// Call agent step function
	nextOpinion, nextTweet, nextFollow, nrAgents, opSumAgents,
		eViewTweets, eRetweet, eRewiring := SMPAgentStep(
		a.ID,
		a.CurOpinion,
		a.Model.CurStep,
		neighbors,
		neighbor_tweets,
		recommended,
		a.Params,
		a.CollectOptions,
	)

	// Update agent state
	a.NextOpinion = nextOpinion
	a.NextTweet = nextTweet
	a.NextFollow = nextFollow
	a.AgentNumber = nrAgents
	a.OpinionSum = opSumAgents

	// Report events if required
	if a.Model.EventLogger != nil {
		if eViewTweets != nil {
			a.Model.EventLogger(&EventRecord{
				Type:    "ViewTweets",
				AgentID: a.ID,
				Step:    a.Model.CurStep,
				Body:    *eViewTweets,
			})
		}
		if eRetweet != nil {
			a.Model.EventLogger(&EventRecord{
				Type:    "Tweet",
				AgentID: a.ID,
				Step:    a.Model.CurStep,
				Body:    *eRetweet,
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
