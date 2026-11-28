package groups

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
)

func TestLegacyPythonDetectorGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/legacy_detection.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ReferenceSHA string `json:"reference_sha256"`
		Epsilon      float64
		Cases        []struct {
			Name     string
			Opinions []float64
			Expected Detection
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ReferenceSHA == "" || len(fixture.Cases) < 7 {
		t.Fatal("missing independent numerical reference")
	}
	var detector Detector
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			got, err := detector.Detect(test.Opinions, DefaultOptions(fixture.Epsilon))
			if err != nil {
				t.Fatal(err)
			}
			want := test.Expected
			if got.MacroK != want.MacroK || got.DensityState != want.DensityState || !reflect.DeepEqual(got.Labels, want.Labels) || !reflect.DeepEqual(got.Residual, want.Residual) || got.LayerMacroK != want.LayerMacroK {
				t.Fatalf("classification differs from Python: got K=%d state=%s layer=%v; want K=%d state=%s layer=%v", got.MacroK, got.DensityState, got.LayerMacroK, want.MacroK, want.DensityState, want.LayerMacroK)
			}
			for i, c := range got.Cores {
				w := want.Cores[i]
				if c.ID != w.ID || c.Mass != w.Mass || c.BandwidthSupport != w.BandwidthSupport || c.BandwidthMatches != w.BandwidthMatches || !reflect.DeepEqual(c.Members, w.Members) {
					t.Fatalf("core %d identity/membership differs", i)
				}
				for _, pair := range [][2]float64{{c.Peak, w.Peak}, {c.Lower, w.Lower}, {c.Upper, w.Upper}, {c.BasinLower, w.BasinLower}, {c.BasinUpper, w.BasinUpper}, {c.SupportWidth, w.SupportWidth}, {c.PeakDensity, w.PeakDensity}, {c.BaselineDensity, w.BaselineDensity}, {c.DensityLevel, w.DensityLevel}} {
					if math.Abs(pair[0]-pair[1]) > 1e-9 {
						t.Fatalf("core %d numeric mismatch %.17g vs %.17g", i, pair[0], pair[1])
					}
				}
			}
		})
	}
}

func TestSparseConvolutionMatchesDenseReflectedKernel(t *testing.T) {
	for _, test := range []struct {
		grid      int
		bandwidth float64
	}{{4, .075}, {4, 3}, {32, .075}, {1024, .075}} {
		grid := test.grid
		o := DefaultOptions(.45)
		o.GridCells = grid
		o.Bandwidth = test.bandwidth
		o.MinCore = 1
		x := []float64{-1, -1, -.7, .11, .11, 1}
		var d Detector
		if _, err := d.Detect(x, o); err != nil {
			t.Fatal(err)
		}
		for k, f := range d.density {
			for i, v := range f {
				want := 0.
				kernel := d.kernels[k]
				radius := len(kernel) / 2
				for j, w := range kernel {
					want += w * d.counts[reflectIndex(i+j-radius, grid)] * float64(grid) / (2 * float64(len(x)))
				}
				if math.Abs(v-want) > 1e-12 {
					t.Fatalf("grid=%d band=%d index=%d sparse=%g dense=%g", grid, k, i, v, want)
				}
			}
		}
	}
}

func TestDetectorOwnsResultsAndReusesKernels(t *testing.T) {
	o := DefaultOptions(.45)
	x := make([]float64, 100)
	for i := range x {
		if i < 50 {
			x[i] = -.6
		} else {
			x[i] = .6
		}
	}
	before := append([]float64(nil), x...)
	var d Detector
	first, err := d.Detect(x, o)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(first)
	kernel := &d.kernels[1][0]
	for i := range x {
		x[i] = .2
	}
	if _, err := d.Detect(x, o); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(first)
	if string(saved) != string(after) || kernel != &d.kernels[1][0] {
		t.Fatal("result alias or rebuilt unchanged Gaussian workspace")
	}
	if reflect.DeepEqual(before, x) {
		t.Fatal("test did not change next input")
	}
	if len(first.Cores) != 2 {
		t.Fatal("expected detached two-core result")
	}
}

func TestDetectorValidationAndSubthresholdResidual(t *testing.T) {
	for _, x := range [][]float64{nil, {math.NaN()}, {math.Inf(1)}, {1.01}} {
		var d Detector
		if _, err := d.Detect(x, DefaultOptions(.45)); err == nil {
			t.Fatalf("accepted invalid opinions %v", x)
		}
	}
	for _, mutate := range []func(*Options){func(o *Options) { o.Bandwidth = 0 }, func(o *Options) { o.GridCells = 0 }, func(o *Options) { o.JaccardMin = 0 }, func(o *Options) { o.LineageOverlap = math.NaN() }, func(o *Options) { o.MinBandwidthSupport = 4 }} {
		o := DefaultOptions(.45)
		mutate(&o)
		var d Detector
		if _, err := d.Detect([]float64{0}, o); err == nil {
			t.Fatal("accepted invalid options")
		}
	}
	var d Detector
	got, err := d.Detect([]float64{-.5, 0, .5}, DefaultOptions(.45))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cores) != 0 || len(got.Residual) != 3 || got.DensityState != "unresolved" {
		t.Fatalf("forced subthreshold opinions into macrocores: %+v", got)
	}
}

func TestDetectorConservesMassAndRetainsAllAgents(t *testing.T) {
	for _, size := range []int{1, 25, 100, 500} {
		x := make([]float64, size)
		for i := range x {
			x[i] = -1 + 2*float64((i*73)%max(2, size))/float64(max(2, size)-1)
		}
		saved := append([]float64(nil), x...)
		var d Detector
		got, err := d.Detect(x, DefaultOptions(.45))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(saved, x) {
			t.Fatal("detector modified opinions")
		}
		for _, density := range d.density {
			mass := 0.
			for _, v := range density {
				if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatal("invalid reflected density")
				}
				mass += v * 2 / float64(d.grid)
			}
			if math.Abs(mass-1) > 1e-12 {
				t.Fatalf("reflected histogram lost mass: %.17g", mass)
			}
		}
		seen := make([]bool, size)
		for _, core := range got.Cores {
			for _, id := range core.Members {
				if seen[id] {
					t.Fatal("core memberships overlap")
				}
				seen[id] = true
			}
		}
		for _, id := range got.Residual {
			if seen[id] {
				t.Fatal("residual overlaps a core")
			}
			seen[id] = true
		}
		for _, found := range seen {
			if !found {
				t.Fatal("agent disappeared from the detector")
			}
		}
	}
}

func BenchmarkDetector500(b *testing.B) {
	for _, distribution := range []string{"compact", "diffuse"} {
		b.Run(distribution, func(b *testing.B) {
			x := make([]float64, 500)
			for i := range x {
				if distribution == "compact" {
					if i < 250 {
						x[i] = -.6
					} else {
						x[i] = .6
					}
				} else {
					x[i] = -1 + 2*float64(i)/499
				}
			}
			var d Detector
			o := DefaultOptions(.45)
			if _, err := d.Detect(x, o); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := d.Detect(x, o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
