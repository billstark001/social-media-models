package simulation

import (
	model "smp/model"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
)

// IModel abstracts over SMPModel[O, P] for use in Scenario.
// It hides opinion-type generics behind a uniform interface.
type IModel interface {
	GetCurStep() int
	SetCurStep(v int)
	GetGraph() *simple.DirectedGraph
	// StepModel runs one simulation step.
	// Returns (changedCount, maxOpinionChange).
	StepModel() (int, float64)
	// InitPosts calls SetAgentCurPosts and Recsys.PostInit.
	InitPosts()
	// Accumulate appends this step's data to acc.
	Accumulate(acc *AccumulativeModelState)
	// ValidateAcc checks that acc has the expected number of steps.
	ValidateAcc(acc *AccumulativeModelState) bool
	// RawDump serializes the model state to msgpack bytes for snapshotting.
	RawDump() ([]byte, error)
}

// ---- Float64ModelWrapper ----

// Float64ModelWrapper wraps SMPModel[float64, P] and implements IModel.
type Float64ModelWrapper[P any] struct {
	M *model.SMPModel[float64, P]
}

func (w *Float64ModelWrapper[P]) GetCurStep() int                 { return w.M.CurStep }
func (w *Float64ModelWrapper[P]) SetCurStep(v int)                { w.M.CurStep = v }
func (w *Float64ModelWrapper[P]) GetGraph() *simple.DirectedGraph { return w.M.Graph }
func (w *Float64ModelWrapper[P]) StepModel() (int, float64)       { return w.M.Step(false) }

func (w *Float64ModelWrapper[P]) InitPosts() {
	w.M.SetAgentCurPosts()
	if w.M.Recsys != nil {
		w.M.Recsys.PostInit(nil)
	}
}

func (w *Float64ModelWrapper[P]) Accumulate(acc *AccumulativeModelState) {
	acc.Opinions = append(acc.Opinions, float64sToFloat32s(w.M.CollectOpinions()))
	acc.AgentNumbers = append(acc.AgentNumbers, int32sToInt16s4(w.M.CollectAgentNumbers()))
	acc.AgentOpinionSums = append(acc.AgentOpinionSums, float64sToFloat32s4(w.M.CollectAgentOpinions()))
}

func (w *Float64ModelWrapper[P]) ValidateAcc(acc *AccumulativeModelState) bool {
	st := w.M.CurStep
	return len(acc.Opinions) == st &&
		len(acc.AgentNumbers) == st &&
		len(acc.AgentOpinionSums) == st
}

func (w *Float64ModelWrapper[P]) RawDump() ([]byte, error) {
	return msgpack.Marshal(w.M.Dump())
}

// ---- BoolModelWrapper ----

// BoolModelWrapper wraps SMPModel[bool, P] and implements IModel.
// Boolean opinions are stored as 0.0 (false) / 1.0 (true) in AccumulativeModelState.
type BoolModelWrapper[P any] struct {
	M *model.SMPModel[bool, P]
}

func (w *BoolModelWrapper[P]) GetCurStep() int                 { return w.M.CurStep }
func (w *BoolModelWrapper[P]) SetCurStep(v int)                { w.M.CurStep = v }
func (w *BoolModelWrapper[P]) GetGraph() *simple.DirectedGraph { return w.M.Graph }
func (w *BoolModelWrapper[P]) StepModel() (int, float64)       { return w.M.Step(false) }

func (w *BoolModelWrapper[P]) InitPosts() {
	w.M.SetAgentCurPosts()
	if w.M.Recsys != nil {
		w.M.Recsys.PostInit(nil)
	}
}

func (w *BoolModelWrapper[P]) Accumulate(acc *AccumulativeModelState) {
	opinions := w.M.CollectOpinions()
	row := make([]float32, len(opinions))
	for i, v := range opinions {
		if v {
			row[i] = 1.0
		}
	}
	acc.Opinions = append(acc.Opinions, row)
	acc.AgentNumbers = append(acc.AgentNumbers, int32sToInt16s4(w.M.CollectAgentNumbers()))
	acc.AgentOpinionSums = append(acc.AgentOpinionSums, float64sToFloat32s4(w.M.CollectAgentOpinions()))
}

func (w *BoolModelWrapper[P]) ValidateAcc(acc *AccumulativeModelState) bool {
	st := w.M.CurStep
	return len(acc.Opinions) == st &&
		len(acc.AgentNumbers) == st &&
		len(acc.AgentOpinionSums) == st
}

func (w *BoolModelWrapper[P]) RawDump() ([]byte, error) {
	return msgpack.Marshal(w.M.Dump())
}
