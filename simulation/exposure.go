package simulation

import (
	"math"
	"smp/model"
)

type SlotCounts struct {
	Shown          int `json:"shown"`
	CrossCamp      int `json:"cross_camp"`
	CrossConfident int `json:"cross_confident"`
	CrossRewire    int `json:"cross_rewire"`
}

type Exposure struct {
	Updates            int        `json:"updates"`
	Requested          int        `json:"requested"`
	NeighborSeen       int        `json:"neighbor_seen"`
	NeighborConcordant int        `json:"neighbor_concordant"`
	NeighborWithPosts  int        `json:"neighbor_with_posts"`
	NeighborShareSum   float64    `json:"neighbor_share_sum"`
	Recommended        int        `json:"recommended"`
	Exploration        SlotCounts `json:"exploration"`
	Policy             SlotCounts `json:"policy"`
	Bridge             SlotCounts `json:"bridge"`
	CrossCamp          int        `json:"cross_camp"`
	CrossConfident     int        `json:"cross_confident"`
	CrossRewire        int        `json:"cross_rewire"`
	Rewires            int        `json:"rewires"`
	AbsoluteShift      float64    `json:"absolute_shift"`
}

func ObserveHKFeed(m *hkModel, exposure *Exposure, record func(int, int64, *model.FeedDetails[float64])) {
	margin := m.AgentParams.Tolerance / 4
	m.CaptureFeedDetails = record != nil
	m.ObserveFeed = func(agent *hkAgent, views model.FeedObservation[float64]) {
		exposure.Updates++
		exposure.Requested += m.ModelParams.RecsysCount
		exposure.AbsoluteShift += math.Abs(agent.NextOpinion - agent.CurOpinion)
		neighborSeen := len(views.NeighborConcordant) + len(views.NeighborDiscordant)
		exposure.NeighborSeen += neighborSeen
		if neighborSeen > 0 {
			exposure.NeighborWithPosts++
			exposure.NeighborShareSum += float64(len(views.NeighborConcordant)) / float64(neighborSeen)
		}
		exposure.NeighborConcordant += len(views.NeighborConcordant)
		exposure.Recommended += len(views.Displayed)
		for index, post := range views.Displayed {
			counts := &exposure.Policy
			switch views.Layout.Source(index) {
			case model.ExplorationSlot:
				counts = &exposure.Exploration
			case model.BridgeSlot:
				counts = &exposure.Bridge
			}
			counts.Shown++
			if !oppositeCamps(agent.CurOpinion, post.Opinion, margin) {
				continue
			}
			exposure.CrossCamp++
			counts.CrossCamp++
			if !m.Dynamics.Concordant(agent.CurOpinion, post.Opinion, m.AgentParams) {
				continue
			}
			exposure.CrossConfident++
			counts.CrossConfident++
			if len(views.NeighborDiscordant) > 0 && agent.CanFollow(post.AgentID) {
				exposure.CrossRewire++
				counts.CrossRewire++
			}
		}
		if record != nil {
			record(m.CurStep, agent.ID, views.Details)
		}
	}
}
