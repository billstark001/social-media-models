package simulation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"smp/dynamics"
	"smp/model"
	"smp/observables"
	smprng "smp/rng"
	"smp/terminal"
)

// ResearchOptions configures canonical online observations and optional policy
// interventions. Observation callbacks consume no random values.
type ResearchOptions struct {
	ObserveAt     []int         `json:"observe_at"`
	PolicyChange  *PolicyChange `json:"policy_change,omitempty"`
	ExposureTrace string        `json:"exposure_trace,omitempty"`
	TraceStart    int           `json:"trace_start,omitempty"`
}

type ResearchEndpoint struct {
	Step         int                      `json:"step"`
	RelativeStep int                      `json:"relative_step"`
	Pathway      observables.PathwayState `json:"pathway"`
	Exposure     Exposure                 `json:"exposure"`
}

// ResearchState is saved together with the microscopic snapshot so a resumed
// observation window includes precisely the updates retained by the trajectory.
type ResearchState struct {
	Options        ResearchOptions    `json:"options"`
	Endpoints      []ResearchEndpoint `json:"endpoints"`
	Window         Exposure           `json:"window"`
	SwitchStep     *int               `json:"switch_step,omitempty"`
	SwitchFallback bool               `json:"switch_fallback,omitempty"`
	Policy         string             `json:"policy"`
	PolicyEnded    bool               `json:"policy_ended,omitempty"`
	Trace          TracePosition      `json:"trace"`
	FixedHorizon   bool               `json:"fixed_horizon"`
}

type ResearchResult struct {
	Index          int                `json:"index"`
	RNG            smprng.Spec        `json:"rng"`
	Endpoints      []ResearchEndpoint `json:"endpoints"`
	Terminal       *terminal.Result   `json:"terminal"`
	StopReason     string             `json:"stop_reason"`
	StoppedAt      int                `json:"stopped_at"`
	SwitchStep     *int               `json:"switch_step,omitempty"`
	SwitchFallback bool               `json:"switch_fallback,omitempty"`
	FinalOpinions  []float64          `json:"final_opinions"`
	ExposureTrace  *TraceReceipt      `json:"exposure_trace,omitempty"`
}

type researchObserver struct {
	state    ResearchState
	measure  observables.PathwayMeasurer
	opinions []float64
	trace    *FeedTrace
}

func validateResearch(options *ResearchOptions, metadata *ScenarioMetadata) error {
	if options == nil {
		return nil
	}
	if metadata.DynamicsType != DynamicsTypeHK {
		return fmt.Errorf("research observations require HK dynamics")
	}
	if len(options.ObserveAt) == 0 || options.ObserveAt[0] < 0 {
		return fmt.Errorf("research observe_at needs a non-negative first step")
	}
	previous := -1
	for _, step := range options.ObserveAt {
		if step <= previous || step > metadata.MaxSimulationStep {
			return fmt.Errorf("research observe_at must increase within the simulation horizon")
		}
		previous = step
	}
	if previous != metadata.MaxSimulationStep {
		return fmt.Errorf("research observe_at must include the simulation horizon")
	}
	if options.ExposureTrace != "" && (!strings.HasSuffix(options.ExposureTrace, ".jsonl.gz") || options.TraceStart < 0 || options.TraceStart >= metadata.MaxSimulationStep) {
		return fmt.Errorf("exposure_trace needs a .jsonl.gz path and trace_start within the horizon")
	}
	return options.PolicyChange.Validate(metadata, metadata.MaxSimulationStep)
}

func (s *Scenario) attachResearch(saved *ResearchState) error {
	if s.ResearchOptions == nil {
		if saved != nil {
			return fmt.Errorf("snapshot research options are missing from this run")
		}
		return nil
	}
	if err := validateResearch(s.ResearchOptions, s.Metadata); err != nil {
		return err
	}
	state := ResearchState{Options: *s.ResearchOptions, Policy: s.Metadata.RecsysFactoryType, FixedHorizon: s.FixedHorizon}
	if saved != nil {
		if !reflect.DeepEqual(saved.Options, *s.ResearchOptions) || saved.FixedHorizon != s.FixedHorizon {
			return fmt.Errorf("research options differ from the saved run")
		}
		state = *saved
	}
	s.research = &researchObserver{state: state, opinions: make([]float64, s.Metadata.NodeCount)}
	m := s.Model.(*Float64ModelWrapper[dynamics.HKParams]).M
	var record func(int, int64, *model.FeedDetails[float64])
	if state.Options.ExposureTrace != "" {
		var err error
		s.research.trace, err = OpenFeedTrace(state.Options.ExposureTrace, state.Trace)
		if err != nil {
			return err
		}
		record = func(step int, recipient int64, feed *model.FeedDetails[float64]) {
			if step > state.Options.TraceStart {
				s.research.trace.Record(step, recipient, feed)
			}
		}
	}
	ObserveHKFeed(m, &s.research.state.Window, record)
	if saved != nil && saved.SwitchStep != nil && !saved.PolicyEnded {
		change := s.ResearchOptions.PolicyChange
		if change == nil || saved.Policy != change.Recommender {
			return fmt.Errorf("snapshot has inconsistent active policy")
		}
		return SetHKPolicy(m, s.Metadata, saved.Policy, saved.Policy, change.Bridge, change.HideReposts)
	}
	if saved == nil {
		return s.observeResearch()
	}
	return nil
}

func (s *Scenario) prepareResearchStep() error {
	if s.research == nil {
		return nil
	}
	m := s.Model.(*Float64ModelWrapper[dynamics.HKParams]).M
	m.CaptureFeedDetails = s.research.trace != nil && m.CurStep > s.ResearchOptions.TraceStart
	if s.ResearchOptions.PolicyChange == nil {
		return nil
	}
	r := s.research
	change := s.ResearchOptions.PolicyChange
	step := s.Model.GetCurStep() - 1
	if r.state.SwitchStep == nil {
		trigger := change.At != nil && step == *change.At
		if change.ExcessAtLeast != nil {
			for _, agent := range m.Schedule.Agents {
				r.opinions[agent.ID] = agent.CurOpinion
			}
			hit, err := r.measure.ExcessAtLeast(r.opinions, m.Graph, m.AgentParams.Tolerance, *change.ExcessAtLeast)
			if err != nil {
				return err
			}
			trigger = hit
			if !trigger && step == change.Latest {
				trigger, r.state.SwitchFallback = true, true
			}
		}
		if trigger {
			r.state.SwitchStep = &step
			if err := SetHKPolicy(m, s.Metadata, r.state.Policy, change.Recommender, change.Bridge, change.HideReposts); err != nil {
				return err
			}
			r.state.Policy = change.Recommender
		}
	} else if !r.state.PolicyEnded && change.Duration > 0 && step == *r.state.SwitchStep+change.Duration {
		if err := SetHKPolicy(m, s.Metadata, r.state.Policy, s.Metadata.RecsysFactoryType, false, false); err != nil {
			return err
		}
		r.state.Policy, r.state.PolicyEnded = s.Metadata.RecsysFactoryType, true
	}
	return nil
}

func (s *Scenario) observeResearch() error {
	if s.research == nil {
		return nil
	}
	r := s.research
	step := s.Model.GetCurStep() - 1
	i := sort.SearchInts(r.state.Options.ObserveAt, step)
	if i == len(r.state.Options.ObserveAt) || r.state.Options.ObserveAt[i] != step {
		return nil
	}
	if len(r.state.Endpoints) != i {
		return fmt.Errorf("research observation sequence is incomplete at step %d", step)
	}
	state, err := r.measure.Measure(s.Model.GetOpinions(), s.Model.GetGraph(), s.Metadata.HKParams.Tolerance)
	if err != nil {
		return err
	}
	if i == 0 {
		r.state.Window = Exposure{}
	}
	r.state.Endpoints = append(r.state.Endpoints, ResearchEndpoint{step, step - r.state.Options.ObserveAt[0], state, r.state.Window})
	r.state.Window = Exposure{}
	return nil
}

func (s *Scenario) researchSnapshot() (*ResearchState, error) {
	if s.research == nil {
		return nil, nil
	}
	if s.research.trace != nil {
		var err error
		s.research.state.Trace, err = s.research.trace.Checkpoint()
		if err != nil {
			return nil, err
		}
	}
	// Snapshot cadence is coarse; clone persisted observer state once per dump,
	// rather than making every update own another copy of the observation history.
	data, err := json.Marshal(s.research.state)
	if err != nil {
		return nil, err
	}
	var state ResearchState
	err = json.Unmarshal(data, &state)
	return &state, err
}

func (s *Scenario) ResearchResult(run RunResult) (*ResearchResult, error) {
	if s.research == nil {
		return nil, fmt.Errorf("research observations were not configured")
	}
	opinions := s.Model.GetOpinions()
	var measure observables.StateMeasurer
	state, err := measure.Measure(opinions, nil, observables.StateOptions{Terminal: &terminal.Options{
		Epsilon: s.Metadata.HKParams.Tolerance, MajorMass: 0.02,
	}})
	if err != nil {
		return nil, err
	}
	r := &s.research.state
	var trace *TraceReceipt
	if s.research.trace != nil && run.Completed {
		trace, err = s.research.trace.Finish()
		if err != nil {
			return nil, err
		}
	}
	return &ResearchResult{ExposureTrace: trace, Index: 0, RNG: s.Metadata.RNG, Endpoints: r.Endpoints, Terminal: state.Terminal,
		StopReason: run.StopReason, StoppedAt: run.Step, SwitchStep: r.SwitchStep,
		SwitchFallback: r.SwitchFallback, FinalOpinions: opinions}, nil
}
