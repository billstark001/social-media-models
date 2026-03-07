package simulation

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"smp/dynamics"
	"smp/model"
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

const MAX_POST_EVENT_INTERVAL = 500
const DB_CACHE_SIZE = 40000

func (s *Scenario) Init() {

	nodeCount := max(s.Metadata.NodeCount, 1)
	edgeCount := max(s.Metadata.NodeFollowCount, 1)
	graph := utils.CreateRandomNetwork(
		nodeCount,
		float64(edgeCount)/(float64(nodeCount)-1),
	)

	switch s.Metadata.DynamicsType {
	case "", DynamicsTypeHK:
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.HKParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		m := model.NewSMPModelFloat64(graph, nil, &params, &s.Metadata.HKParams, &dynamics.HK{}, &s.Metadata.CollectItemOptions, s.logEvent)
		s.Model = &Float64ModelWrapper[dynamics.HKParams]{M: m}
	case DynamicsTypeDeffuant:
		factories := GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[float64, dynamics.DeffuantParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		m := model.NewSMPModelFloat64(graph, nil, &params, &s.Metadata.DeffuantParams, &dynamics.Deffuant{}, &s.Metadata.CollectItemOptions, s.logEvent)
		s.Model = &Float64ModelWrapper[dynamics.DeffuantParams]{M: m}
	case DynamicsTypeGalam:
		factories := GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.GalamParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		n := graph.Nodes().Len()
		ops := make([]bool, n)
		for i := range ops {
			ops[i] = rand.IntN(2) == 1
		}
		m := model.NewSMPModel(graph, &ops, &params, &s.Metadata.GalamParams, &dynamics.Galam{}, &s.Metadata.CollectItemOptions, s.logEvent)
		s.Model = &BoolModelWrapper[dynamics.GalamParams]{M: m}
	case DynamicsTypeVoter:
		factories := GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](s.Metadata.RecSysParams)
		params := model.SMPModelParams[bool, dynamics.VoterParams]{
			SMPModelPureParams: s.Metadata.SMPModelPureParams,
			RecsysFactory:      factories[s.Metadata.RecsysFactoryType],
		}
		n := graph.Nodes().Len()
		ops := make([]bool, n)
		for i := range ops {
			ops[i] = rand.IntN(2) == 1
		}
		m := model.NewSMPModel(graph, &ops, &params, &s.Metadata.VoterParams, &dynamics.Voter{}, &s.Metadata.CollectItemOptions, s.logEvent)
		s.Model = &BoolModelWrapper[dynamics.VoterParams]{M: m}
	default:
		log.Fatalf("Unknown DynamicsType: %q", s.Metadata.DynamicsType)
	}

	s.Model.InitPosts()

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

	s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.GetGraph()), s.Model.GetCurStep())
	s.Model.Accumulate(s.AccState)
	s.Model.SetCurStep(1)

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
			M: dump.Load(&params, &s.Metadata.HKParams, &dynamics.HK{}, &s.Metadata.CollectItemOptions, s.logEvent),
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
			M: dump.Load(&params, &s.Metadata.DeffuantParams, &dynamics.Deffuant{}, &s.Metadata.CollectItemOptions, s.logEvent),
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
			M: dump.Load(&params, &s.Metadata.GalamParams, &dynamics.Galam{}, &s.Metadata.CollectItemOptions, s.logEvent),
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
			M: dump.Load(&params, &s.Metadata.VoterParams, &dynamics.Voter{}, &s.Metadata.CollectItemOptions, s.logEvent),
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
	s.DB.DeleteEventsAfterStep(s.Model.GetCurStep())
	s.Serializer.DeleteGraphsAfterStep(s.Model.GetCurStep(), false)
}

func (s *Scenario) Dump() {
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

	s.Model.Accumulate(s.AccState)
	s.AccState.UnsafePostEvent += changedCount

	if s.AccState.UnsafePostEvent > MAX_POST_EVENT_INTERVAL {
		s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.GetGraph()), s.Model.GetCurStep())
		s.AccState.UnsafePostEvent = 0
	}

	s.Model.SetCurStep(s.Model.GetCurStep() + 1)

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
		fmt.Printf("TASK:%s;TYPE:INIT;STEP:%d;\n", s.Metadata.UniqueName, s.Model.GetCurStep())
	} else {
		bar = progressbar.Default(int64(maxSimCount))
		bar.Set(s.Model.GetCurStep())
	}

	lastSaveTime := time.Now()
	successiveThresholdMet := 0

	unitStep := func() (bool, bool) {

		didDump := false

		if s.OutputParsableProgress {
			if time.Since(lastPrintTime).Milliseconds() > 250 {
				fmt.Printf("TASK:%s;TYPE:PROGRESS;STEP:%d;\n", s.Metadata.UniqueName, s.Model.GetCurStep())
				lastPrintTime = time.Now()
			}
		} else {
			bar.Set(s.Model.GetCurStep())
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

	if !s.OutputParsableProgress && s.Model.GetCurStep() <= maxSimCount {
		fmt.Println("")
	}

	if !didDump {
		s.Dump()
	}

	st := s.Model.GetCurStep() - 1

	if isCtxDone {
		if s.OutputParsableProgress {
			fmt.Printf("TASK:%s;TYPE:DONE;DONE_TYPE:SIG;STEP:%d;\n", s.Metadata.UniqueName, s.Model.GetCurStep())
		} else {
			log.Printf("Simulation ended (`ctx.Done()` received, step: %d)", st)
		}
	} else {
		if !isShouldNotContinue {
			if s.OutputParsableProgress {
				fmt.Printf("TASK:%s;TYPE:DONE;DONE_TYPE:ITER;STEP:%d;\n", s.Metadata.UniqueName, s.Model.GetCurStep())
			} else {
				log.Printf("Simulation ended (max iteration reached, step: %d)", st)
			}
		} else {
			if s.OutputParsableProgress {
				fmt.Printf("TASK:%s;TYPE:DONE;DONE_TYPE:HALT;STEP:%d;\n", s.Metadata.UniqueName, s.Model.GetCurStep())
			} else {
				log.Printf("Simulation ended (shouldContinue == false, step: %d)", st)
			}
		}
		s.Serializer.MarkFinished()
		s.Serializer.SaveGraph(utils.SerializeGraph(s.Model.GetGraph()), st)
	}

}

func (s *Scenario) logEvent(event *model.EventRecord) {

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
