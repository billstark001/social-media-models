// Package progress defines the compact JSONL progress wire format shared by
// the persistent and batch simulation commands.
package progress

import (
	"encoding/json"
	"io"
)

const Version = 1

const (
	TypeRNG      = "rng"
	TypeStart    = "start"
	TypeProgress = "progress"
	TypeDone     = "done"
)

// Event uses deliberately short JSON keys because progress can be emitted
// repeatedly during long simulations. Field names remain descriptive in Go.
type Event struct {
	Version    int    `json:"v"`
	RequestID  string `json:"id"`
	Type       string `json:"t"`
	Step       int    `json:"s,omitempty"`
	MaxStep    int    `json:"m,omitempty"`
	StopReason string `json:"r,omitempty"`
	Algorithm  string `json:"a,omitempty"`
	Seed1      string `json:"x,omitempty"`
	Seed2      string `json:"y,omitempty"`
}

// WriteJSONL writes exactly one compact event and a trailing newline.
func WriteJSONL(writer io.Writer, event Event) error {
	event.Version = Version
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(event)
}
