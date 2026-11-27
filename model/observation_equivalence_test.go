package model_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"smp/model"
	smprng "smp/rng"
	"smp/utils"
)

type recommendationSelection[O any] struct {
	Agent int64
	Posts []model.PostRecord[O]
}

type tracingRecommender[O, P any] struct {
	model.SMPModelRecommendationSystem[O, P]
	record func(int64, []*model.PostRecord[O])
}

func (r *tracingRecommender[O, P]) Recommend(a *model.SMPAgent[O, P], excluded map[int64]bool, count int) []*model.PostRecord[O] {
	posts := r.SMPModelRecommendationSystem.Recommend(a, excluded, count)
	r.record(a.ID, posts)
	return posts
}

type tracingLayoutRecommender[O, P any] struct {
	*tracingRecommender[O, P]
	layout model.RecommendationLayoutSystem[O, P]
}

func (r *tracingLayoutRecommender[O, P]) RecommendWithLayout(a *model.SMPAgent[O, P], excluded map[int64]bool, count int) ([]*model.PostRecord[O], model.RecommendationLayout) {
	posts, layout := r.layout.RecommendWithLayout(a, excluded, count)
	r.record(a.ID, posts)
	return posts, layout
}

// The baseline hash intentionally checks wire output, including collected
// statistics/events. Compare physical state separately so a mode cannot silently
// produce a different trajectory while both modes acquire new golden hashes.
func checkStepObservationModes[O, P any](t *testing.T, recsys string,
	newDynamics func() model.Dynamics[O, P], params P, factories map[string]model.RecsysFactory[O, P],
	opinion func(*rand.Rand) O,
) {
	t.Helper()
	profiles := []struct {
		name     string
		collect  model.CollectItemOptions
		logger   bool
		observer bool
		capture  bool
	}{
		{name: "disabled"},
		{name: "counters", collect: model.CollectItemOptions{AgentNumber: true, OpinionSum: true}},
		{name: "events_without_logger", collect: model.CollectItemOptions{PostEvent: true, RewiringEvent: true, ViewPostsEvent: true, ViewPostsDetails: true}},
		{name: "events", collect: model.CollectItemOptions{PostEvent: true, RewiringEvent: true, ViewPostsEvent: true}, logger: true},
		{name: "logged_details", collect: model.CollectItemOptions{ViewPostsDetails: true}, logger: true},
		{name: "observer", observer: true},
		{name: "capture", capture: true},
		{name: "observer_details", observer: true, capture: true},
		{name: "all", collect: model.CollectItemOptions{AgentNumber: true, OpinionSum: true, PostEvent: true, RewiringEvent: true, ViewPostsEvent: true, ViewPostsDetails: true}, logger: true, observer: true, capture: true},
	}
	for _, retain := range []int{0, 3} {
		var wantStates []*model.SMPModelDumpData[O, P]
		var wantSelections [][]recommendationSelection[O]
		var wantChanges []int
		var wantDeltas []float64
		for _, profile := range profiles {
			t.Run(fmt.Sprintf("retain_%d/%s", retain, profile.name), func(t *testing.T) {
				pool := smprng.MustNewPool(smprng.FixedSpec(193, 29))
				graph := utils.CreateRandomNetwork(18, .25, pool.Stream(smprng.StreamNetwork))
				ops := make([]O, 18)
				for i := range ops {
					ops[i] = opinion(pool.Stream(smprng.StreamOpinion))
				}
				var events []*model.EventRecord
				var encodedEvents [][]byte
				var logger func(*model.EventRecord)
				if profile.logger {
					logger = func(event *model.EventRecord) {
						encoded, err := json.Marshal(event)
						if err != nil {
							t.Fatal(err)
						}
						events = append(events, event)
						encodedEvents = append(encodedEvents, encoded)
					}
				}
				m := model.NewSMPModel(graph, &ops, &model.SMPModelParams[O, P]{
					SMPModelPureParams: model.SMPModelPureParams{RecsysCount: 4, PostRetainCount: retain}, RecsysFactory: factories[recsys],
				}, &params, newDynamics(), &profile.collect, logger, pool)
				m.SetAgentCurPosts()
				m.Recsys.PostInit(nil)
				var selections []recommendationSelection[O]
				recorder := &tracingRecommender[O, P]{SMPModelRecommendationSystem: m.Recsys, record: func(agent int64, posts []*model.PostRecord[O]) {
					selection := recommendationSelection[O]{Agent: agent, Posts: make([]model.PostRecord[O], len(posts))}
					for i, post := range posts {
						selection.Posts[i] = *post
					}
					selections = append(selections, selection)
				}}
				// Preserve the optional layout interface so observation follows the
				// same entry point as the original recommender in every profile.
				if layout, ok := m.Recsys.(model.RecommendationLayoutSystem[O, P]); ok {
					m.Recsys = &tracingLayoutRecommender[O, P]{tracingRecommender: recorder, layout: layout}
				} else {
					m.Recsys = recorder
				}
				m.CaptureFeedDetails = profile.capture
				if profile.observer {
					m.ObserveFeed = func(a *model.SMPAgent[O, P], feed model.FeedObservation[O]) {
						last := selections[len(selections)-1]
						posts := make([]model.PostRecord[O], len(feed.Displayed))
						for i, post := range feed.Displayed {
							posts[i] = *post
						}
						if last.Agent != a.ID || !reflect.DeepEqual(last.Posts, posts) {
							t.Fatal("observation changed the selected recommendation order")
						}
					}
				}
				for step := 0; step < 10; step++ {
					changed, delta := m.Step(true)
					state := m.Dump()
					state.RecsysDumpData = nil // unordered serialization; feeds are compared directly
					state.AgentNumbers, state.AgentOpinionSums = nil, nil
					if profile.name == "disabled" {
						wantStates = append(wantStates, state)
						wantSelections = append(wantSelections, selections)
						wantChanges = append(wantChanges, changed)
						wantDeltas = append(wantDeltas, delta)
					} else if changed != wantChanges[step] || delta != wantDeltas[step] || !reflect.DeepEqual(state, wantStates[step]) || !reflect.DeepEqual(selections, wantSelections[step]) {
						t.Fatalf("collection/observation changed ordered recommendations, physical state or RNG at step %d", step)
					}
					selections = nil
				}
				for i, event := range events {
					encoded, err := json.Marshal(event)
					if err != nil || !bytes.Equal(encoded, encodedEvents[i]) {
						t.Fatalf("a queued event changed after later updates: event %d, error %v", i, err)
					}
				}
			})
		}
	}
}
