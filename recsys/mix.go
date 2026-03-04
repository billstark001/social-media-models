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

type Mix struct {
	model.SMPModelRecommendationSystem
	Model       *model.SMPModel
	RecSys1     model.SMPModelRecommendationSystem
	RecSys2     model.SMPModelRecommendationSystem
	RecSys1Rate float64
}

func _() model.SMPModelRecommendationSystem {
	return &Mix{}
}

func (rs *Mix) PostInit(dumpData []byte) {
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

func (rs *Mix) PreStep() {
	rs.RecSys1.PreStep()
	rs.RecSys2.PreStep()
}

func (rs *Mix) PreCommit() {
	rs.RecSys1.PreCommit()
	rs.RecSys2.PreCommit()
}

func (rs *Mix) PostStep(changed []*model.RewiringEventBody) {
	rs.RecSys1.PostStep(changed)
	rs.RecSys2.PostStep(changed)
}

func (rs *Mix) Recommend(
	agent *model.SMPAgent,
	neighborIDs map[int64]bool,
	count int,
) []*model.TweetRecord {
	r1Count := int(float64(count)*rs.RecSys1Rate + 0.5)
	r2Count := count - r1Count
	ret := make([]*model.TweetRecord, 0)
	if r1Count > 0 {
		ret = append(ret, rs.RecSys1.Recommend(agent, neighborIDs, r1Count)...)
	}
	if r2Count > 0 {
		ret = append(ret, rs.RecSys2.Recommend(agent, neighborIDs, r2Count)...)
	}
	return ret
}

func (rs *Mix) Dump() []byte {
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
