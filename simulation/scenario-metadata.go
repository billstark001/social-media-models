package simulation

import (
	"fmt"
	"smp/dynamics"
	"smp/model"
	"smp/recsys"
)

// DynamicsType constants for the four supported opinion dynamics.
const (
	DynamicsTypeHK       = "HK"
	DynamicsTypeDeffuant = "Deffuant"
	DynamicsTypeGalam    = "Galam"
	DynamicsTypeVoter    = "Voter"
)

// ScenarioMetadata holds all parameters needed to create or reproduce a simulation.
// DynamicsType selects which dynamics to use; the corresponding *Params field is read.
type ScenarioMetadata struct {
	UniqueName string

	// DynamicsType selects the opinion dynamics.
	// Accepted values: "HK" (default), "Deffuant", "Galam", "Voter".
	DynamicsType string

	HKParams       dynamics.HKParams
	DeffuantParams dynamics.DeffuantParams
	GalamParams    dynamics.GalamParams
	VoterParams    dynamics.VoterParams

	model.SMPModelPureParams
	model.CollectItemOptions

	MaxSimulationStep int
	RecsysFactoryType string
	NetworkType       string
	NodeCount         int
	NodeFollowCount   int
}

// GetFloat64RecsysFactories returns the full set of recsys factories for
// float64-opinion models with params type P (used by HK and Deffuant).
func GetFloat64RecsysFactories[P any]() map[string]model.RecsysFactory[float64, P] {
	ret := map[string]model.RecsysFactory[float64, P]{
		"Random": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewRandom(h, nil)
		},
		"Opinion": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewOpinion(h, 0.1, nil)
		},
		"Structure": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewStructure(h, 0.1, nil, true, func(s string) {
				fmt.Println(s)
			})
		},
		"OpinionRandom": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewOpinionRandom(h, nil, 0.4, 1, 2, 0)
		},
		"StructureRandom": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewStructureRandom(h, nil, 1, 0.1, 0, true, func(s string) {
				fmt.Println(s)
			})
		},
	}

	ret["OpinionM9"] = func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
		return &recsys.Mix[float64, P]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Opinion"](h),
			RecSys1Rate: 0.1,
		}
	}

	ret["StructureM9"] = func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
		return &recsys.Mix[float64, P]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Structure"](h),
			RecSys1Rate: 0.1,
		}
	}

	return ret
}

// GetBoolRecsysFactories returns the available recsys factories for bool-opinion
// models with params type P (used by Galam and Voter).
// "Random" and "StructureM9" (90% structure + 10% random) are supported.
func GetBoolRecsysFactories[P any]() map[string]model.RecsysFactory[bool, P] {
	ret := map[string]model.RecsysFactory[bool, P]{
		"Random": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewRandom(h, nil)
		},
		"Structure": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewStructure(h, 0.1, nil, true, func(s string) {
				fmt.Println(s)
			})
		},
		"StructureRandom": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewStructureRandom(h, nil, 1, 0.1, 0, true, func(s string) {
				fmt.Println(s)
			})
		},
	}

	ret["StructureM9"] = func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
		return &recsys.Mix[bool, P]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Structure"](h),
			RecSys1Rate: 0.1,
		}
	}

	return ret
}

// GetDefaultRecsysFactoryDefs returns the HK recsys factories for backward compatibility.
func GetDefaultRecsysFactoryDefs() map[string]model.RecsysFactory[float64, dynamics.HKParams] {
	return GetFloat64RecsysFactories[dynamics.HKParams]()
}
