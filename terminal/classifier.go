// Package terminal defines the common measure-level terminal classifier used
// by microscopic and lifted simulations.
package terminal

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

const (
	StatusAbsorbed      = "absorbed"
	StatusNonterminal   = "nonterminal"
	StatusGridAmbiguous = "grid_ambiguous"
)

type Options struct {
	Epsilon            float64 `json:"epsilon"`
	OccupiedMass       float64 `json:"occupied_mass"`
	MajorMass          float64 `json:"major_mass"`
	PositionResolution float64 `json:"position_resolution"`
	MassResolution     float64 `json:"mass_resolution"`
}

type Component struct {
	Minimum float64 `json:"minimum"`
	Maximum float64 `json:"maximum"`
	Mass    float64 `json:"mass"`
	Major   bool    `json:"major"`
}

type Margins struct {
	GapToEpsilon      float64 `json:"gap_to_epsilon"`
	DiameterToEpsilon float64 `json:"diameter_to_epsilon"`
	MassToMajorCutoff float64 `json:"mass_to_major_cutoff"`
}

type Result struct {
	Status     string      `json:"status"`
	Category   string      `json:"category"`
	KAll       int         `json:"k_all"`
	KMajor     int         `json:"k_major"`
	Components []Component `json:"components"`
	Margins    Margins     `json:"margins"`
}

type atom struct {
	position float64
	mass     float64
}

func validate(options Options, positions, masses []float64) error {
	if len(positions) != len(masses) {
		return errors.New("positions and masses must have equal length")
	}
	if !isFinite(options.Epsilon) || options.Epsilon < 0 {
		return errors.New("epsilon must be finite and non-negative")
	}
	if !isFinite(options.OccupiedMass) || options.OccupiedMass < 0 {
		return errors.New("occupied_mass must be finite and non-negative")
	}
	if !isFinite(options.MajorMass) || options.MajorMass <= 0 {
		return errors.New("major_mass must be finite and positive")
	}
	if !isFinite(options.PositionResolution) || options.PositionResolution < 0 {
		return errors.New("position_resolution must be finite and non-negative")
	}
	if !isFinite(options.MassResolution) || options.MassResolution < 0 {
		return errors.New("mass_resolution must be finite and non-negative")
	}
	for index := range positions {
		if !isFinite(positions[index]) {
			return fmt.Errorf("position %d is not finite", index)
		}
		if !isFinite(masses[index]) || masses[index] < 0 {
			return fmt.Errorf("mass %d must be finite and non-negative", index)
		}
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func category(count int) string {
	switch count {
	case 1:
		return "k1"
	case 2:
		return "k2"
	case 3:
		return "k3"
	default:
		return "k4plus"
	}
}

func minimum(current, candidate float64) float64 {
	if current < 0 || candidate < current {
		return candidate
	}
	return current
}

// Classify applies the same support-component functional to any atomic
// probability measure. Positions may be unsorted and duplicated.
func Classify(positions, masses []float64, options Options) (Result, error) {
	if err := validate(options, positions, masses); err != nil {
		return Result{}, err
	}
	atoms := make([]atom, 0, len(positions))
	for index, position := range positions {
		if masses[index] > 0 {
			atoms = append(atoms, atom{position: position, mass: masses[index]})
		}
	}
	sort.Slice(atoms, func(i, j int) bool { return atoms[i].position < atoms[j].position })
	aggregated := make([]atom, 0, len(atoms))
	for _, item := range atoms {
		last := len(aggregated) - 1
		if last >= 0 && aggregated[last].position == item.position {
			aggregated[last].mass += item.mass
		} else {
			aggregated = append(aggregated, item)
		}
	}
	occupied := make([]atom, 0, len(aggregated))
	for _, item := range aggregated {
		if item.mass >= options.OccupiedMass {
			occupied = append(occupied, item)
		}
	}
	result := Result{
		Status:   StatusNonterminal,
		Category: "censored",
		Margins: Margins{
			GapToEpsilon:      -1,
			DiameterToEpsilon: -1,
			MassToMajorCutoff: -1,
		},
	}
	if len(occupied) == 0 {
		return result, nil
	}

	type indexRange struct{ first, last int }
	ranges := []indexRange{{first: 0, last: 0}}
	ambiguous := false
	for index := 1; index < len(occupied); index++ {
		gap := occupied[index].position - occupied[index-1].position
		gapMargin := math.Abs(gap - options.Epsilon)
		result.Margins.GapToEpsilon = minimum(result.Margins.GapToEpsilon, gapMargin)
		if options.PositionResolution > 0 && gapMargin <= options.PositionResolution {
			ambiguous = true
		}
		if gap > options.Epsilon {
			ranges = append(ranges, indexRange{first: index, last: index})
		} else {
			ranges[len(ranges)-1].last = index
		}
	}

	absorbed := true
	largestMass := 0.0
	for _, span := range ranges {
		component := Component{
			Minimum: occupied[span.first].position,
			Maximum: occupied[span.last].position,
		}
		for index := span.first; index <= span.last; index++ {
			component.Mass += occupied[index].mass
		}
		diameter := component.Maximum - component.Minimum
		diameterMargin := math.Abs(options.Epsilon - diameter)
		result.Margins.DiameterToEpsilon = minimum(
			result.Margins.DiameterToEpsilon,
			diameterMargin,
		)
		if diameter > options.Epsilon {
			absorbed = false
		}
		if options.PositionResolution > 0 && diameterMargin <= options.PositionResolution {
			ambiguous = true
		}
		massMargin := math.Abs(component.Mass - options.MajorMass)
		result.Margins.MassToMajorCutoff = minimum(
			result.Margins.MassToMajorCutoff,
			massMargin,
		)
		if options.MassResolution > 0 && massMargin <= options.MassResolution {
			ambiguous = true
		}
		component.Major = component.Mass >= options.MajorMass
		if component.Major {
			result.KMajor++
		}
		largestMass = math.Max(largestMass, component.Mass)
		result.Components = append(result.Components, component)
	}
	result.KAll = len(result.Components)
	if result.KMajor == 0 && largestMass > 0 {
		result.KMajor = 1
	}
	if ambiguous {
		result.Status = StatusGridAmbiguous
		return result, nil
	}
	if !absorbed {
		return result, nil
	}
	result.Status = StatusAbsorbed
	result.Category = category(result.KMajor)
	return result, nil
}

// ClassifyOpinions classifies an empirical measure with one atom per agent.
func ClassifyOpinions(
	opinions []float64,
	epsilon float64,
	majorMass float64,
	positionResolution float64,
	massResolution float64,
) (Result, error) {
	if len(opinions) == 0 {
		return Result{}, errors.New("opinions must not be empty")
	}
	agentMass := 1 / float64(len(opinions))
	masses := make([]float64, len(opinions))
	for index := range masses {
		masses[index] = agentMass
	}
	return Classify(opinions, masses, Options{
		Epsilon:            epsilon,
		OccupiedMass:       0.5 * agentMass,
		MajorMass:          majorMass,
		PositionResolution: positionResolution,
		MassResolution:     massResolution,
	})
}
