package recsys

import (
	"log"
	"smp/model"

	"github.com/vmihailenco/msgpack/v5"
)

type MixDump struct {
	RecSys1Dump []byte
	RecSys2Dump []byte
}

type Mix[O any, P any] struct {
	model.BaseRecommendationSystem[O, P]
	Model       *model.SMPModel[O, P]
	RecSys1     model.SMPModelRecommendationSystem[O, P]
	RecSys2     model.SMPModelRecommendationSystem[O, P]
	RecSys1Rate float64
}

func (rs *Mix[O, P]) PostInit(dumpData []byte) {
	dumpStruct := &MixDump{}
	if dumpData != nil {
		err := msgpack.Unmarshal(dumpData, dumpStruct)
		if err == nil {
			rs.RecSys1.PostInit(dumpStruct.RecSys1Dump)
			rs.RecSys2.PostInit(dumpStruct.RecSys2Dump)
			return
		} else {
			panic("test")
		}
	}
	rs.RecSys1.PostInit(nil)
	rs.RecSys2.PostInit(nil)
}

func (rs *Mix[O, P]) PreStep() {
	rs.RecSys1.PreStep()
	rs.RecSys2.PreStep()
}

func (rs *Mix[O, P]) PreCommit() {
	rs.RecSys1.PreCommit()
	rs.RecSys2.PreCommit()
}

func (rs *Mix[O, P]) PostStep(changed []*model.RewiringEventBody) {
	rs.RecSys1.PostStep(changed)
	rs.RecSys2.PostStep(changed)
}

func (rs *Mix[O, P]) Recommend(
	agent *model.SMPAgent[O, P],
	neighborIDs map[int64]bool,
	count int,
) []*model.PostRecord[O] {
	r1Count := int(float64(count)*rs.RecSys1Rate + 0.5)
	r2Count := count - r1Count
	ret := make([]*model.PostRecord[O], 0)
	if r1Count > 0 {
		ret = append(ret, rs.RecSys1.Recommend(agent, neighborIDs, r1Count)...)
	}
	if r2Count > 0 {
		ret = append(ret, rs.RecSys2.Recommend(agent, neighborIDs, r2Count)...)
	}
	return ret
}

func (rs *Mix[O, P]) Dump() []byte {
	ret := MixDump{
		RecSys1Dump: rs.RecSys1.Dump(),
		RecSys2Dump: rs.RecSys2.Dump(),
	}
	retByte, err := msgpack.Marshal(ret)
	if err != nil {
		log.Fatalf("Failed to marshal structure recsys dump data: %v", err)
	}
	return retByte
}
