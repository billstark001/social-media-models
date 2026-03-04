package simulation

import (
	"fmt"
	"smp/model"
	"smp/recsys"
)

type ScenarioMetadata struct {
	UniqueName string

	model.SMPAgentParams
	model.SMPModelPureParams
	model.CollectItemOptions

	MaxSimulationStep int
	RecsysFactoryType string
	NetworkType       string // currently useless
	NodeCount         int
	NodeFollowCount   int
}

func GetDefaultRecsysFactoryDefs() map[string]model.RecsysFactory {
	ret := map[string]model.RecsysFactory{

		"Random": func(h *model.SMPModel) model.SMPModelRecommendationSystem {
			return recsys.NewRandom(h, nil)
		},

		"Opinion": func(h *model.SMPModel) model.SMPModelRecommendationSystem {
			return recsys.NewOpinion(h, 0.1, nil)
		},

		"Structure": func(h *model.SMPModel) model.SMPModelRecommendationSystem {
			return recsys.NewStructure(h, 0.1, nil, true, func(s string) {
				fmt.Println(s)
			})
		},

		"OpinionRandom": func(h *model.SMPModel) model.SMPModelRecommendationSystem {
			return recsys.NewOpinionRandom(h, nil, 0.4, 1, 2, 0)
		},

		"StructureRandom": func(h *model.SMPModel) model.SMPModelRecommendationSystem {
			return recsys.NewStructureRandom(h, nil, 1, 0.1, 0, true, func(s string) {
				fmt.Println(s)
			})
		},
	}

	ret["OpinionM9"] = func(h *model.SMPModel) model.SMPModelRecommendationSystem {
		return &recsys.Mix{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Opinion"](h),
			RecSys1Rate: 0.1,
		}
	}

	ret["StructureM9"] = func(h *model.SMPModel) model.SMPModelRecommendationSystem {
		return &recsys.Mix{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Structure"](h),
			RecSys1Rate: 0.1,
		}
	}

	return ret
}
