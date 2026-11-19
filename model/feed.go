package model

type SlotSource string

const (
	NeighborSlot    SlotSource = "neighbor"
	ExplorationSlot SlotSource = "exploration"
	PolicySlot      SlotSource = "policy"
	BridgeSlot      SlotSource = "bridge"
)

// RecommendationLayout identifies a filled exploration prefix and an optional
// bridge slot. Its zero value describes ordinary policy recommendations.
type RecommendationLayout struct {
	Exploration int
	Bridge      int
	HasBridge   bool
}

func (layout RecommendationLayout) Source(index int) SlotSource {
	switch {
	case layout.HasBridge && index == layout.Bridge:
		return BridgeSlot
	case index < layout.Exploration:
		return ExplorationSlot
	default:
		return PolicySlot
	}
}

// FeedObservation references the current pre-commit feed. Consumers retaining
// it after the callback must copy the supplied slices.
type FeedObservation[O any] struct {
	ViewPostsEventBody[O]
	Displayed []*PostRecord[O]
	Layout    RecommendationLayout
}

type PostPlacement struct {
	Holder int64 `json:"holder" msgpack:"holder"`
	Round  int   `json:"round" msgpack:"round"`
}

type FeedSlot[O any] struct {
	Post      *PostRecord[O] `json:"post" msgpack:"post"`
	Placement *PostPlacement `json:"placement" msgpack:"placement"`
	Source    SlotSource     `json:"source" msgpack:"source"`
}

type FeedDetails[O any] struct {
	Opinion         O             `json:"opinion" msgpack:"opinion"`
	Requested       int           `json:"requested" msgpack:"requested"`
	Neighbors       []FeedSlot[O] `json:"neighbors" msgpack:"neighbors"`
	Recommendations []FeedSlot[O] `json:"recommendations" msgpack:"recommendations"`
}

// CanFollow reports whether the recommended author can be a new followee.
func (a *SMPAgent[O, P]) CanFollow(author int64) bool {
	return author != a.ID && !a.Model.Graph.HasEdgeFromTo(a.ID, author)
}

func (a *SMPAgent[O, P]) feedDetails(neighbors []*SMPAgent[O, P], posts []*PostRecord[O], layout RecommendationLayout) *FeedDetails[O] {
	slots := make([]FeedSlot[O], len(neighbors)+len(posts))
	feed := &FeedDetails[O]{
		Opinion: a.CurOpinion, Requested: a.Model.ModelParams.RecsysCount,
		Neighbors:       slots[:len(neighbors):len(neighbors)],
		Recommendations: slots[len(neighbors):],
	}
	knownRecommendations := len(a.Model.placements) == len(posts)
	placementCount := len(neighbors)
	if knownRecommendations {
		placementCount += len(posts)
	}
	// Each feed owns its placement values while event logging retains it.
	placements := make([]PostPlacement, placementCount)
	for i, neighbor := range neighbors {
		placements[i] = PostPlacement{Holder: neighbor.ID, Round: a.Model.CurStep - 1}
		feed.Neighbors[i] = FeedSlot[O]{
			Post: neighbor.CurPost, Placement: &placements[i], Source: NeighborSlot,
		}
	}
	for i, post := range posts {
		slot := FeedSlot[O]{Post: post, Source: layout.Source(i)}
		if knownRecommendations {
			index := len(neighbors) + i
			placements[index] = a.Model.placements[i]
			slot.Placement = &placements[index]
		}
		feed.Recommendations[i] = slot
	}
	return feed
}
