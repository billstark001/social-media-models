package simulation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"smp/dynamics"
	"smp/model"
	"smp/recsys"
	smprng "smp/rng"
	"smp/trajectory"
	"sort"
	"strings"
)

// DynamicsType constants for the four supported opinion dynamics.
const (
	// CurrentDataVersion is the newest supported simulation format. Version 3
	// adds recovery state for persistent research observations and interventions.
	CurrentDataVersion = 3

	// DefaultDataVersion preserves ordinary-run output when no research state is
	// requested. Existing version-2 readers can continue reading those records.
	DefaultDataVersion = 2

	DynamicsTypeHK       = "HK"
	DynamicsTypeDeffuant = "Deffuant"
	DynamicsTypeGalam    = "Galam"
	DynamicsTypeVoter    = "Voter"
)

// ScenarioMetadata holds all parameters needed to create or reproduce a simulation.
// DynamicsType selects which dynamics to use; the corresponding *Params field is read.
type ScenarioMetadata struct {
	DataVersion int
	RNG         smprng.Spec

	UniqueName string

	// DynamicsType selects the opinion dynamics.
	// Accepted values: "HK", "Deffuant", "Galam", "Voter".
	DynamicsType string

	HKParams       dynamics.HKParams
	DeffuantParams dynamics.DeffuantParams
	GalamParams    dynamics.GalamParams
	VoterParams    dynamics.VoterParams

	model.SMPModelPureParams
	model.CollectItemOptions

	MaxSimulationStep   int
	CheckpointSteps     []int
	TrajectoryPrecision trajectory.Precision
	RecsysFactoryType   string
	NetworkType         string
	NodeCount           int
	NodeFollowCount     int

	// RecSysParams holds optional per-key overrides passed to the recsys factory.
	// Keys and their defaults:
	//   "NoiseStd"         float64 – noise std for Opinion, Structure, StructureRandom (default 0.1)
	//   "OpRandomNoiseStd" float64 – noise std for OpinionRandom (default 2.0)
	//   "UseCache"         bool    – lazy caching for Structure / StructureRandom (default true)
	//   "Tolerance"        float64 – opinion tolerance window for OpinionRandom (default 0.4)
	//   "Steepness"        float64 – score sharpening for OpinionRandom / StructureRandom (default 1.0)
	//   "RandomRatio"      float64 – uniform-random fraction for OpinionRandom / StructureRandom (default 0.0)
	//   "MixRate"          float64 – RecSys1 fraction in Mix variants (OpinionM9, StructureM9) (default 0.1)
	//   "LogRecommendations" bool – Structure diagnostics on stderr (default true)
	RecSysParams map[string]any
}

// PrepareForNewRun resolves defaults that must be persisted before sampling.
func (m *ScenarioMetadata) PrepareForNewRun() error {
	if err := m.TrajectoryPrecision.Validate(); err != nil {
		return err
	}
	m.TrajectoryPrecision = m.TrajectoryPrecision.Resolved()
	if m.DataVersion == 0 || m.DataVersion == 1 {
		m.DataVersion = DefaultDataVersion
	}
	resolved, err := smprng.Resolve(m.RNG)
	if err != nil {
		return err
	}
	m.RNG = resolved
	return nil
}

var uniqueNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Validate checks whether metadata values are executable and safe.
func (m *ScenarioMetadata) Validate() error {
	if m == nil {
		return errors.New("metadata is nil")
	}
	if m.DataVersion < 0 || m.DataVersion > CurrentDataVersion {
		return fmt.Errorf(
			"unsupported DataVersion %d (supported versions are 0 through %d)",
			m.DataVersion,
			CurrentDataVersion,
		)
	}
	if m.DataVersion >= 1 || !m.RNG.IsZero() {
		if _, err := smprng.Resolve(m.RNG); err != nil {
			return err
		}
	}
	if err := m.TrajectoryPrecision.Validate(); err != nil {
		return err
	}

	uniqueName := strings.TrimSpace(m.UniqueName)
	if uniqueName == "" {
		return errors.New("UniqueName must not be empty")
	}
	if uniqueName != m.UniqueName {
		return errors.New("UniqueName must not have leading/trailing spaces")
	}
	if uniqueName == "." || uniqueName == ".." {
		return errors.New("UniqueName must not be '.' or '..'")
	}
	if len(uniqueName) > 128 {
		return fmt.Errorf("UniqueName is too long: %d > 128", len(uniqueName))
	}
	if !uniqueNamePattern.MatchString(uniqueName) {
		return errors.New("UniqueName may only contain [A-Za-z0-9._-]")
	}

	if m.RecsysCount <= 0 {
		return fmt.Errorf("RecsysCount must be > 0, got %d", m.RecsysCount)
	}
	if m.PostRetainCount < 0 {
		return fmt.Errorf("PostRetainCount must be >= 0, got %d", m.PostRetainCount)
	}

	if m.MaxSimulationStep <= 0 {
		return fmt.Errorf("MaxSimulationStep must be > 0, got %d", m.MaxSimulationStep)
	}
	for _, step := range m.CheckpointSteps {
		if step < 0 || step > m.MaxSimulationStep {
			return fmt.Errorf("CheckpointSteps contains out-of-range step %d", step)
		}
	}
	if m.NodeCount < 2 {
		return fmt.Errorf("NodeCount must be >= 2, got %d", m.NodeCount)
	}
	if m.NodeFollowCount < 1 || m.NodeFollowCount >= m.NodeCount {
		return fmt.Errorf("NodeFollowCount must be in [1, NodeCount-1], got %d (NodeCount=%d)", m.NodeFollowCount, m.NodeCount)
	}

	networkType := strings.TrimSpace(m.NetworkType)
	if networkType != "Random" && networkType != "Explicit" {
		return fmt.Errorf("unsupported NetworkType %q (expected Random or Explicit)", m.NetworkType)
	}

	dynamicsType := strings.TrimSpace(m.DynamicsType)
	if dynamicsType == "" {
		return errors.New("DynamicsType must not be empty")
	}

	if err := m.validateDynamicsParams(dynamicsType); err != nil {
		return err
	}

	if strings.TrimSpace(m.RecsysFactoryType) == "" {
		return errors.New("RecsysFactoryType must not be empty")
	}
	if err := ValidateRecsysParams(m.RecSysParams); err != nil {
		return err
	}
	if err := m.validateRecsysFactoryType(dynamicsType); err != nil {
		return err
	}

	return nil
}

func validateFiniteFloat(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite, got %v", name, value)
	}
	return nil
}

func validateProbability(name string, value float64) error {
	if err := validateFiniteFloat(name, value); err != nil {
		return err
	}
	if value < 0 || value > 1 {
		return fmt.Errorf("%s must be in [0, 1], got %v", name, value)
	}
	return nil
}

func validateTolerance(name string, value float64) error {
	if err := validateFiniteFloat(name, value); err != nil {
		return err
	}
	if value < 0 {
		return fmt.Errorf("%s must be >= 0, got %v", name, value)
	}
	return nil
}

func (m *ScenarioMetadata) validateDynamicsParams(dynamicsType string) error {
	switch dynamicsType {
	case DynamicsTypeHK:
		if err := validateTolerance("HKParams.Tolerance", m.HKParams.Tolerance); err != nil {
			return err
		}
		if err := validateProbability("HKParams.Influence", m.HKParams.Influence); err != nil {
			return err
		}
		if err := validateProbability("HKParams.RewiringRate", m.HKParams.RewiringRate); err != nil {
			return err
		}
		if err := validateProbability("HKParams.RepostRate", m.HKParams.RepostRate); err != nil {
			return err
		}
	case DynamicsTypeDeffuant:
		if err := validateTolerance("DeffuantParams.Tolerance", m.DeffuantParams.Tolerance); err != nil {
			return err
		}
		if err := validateProbability("DeffuantParams.Influence", m.DeffuantParams.Influence); err != nil {
			return err
		}
		if err := validateProbability("DeffuantParams.RewiringRate", m.DeffuantParams.RewiringRate); err != nil {
			return err
		}
		if err := validateProbability("DeffuantParams.RepostRate", m.DeffuantParams.RepostRate); err != nil {
			return err
		}
	case DynamicsTypeGalam:
		if err := validateProbability("GalamParams.Influence", m.GalamParams.Influence); err != nil {
			return err
		}
		if err := validateProbability("GalamParams.RewiringRate", m.GalamParams.RewiringRate); err != nil {
			return err
		}
		if err := validateProbability("GalamParams.RepostRate", m.GalamParams.RepostRate); err != nil {
			return err
		}
	case DynamicsTypeVoter:
		if err := validateProbability("VoterParams.Influence", m.VoterParams.Influence); err != nil {
			return err
		}
		if err := validateProbability("VoterParams.RewiringRate", m.VoterParams.RewiringRate); err != nil {
			return err
		}
		if err := validateProbability("VoterParams.RepostRate", m.VoterParams.RepostRate); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported DynamicsType %q", m.DynamicsType)
	}

	return nil
}

func (m *ScenarioMetadata) validateRecsysFactoryType(dynamicsType string) error {
	allowed := make(map[string]struct{})

	switch dynamicsType {
	case DynamicsTypeHK:
		for name := range GetFloat64RecsysFactoriesWithParams[dynamics.HKParams](m.RecSysParams) {
			allowed[name] = struct{}{}
		}
	case DynamicsTypeDeffuant:
		for name := range GetFloat64RecsysFactoriesWithParams[dynamics.DeffuantParams](m.RecSysParams) {
			allowed[name] = struct{}{}
		}
	case DynamicsTypeGalam:
		for name := range GetBoolRecsysFactoriesWithParams[dynamics.GalamParams](m.RecSysParams) {
			allowed[name] = struct{}{}
		}
	case DynamicsTypeVoter:
		for name := range GetBoolRecsysFactoriesWithParams[dynamics.VoterParams](m.RecSysParams) {
			allowed[name] = struct{}{}
		}
	default:
		return fmt.Errorf("unsupported DynamicsType %q", m.DynamicsType)
	}

	if _, ok := allowed[m.RecsysFactoryType]; !ok {
		names := make([]string, 0, len(allowed))
		for name := range allowed {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Errorf("unsupported RecsysFactoryType %q for DynamicsType %q; allowed: %s", m.RecsysFactoryType, dynamicsType, strings.Join(names, ", "))
	}

	return nil
}

// getParam reads validated parameters. Numeric wire representations share the
// same float64 value regardless of JSON or MsgPack integer encoding.
func getParam[T any](params map[string]any, key string, defaultVal T) T {
	if params == nil {
		return defaultVal
	}
	v, ok := params[key]
	if !ok {
		return defaultVal
	}
	if _, numeric := any(defaultVal).(float64); numeric {
		if value, ok := numericParam(v); ok {
			return any(value).(T)
		}
	}
	typed, ok := v.(T)
	if !ok {
		return defaultVal
	}
	return typed
}

type recsysParamSpec struct {
	name        string
	boolean     bool
	nonnegative bool
	ratio       bool
}

var recsysParamSpecs = [...]recsysParamSpec{
	{name: "NoiseStd", nonnegative: true},
	{name: "OpRandomNoiseStd", nonnegative: true},
	{name: "UseCache", boolean: true},
	{name: "Tolerance", nonnegative: true},
	{name: "Steepness"},
	{name: "RandomRatio", ratio: true},
	{name: "MixRate", ratio: true},
	{name: "LogRecommendations", boolean: true},
}

func numericParam(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

// ValidateRecsysParamNames rejects obsolete spellings without requiring a full
// runnable scenario. Conversion belongs to explicit metadata/checkpoint migration.
func ValidateRecsysParamNames(params map[string]any) error {
	for name := range params {
		for _, spec := range recsysParamSpecs {
			canonical := spec.name
			if name != canonical && strings.EqualFold(name, canonical) {
				return fmt.Errorf("obsolete RecSysParams key %q; migrate metadata or checkpoints to %q with smp-migrate", name, canonical)
			}
		}
	}
	return nil
}

// ValidateRecsysParams validates executable parameter values without requiring
// a model, so simulation and counterfactual evaluation use the same rules.
func ValidateRecsysParams(params map[string]any) error {
	if err := ValidateRecsysParamNames(params); err != nil {
		return err
	}
	for _, spec := range recsysParamSpecs {
		value, exists := params[spec.name]
		if !exists {
			continue
		}
		if spec.boolean {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("RecSysParams.%s must be boolean", spec.name)
			}
			continue
		}
		n, ok := numericParam(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("RecSysParams.%s must be a finite number", spec.name)
		}
		if spec.ratio && (n < 0 || n > 1) {
			return fmt.Errorf("RecSysParams.%s must be in [0, 1]", spec.name)
		}
		if spec.nonnegative && n < 0 {
			return fmt.Errorf("RecSysParams.%s must be nonnegative", spec.name)
		}
	}
	return nil
}

func recommendationLogger(params map[string]any) func(string) {
	if !getParam(params, "LogRecommendations", true) {
		return nil
	}
	return func(message string) { fmt.Fprintln(os.Stderr, message) }
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
	logRecommendations := recommendationLogger(params)

	ret := map[string]model.RecsysFactory[float64, P]{
		"Random": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewRandom(h, nil)
		},
		"Opinion": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewOpinion(h, noiseStd, nil)
		},
		"Structure": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewStructure(h, noiseStd, nil, useCache, logRecommendations)
		},
		"OpinionRandom": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewOpinionRandom(h, nil, tolerance, steepness, opRandNoiseStd, randomRatio)
		},
		"StructureRandom": func(h *model.SMPModel[float64, P]) model.SMPModelRecommendationSystem[float64, P] {
			return recsys.NewStructureRandom(h, nil, steepness, noiseStd, randomRatio, useCache, logRecommendations)
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
	noiseStd := getParam(params, "NoiseStd", 0.1)
	opRandNoiseStd := getParam(params, "OpRandomNoiseStd", 2.0)
	useCache := getParam(params, "UseCache", true)
	tolerance := getParam(params, "Tolerance", 0.4)
	steepness := getParam(params, "Steepness", 1.0)
	randomRatio := getParam(params, "RandomRatio", 0.0)
	mixRate := getParam(params, "MixRate", 0.1)
	logRecommendations := recommendationLogger(params)

	ret := map[string]model.RecsysFactory[bool, P]{
		"Random": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewRandom(h, nil)
		},
		"Opinion": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewOpinion(h, noiseStd, nil)
		},
		"Structure": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewStructure(h, noiseStd, nil, useCache, logRecommendations)
		},
		"OpinionRandom": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewOpinionRandom(h, nil, tolerance, steepness, opRandNoiseStd, randomRatio)
		},
		"StructureRandom": func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
			return recsys.NewStructureRandom(h, nil, steepness, noiseStd, randomRatio, useCache, logRecommendations)
		},
	}

	ret["OpinionM9"] = func(h *model.SMPModel[bool, P]) model.SMPModelRecommendationSystem[bool, P] {
		return &recsys.Mix[bool, P]{
			Model:       h,
			RecSys1:     ret["Random"](h),
			RecSys2:     ret["Opinion"](h),
			RecSys1Rate: mixRate,
		}
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
