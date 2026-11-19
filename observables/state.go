package observables

import (
	"errors"

	"smp/terminal"

	"gonum.org/v1/gonum/graph/simple"
)

type EnergyOptions struct {
	Epsilon float64 `json:"epsilon" msgpack:"epsilon"`
	Scale   float64 `json:"scale" msgpack:"scale"`
}

type PathwayOptions struct {
	Epsilon float64 `json:"epsilon" msgpack:"epsilon"`
}

type LandscapeOptions struct {
	Epsilon float64   `json:"epsilon" msgpack:"epsilon"`
	Scale   float64   `json:"scale" msgpack:"scale"`
	Queries []float64 `json:"queries" msgpack:"queries"`
}

// StateOptions selects measurements of a frozen opinion state. Each selected
// measurement validates its own inputs; omitted measurements impose no
// requirements. Callers supply defaults before measuring.
type StateOptions struct {
	Energy    *EnergyOptions    `json:"energy,omitempty" msgpack:"energy,omitempty"`
	Pathway   *PathwayOptions   `json:"pathway,omitempty" msgpack:"pathway,omitempty"`
	Bins      []float64         `json:"bins,omitempty" msgpack:"bins,omitempty"`
	Landscape *LandscapeOptions `json:"landscape,omitempty" msgpack:"landscape,omitempty"`
	// Terminal classifies the empirical population. OccupiedMass must be zero;
	// the canonical empirical cutoff is fixed at 0.5/N.
	Terminal *terminal.Options `json:"terminal,omitempty" msgpack:"terminal,omitempty"`
}

type StateResult struct {
	Energy    *Energy          `json:"energy,omitempty" msgpack:"energy,omitempty"`
	Pathway   *PathwayState    `json:"pathway,omitempty" msgpack:"pathway,omitempty"`
	Binned    *BinnedState     `json:"binned,omitempty" msgpack:"binned,omitempty"`
	Landscape *Landscape       `json:"landscape,omitempty" msgpack:"landscape,omitempty"`
	Terminal  *terminal.Result `json:"terminal,omitempty" msgpack:"terminal,omitempty"`
}

// StateMeasurer reuses pathway workspace without owning or advancing a model.
// Its zero value is ready to use. Use one per independent evaluation stream.
type StateMeasurer struct {
	Pathway PathwayMeasurer
}

// Measure computes the selected canonical observables without modifying the
// input state or consuming random streams. Results do not reference workspace
// that a later call will overwrite.
func (m *StateMeasurer) Measure(opinions []float64, graph *simple.DirectedGraph, options StateOptions) (StateResult, error) {
	var result StateResult
	if options.Energy != nil {
		value, err := MeasureEnergy(opinions, graph, options.Energy.Epsilon, options.Energy.Scale)
		if err != nil {
			return result, err
		}
		result.Energy = &value
	}
	if len(options.Bins) > 0 {
		value, err := MeasureBinnedState(opinions, graph, options.Bins)
		if err != nil {
			return result, err
		}
		result.Binned = &value
	}
	if options.Pathway != nil {
		value, err := m.Pathway.Measure(opinions, graph, options.Pathway.Epsilon)
		if err != nil {
			return result, err
		}
		result.Pathway = &value
	}
	if options.Landscape != nil {
		value, err := MeasureLandscape(opinions, graph, options.Landscape.Epsilon, options.Landscape.Scale, options.Landscape.Queries)
		if err != nil {
			return result, err
		}
		result.Landscape = &value
	}
	if options.Terminal != nil {
		if options.Terminal.OccupiedMass != 0 {
			return result, errors.New("terminal occupied_mass must be zero for empirical opinions; the cutoff is fixed at 0.5/N")
		}
		value, err := terminal.ClassifyOpinions(opinions, options.Terminal.Epsilon, options.Terminal.MajorMass, options.Terminal.PositionResolution, options.Terminal.MassResolution)
		if err != nil {
			return result, err
		}
		result.Terminal = &value
	}
	return result, nil
}
