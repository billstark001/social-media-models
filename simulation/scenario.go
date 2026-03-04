package simulation

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"smp/model"
	"smp/utils"
	"time"

	"github.com/schollz/progressbar/v3"
)

type Scenario struct {
	BaseDir    string
	Metadata   *ScenarioMetadata
	Model      *model.SMPModel
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

const MAX_TWEET_EVENT_INTERVAL = 500
const DB_CACHE_SIZE = 40000

func (s *Scenario) Init() {

	nodeCount := max(s.Metadata.NodeCount, 1)
	edgeCount := max(s.Metadata.NodeFollowCount, 1)
	graph := utils.CreateRandomNetwork(
		nodeCount,
		float64(edgeCount)/(float64(nodeCount)-1),
	)

	// initialize model

	factory := RECSYS_FACTORY[s.Metadata.RecsysFactoryType]
	modelParams := model.SMPModelParams{
		SMPModelPureParams: s.Metadata.SMPModelPureParams,
		RecsysFactory:      factory,
	}

	m := model.NewSMPModel(
		graph,
		nil,
		&modelParams,
		&s.Metadata.SMPAgentParams,
		&s.Metadata.CollectItemOptions,
		s.logEvent,
	)
	m.SetAgentCurTweets()
	if m.Recsys != nil {
		m.Recsys.PostInit(nil)
	}

	s.Model = m

	// create model record dump
	err := os.MkdirAll(
		filepath.Join(s.BaseDir, s.Metadata.UniqueName),
		0755,
	)
	if err != nil {
		log.Fatalf("Failed to create scenario dump folder: %v", err)
	}

	// initialize accumulative record

	s.AccState = NewAccumulativeModelState()

	db, err := OpenEventDB(filepath.Join(s.BaseDir, s.Metadata.UniqueName, "events.db"), DB_CACHE_SIZE)
	if err != nil {
		log.Fatalf("Failed to create event db logger: %v", err)
	}

	s.DB = db

	// write initial record
	s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.Graph), s.Model.CurStep)
	s.AccState.accumulate(*s.Model)
	s.Model.CurStep = 1

	s.sanitize()
}

func (s *Scenario) Load() bool {

	// initialize event db
	dbPath := filepath.Join(s.BaseDir, s.Metadata.UniqueName, "events.db")
	_, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		// db inexistent
		return false
	}
	db, err := OpenEventDB(dbPath, DB_CACHE_SIZE)
	if err != nil {
		log.Printf("Failed to create event db logger: %v", err)
		return false
	}

	s.DB = db

	// initialize model

	modelDump, err := s.Serializer.GetLatestSnapshot()
	if err != nil {
		log.Printf("Failed to load model dump: %v", err)
		return false
	}

	if modelDump == nil {
		return false
	}

	factory := RECSYS_FACTORY[s.Metadata.RecsysFactoryType]
	modelParams := model.SMPModelParams{
		SMPModelPureParams: s.Metadata.SMPModelPureParams,
		RecsysFactory:      factory,
	}
	s.Model = modelDump.Load(
		&modelParams,
		&s.Metadata.SMPAgentParams,
		&s.Metadata.CollectItemOptions,
		s.logEvent,
	)

	// initialize accumulative record

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
	// delete potentially dirty data
	// 'after': >=
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

	// event is naturally logged

	// log accumulative state
	s.AccState.accumulate(*s.Model)
	s.AccState.UnsafeTweetEvent += changedCount

	// log graph if necessary
	if s.AccState.UnsafeTweetEvent > MAX_TWEET_EVENT_INTERVAL {
		s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.Graph), s.Model.CurStep)
		s.AccState.UnsafeTweetEvent = 0
	}

	// increase the counter manually
	// to ensure the graph records' step numbers stay consistent
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

	// if finished, jump this simulation
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

		// step
		if s.OutputParsableProgress {
			if time.Since(lastPrintTime).Milliseconds() > 250 {
				fmt.Printf("TASK:%s;TYPE:PROGRESS;STEP:%d;\n", s.Metadata.UniqueName, s.Model.CurStep)
				lastPrintTime = time.Now()
			}
		} else {
			bar.Set(s.Model.CurStep)
		}

		nwChange, opChange := s.Step()

		// if threshold is met, end in prior
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

		// save at fixed interval
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
				// do nothing
			} else {
				isShouldNotContinue = true
				break iterLoop
			}
		}
	}

	// bar.Close()
	if !s.OutputParsableProgress && s.Model.CurStep <= maxSimCount {
		fmt.Println("")
	}

	if !didDump {
		s.Dump()
	}

	// st is the last step that has full simulation record
	st := s.Model.CurStep - 1

	if isCtxDone {
		if s.OutputParsableProgress {
			fmt.Printf("TASK:%s;TYPE:DONE;DONE_TYPE:SIG;STEP:%d;\n", s.Metadata.UniqueName, s.Model.CurStep)
		} else {
			log.Printf("Simulation ended (`ctx.Done()` received, step: %d)", st)
		}
		// the simulation is halted
		// do nothing
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
		// the simulation is finished
		s.Serializer.MarkFinished()
		s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.Graph), st)
	}

}

func (s *Scenario) logEvent(event *model.EventRecord) {

	// add to database when necessary

	switch event.Type {

	case "Tweet":
		body := event.Body.(model.TweetEventBody)
		if body.IsRetweet && s.Metadata.CollectItemOptions.TweetEvent {
			s.DB.StoreEvent(event)
		} else {
			// do nothing
		}

	case "Rewiring":
		if s.Metadata.CollectItemOptions.RewiringEvent {
			s.DB.StoreEvent(event)
		}

	case "ViewTweets":
		if s.Metadata.CollectItemOptions.ViewTweetsEvent {
			s.DB.StoreEvent(event)
		}

	}

}
