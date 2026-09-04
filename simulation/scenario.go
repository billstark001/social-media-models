package simulation

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"smp/dynamics"
	"smp/model"
	"smp/progress"
	smprng "smp/rng"
	"smp/utils"
	"time"

	"github.com/schollz/progressbar/v3"
	"github.com/vmihailenco/msgpack/v5"
)

type Scenario struct {
	BaseDir    string
	Metadata   *ScenarioMetadata
	Model      IModel
	AccState   *AccumulativeModelState
	Serializer *SimulationSerializer
	DB         *EventDB
	RNG        *smprng.Pool

	OutputJSONProgress   bool
	EnableDumps          bool
	Quiet                bool
	ProgressStepInterval int
	ProgressCallback     func(ScenarioProgress)
}

type ScenarioProgress struct {
	Step    int
	MaxStep int
}

type RunResult struct {
	Step       int
	StopReason string
	Completed  bool
}

// ScenarioOptions controls runner behavior that is not part of the simulated
// model. The zero value is useful for lightweight tests.
type ScenarioOptions struct {
	OutputJSONProgress   bool
	EnableDumps          bool
	Quiet                bool
	ProgressStepInterval int
	ProgressCallback     func(ScenarioProgress)
}

// NewScenario preserves the historical constructor behavior, including dumps.
func NewScenario(dir string, metadata *ScenarioMetadata, outputJSONProgress bool) *Scenario {
	return NewScenarioWithOptions(dir, metadata, ScenarioOptions{
		OutputJSONProgress: outputJSONProgress,
		EnableDumps:        true,
	})
}

// NewScenarioWithOptions constructs a scenario with explicit runner options.
func NewScenarioWithOptions(dir string, metadata *ScenarioMetadata, options ScenarioOptions) *Scenario {
	scenario := &Scenario{
		BaseDir:              dir,
		Metadata:             metadata,
		OutputJSONProgress:   options.OutputJSONProgress,
		EnableDumps:          options.EnableDumps,
		Quiet:                options.Quiet,
		ProgressStepInterval: options.ProgressStepInterval,
		ProgressCallback:     options.ProgressCallback,
	}
	if options.EnableDumps {
		scenario.Serializer = NewSimulationSerializer(dir, metadata.UniqueName, 2)
	}
	return scenario
}

const MAX_POST_EVENT_INTERVAL = 500
const DB_CACHE_SIZE = 40000

func (s *Scenario) Init() {
	if err := s.InitError(); err != nil {
		log.Fatalf("Failed to initialize scenario: %v", err)
	}
}

// InitError initializes a scenario and returns recoverable configuration or
// persistence errors. With EnableDumps false it performs no filesystem I/O and
// does not allocate an accumulative history.
func (s *Scenario) InitError() error {
	if err := s.Metadata.PrepareForNewRun(); err != nil {
		return fmt.Errorf("resolve scenario RNG: %w", err)
	}
	if err := s.Metadata.Validate(); err != nil {
		return fmt.Errorf("invalid scenario metadata: %w", err)
	}
	rngPool, err := smprng.NewPool(s.Metadata.RNG)
	if err != nil {
		return fmt.Errorf("initialize scenario RNG: %w", err)
	}
	s.RNG = rngPool
	if s.EnableDumps {
		if s.Serializer == nil {
			return fmt.Errorf("persistence is enabled without a serializer")
		}
		if err := s.Serializer.SaveMetadata(s.Metadata); err != nil {
			return fmt.Errorf("persist resolved metadata: %w", err)
		}
	}

	var eventLogger func(*model.EventRecord)
	if s.EnableDumps {
		eventLogger = s.logEvent
	}

	nodeCount := max(s.Metadata.NodeCount, 1)
	edgeCount := max(s.Metadata.NodeFollowCount, 1)
	graph := utils.CreateRandomNetwork(
		nodeCount,
		float64(edgeCount)/(float64(nodeCount)-1),
		s.RNG.Stream(smprng.StreamNetwork),
	)

	switch s.Metadata.DynamicsType {
	case "", DynamicsTypeHK:
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.HKParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		m := model.NewSMPModelFloat64(graph, nil, &params, &s.Metadata.HKParams, &dynamics.HK{}, &s.Metadata.CollectItemOptions, eventLogger, s.RNG)
		s.Model = &Float64ModelWrapper[dynamics.HKParams]{M: m}
	case DynamicsTypeDeffuant:
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.DeffuantParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		m := model.NewSMPModelFloat64(graph, nil, &params, &s.Metadata.DeffuantParams, &dynamics.Deffuant{}, &s.Metadata.CollectItemOptions, eventLogger, s.RNG)
		s.Model = &Float64ModelWrapper[dynamics.DeffuantParams]{M: m}
	case DynamicsTypeGalam:
		factories := GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.GalamParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		n := graph.Nodes().Len()
		ops := make([]bool, n)
		opinionRNG := s.RNG.Stream(smprng.StreamOpinion)
		for i := range ops {
			ops[i] = opinionRNG.IntN(2) == 1
		}
		m := model.NewSMPModel(graph, &ops, &params, &s.Metadata.GalamParams, &dynamics.Galam{}, &s.Metadata.CollectItemOptions, eventLogger, s.RNG)
		s.Model = &BoolModelWrapper[dynamics.GalamParams]{M: m}
	case DynamicsTypeVoter:
		factories := GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.VoterParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		n := graph.Nodes().Len()
		ops := make([]bool, n)
		opinionRNG := s.RNG.Stream(smprng.StreamOpinion)
		for i := range ops {
			ops[i] = opinionRNG.IntN(2) == 1
		}
		m := model.NewSMPModel(graph, &ops, &params, &s.Metadata.VoterParams, &dynamics.Voter{}, &s.Metadata.CollectItemOptions, eventLogger, s.RNG)
		s.Model = &BoolModelWrapper[dynamics.VoterParams]{M: m}
	default:
		return fmt.Errorf("unknown DynamicsType: %q", s.Metadata.DynamicsType)
	}

	s.Model.InitPosts()

	if s.EnableDumps {
		err = os.MkdirAll(
			filepath.Join(s.BaseDir, s.Metadata.UniqueName),
			0755,
		)
		if err != nil {
			return fmt.Errorf("create scenario dump folder: %w", err)
		}

		s.AccState = NewAccumulativeModelState()
		db, openErr := OpenEventDB(
			filepath.Join(s.BaseDir, s.Metadata.UniqueName, "events.db"),
			DB_CACHE_SIZE,
		)
		if openErr != nil {
			return fmt.Errorf("create event db logger: %w", openErr)
		}
		s.DB = db
		s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.GetGraph()), s.Model.GetCurStep())
		s.Model.Accumulate(s.AccState)
	}
	s.Model.SetCurStep(1)

	if s.EnableDumps {
		s.sanitize()
	}
	return nil
}

func (s *Scenario) Load() bool {
	if !s.EnableDumps || s.Serializer == nil {
		return false
	}
	storedMetadata, err := s.Serializer.LoadMetadata()
	if err != nil {
		log.Printf("Failed to load resolved metadata: %v", err)
		return false
	}
	if storedMetadata != nil {
		s.Metadata.DataVersion = storedMetadata.DataVersion
		s.Metadata.RNG = storedMetadata.RNG
	}
	if err := s.Metadata.Validate(); err != nil {
		log.Fatalf("Invalid scenario metadata: %v", err)
	}

	dbPath := filepath.Join(s.BaseDir, s.Metadata.UniqueName, "events.db")
	_, err = os.Stat(dbPath)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		log.Printf("Failed to inspect event db: %v", err)
		return false
	}

	if s.Metadata.RNG.IsZero() {
		resolved, resolveErr := smprng.Resolve(s.Metadata.RNG)
		if resolveErr != nil {
			log.Printf("Failed to create compatibility RNG for legacy data: %v", resolveErr)
			return false
		}
		s.Metadata.RNG = resolved
		log.Printf("Loading legacy DataVersion 0 without reproducible RNG state")
	}
	rngPool, err := smprng.NewPool(s.Metadata.RNG)
	if err != nil {
		log.Printf("Failed to initialize scenario RNG: %v", err)
		return false
	}
	s.RNG = rngPool

	db, err := OpenEventDB(dbPath, DB_CACHE_SIZE)
	if err != nil {
		log.Printf("Failed to create event db logger: %v", err)
		return false
	}

	s.DB = db

	rawSnapshot, err := s.Serializer.GetLatestRawSnapshot()
	if err != nil {
		log.Printf("Failed to load model dump: %v", err)
		return false
	}

	if rawSnapshot == nil {
		return false
	}

	// Decode the raw snapshot into the concrete type matching DynamicsType.
	// The DynamicsType stored in the snapshot is authoritative; the metadata field
	// is used as a fallback when the snapshot was saved before this field existed.
	dynamicsType := rawSnapshot.DynamicsType
	if dynamicsType == "" {
		dynamicsType = s.Metadata.DynamicsType
	}

	switch dynamicsType {
	case "", DynamicsTypeHK:
		var dump model.SMPModelDumpData[float64, dynamics.HKParams]
		if err := msgpack.Unmarshal(rawSnapshot.Data, &dump); err != nil {
			log.Printf("Failed to unmarshal HK snapshot: %v", err)
			return false
		}
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.HKParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		s.Model = &Float64ModelWrapper[dynamics.HKParams]{
			M: dump.Load(&params, &s.Metadata.HKParams, &dynamics.HK{}, &s.Metadata.CollectItemOptions, s.logEvent, s.RNG),
		}
	case DynamicsTypeDeffuant:
		var dump model.SMPModelDumpData[float64, dynamics.DeffuantParams]
		if err := msgpack.Unmarshal(rawSnapshot.Data, &dump); err != nil {
			log.Printf("Failed to unmarshal Deffuant snapshot: %v", err)
			return false
		}
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.DeffuantParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		s.Model = &Float64ModelWrapper[dynamics.DeffuantParams]{
			M: dump.Load(&params, &s.Metadata.DeffuantParams, &dynamics.Deffuant{}, &s.Metadata.CollectItemOptions, s.logEvent, s.RNG),
		}
	case DynamicsTypeGalam:
		var dump model.SMPModelDumpData[bool, dynamics.GalamParams]
		if err := msgpack.Unmarshal(rawSnapshot.Data, &dump); err != nil {
			log.Printf("Failed to unmarshal Galam snapshot: %v", err)
			return false
		}
		factories := GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.GalamParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		s.Model = &BoolModelWrapper[dynamics.GalamParams]{
			M: dump.Load(&params, &s.Metadata.GalamParams, &dynamics.Galam{}, &s.Metadata.CollectItemOptions, s.logEvent, s.RNG),
		}
	case DynamicsTypeVoter:
		var dump model.SMPModelDumpData[bool, dynamics.VoterParams]
		if err := msgpack.Unmarshal(rawSnapshot.Data, &dump); err != nil {
			log.Printf("Failed to unmarshal Voter snapshot: %v", err)
			return false
		}
		factories := GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.VoterParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		s.Model = &BoolModelWrapper[dynamics.VoterParams]{
			M: dump.Load(&params, &s.Metadata.VoterParams, &dynamics.Voter{}, &s.Metadata.CollectItemOptions, s.logEvent, s.RNG),
		}
	default:
		log.Printf("Unknown DynamicsType in snapshot: %q", dynamicsType)
		return false
	}

	acc, err := s.Serializer.GetLatestAccumulativeState()
	if err != nil {
		log.Printf("Failed to load accumulative state: %v", err)
		return false
	} else {
		validated := s.Model.ValidateAcc(acc)
		if !validated {
			log.Printf("Accumulative state validation failed")
			return false
		}
	}

	s.AccState = acc

	s.sanitize()

	return true
}

func (s *Scenario) sanitize() {
	if !s.EnableDumps || s.DB == nil || s.Serializer == nil {
		return
	}
	s.DB.DeleteEventsAfterStep(s.Model.GetCurStep())
	s.Serializer.DeleteGraphsAfterStep(s.Model.GetCurStep(), false)
}

func (s *Scenario) Dump() {
	if !s.EnableDumps || s.DB == nil || s.Serializer == nil || s.AccState == nil {
		return
	}
	s.DB.Flush()
	data, err := s.Model.RawDump()
	if err != nil {
		log.Printf("Failed to serialize model snapshot: %v", err)
	} else {
		s.Serializer.SaveRawSnapshot(s.Metadata.DynamicsType, data)
	}
	s.Serializer.SaveAccumulativeState(s.AccState)
}

func (s *Scenario) Step() (int, float64) {
	changedCount, maxOpinionChange := s.Model.StepModel()

	if s.EnableDumps && s.AccState != nil {
		s.Model.Accumulate(s.AccState)
		s.AccState.UnsafePostEvent += changedCount
		if s.AccState.UnsafePostEvent > MAX_POST_EVENT_INTERVAL {
			s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.GetGraph()), s.Model.GetCurStep())
			s.AccState.UnsafePostEvent = 0
		}
	}

	s.Model.SetCurStep(s.Model.GetCurStep() + 1)

	return changedCount, maxOpinionChange
}

func (s *Scenario) IsFinished() bool {
	if !s.EnableDumps || s.Serializer == nil {
		return false
	}
	finished, _ := s.Serializer.IsFinished()
	return finished
}

const NETWORK_CHANGE_THRESHOLD = 1
const OPINION_CHANGE_THRESHOLD = 1e-7
const STOP_SIM_STEPS = 60
const SAVE_INTERVAL = 300 // seconds

func (s *Scenario) StepTillEnd(ctx context.Context) {
	_ = s.StepTillEndResult(ctx)
}

// StepTillEndResult advances the scenario and returns a compact in-memory run
// summary. Persistence is controlled solely by EnableDumps.
func (s *Scenario) StepTillEndResult(ctx context.Context) RunResult {
	maxSimCount := s.Metadata.MaxSimulationStep
	if maxSimCount < 0 {
		maxSimCount = 1
	}

	if s.IsFinished() {
		return RunResult{
			Step:       s.Model.GetCurStep() - 1,
			StopReason: "already_finished",
			Completed:  true,
		}
	}

	var bar *progressbar.ProgressBar
	lastPrintTime := time.Now()
	if s.OutputJSONProgress {
		_ = progress.WriteJSONL(os.Stdout, progress.Event{
			RequestID: s.Metadata.UniqueName,
			Type:      progress.TypeRNG,
			Algorithm: s.Metadata.RNG.Algorithm,
			Seed1:     s.Metadata.RNG.Seed1,
			Seed2:     s.Metadata.RNG.Seed2,
		})
		_ = progress.WriteJSONL(os.Stdout, progress.Event{
			RequestID: s.Metadata.UniqueName,
			Type:      progress.TypeStart,
			Step:      s.Model.GetCurStep(),
			MaxStep:   maxSimCount,
		})
	} else if !s.Quiet {
		log.Printf(
			"RNG algorithm=%s seed1=%s seed2=%s",
			s.Metadata.RNG.Algorithm,
			s.Metadata.RNG.Seed1,
			s.Metadata.RNG.Seed2,
		)
		bar = progressbar.Default(int64(maxSimCount))
		bar.Set(s.Model.GetCurStep())
	}

	lastSaveTime := time.Now()
	successiveThresholdMet := 0

	unitStep := func() (bool, bool) {

		didDump := false

		if s.OutputJSONProgress {
			if time.Since(lastPrintTime).Milliseconds() > 250 {
				_ = progress.WriteJSONL(os.Stdout, progress.Event{
					RequestID: s.Metadata.UniqueName,
					Type:      progress.TypeProgress,
					Step:      s.Model.GetCurStep(),
					MaxStep:   maxSimCount,
				})
				lastPrintTime = time.Now()
			}
		} else if bar != nil {
			bar.Set(s.Model.GetCurStep())
		}

		nwChange, opChange := s.Step()
		completedStep := s.Model.GetCurStep() - 1
		if s.ProgressCallback != nil && s.ProgressStepInterval > 0 &&
			completedStep%s.ProgressStepInterval == 0 {
			s.ProgressCallback(ScenarioProgress{
				Step:    completedStep,
				MaxStep: maxSimCount,
			})
		}

		thresholdMet := nwChange < NETWORK_CHANGE_THRESHOLD &&
			opChange < OPINION_CHANGE_THRESHOLD
		if thresholdMet {
			successiveThresholdMet++
		} else {
			successiveThresholdMet = 0
		}
		if successiveThresholdMet > STOP_SIM_STEPS {
			return false, didDump
		}

		timeInterval := time.Since(lastSaveTime)
		if s.EnableDumps && timeInterval.Seconds() >= SAVE_INTERVAL {
			lastSaveTime = time.Now()
			s.Dump()
			didDump = true
		}

		return true, didDump

	}

	isCtxDone := false
	isShouldNotContinue := false
	didDump := false

iterLoop:
	for s.Model.GetCurStep() <= maxSimCount {
		select {
		case <-ctx.Done():
			isCtxDone = true
			break iterLoop

		default:
			didDump = false
			shouldContinue, _didDump := unitStep()
			didDump = _didDump

			if shouldContinue {
			} else {
				isShouldNotContinue = true
				break iterLoop
			}
		}
	}

	if !s.OutputJSONProgress && !s.Quiet && s.Model.GetCurStep() <= maxSimCount {
		fmt.Println("")
	}

	if s.EnableDumps && !didDump {
		s.Dump()
	}

	st := s.Model.GetCurStep() - 1
	stopReason := "horizon"

	if isCtxDone {
		stopReason = "cancelled"
		if s.OutputJSONProgress {
			_ = progress.WriteJSONL(os.Stdout, progress.Event{
				RequestID:  s.Metadata.UniqueName,
				Type:       progress.TypeDone,
				Step:       st,
				MaxStep:    maxSimCount,
				StopReason: stopReason,
			})
		} else if !s.Quiet {
			log.Printf("Simulation ended (`ctx.Done()` received, step: %d)", st)
		}
	} else {
		if !isShouldNotContinue {
			if s.OutputJSONProgress {
				_ = progress.WriteJSONL(os.Stdout, progress.Event{
					RequestID:  s.Metadata.UniqueName,
					Type:       progress.TypeDone,
					Step:       st,
					MaxStep:    maxSimCount,
					StopReason: stopReason,
				})
			} else if !s.Quiet {
				log.Printf("Simulation ended (max iteration reached, step: %d)", st)
			}
		} else {
			stopReason = "halt"
			if s.OutputJSONProgress {
				_ = progress.WriteJSONL(os.Stdout, progress.Event{
					RequestID:  s.Metadata.UniqueName,
					Type:       progress.TypeDone,
					Step:       st,
					MaxStep:    maxSimCount,
					StopReason: stopReason,
				})
			} else if !s.Quiet {
				log.Printf("Simulation ended (shouldContinue == false, step: %d)", st)
			}
		}
		if s.EnableDumps && s.Serializer != nil {
			s.Serializer.MarkFinished(FinishMark{
				DataVersion: s.Metadata.DataVersion,
				RNG:         s.Metadata.RNG,
			})
			s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.GetGraph()), st)
		}
	}
	return RunResult{
		Step:       st,
		StopReason: stopReason,
		Completed:  !isCtxDone,
	}
}

func (s *Scenario) logEvent(event *model.EventRecord) {
	if s.DB == nil {
		return
	}

	switch event.Type {

	case "Post":
		var isRepost bool
		switch body := event.Body.(type) {
		case model.PostEventBody[float64]:
			isRepost = body.IsRepost
		case model.PostEventBody[bool]:
			isRepost = body.IsRepost
		}
		if isRepost && s.Metadata.CollectItemOptions.PostEvent {
			s.DB.StoreEvent(event)
		}

	case "Rewiring":
		if s.Metadata.CollectItemOptions.RewiringEvent {
			s.DB.StoreEvent(event)
		}

	case "ViewPosts":
		if s.Metadata.CollectItemOptions.ViewPostsEvent {
			s.DB.StoreEvent(event)
		}

	}

}
