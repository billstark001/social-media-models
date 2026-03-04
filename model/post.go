package model

// PostRecord represents a post with agent ID, step, and opinion.
type PostRecord[O any] struct {
	AgentID int64
	Step    int
	Opinion O
}
