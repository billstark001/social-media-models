package model

// SMPModelRecommendationSystem defines the interface for recommendation systems.
type SMPModelRecommendationSystem[O any, P any] interface {
	PostInit(dumpData []byte)
	PreStep()
	PreCommit()
	PostStep(changed []*RewiringEventBody)
	Recommend(agent *SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*PostRecord[O]
	Dump() []byte
}

// BaseRecommendationSystem provides default empty implementations.
type BaseRecommendationSystem[O any, P any] struct{}

func (rs *BaseRecommendationSystem[O, P]) PostInit(dumpData []byte) {}
func (rs *BaseRecommendationSystem[O, P]) PreStep()                 {}
func (rs *BaseRecommendationSystem[O, P]) PreCommit()               {}
func (rs *BaseRecommendationSystem[O, P]) PostStep(changed []*RewiringEventBody) {
}
func (rs *BaseRecommendationSystem[O, P]) Recommend(agent *SMPAgent[O, P], neighborIDs map[int64]bool, count int) []*PostRecord[O] {
	return []*PostRecord[O]{}
}
func (rs *BaseRecommendationSystem[O, P]) Dump() []byte { return nil }
