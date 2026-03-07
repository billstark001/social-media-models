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

	// RecSysParams holds optional per-key overrides passed to the recsys factory.
	// Keys and their defaults:
	//   "noiseStd"         float64 – noise std for Opinion, Structure, StructureRandom (default 0.1)
	//   "opRandomNoiseStd" float64 – noise std for OpinionRandom (default 2.0)
	//   "useCache"         bool    – lazy caching for Structure / StructureRandom (default true)
	//   "tolerance"        float64 – opinion tolerance window for OpinionRandom (default 0.4)
	//   "steepness"        float64 – score sharpening for OpinionRandom / StructureRandom (default 1.0)
	//   "randomRatio"      float64 – uniform-random fraction for OpinionRandom / StructureRandom (default 0.0)
	//   "mixRate"          float64 – RecSys1 fraction in Mix variants (OpinionM9, StructureM9) (default 0.1)
	RecSysParams map[string]any
}

// getParam reads key from params with a type assertion, returning defaultVal on any miss or type mismatch.
func getParam[T any](params map[string]any, key string, defaultVal T) T {
	if params == nil {
		return defaultVal
	}
	v, ok := params[key]
	if !ok {
		return defaultVal
	}
	typed, ok := v.(T)
	if !ok {
		return defaultVal
	}
	return typed
}

// GetFloat64RecsysFactoriesWithParams returns the full set of recsys factories for
// float64-opinion models with params type P (used by HK and Deffuant),
// reading configuration from the provided params map (nil uses all defaults).
func GetFloat64RecsysFactoriesWithParams[P any](params map[string]any) map[string]model.RecsysFactory[float64, P] {
	noiseStd := getParam(params, "NoiseStd", 0.1)
	opRandNoiseStd := getParam(params, "OpRandomNoiseStd", 2.0)
	useCache := getParam(params, "UseCache", true)
	tolerance := getParam(params, "Tolerance", 0.4)
	steepness := getParam(params, "Steepness", 1.0)
	randomRatio := getParam(params, "RandomRatio", 0.0)
	mixRate := getParam(params, "MixRate", 0.1)

	ret := map[string]model.RecsysFactory[float64, P]{
		"Random": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewRandom(h, nil)
		},
		"Opinion": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewOpinion(h, noiseStd, nil)
		},
		"Structure": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewStructure(h, noiseStd, nil, useCache, func(s string) {
				fmt.Println(s)
			})
		},
		"OpinionRandom": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewOpinionRandom(h, nil, tolerance, steepness, opRandNoiseStd, randomRatio)
		},
		"StructureRandom": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewStructureRandom(h, nil, steepness, noiseStd, randomRatio, useCache, func(s string) {
				fmt.Println(s)
			})
		},
	}

	ret["OpinionM9"] = func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
		return &recsys.Mix[float64, P]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Opinion"](h),
			RecSys1Rate: mixRate,
		}
	}

	ret["StructureM9"] = func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
		return &recsys.Mix[float64, P]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Structure"](h),
			RecSys1Rate: mixRate,
		}
	}

	return ret
}

// GetFloat64RecsysFactories returns the full set of recsys factories for
// float64-opinion models with params type P (used by HK and Deffuant).
func GetFloat64RecsysFactories[P any]() map[string]model.RecsysFactory[float64, P] {
	return GetFloat64RecsysFactoriesWithParams[P](nil)
}

// GetBoolRecsysFactoriesWithParams returns the available recsys factories for bool-opinion
// models with params type P (used by Galam and Voter),
// reading configuration from the provided params map (nil uses all defaults).
func GetBoolRecsysFactoriesWithParams[P any](params map[string]any) map[string]model.RecsysFactory[bool, P] {
	noiseStd := getParam(params, "noiseStd", 0.1)
	useCache := getParam(params, "useCache", true)
	steepness := getParam(params, "steepness", 1.0)
	randomRatio := getParam(params, "randomRatio", 0.0)
	mixRate := getParam(params, "mixRate", 0.1)

	ret := map[string]model.RecsysFactory[bool, P]{
		"Random": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewRandom(h, nil)
		},
		"Structure": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewStructure(h, noiseStd, nil, useCache, func(s string) {
				fmt.Println(s)
			})
		},
		"StructureRandom": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewStructureRandom(h, nil, steepness, noiseStd, randomRatio, useCache, func(s string) {
				fmt.Println(s)
			})
		},
	}

	ret["StructureM9"] = func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
		return &recsys.Mix[bool, P]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Structure"](h),
			RecSys1Rate: mixRate,
		}
	}

	return ret
}

// GetBoolRecsysFactories returns the available recsys factories for bool-opinion
// models with params type P (used by Galam and Voter).
func GetBoolRecsysFactories[P any]() map[string]model.RecsysFactory[bool, P] {
	return GetBoolRecsysFactoriesWithParams[P](nil)
}

// GetDefaultRecsysFactoryDefs returns the HK recsys factories for backward compatibility.
func GetDefaultRecsysFactoryDefs() map[string]model.RecsysFactory[float64, dynamics.HKParams] {
	return GetFloat64RecsysFactories[dynamics.HKParams]()
}
