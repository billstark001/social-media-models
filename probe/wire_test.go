package probe_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/probe"
	smprng "smp/rng"
	"smp/simulation"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

func TestHEADForceMsgpackWithCLIJSONTagConfiguration(t *testing.T) {
	request := &probe.Request{
		Version: 1, RNG: smprng.FixedSpec(11, 22), DynamicsType: "HK",
		HKParams:          dynamics.HKParams{Tolerance: .5, Influence: 1},
		RecsysFactoryType: "Random", RecsysCount: 1, Grid: probe.Grid{Min: 0, Max: 0, Step: 1},
		States: []probe.FrozenState{{
			Step: 0, Graph: utils.NetworkXGraph{Directed: true, Nodes: map[int64]map[string]any{0: {}}, Adjacency: map[int64]map[int64]any{0: {}}},
			Opinions: []float64{0}, Posts: map[int64][]model.PostRecord[float64]{0: {{AgentID: 0, Step: -1, Opinion: 0}}}, Precision: "history_f32",
		}},
	}
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture generated from commit 9b090ce. Every map
	// has at most one key, so these hashes do not depend on map iteration.
	for _, fixture := range []struct {
		name   string
		value  any
		digest string
	}{
		{"request", request, "ef55742a3501bd491a20ec6ee82650229439713e508a0c2006ebd5163fd64aec"},
		{"response", response, "1b596b594d8ed139824f935604690ae78d1f37c290dcccaab12335428152594b"},
	} {
		for _, jsonTags := range []bool{false, true} {
			t.Run(fixture.name+map[bool]string{false: "/default", true: "/json-tags"}[jsonTags], func(t *testing.T) {
				var output bytes.Buffer
				encoder := msgpack.NewEncoder(&output)
				if jsonTags {
					encoder.SetCustomStructTag("json")
				}
				if err := encoder.Encode(fixture.value); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(output.Bytes())
				if hex.EncodeToString(digest[:]) != fixture.digest {
					t.Fatalf("HEAD %s encoding changed: %x", fixture.name, digest)
				}
				if fixture.name == "request" {
					decoder := msgpack.NewDecoder(bytes.NewReader(output.Bytes()))
					decoder.SetCustomStructTag("json")
					var decoded probe.Request
					if err := decoder.Decode(&decoded); err != nil {
						t.Fatal(err)
					}
					if _, err := probe.Evaluate(&decoded); err != nil {
						t.Fatalf("HEAD force payload no longer evaluates: %v", err)
					}
				}
			})
		}
	}
}

func TestHEADRandomForceSampling(t *testing.T) {
	request := requestFor(simulation.DynamicsTypeHK)
	request.AnchorIDs = nil
	request.RecsysFactoryType = "Random"
	request.Replicates = 3
	request.Grid.Step = .5
	second := frozenState()
	second.Step++
	request.States = append(request.States, second)
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	// Discrete recommendations from 9b090ce, indexed by state, replicate,
	// and anchor. Reproduced with Go 1.24.1 and 1.27.4 on windows/amd64.
	// The previous hash (0773556e...) is exactly reproduced by fused multiply-add
	// in both Welford's m2 accumulation and E[delta²] - E[delta]². Compare independent
	// mathematical summaries to tolerate that platform rounding difference while
	// guarding the sampled recommendations. Wire encoding is checked above.
	recommendedIDs := [2][3][4]int{
		{{2, 0, 1, 2}, {2, 0, 0, 2}, {2, 3, 1, 1}},
		{{3, 0, 1, 2}, {3, 0, 1, 1}, {3, 0, 1, 2}},
	}
	if response.Version != probe.ProtocolVersion || response.RNG != request.RNG || response.DynamicsType != request.DynamicsType || len(response.Results) != len(recommendedIDs) {
		t.Fatalf("unexpected response envelope: %+v", response)
	}
	for stateIndex, result := range response.Results {
		if result.Step != request.States[stateIndex].Step || result.Precision != request.States[stateIndex].Precision || len(result.Points) != 5 {
			t.Fatalf("unexpected state result: %+v", result)
		}
		for pointIndex, point := range result.Points {
			t.Run(fmt.Sprintf("state-%d/x-%g", stateIndex, point.X), func(t *testing.T) {
				check := func(name string, got, want float64) {
					t.Helper()
					if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-14 {
						t.Fatalf("%s: got %.17g, want %.17g", name, got, want)
					}
				}
				x := -1 + float64(pointIndex)*.5
				check("x", point.X, x)
				var samples [4][]float64
				var active [4]int
				neighbors, recommendations, zeros := 0, 0, 0
				for _, replicate := range recommendedIDs[stateIndex] {
					for anchor, recommendedID := range replicate {
						opinions := request.States[stateIndex].Opinions
						neighbor, recommended, n, r := 0.0, 0.0, 0, 0
						if gap := opinions[(anchor+1)%4] - x; math.Abs(gap) <= .75 {
							neighbor, n = gap, 1
						}
						if gap := opinions[recommendedID] - x; math.Abs(gap) <= .75 {
							recommended, r = gap, 1
						}
						if count := n + r; count > 0 {
							neighbor *= .5 / float64(count)
							recommended *= .5 / float64(count)
							active[0]++
							active[3]++
						}
						active[1] += n
						active[2] += r
						total := neighbor + recommended
						for i, value := range [4]float64{total, neighbor, recommended, total * total} {
							samples[i] = append(samples[i], value)
						}
						neighbors += n
						recommendations += r
						if total == 0 {
							zeros++
						}
					}
				}
				var means [4]float64
				for i, got := range [4]probe.Summary{point.FProbe, point.FNeighbor, point.FRecommendation, point.UpdateSecondMoment} {
					for _, value := range samples[i] {
						means[i] += value / float64(len(samples[i]))
					}
					variance := 0.0
					for _, value := range samples[i] {
						variance += (value - means[i]) * (value - means[i]) / float64(len(samples[i]))
					}
					check(fmt.Sprintf("summary-%d mean", i), got.Mean, means[i])
					check(fmt.Sprintf("summary-%d variance", i), got.Variance, variance)
					if got.Samples != 12 || got.Active != active[i] {
						t.Fatalf("summary-%d sample counts: %+v; want samples=12 active=%d", i, got, active[i])
					}
				}
				check("mean concordant neighbors", point.MeanConcordantNeighbor, float64(neighbors)/12)
				check("mean concordant recommendations", point.MeanConcordantRecommendation, float64(recommendations)/12)
				check("no-update probability", point.NoUpdateProbability, float64(zeros)/12)
				check("update variance", point.UpdateVariance, means[3]-means[0]*means[0])
			})
		}
	}
}
