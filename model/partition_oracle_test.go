package model

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

type partitionOracleDynamics struct {
	calls []float64
}

func (d *partitionOracleDynamics) Concordant(_, other float64, _ *feedTestParams) bool {
	d.calls = append(d.calls, other)
	// Depend on call position too: this catches repeated or reordered tests even
	// when the final inputs contain the same set of post opinions.
	return (int(other)+len(d.calls))%3 == 0
}

func (*partitionOracleDynamics) Step(x float64, _, _, _, _ []float64, _ *feedTestParams, _ *rand.Rand) (float64, AgentOpinionSumRecord) {
	return x, AgentOpinionSumRecord{}
}

type oraclePartition struct {
	cNA, dNA       []*SMPAgent[float64, feedTestParams]
	cN, cR, dN, dR []*PostRecord[float64]
}

// Keep the append-only partition used before this optimization as an independent
// oracle. In particular, do not derive expected results from the new offsets.
func oldPartitionOracle(neighbors []*SMPAgent[float64, feedTestParams], neighborPosts, recommended []*PostRecord[float64], dynamics *partitionOracleDynamics) oraclePartition {
	var result oraclePartition
	for i, post := range neighborPosts {
		if post == nil {
			continue
		}
		if dynamics.Concordant(0, post.Opinion, nil) {
			result.cNA = append(result.cNA, neighbors[i])
			result.cN = append(result.cN, post)
		} else {
			result.dNA = append(result.dNA, neighbors[i])
			result.dN = append(result.dN, post)
		}
	}
	for _, post := range recommended {
		if dynamics.Concordant(0, post.Opinion, nil) {
			result.cR = append(result.cR, post)
		} else {
			result.dR = append(result.dR, post)
		}
	}
	return result
}

func TestPartitionMatchesOldOracleAndConcordanceCallOrder(t *testing.T) {
	for count := 0; count <= 7; count++ {
		neighbors := make([]*SMPAgent[float64, feedTestParams], count)
		allPosts := make([]*PostRecord[float64], count)
		for i := range neighbors {
			neighbors[i] = &SMPAgent[float64, feedTestParams]{ID: int64(i)}
			allPosts[i] = &PostRecord[float64]{AgentID: int64(i), Opinion: float64(3*i + 1)}
		}
		for present := 0; present < 1<<count; present++ {
			posts := append([]*PostRecord[float64](nil), allPosts...)
			for i := range posts {
				if present&(1<<i) == 0 {
					posts[i] = nil
				}
			}
			for recCount := 0; recCount <= 7; recCount++ {
				recommended := make([]*PostRecord[float64], recCount)
				for i := range recommended {
					recommended[i] = &PostRecord[float64]{AgentID: int64(count + i), Opinion: float64(i + 20)}
				}
				beforePosts := append([]*PostRecord[float64](nil), posts...)
				beforeRecommended := append([]*PostRecord[float64](nil), recommended...)
				var oldDynamics, newDynamics partitionOracleDynamics
				want := oldPartitionOracle(neighbors, posts, recommended, &oldDynamics)
				cNA, cN, cR, dNA, dN, dR := PartitionPosts(0.0, neighbors, posts, recommended, &newDynamics, &feedTestParams{})
				got := oraclePartition{cNA: cNA, dNA: dNA, cN: cN, cR: cR, dN: dN, dR: dR}
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(newDynamics.calls, oldDynamics.calls) {
					t.Fatalf("partition differs from old order/values: neighbors=%d present=%b recommendations=%d", count, present, recCount)
				}
				if !reflect.DeepEqual(posts, beforePosts) || (recCount > 0 && !reflect.DeepEqual(recommended, beforeRecommended)) {
					t.Fatal("partition mutated its caller's post slices")
				}
			}
		}
	}
}
