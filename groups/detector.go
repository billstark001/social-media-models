// Package groups detects macroscopic opinion cores and tracks their qualified
// identities without inspecting follow edges, content, or simulation outcomes.
package groups

import (
	"fmt"
	"math"
)

// Options are operational measurement settings, not significance thresholds.
// Use DefaultOptions explicitly; a zero Options is deliberately invalid.
type Options struct {
	Epsilon             float64 `json:"epsilon" msgpack:"epsilon"`
	Bandwidth           float64 `json:"bandwidth" msgpack:"bandwidth"`
	GridCells           int     `json:"grid_cells" msgpack:"grid_cells"`
	MinCore             int     `json:"min_core" msgpack:"min_core"`
	Valley              float64 `json:"valley" msgpack:"valley"`
	Half                float64 `json:"half" msgpack:"half"`
	MinBandwidthSupport int     `json:"min_bandwidth_support" msgpack:"min_bandwidth_support"`
	Confirm             int     `json:"confirm" msgpack:"confirm"`
	Exit                int     `json:"exit" msgpack:"exit"`
	JaccardMin          float64 `json:"jaccard_min" msgpack:"jaccard_min"`
	LineageOverlap      float64 `json:"lineage_overlap" msgpack:"lineage_overlap"`
}

func DefaultOptions(epsilon float64) Options {
	return Options{Epsilon: epsilon, Bandwidth: epsilon / 6, GridCells: 1024,
		MinCore: 25, Valley: .5, Half: .5, MinBandwidthSupport: 2,
		Confirm: 3, Exit: 3, JaccardMin: .5, LineageOverlap: .5}
}

func (o Options) Validate() error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !finite(o.Epsilon) || o.Epsilon <= 0 || !finite(o.Bandwidth) || o.Bandwidth <= 0 {
		return fmt.Errorf("group epsilon and bandwidth must be positive and finite")
	}
	if o.GridCells < 4 || o.GridCells > 65536 || o.MinCore < 1 || o.Confirm < 1 || o.Exit < 1 {
		return fmt.Errorf("invalid group grid/mass/confirmation settings")
	}
	if !finite(o.Valley) || o.Valley <= 0 || o.Valley >= 1 || !finite(o.Half) || o.Half <= 0 || o.Half >= 1 {
		return fmt.Errorf("group valley and half levels must be in (0,1)")
	}
	if o.MinBandwidthSupport < 1 || o.MinBandwidthSupport > 3 ||
		!finite(o.JaccardMin) || o.JaccardMin <= 0 || o.JaccardMin > 1 ||
		!finite(o.LineageOverlap) || o.LineageOverlap <= 0 || o.LineageOverlap > 1 {
		return fmt.Errorf("invalid group bandwidth/Jaccard/lineage support")
	}
	if o.Bandwidth*math.Sqrt2/(2/float64(o.GridCells))*5 > 1_000_000 {
		return fmt.Errorf("group smoothing kernel exceeds allocation limit")
	}
	return nil
}

// Core.ID is local to one Detection, ordered by opinion peak. Members are dense
// agent indices, sorted by ID. Bounds are closed half-prominence bin edges;
// basin bounds are density-grid centers at the neighboring valleys.
type Core struct {
	ID               int     `json:"id" msgpack:"id"`
	Members          []int   `json:"members" msgpack:"members"`
	Peak             float64 `json:"peak" msgpack:"peak"`
	Lower            float64 `json:"lower" msgpack:"lower"`
	Upper            float64 `json:"upper" msgpack:"upper"`
	BasinLower       float64 `json:"basin_lower" msgpack:"basin_lower"`
	BasinUpper       float64 `json:"basin_upper" msgpack:"basin_upper"`
	Mass             int     `json:"mass" msgpack:"mass"`
	SupportWidth     float64 `json:"support_width" msgpack:"support_width"`
	PeakDensity      float64 `json:"peak_density" msgpack:"peak_density"`
	BaselineDensity  float64 `json:"baseline_density" msgpack:"baseline_density"`
	DensityLevel     float64 `json:"density_level" msgpack:"density_level"`
	BandwidthSupport int     `json:"bandwidth_support" msgpack:"bandwidth_support"`
	BandwidthMatches [3]bool `json:"bandwidth_matches" msgpack:"bandwidth_matches"`
}

type Detection struct {
	Cores        []Core     `json:"cores" msgpack:"cores"`
	Labels       []int      `json:"labels" msgpack:"labels"`
	Residual     []int      `json:"residual" msgpack:"residual"`
	MacroK       int        `json:"macro_k" msgpack:"macro_k"`
	DensityState string     `json:"density_state" msgpack:"density_state"`
	Bandwidths   [3]float64 `json:"bandwidths" msgpack:"bandwidths"`
	LayerMacroK  [3]int     `json:"layer_macro_k" msgpack:"layer_macro_k"`
}

type mode struct {
	peak, lower, upper, basinLower, basinUpper, peakDensity, baseline, level float64
	mass                                                                     int
}

// Detector reuses histogram, Gaussian kernels, density and mode scratch. Its
// zero value is ready to use. Returned values own their storage; use one
// Detector per independent stream, not concurrently.
type Detector struct {
	grid                     int
	bandwidth                float64
	counts                   []float64
	occupied                 []int
	density                  [3][]float64
	kernels                  [3][]float64
	layers                   [3][]mode
	peaks, valleys, retained []int
}

func (d *Detector) prepare(o Options) {
	if d.grid == o.GridCells && d.bandwidth == o.Bandwidth {
		return
	}
	d.grid, d.bandwidth = o.GridCells, o.Bandwidth
	d.counts = make([]float64, o.GridCells)
	d.occupied = make([]int, 0, o.GridCells)
	dx := 2 / float64(o.GridCells)
	for k, h := range [3]float64{o.Bandwidth / math.Sqrt2, o.Bandwidth, o.Bandwidth * math.Sqrt2} {
		d.density[k] = make([]float64, o.GridCells)
		sigma := h / dx
		radius := int(5*sigma + .5)
		kernel := make([]float64, 2*radius+1)
		sum := 0.
		for j := range kernel {
			x := float64(j - radius)
			// A valid subnormal bandwidth can underflow sigma*sigma to zero.
			// Its radius is zero; the Gaussian center is exactly one, not 0/0.
			kernel[j] = 1
			if j != radius {
				kernel[j] = math.Exp(-.5 * x * x / (sigma * sigma))
			}
			sum += kernel[j]
		}
		for j := range kernel {
			kernel[j] /= sum
		}
		d.kernels[k] = kernel
	}
}

func reflectIndex(i, n int) int {
	// Default kernels are shorter than the domain. Avoid integer division in
	// their hot inner loop, retaining periodic reflection for wider kernels.
	if i >= 0 && i < n {
		return i
	}
	if i < 0 && i >= -n {
		return -1 - i
	}
	if i >= n && i < 2*n {
		return 2*n - 1 - i
	}
	i %= 2 * n
	if i < 0 {
		i += 2 * n
	}
	if i >= n {
		i = 2*n - 1 - i
	}
	return i
}

func minimum(f []float64, a, b int) int {
	best := a
	for i := a + 1; i <= b; i++ {
		if f[i] < f[best] {
			best = i
		}
	}
	return best
}

func countBetween(x []float64, lower, upper float64) int {
	n := 0
	for _, v := range x {
		if lower <= v && v <= upper {
			n++
		}
	}
	return n
}

func (d *Detector) modes(x []float64, layer int, o Options) []mode {
	f := d.density[layer]
	clear(f)
	kernel := d.kernels[layer]
	radius := len(kernel) / 2
	scale := float64(o.GridCells) / (2 * float64(len(x)))
	// Sparse reflected histogram convolution is the same finite Gaussian
	// kernel as scipy.ndimage.gaussian_filter1d(mode=reflect, truncate=5).
	for _, j := range d.occupied {
		weight := d.counts[j] * scale
		for k, v := range kernel {
			f[reflectIndex(j+k-radius, len(f))] += weight * v
		}
	}
	d.peaks = d.peaks[:0]
	if f[0] > f[1] {
		d.peaks = append(d.peaks, 0)
	}
	for i := 1; i < len(f)-1; i++ {
		if f[i] <= f[i-1] {
			continue
		}
		j := i
		for j+1 < len(f) && f[j+1] == f[i] {
			j++
		}
		if j < len(f)-1 && f[j] > f[j+1] {
			d.peaks = append(d.peaks, (i+j)/2)
		}
		i = j
	}
	if f[len(f)-1] > f[len(f)-2] {
		d.peaks = append(d.peaks, len(f)-1)
	}
	if len(d.peaks) == 0 {
		best := 0
		for i := range f {
			if f[i] > f[best] {
				best = i
			}
		}
		d.peaks = append(d.peaks, best)
	}
	axis := func(i int) float64 { return -1 + (float64(i)+.5)*2/float64(o.GridCells) }
	d.valleys = d.valleys[:0]
	for k := 1; k < len(d.peaks); k++ {
		d.valleys = append(d.valleys, minimum(f, d.peaks[k-1], d.peaks[k]))
	}
	d.retained = d.retained[:0]
	for k, p := range d.peaks {
		lo, hi := -1., 1.
		if k > 0 {
			lo = axis(d.valleys[k-1])
		}
		if k < len(d.valleys) {
			hi = axis(d.valleys[k])
		}
		if countBetween(x, lo, hi) >= o.MinCore {
			d.retained = append(d.retained, p)
		}
	}
	for len(d.retained) > 1 {
		bestRatio, bestIndex := o.Valley, -1
		for k := 1; k < len(d.retained); k++ {
			a, b := d.retained[k-1], d.retained[k]
			ratio := f[minimum(f, a, b)] / math.Max(math.Min(f[a], f[b]), 1e-300)
			if ratio > o.Valley && (bestIndex < 0 || ratio >= bestRatio) {
				bestRatio, bestIndex = ratio, k-1
			}
		}
		if bestIndex < 0 {
			break
		}
		remove := bestIndex + 1
		if f[d.retained[bestIndex]] < f[d.retained[bestIndex+1]] {
			remove = bestIndex
		}
		d.retained = append(d.retained[:remove], d.retained[remove+1:]...)
	}
	d.valleys = d.valleys[:0]
	for k := 1; k < len(d.retained); k++ {
		d.valleys = append(d.valleys, minimum(f, d.retained[k-1], d.retained[k]))
	}
	result := d.layers[layer][:0]
	for k, p := range d.retained {
		lo, hi := 0, len(f)-1
		if k > 0 {
			lo = d.valleys[k-1]
		}
		if k < len(d.valleys) {
			hi = d.valleys[k]
		}
		baseline := 0.
		if k > 0 {
			baseline = f[lo]
		}
		if k < len(d.valleys) {
			baseline = math.Max(baseline, f[hi])
		}
		level := baseline + o.Half*(f[p]-baseline)
		if level <= baseline && f[p] > baseline {
			// Preserve the strictly positive half-prominence increment even
			// when it is smaller than the baseline's representable spacing.
			// Including the shared valley would assign its agents to two cores.
			level = math.Nextafter(baseline, f[p])
		}
		a, b := p, p
		for a > lo && f[a-1] >= level {
			a--
		}
		for b < hi && f[b+1] >= level {
			b++
		}
		lower, upper := -1., 1.
		if a > 0 {
			lower = -1 + float64(a)*2/float64(o.GridCells)
		}
		if b < len(f)-1 {
			upper = -1 + float64(b+1)*2/float64(o.GridCells)
		}
		mass := countBetween(x, lower, upper)
		if mass >= o.MinCore {
			result = append(result, mode{peak: axis(p), lower: lower, upper: upper, basinLower: axis(lo), basinUpper: axis(hi), mass: mass, peakDensity: f[p], baseline: baseline, level: level})
		}
	}
	d.layers[layer] = result
	return result
}

func (d *Detector) Detect(x []float64, o Options) (Detection, error) {
	if err := o.Validate(); err != nil {
		return Detection{}, err
	}
	if len(x) == 0 {
		return Detection{}, fmt.Errorf("group detection requires opinions")
	}
	for _, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < -1 || v > 1 {
			return Detection{}, fmt.Errorf("group opinions must be finite in [-1,1]")
		}
	}
	d.prepare(o)
	clear(d.counts)
	d.occupied = d.occupied[:0]
	for _, v := range x {
		j := int((v + 1) * float64(o.GridCells) / 2)
		if j == o.GridCells {
			j--
		}
		d.counts[j]++
	}
	for j, v := range d.counts {
		if v != 0 {
			d.occupied = append(d.occupied, j)
		}
	}
	result := Detection{Cores: make([]Core, 0), Labels: make([]int, len(x)), Residual: make([]int, 0),
		Bandwidths: [3]float64{o.Bandwidth / math.Sqrt2, o.Bandwidth, o.Bandwidth * math.Sqrt2}}
	for i := range result.Labels {
		result.Labels[i] = -1
	}
	for k := 0; k < 3; k++ {
		d.modes(x, k, o)
		result.LayerMacroK[k] = len(d.layers[k])
	}
	for _, candidate := range d.layers[1] {
		matches := [3]bool{}
		support := 0
		for k, layer := range d.layers {
			for _, other := range layer {
				if math.Abs(other.peak-candidate.peak) <= math.Max(o.Bandwidth, result.Bandwidths[k]) {
					matches[k] = true
					support++
					break
				}
			}
		}
		if support < o.MinBandwidthSupport {
			continue
		}
		core := Core{ID: len(result.Cores), Peak: candidate.peak, Lower: candidate.lower, Upper: candidate.upper,
			BasinLower: candidate.basinLower, BasinUpper: candidate.basinUpper, Mass: candidate.mass,
			PeakDensity: candidate.peakDensity, BaselineDensity: candidate.baseline, DensityLevel: candidate.level,
			BandwidthSupport: support, BandwidthMatches: matches, Members: make([]int, 0, candidate.mass)}
		minX, maxX := math.Inf(1), math.Inf(-1)
		for i, v := range x {
			if core.Lower <= v && v <= core.Upper {
				core.Members = append(core.Members, i)
				result.Labels[i] = core.ID
				minX = math.Min(minX, v)
				maxX = math.Max(maxX, v)
			}
		}
		core.SupportWidth = maxX - minX
		result.Cores = append(result.Cores, core)
	}
	for i, label := range result.Labels {
		if label < 0 {
			result.Residual = append(result.Residual, i)
		}
	}
	result.MacroK = len(result.Cores)
	switch {
	case result.MacroK > 1:
		result.DensityState = "multiple_macrocores"
	case result.MacroK == 1 && result.Cores[0].SupportWidth <= o.Epsilon:
		result.DensityState = "single_compact_mode"
	case result.MacroK == 1:
		result.DensityState = "diffuse_single_mode"
	default:
		result.DensityState = "unresolved"
	}
	return result, nil
}
