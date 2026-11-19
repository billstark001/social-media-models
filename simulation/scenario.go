package simulation

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"smp/model"
	"smp/progress"
	smprng "smp/rng"
	"smp/trajectory"
	"smp/utils"
	"time"

	"github.com/schollz/progressbar/v3"
)

type Scenario struct {
	BaseDir         string
	Metadata        *ScenarioMetadata
	Model           IModel
	AccState        *AccumulativeModelState
	Trajectory      *trajectory.Writer
	Serializer      *SimulationSerializer
	DB              *EventDB
	RNG             *smprng.Pool
	StableSteps     int
	FixedHorizon    bool
	ResearchOptions *ResearchOptions
	research        *researchObserver

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
	FixedHorizon         bool
	Research             *ResearchOptions
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
		FixedHorizon:         options.FixedHorizon,
		ResearchOptions:      options.Research,
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
	return s.InitFromState(nil)
}

// InitFromState shares standard scenario initialization while accepting an
// explicit microscopic graph/opinions. Persistence and step numbering are
// identical to InitError; with dumps disabled it performs no filesystem I/O.
func (s *Scenario) InitFromState(initial *InitialState) error {
	if s.Metadata == nil {
		return fmt.Errorf("metadata is nil")
	}
	if s.ResearchOptions != nil {
		if s.Metadata.DataVersion < 0 || s.Metadata.DataVersion > CurrentDataVersion {
			return fmt.Errorf("unsupported research DataVersion %d", s.Metadata.DataVersion)
		}
		s.Metadata.DataVersion = CurrentDataVersion
	}
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

	if err := validateResearch(s.ResearchOptions, s.Metadata); err != nil {
		return err
	}
	nodeCount := s.Metadata.NodeCount
	s.Model, err = NewModel(s.Metadata, initial, s.RNG, eventLogger)
	if err != nil {
		return fmt.Errorf("initialize model: %w", err)
	}

	if s.EnableDumps {
		err = os.MkdirAll(
			filepath.Join(s.BaseDir, s.Metadata.UniqueName),
			0755,
		)
		if err != nil {
			return fmt.Errorf("create scenario dump folder: %w", err)
		}

		trajectory, trajectoryErr := trajectory.Open(filepath.Join(s.BaseDir, s.Metadata.UniqueName), nodeCount, s.Metadata.TrajectoryPrecision)
		if trajectoryErr != nil {
			return fmt.Errorf("open trajectory: %w", trajectoryErr)
		}
		if trajectory.Manifest.NextStep != 0 {
			return fmt.Errorf("existing trajectory has %d steps but no restorable model snapshot", trajectory.Manifest.NextStep)
		}
		s.Trajectory = trajectory
		s.AccState = NewTrajectoryAccumulativeState(nodeCount, s.Metadata.TrajectoryPrecision)
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
	if err := s.attachResearch(nil); err != nil {
		return err
	}
	if s.EnableDumps && s.checkpointRequested(0) {
		if err := s.SaveResearchCheckpoint(); err != nil {
			return fmt.Errorf("save step-zero checkpoint: %w", err)
		}
	}

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
		if storedMetadata.TrajectoryPrecision != (trajectory.Precision{}) {
			s.Metadata.TrajectoryPrecision = storedMetadata.TrajectoryPrecision
		}
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

	restoreMetadata := *s.Metadata
	if rawSnapshot.Research != nil {
		restoreMetadata.RecsysFactoryType = rawSnapshot.Research.Policy
	}
	physicalSnapshot := *rawSnapshot
	physicalSnapshot.Research = nil
	loadedModel, restoreErr := RestoreRawModel(&physicalSnapshot, &restoreMetadata, s.RNG, s.logEvent, false)
	if restoreErr != nil {
		log.Printf("Failed to restore model snapshot: %v", restoreErr)
		return false
	}
	s.Model = loadedModel
	s.StableSteps = rawSnapshot.StableSteps
	if err := s.attachResearch(rawSnapshot.Research); err != nil {
		log.Printf("Failed to restore research state: %v", err)
		return false
	}

	trajectoryPath := filepath.Join(s.BaseDir, s.Metadata.UniqueName, "trajectory.json")
	if _, statErr := os.Stat(trajectoryPath); statErr == nil {
		trajectory, openErr := trajectory.Open(filepath.Join(s.BaseDir, s.Metadata.UniqueName), s.Metadata.NodeCount, s.Metadata.TrajectoryPrecision)
		if openErr != nil {
			log.Printf("Failed to open trajectory: %v", openErr)
			return false
		}
		if verifyErr := trajectory.VerifyIndex(); verifyErr != nil {
			log.Printf("Invalid trajectory: %v", verifyErr)
			return false
		}
		if truncateErr := trajectory.Truncate(s.Model.GetCurStep()); truncateErr != nil {
			log.Printf("Failed to align trajectory: %v", truncateErr)
			return false
		}
		s.Trajectory = trajectory
		s.AccState = NewTrajectoryAccumulativeState(s.Metadata.NodeCount, s.Metadata.TrajectoryPrecision)
	} else {
		acc, loadErr := s.Serializer.GetLatestAccumulativeState()
		if loadErr != nil || acc == nil || !s.Model.ValidateAcc(acc) {
			log.Printf("Failed to load or validate legacy accumulative state: %v", loadErr)
			return false
		}
		s.AccState = acc
	}

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
	if err := s.DumpError(); err != nil {
		log.Printf("Failed to save scenario: %v", err)
	}
}

// DumpError commits the event log, trajectory and recovery snapshot in that
// order. A failed snapshot leaves the previous recovery point usable.
func (s *Scenario) DumpError() error {
	if !s.EnableDumps {
		return nil
	}
	if s.DB == nil || s.Serializer == nil || s.AccState == nil {
		return fmt.Errorf("scenario persistence is not initialized")
	}
	if err := s.DB.Flush(); err != nil {
		return fmt.Errorf("flush events: %w", err)
	}
	if s.Trajectory != nil {
		if err := s.Trajectory.Flush(s.AccState.Packed); err != nil {
			return fmt.Errorf("flush trajectory: %w", err)
		}
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		return fmt.Errorf("serialize model snapshot: %w", err)
	}
	if err := s.Serializer.SaveRawSnapshot(snapshot); err != nil {
		return fmt.Errorf("save model snapshot: %w", err)
	}
	if s.Trajectory == nil {
		return s.Serializer.SaveAccumulativeState(s.AccState)
	}
	return nil
}

func (s *Scenario) Step() (int, float64) {
	if err := s.prepareResearchStep(); err != nil {
		panic(err)
	}
	changedCount, maxOpinionChange := s.Model.StepModel()
	if changedCount < NETWORK_CHANGE_THRESHOLD && maxOpinionChange < OPINION_CHANGE_THRESHOLD {
		s.StableSteps++
	} else {
		s.StableSteps = 0
	}

	if s.EnableDumps && s.AccState != nil {
		s.Model.Accumulate(s.AccState)
		if s.Trajectory != nil && s.AccState.Len() >= trajectory.ChunkSteps {
			if err := s.Trajectory.Flush(s.AccState.Packed); err != nil {
				panic(fmt.Errorf("flush trajectory: %w", err))
			}
		}
		s.AccState.UnsafePostEvent += changedCount
		if s.AccState.UnsafePostEvent > MAX_POST_EVENT_INTERVAL {
			s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.GetGraph()), s.Model.GetCurStep())
			s.AccState.UnsafePostEvent = 0
		}
	}

	s.Model.SetCurStep(s.Model.GetCurStep() + 1)
	if s.research != nil {
		s.research.state.Window.Rewires += changedCount
		if s.research.trace != nil && s.research.trace.err != nil {
			panic(s.research.trace.err)
		}
		if err := s.observeResearch(); err != nil {
			panic(err)
		}
	}
	if s.EnableDumps && s.checkpointRequested(s.Model.GetCurStep()-1) {
		if err := s.SaveResearchCheckpoint(); err != nil {
			panic(fmt.Errorf("save research checkpoint: %w", err))
		}
	}

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
	var persistenceError error

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

		s.Step()
		completedStep := s.Model.GetCurStep() - 1
		if s.ProgressCallback != nil && s.ProgressStepInterval > 0 &&
			completedStep%s.ProgressStepInterval == 0 {
			s.ProgressCallback(ScenarioProgress{
				Step:    completedStep,
				MaxStep: maxSimCount,
			})
		}

		if !s.FixedHorizon && s.StableSteps > STOP_SIM_STEPS {
			return false, didDump
		}

		timeInterval := time.Since(lastSaveTime)
		if s.EnableDumps && timeInterval.Seconds() >= SAVE_INTERVAL {
			lastSaveTime = time.Now()
			persistenceError = s.DumpError()
			if persistenceError != nil {
				return false, false
			}
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
		persistenceError = s.DumpError()
	}

	st := s.Model.GetCurStep() - 1
	stopReason := "horizon"
	if persistenceError != nil {
		log.Printf("Failed to persist run: %v", persistenceError)
		return RunResult{Step: st, StopReason: "persistence_error", Completed: false}
	}

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
			if err := s.Serializer.MarkFinished(FinishMark{
				DataVersion: s.Metadata.DataVersion,
				RNG:         s.Metadata.RNG,
			}); err != nil {
				log.Printf("Failed to mark run finished: %v", err)
				return RunResult{Step: st, StopReason: "persistence_error", Completed: false}
			}
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
		if s.Metadata.CollectItemOptions.ViewPostsEvent || s.Metadata.CollectItemOptions.ViewPostsDetails {
			s.DB.StoreEvent(event)
		}

	}

}

// Close releases owned resources after the last dump. An unfinished feed trace
// retains its checkpointed prefix for the next invocation.
func (s *Scenario) Close() error {
	var first error
	if s.research != nil && s.research.trace != nil {
		first = s.research.trace.Close()
	}
	if s.DB != nil {
		if err := s.DB.Close(); first == nil {
			first = err
		}
	}
	return first
}
