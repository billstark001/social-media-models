package probe

import (
	"errors"
	"fmt"

	"smp/bridges"
	"smp/groups"
)

// ScanOptions selects an inclusive saved-run interval. OutputStride thins only
// returned snapshots, never the states used by lifecycle observers. Explicit
// Stride greater than one produces observed, gap-censored histories.
type ScanOptions struct {
	Start           int   `json:"start" msgpack:"start"`
	End             *int  `json:"end,omitempty" msgpack:"end,omitempty"`
	Stride          int   `json:"stride,omitempty" msgpack:"stride,omitempty"`
	OutputStride    int   `json:"output_stride,omitempty" msgpack:"output_stride,omitempty"`
	IncludeEvents   *bool `json:"include_events,omitempty" msgpack:"include_events,omitempty"`
	IncludeEpisodes *bool `json:"include_episodes,omitempty" msgpack:"include_episodes,omitempty"`
}

// AnalysisResult carries lifecycle records independently of snapshot thinning.
// Every record belongs to one request; independent runs never share observers.
type AnalysisResult struct {
	DefinitionVersion      int              `json:"definition_version" msgpack:"definition_version"`
	BranchLedgerVersion    int              `json:"branch_ledger_version,omitempty" msgpack:"branch_ledger_version,omitempty"`
	EventsIncluded         bool             `json:"events_included" msgpack:"events_included"`
	EpisodesIncluded       bool             `json:"episodes_included" msgpack:"episodes_included"`
	GroupOptions           groups.Options   `json:"group_options" msgpack:"group_options"`
	BridgeOptions          *bridges.Options `json:"bridge_options,omitempty" msgpack:"bridge_options,omitempty"`
	Observations           int              `json:"observations" msgpack:"observations"`
	FirstStep              int              `json:"first_step" msgpack:"first_step"`
	LastStep               int              `json:"last_step" msgpack:"last_step"`
	Sampled                bool             `json:"sampled" msgpack:"sampled"`
	PostsUnavailableReason string           `json:"posts_unavailable_reason,omitempty" msgpack:"posts_unavailable_reason,omitempty"`
	// ReferenceMode also names the namespace used by bridge group_ids. In
	// fixed_cohorts mode these are one-based input cohort IDs, independent of
	// the dynamic lineage IDs in result.Groups and GroupEvents.
	ReferenceMode   string                  `json:"reference_mode" msgpack:"reference_mode"`
	GroupEvents     []groups.Event          `json:"group_events,omitempty" msgpack:"group_events,omitempty"`
	BridgeEvents    []bridges.Event         `json:"bridge_events,omitempty" msgpack:"bridge_events,omitempty"`
	BridgeEpisodes  []bridges.Episode       `json:"bridge_episodes,omitempty" msgpack:"bridge_episodes,omitempty"`
	Epochs          []bridges.Epoch         `json:"epochs,omitempty" msgpack:"epochs,omitempty"`
	Summaries       []bridges.Summary       `json:"summaries,omitempty" msgpack:"summaries,omitempty"`
	BranchEvents    []bridges.BranchEvent   `json:"branch_events,omitempty" msgpack:"branch_events,omitempty"`
	BranchEpisodes  []bridges.BranchEpisode `json:"branch_episodes,omitempty" msgpack:"branch_episodes,omitempty"`
	BranchSummaries []bridges.BranchSummary `json:"branch_summaries,omitempty" msgpack:"branch_summaries,omitempty"`
}

func validateScan(scan *ScanOptions, maximum int) error {
	if scan == nil {
		return nil
	}
	end := maximum
	if scan.End != nil {
		end = *scan.End
	}
	if scan.Start < 0 || end < scan.Start || end > maximum {
		return fmt.Errorf("scan must satisfy 0 <= start <= end <= source maximum %d", maximum)
	}
	if scan.Stride < 0 || scan.OutputStride < 0 {
		return errors.New("scan strides cannot be negative; zero selects defaults")
	}
	return nil
}

func scanSteps(scan *ScanOptions, maximum int) []int {
	end, stride := maximum, max(scan.Stride, 1)
	if scan.End != nil {
		end = *scan.End
	}
	steps := make([]int, 0, (end-scan.Start)/stride+1)
	for step := scan.Start; step <= end; {
		steps = append(steps, step)
		if end-step < stride {
			break
		}
		step += stride
	}
	return steps
}

func keepSnapshot(request *Request, index, count int) bool {
	return request.Scan == nil || index%max(request.Scan.OutputStride, 1) == 0 || index == count-1
}

func thinResults(request *Request, results []StateResult) []StateResult {
	if request.Scan == nil || request.Scan.OutputStride < 2 {
		return results
	}
	kept := make([]StateResult, 0, len(results)/request.Scan.OutputStride+2)
	for i, result := range results {
		if keepSnapshot(request, i, len(results)) {
			kept = append(kept, result)
		}
	}
	return kept
}

type analysisEvaluator struct {
	groups   *groups.Tracker
	bridges  *bridges.Observer
	result   AnalysisResult
	events   bool
	episodes bool
	fixed    []groups.Group
}

func newAnalysisEvaluator(request *Request, selected map[string]bool, epsilon float64) (*analysisEvaluator, error) {
	if !selected["groups"] && !selected["bridges"] {
		return nil, nil
	}
	if request.Epsilon != nil {
		epsilon = *request.Epsilon
	} else if request.Groups != nil {
		epsilon = request.Groups.Epsilon
	} else if selected["bridges"] && request.Bridges != nil {
		epsilon = request.Bridges.Epsilon
	}
	groupOptions := groups.DefaultOptions(epsilon)
	if request.Groups != nil {
		groupOptions = *request.Groups
	}
	tracker, err := groups.NewTracker(groupOptions)
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	e := &analysisEvaluator{groups: tracker, events: true, episodes: true,
		result: AnalysisResult{DefinitionVersion: 2, GroupOptions: groupOptions, ReferenceMode: "dynamic_lineage"}}
	if request.Scan != nil {
		if request.Scan.IncludeEvents != nil {
			e.events = *request.Scan.IncludeEvents
		}
		if request.Scan.IncludeEpisodes != nil {
			e.episodes = *request.Scan.IncludeEpisodes
		}
	}
	if selected["bridges"] {
		e.result.BranchLedgerVersion = bridges.BranchLedgerVersion
		options := bridges.DefaultOptions(epsilon)
		if request.Bridges != nil {
			options = *request.Bridges
		}
		// The recorded configuration belongs to this response. The observer
		// separately owns its copy; neither may alias the caller's slice.
		options.FixedPairs = append([][2]int64(nil), options.FixedPairs...)
		e.bridges, err = bridges.NewObserver(options)
		if err != nil {
			return nil, fmt.Errorf("bridges: %w", err)
		}
		e.result.BridgeOptions = &options
		if len(request.BridgeCores) > 0 {
			e.result.ReferenceMode = "fixed_cohorts"
			for i, members := range request.BridgeCores {
				e.fixed = append(e.fixed, groups.Group{ID: int64(i + 1), State: "observed", Core: groups.Core{ID: i, Members: append([]int(nil), members...), Mass: len(members)}})
			}
		}
	}
	e.result.EventsIncluded = e.events
	e.result.EpisodesIncluded = selected["bridges"] && e.episodes
	return e, nil
}

func (e *analysisEvaluator) collect(frame *bridges.Frame) {
	if e.events {
		e.result.BridgeEvents = append(e.result.BridgeEvents, frame.Events...)
		e.result.BranchEvents = append(e.result.BranchEvents, frame.BranchEvents...)
	}
	if e.episodes {
		e.result.BridgeEpisodes = append(e.result.BridgeEpisodes, frame.Episodes...)
		e.result.BranchEpisodes = append(e.result.BranchEpisodes, frame.BranchEpisodes...)
	}
	e.result.Epochs = append(e.result.Epochs, frame.Epochs...)
	e.result.Summaries = append(e.result.Summaries, frame.Summaries...)
	e.result.BranchSummaries = append(e.result.BranchSummaries, frame.BranchSummaries...)
	frame.Events, frame.Episodes, frame.Epochs, frame.Summaries = nil, nil, nil, nil
	frame.BranchEvents, frame.BranchEpisodes, frame.BranchSummaries = nil, nil, nil
}

func (e *analysisEvaluator) measure(state *FrozenState, result *StateResult) error {
	frame, err := e.groups.Observe(state.Step, state.Opinions)
	if err != nil {
		return err
	}
	if e.result.Observations == 0 {
		e.result.FirstStep = state.Step
	}
	e.result.Observations++
	e.result.LastStep = state.Step
	e.result.Sampled = e.result.Sampled || frame.Gap
	if state.PostsUnavailableReason != "" {
		e.result.PostsUnavailableReason = state.PostsUnavailableReason
	}
	if e.events {
		e.result.GroupEvents = append(e.result.GroupEvents, frame.Events...)
	}
	if e.bridges != nil {
		reference := frame
		if len(e.fixed) > 0 {
			reference = groups.Frame{Step: state.Step, Groups: e.fixed, Gap: frame.Gap}
		}
		value, err := e.bridges.Measure(state.Step, state.Opinions, state.Graph, state.Posts, reference)
		if err != nil {
			return err
		}
		e.collect(&value)
		result.Bridges = &value
	}
	frame.Events = nil
	result.Groups = &frame
	return nil
}

func (e *analysisEvaluator) finish(request *Request) error {
	if e == nil || e.result.Observations == 0 {
		return nil
	}
	reason := "record_end"
	if request.sourceMaxStep != nil && e.result.LastStep < *request.sourceMaxStep {
		reason = "analysis_horizon"
	}
	closed, err := e.groups.FinalClose(e.result.LastStep, reason)
	if err != nil {
		return err
	}
	if e.events {
		e.result.GroupEvents = append(e.result.GroupEvents, closed.Events...)
	}
	if e.bridges != nil {
		closed, err := e.bridges.FinalClose(e.result.LastStep, reason)
		if err != nil {
			return err
		}
		e.collect(&closed)
	}
	request.analysis = &e.result
	return nil
}
