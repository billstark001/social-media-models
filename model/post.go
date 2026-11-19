package model

// PostRecord represents a post with agent ID, step, and opinion.
type PostRecord[O any] struct {
	AgentID int64
	Step    int
	Opinion O
}

// IsOriginalPlacement reports whether this copy comes from authorship in the
// placement round. Initial posts use step -1 in the step-zero state.
func (p *PostRecord[O]) IsOriginalPlacement(holder int64, round int) bool {
	return p.AgentID == holder && (p.Step == round || (round == 0 && p.Step == -1))
}
