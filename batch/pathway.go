package batch

import (
	"smp/dynamics"
	"smp/model"
	"smp/simulation"
)

func measureFeed(m *model.SMPModel[float64, dynamics.HKParams], exposure *simulation.Exposure, trace *exposureTrace) {
	var record func(int, int64, *model.FeedDetails[float64])
	if trace != nil {
		record = trace.record
	}
	simulation.ObserveHKFeed(m, exposure, record)
}
