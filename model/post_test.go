package model

import "testing"

func TestOriginalPlacementDistinguishesRepostsByRound(t *testing.T) {
	post := &PostRecord[float64]{AgentID: 3, Step: 7}
	if !post.IsOriginalPlacement(3, 7) || post.IsOriginalPlacement(5, 7) || post.IsOriginalPlacement(3, 8) {
		t.Fatal("expected original at publication and reposts at later or other-user placements")
	}
	initial := &PostRecord[float64]{AgentID: 3, Step: -1}
	if !initial.IsOriginalPlacement(3, 0) || initial.IsOriginalPlacement(3, 1) {
		t.Fatal("initial posts are original only at initialization")
	}
}
