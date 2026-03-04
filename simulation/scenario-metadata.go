package simulation

import (
	"fmt"
	"smp/dynamics"
	"smp/model"
	"smp/recsys"
)

type ScenarioMetadata struct {
	UniqueName string

	dynamics.HKParams
	model.SMPModelPureParams
	model.CollectItemOptions

	MaxSimulationStep int
	RecsysFactoryType string
	NetworkType       string
	NodeCount         int
	NodeFollowCount   int
}

func GetDefaultRecsysFactoryDefs() map[string]model.RecsysFactory[float64, dynamics.HKParams] {
	ret := map[string]model.RecsysFactory[float64, dynamics.HKParams]{

		"Random": func(h *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
			return recsys.NewRandom(h, nil)
		},

		"Opinion": func(h *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
			return recsys.NewOpinion(h, 0.1, nil)
		},

		"Structure": func(h *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
			return recsys.NewStructure(h, 0.1, nil, true, func(s string) {
				fmt.Println(s)
			})
		},

		"OpinionRandom": func(h *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
			return recsys.NewOpinionRandom(h, nil, 0.4, 1, 2, 0)
		},

		"StructureRandom": func(h *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
			return recsys.NewStructureRandom(h, nil, 1, 0.1, 0, true, func(s string) {
				fmt.Println(s)
			})
		},
	}

	ret["OpinionM9"] = func(h *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
		return &recsys.Mix[float64, dynamics.HKParams]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Opinion"](h),
			RecSys1Rate: 0.1,
		}
	}

	ret["StructureM9"] = func(h *model.SMPModel[float64, dynamics.HKParams]) model.SMPModelRecommendationSystem[float64, dynamics.HKParams] {
		return &recsys.Mix[float64, dynamics.HKParams]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Structure"](h),
			RecSys1Rate: 0.1,
		}
	}

	return ret
}
