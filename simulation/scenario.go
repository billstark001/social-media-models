package simulation

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"smp/dynamics"
	"smp/model"
	"smp/utils"
	"time"

	"github.com/schollz/progressbar/v3"
)

type Scenario struct {
	BaseDir    string
	Metadata   *ScenarioMetadata
	Model      *model.SMPModel[float64, dynamics.HKParams]
	AccState   *AccumulativeModelState
	Serializer *SimulationSerializer
	DB         *EventDB

	OutputParsableProgress bool
}

func NewScenario(dir string, metadata *ScenarioMetadata, outputParsableProgress bool) *Scenario {
	return &Scenario{
		BaseDir:                dir,
		Metadata:               metadata,
		Serializer:             NewSimulationSerializer(dir, metadata.UniqueName, 2),
		OutputParsableProgress: outputParsableProgress,
	}
}

var RECSYS_FACTORY = GetDefaultRecsysFactoryDefs()

const MAX_POST_EVENT_INTERVAL = 500
const DB_CACHE_SIZE = 40000

func (s *Scenario) Init() {

	nodeCount := max(s.Metadata.NodeCount, 1)
	edgeCount := max(s.Metadata.NodeFollowCount, 1)
	graph := utils.CreateRandomNetwork(
		nodeCount,
		float64(edgeCount)/(float64(nodeCount)-1),
	)

	factory := RECSYS_FACTORY[s.Metadata.RecsysFactoryType]
	modelParams := model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: s.Metadata.SMPModelPureParams,
		RecsysFactory:      factory,
	}

	m := model.NewSMPModelFloat64(
		graph,
		nil, // NewSMPModelFloat64 generates random opinions in [-1,1] when nil
		&modelParams,
		&s.Metadata.HKParams,
		&dynamics.HK{},
		&s.Metadata.CollectItemOptions,
		s.logEvent,
	)
	m.SetAgentCurPosts()
	if m.Recsys != nil {
		m.Recsys.PostInit(nil)
	}

	s.Model = m

	err := os.MkdirAll(
		filepath.Join(s.BaseDir, s.Metadata.UniqueName),
		0755,
	)
	if err != nil {
		log.Fatalf("Failed to create scenario dump folder: %v", err)
	}

	s.AccState = NewAccumulativeModelState()

	db, err := OpenEventDB(filepath.Join(s.BaseDir, s.Metadata.UniqueName, "events.db"), DB_CACHE_SIZE)
	if err != nil {
		log.Fatalf("Failed to create event db logger: %v", err)
	}

	s.DB = db

	s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.Graph), s.Model.CurStep)
	s.AccState.accumulate(*s.Model)
	s.Model.CurStep = 1

	s.sanitize()
}

func (s *Scenario) Load() bool {

	dbPath := filepath.Join(s.BaseDir, s.Metadata.UniqueName, "events.db")
	_, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		return false
	}
	db, err := OpenEventDB(dbPath, DB_CACHE_SIZE)
	if err != nil {
		log.Printf("Failed to create event db logger: %v", err)
		return false
	}

	s.DB = db

	modelDump, err := s.Serializer.GetLatestSnapshot()
	if err != nil {
		log.Printf("Failed to load model dump: %v", err)
		return false
	}

	if modelDump == nil {
		return false
	}

	factory := RECSYS_FACTORY[s.Metadata.RecsysFactoryType]
	modelParams := model.SMPModelParams[float64, dynamics.HKParams]{
		SMPModelPureParams: s.Metadata.SMPModelPureParams,
		RecsysFactory:      factory,
	}
	s.Model = modelDump.Load(
		&modelParams,
		&s.Metadata.HKParams,
		&dynamics.HK{},
		&s.Metadata.CollectItemOptions,
		s.logEvent,
	)

	acc, err := s.Serializer.GetLatestAccumulativeState()
	if err != nil {
		log.Printf("Failed to load accumulative state: %v", err)
		return false
	} else {
		validated := acc.validate((*s.Model))
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
	s.DB.DeleteEventsAfterStep(s.Model.CurStep)
	s.Serializer.DeleteGraphsAfterStep(s.Model.CurStep, false)
}

func (s *Scenario) Dump() {
	s.DB.Flush()
	s.Serializer.SaveSnapshot(s.Model.Dump())
	s.Serializer.SaveAccumulativeState(s.AccState)
}

func (s *Scenario) Step() (int, float64) {
	changedCount, maxOpinionChange := s.Model.Step(false)

	s.AccState.accumulate(*s.Model)
	s.AccState.UnsafePostEvent += changedCount

	if s.AccState.UnsafePostEvent > MAX_POST_EVENT_INTERVAL {
		s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.Graph), s.Model.CurStep)
		s.AccState.UnsafePostEvent = 0
	}

	s.Model.CurStep++

	return changedCount, maxOpinionChange
}

func (s *Scenario) IsFinished() bool {
	finished, _ := s.Serializer.IsFinished()
	return finished
}

const NETWORK_CHANGE_THRESHOLD = 1
const OPINION_CHANGE_THRESHOLD = 1e-7
const STOP_SIM_STEPS = 60
const SAVE_INTERVAL = 300 // seconds

func (s *Scenario) StepTillEnd(ctx context.Context) {

	maxSimCount := s.Metadata.MaxSimulationStep
	if maxSimCount < 0 {
		maxSimCount = 1
	}

	if s.IsFinished() {
		return
	}

	var bar *progressbar.ProgressBar
	lastPrintTime := time.Now()
	if s.OutputParsableProgress {
		fmt.Printf("TASK:%s;TYPE:INIT;STEP:%d;\n", s.Metadata.UniqueName, s.Model.CurStep)
	} else {
		bar = progressbar.Default(int64(maxSimCount))
		bar.Set(s.Model.CurStep)
	}

	lastSaveTime := time.Now()
	successiveThresholdMet := 0

	unitStep := func() (bool, bool) {

		didDump := false

		if s.OutputParsableProgress {
			if time.Since(lastPrintTime).Milliseconds() > 250 {
				fmt.Printf("TASK:%s;TYPE:PROGRESS;STEP:%d;\n", s.Metadata.UniqueName, s.Model.CurStep)
				lastPrintTime = time.Now()
			}
		} else {
			bar.Set(s.Model.CurStep)
		}

		nwChange, opChange := s.Step()

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
		if timeInterval.Seconds() >= SAVE_INTERVAL {
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
	for s.Model.CurStep <= maxSimCount {
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

	if !s.OutputParsableProgress && s.Model.CurStep <= maxSimCount {
		fmt.Println("")
	}

	if !didDump {
		s.Dump()
	}

	st := s.Model.CurStep - 1

	if isCtxDone {
		if s.OutputParsableProgress {
			fmt.Printf("TASK:%s;TYPE:DONE;DONE_TYPE:SIG;STEP:%d;\n", s.Metadata.UniqueName, s.Model.CurStep)
		} else {
			log.Printf("Simulation ended (`ctx.Done()` received, step: %d)", st)
		}
	} else {
		if !isShouldNotContinue {
			if s.OutputParsableProgress {
				fmt.Printf("TASK:%s;TYPE:DONE;DONE_TYPE:ITER;STEP:%d;\n", s.Metadata.UniqueName, s.Model.CurStep)
			} else {
				log.Printf("Simulation ended (max iteration reached, step: %d)", st)
			}
		} else {
			if s.OutputParsableProgress {
				fmt.Printf("TASK:%s;TYPE:DONE;DONE_TYPE:HALT;STEP:%d;\n", s.Metadata.UniqueName, s.Model.CurStep)
			} else {
				log.Printf("Simulation ended (shouldContinue == false, step: %d)", st)
			}
		}
		s.Serializer.MarkFinished()
		s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.Graph), st)
	}

}

func (s *Scenario) logEvent(event *model.EventRecord) {

	switch event.Type {

	case "Post":
		body := event.Body.(model.PostEventBody[float64])
		if body.IsRepost && s.Metadata.CollectItemOptions.PostEvent {
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
