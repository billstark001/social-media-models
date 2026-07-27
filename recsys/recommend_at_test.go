package recsys_test

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"testing"
)

func TestRecommendAtIsReproducibleAndDoesNotConsumeSimulationRNG(t *testing.T) {
	recommenders := []string{
		"Random",
		"Opinion",
		"Structure",
		"OpinionRandom",
		"StructureRandom",
		"OpinionM9",
		"StructureM9",
	}
	for _, recommender := range recommenders {
		t.Run(recommender, func(t *testing.T) {
			first := benchmarkRecommendationModel(recommender)
			firstAgent := first.Schedule.Agents[0]
			firstNeighbors := first.Grid.GetNeighbors(firstAgent.ID, false)
			rngBefore, err := first.RNG.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			cacheBefore := first.Recsys.Dump()
			firstPosts, err := first.GetRecommendationAt(
				firstAgent,
				0.125,
				firstNeighbors,
				rand.New(rand.NewPCG(123, 456)),
			)
			if err != nil {
				t.Fatal(err)
			}
			rngAfter, err := first.RNG.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rngBefore, rngAfter) {
				t.Fatal("RecommendAt consumed a simulation RNG stream")
			}
			if !bytes.Equal(cacheBefore, first.Recsys.Dump()) {
				t.Fatal("RecommendAt changed the simulation recommender cache")
			}

			second := benchmarkRecommendationModel(recommender)
			secondAgent := second.Schedule.Agents[0]
			secondNeighbors := second.Grid.GetNeighbors(secondAgent.ID, false)
			secondPosts, err := second.GetRecommendationAt(
				secondAgent,
				0.125,
				secondNeighbors,
				rand.New(rand.NewPCG(123, 456)),
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(firstPosts, secondPosts) {
				t.Fatalf("RecommendAt is not reproducible: first=%v second=%v", firstPosts, secondPosts)
			}
		})
	}
}
