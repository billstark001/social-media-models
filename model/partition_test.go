package model

import (
	"reflect"
	"testing"
)

func TestPartitionOrderAndAppendOwnership(t *testing.T) {
	neighbors := make([]*SMPAgent[float64, feedTestParams], 5)
	posts := make([]*PostRecord[float64], len(neighbors))
	for i := range neighbors {
		neighbors[i] = &SMPAgent[float64, feedTestParams]{ID: int64(i)}
		posts[i] = &PostRecord[float64]{AgentID: int64(i), Opinion: float64(i % 2)}
	}
	posts[2] = nil
	recommended := []*PostRecord[float64]{{Opinion: 1}, {Opinion: 0}, {Opinion: 1}, {Opinion: 0}}
	cNA, cN, cR, dNA, dN, dR := PartitionPosts(0.0, neighbors, posts, recommended, feedTestDynamics{}, &feedTestParams{})
	if !reflect.DeepEqual(cNA, []*SMPAgent[float64, feedTestParams]{neighbors[0], neighbors[4]}) ||
		!reflect.DeepEqual(dNA, []*SMPAgent[float64, feedTestParams]{neighbors[1], neighbors[3]}) ||
		!reflect.DeepEqual(cN, []*PostRecord[float64]{posts[0], posts[4]}) ||
		!reflect.DeepEqual(dN, []*PostRecord[float64]{posts[1], posts[3]}) ||
		!reflect.DeepEqual(cR, []*PostRecord[float64]{recommended[1], recommended[3]}) ||
		!reflect.DeepEqual(dR, []*PostRecord[float64]{recommended[0], recommended[2]}) {
		t.Fatal("partition changed input order or the alignment of postless followees")
	}
	for _, group := range [][]*PostRecord[float64]{cN, cR, dN, dR} {
		_ = append(group, nil)
	}
	_ = append(cNA, nil)
	_ = append(dNA, nil)
	if dN[0] != posts[1] || cR[0] != recommended[1] || dR[0] != recommended[0] || dNA[0] != neighbors[1] {
		t.Fatal("appending to one partition overwrote another")
	}
	cNOps, cROps, dNOps, dROps := feedOpinions(cN, cR, dN, dR)
	for _, group := range [][]float64{cNOps, cROps, dNOps, dROps} {
		_ = append(group, 99)
	}
	if cROps[0] != 0 || dNOps[0] != 1 || dROps[0] != 1 {
		t.Fatal("appending an opinion overwrote another dynamics input")
	}
}

func TestEmptyPartitionsKeepNilPostsAndNonNilOpinions(t *testing.T) {
	cNA, cN, cR, dNA, dN, dR := PartitionPosts[float64, feedTestParams](0, nil, nil, nil, feedTestDynamics{}, &feedTestParams{})
	if cNA != nil || cN != nil || cR != nil || dNA != nil || dN != nil || dR != nil {
		t.Fatal("empty post partitions must remain nil")
	}
	cNOps, cROps, dNOps, dROps := feedOpinions(cN, cR, dN, dR)
	if cNOps == nil || cROps == nil || dNOps == nil || dROps == nil {
		t.Fatal("dynamics must still receive non-nil empty opinion slices")
	}
}
