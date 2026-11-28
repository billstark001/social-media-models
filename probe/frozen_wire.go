package probe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"smp/model"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

// Inline posts must carry explicit value and provenance fields. A missing
// author cannot become author zero, and an unavailable value cannot become a
// compatible value zero. Use author -1 for unknown source; omit a holder's
// history when its displayed value is unavailable. Force still requires known
// valid authors and complete histories in validateForceState.
type inlinePostWire struct {
	AgentID *int64   `json:"AgentID" msgpack:"AgentID"`
	Step    *int     `json:"Step" msgpack:"Step"`
	Opinion *float64 `json:"Opinion" msgpack:"Opinion"`
}

type frozenStateWire struct {
	Step                   int                        `json:"step" msgpack:"step"`
	Graph                  utils.NetworkXGraph        `json:"graph" msgpack:"graph"`
	Opinions               []float64                  `json:"opinions" msgpack:"opinions"`
	Posts                  map[int64][]inlinePostWire `json:"posts" msgpack:"posts"`
	Precision              string                     `json:"precision" msgpack:"precision"`
	PostsUnavailableReason string                     `json:"posts_unavailable_reason,omitempty" msgpack:"posts_unavailable_reason,omitempty"`
}

func (w frozenStateWire) state() (FrozenState, error) {
	state := FrozenState{Step: w.Step, Graph: w.Graph, Opinions: w.Opinions,
		Precision: w.Precision, PostsUnavailableReason: w.PostsUnavailableReason}
	if w.Posts != nil {
		state.Posts = make(map[int64][]model.PostRecord[float64], len(w.Posts))
	}
	for holder, history := range w.Posts {
		var posts []model.PostRecord[float64]
		if history != nil {
			posts = make([]model.PostRecord[float64], 0, len(history))
		}
		for index, post := range history {
			if post.AgentID == nil || post.Step == nil || post.Opinion == nil {
				return FrozenState{}, fmt.Errorf("inline posts holder %d record %d requires explicit non-null AgentID, Step and Opinion; use AgentID=-1 for unknown author or omit the holder when its value is unavailable", holder, index)
			}
			posts = append(posts, model.PostRecord[float64]{AgentID: *post.AgentID, Step: *post.Step, Opinion: *post.Opinion})
		}
		state.Posts[holder] = posts
	}
	return state, nil
}

// UnmarshalJSON retains the command's strict nested-field validation. It is
// also safe when called directly, independently of the outer request decoder.
func (s *FrozenState) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("inline state must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire frozenStateWire
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("inline state requires exactly one JSON object")
	}
	state, err := wire.state()
	if err == nil {
		*s = state
	}
	return err
}

// DecodeMsgpack gives binary and JSON requests the same presence semantics.
func (s *FrozenState) DecodeMsgpack(decoder *msgpack.Decoder) error {
	code, err := decoder.PeekCode()
	if err != nil {
		return err
	}
	if !msgpcode.IsFixedMap(code) && code != msgpcode.Map16 && code != msgpcode.Map32 {
		return errors.New("inline state must be a map")
	}
	decoder.DisallowUnknownFields(true)
	var wire frozenStateWire
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	state, err := wire.state()
	if err == nil {
		*s = state
	}
	return err
}
