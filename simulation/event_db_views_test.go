package simulation

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"smp/model"
	smprng "smp/rng"

	"github.com/vmihailenco/msgpack/v5"
)

func TestBooleanViewEventsPersistAsNumericOpinionData(t *testing.T) {
	for _, detail := range []bool{false, true} {
		t.Run(fmt.Sprintf("details_%v", detail), func(t *testing.T) {
			db, err := OpenEventDB(filepath.Join(t.TempDir(), "events.db"), 10)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			left := &model.PostRecord[bool]{AgentID: 2, Step: 1, Opinion: false}
			right := &model.PostRecord[bool]{AgentID: 3, Step: 1, Opinion: true}
			body := model.ViewPostsEventBody[bool]{
				NeighborConcordant:    []*model.PostRecord[bool]{left},
				NeighborDiscordant:    []*model.PostRecord[bool]{right},
				RecommendedConcordant: []*model.PostRecord[bool]{left},
				RecommendedDiscordant: []*model.PostRecord[bool]{right},
			}
			placement := &model.PostPlacement{Holder: 4, Round: 1}
			if detail {
				body.Details = &model.FeedDetails[bool]{
					Opinion: true, Requested: 2,
					Neighbors: []model.FeedSlot[bool]{
						{Post: right, Placement: placement, Source: model.NeighborSlot},
						{Placement: &model.PostPlacement{Holder: 5, Round: 1}, Source: model.NeighborSlot},
					},
					Recommendations: []model.FeedSlot[bool]{
						{Post: left, Source: model.ExplorationSlot},
						{Post: right, Placement: placement, Source: model.PolicySlot},
					},
				}
			}
			if err := db.StoreEvent(&model.EventRecord{Type: "ViewPosts", AgentID: 1, Step: 2, Body: body}); err != nil {
				t.Fatal(err)
			}
			if err := db.Flush(); err != nil {
				t.Fatal(err)
			}
			events, err := db.GetEvents()
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Fatalf("expected one event, got %d", len(events))
			}
			actual, ok := events[0].Body.(model.ViewPostsEventBody[float64])
			if !ok {
				t.Fatalf("reader returned %T", events[0].Body)
			}
			if actual.NeighborConcordant[0].Opinion != 0 || actual.NeighborDiscordant[0].Opinion != 1 ||
				actual.RecommendedConcordant[0].Opinion != 0 || actual.RecommendedDiscordant[0].Opinion != 1 {
				t.Fatalf("bool confidence buckets did not retain 0/1 opinions: %+v", actual)
			}
			if !detail {
				if actual.Details != nil {
					t.Fatal("lightweight event gained details")
				}
			} else {
				details := actual.Details
				if details == nil || details.Opinion != 1 || details.Requested != 2 ||
					len(details.Neighbors) != 2 || len(details.Recommendations) != 2 {
					t.Fatalf("invalid numeric details: %+v", details)
				}
				if details.Neighbors[0].Post.Opinion != 1 || details.Neighbors[1].Post != nil ||
					details.Neighbors[1].Placement.Holder != 5 || details.Recommendations[0].Placement != nil ||
					details.Recommendations[0].Source != model.ExplorationSlot ||
					details.Recommendations[0].Post.Opinion != 0 ||
					!reflect.DeepEqual(details.Recommendations[1].Placement, placement) {
					t.Fatalf("placement, missing neighbor, source, or opinion lost: %+v", details)
				}
			}

			// Readers without feed-detail fields can still decode the four confidence buckets.
			var payload []byte
			if err := db.db.QueryRow("SELECT data FROM view_posts_events").Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var old struct {
				NeighborConcordant    []*model.PostRecord[float64]
				NeighborDiscordant    []*model.PostRecord[float64]
				RecommendedConcordant []*model.PostRecord[float64]
				RecommendedDiscordant []*model.PostRecord[float64]
			}
			if err := msgpack.Unmarshal(payload, &old); err != nil || old.NeighborDiscordant[0].Opinion != 1 {
				t.Fatalf("old reader cannot decode numeric bool event: %v", err)
			}
		})
	}
}

func TestFloat64ViewEventKeepsExistingMsgpackEncoding(t *testing.T) {
	db, err := OpenEventDB(filepath.Join(t.TempDir(), "events.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	body := model.ViewPostsEventBody[float64]{
		NeighborConcordant: []*model.PostRecord[float64]{{AgentID: 2, Step: 1, Opinion: 0.4}},
	}
	expected, err := msgpack.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.StoreEvent(&model.EventRecord{Type: "ViewPosts", AgentID: 1, Step: 2, Body: body}); err != nil {
		t.Fatal(err)
	}
	var actual []byte
	if err := db.db.QueryRow("SELECT data FROM view_posts_events").Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("float64 view encoding changed")
	}
}

func TestBooleanScenarioLogsLightweightAndDetailedFeeds(t *testing.T) {
	for _, dynamicsType := range []string{DynamicsTypeGalam, DynamicsTypeVoter} {
		for _, detail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/details_%v", dynamicsType, detail), func(t *testing.T) {
				metadata := DefaultScenarioMetadata()
				metadata.UniqueName = "bool-views"
				metadata.DynamicsType = dynamicsType
				metadata.RecsysFactoryType = "Random"
				metadata.NodeCount, metadata.NodeFollowCount = 12, 3
				metadata.RNG = smprng.FixedSpec(2, 8)
				metadata.ViewPostsEvent = !detail
				metadata.ViewPostsDetails = detail
				scenario := NewScenarioWithOptions(t.TempDir(), metadata, ScenarioOptions{EnableDumps: true, Quiet: true})
				if err := scenario.InitError(); err != nil {
					t.Fatal(err)
				}
				defer scenario.DB.Close()
				scenario.Step()
				if err := scenario.DB.Flush(); err != nil {
					t.Fatal(err)
				}
				events, err := scenario.DB.GetEvents()
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, event := range events {
					if event.Type == "ViewPosts" {
						feed := event.Body.(model.ViewPostsEventBody[float64])
						count++
						if (feed.Details != nil) != detail {
							t.Fatal("detailed-feed switch was not preserved")
						}
					}
				}
				if count != metadata.NodeCount {
					t.Fatalf("expected %d bool feeds, got %d", metadata.NodeCount, count)
				}
			})
		}
	}
}
