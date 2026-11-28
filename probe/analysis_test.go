package probe_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"smp/bridges"
	"smp/groups"
	"smp/probe"
	"smp/simulation"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
)

// A saved run with two stable cores and a holder that loses and regains
// opinion compatibility. Deliberately unusable force configuration proves
// static observers do not construct a simulator or resolve its RNG.
func analysisHistory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	metadata := map[string]any{"DynamicsType": "HK", "NodeCount": 101,
		"PostRetainCount": 0, "RecsysCount": -1, "RecsysFactoryType": "not-a-recommender",
		"RNG":      map[string]any{"Algorithm": "not-an-rng"},
		"HKParams": map[string]any{"Tolerance": .65, "RewiringRate": 0, "RepostRate": 0}}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	state := &simulation.AccumulativeModelState{}
	for step := 0; step <= 8; step++ {
		x := make([]float64, 101)
		for i := 0; i < 100; i++ {
			if i < 50 {
				x[i] = -.6
			} else {
				x[i] = .6
			}
		}
		if step == 4 || step == 5 {
			x[100] = .95
		}
		state.Opinions = append(state.Opinions, x)
		state.AgentNumbers = append(state.AgentNumbers, make([][4]int16, 101))
		state.AgentOpinionSums = append(state.AgentOpinionSums, make([][4]float64, 101))
	}
	if err := simulation.SaveAccumulativeModelState(filepath.Join(dir, "acc-state-2026-11-28T000000Z.lz4"), state); err != nil {
		t.Fatal(err)
	}
	g := utils.NetworkXGraph{Directed: true, Nodes: make(map[int64]map[string]any), Adjacency: make(map[int64]map[int64]any)}
	for i := int64(0); i < 101; i++ {
		g.Nodes[i] = map[string]any{}
		g.Adjacency[i] = make(map[int64]any)
		if i < 100 {
			g.Adjacency[i][100] = map[string]any{}
		}
	}
	g.Adjacency[100][0] = map[string]any{}
	data, err = msgpack.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph-0.msgpack"), data, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func analysisRequest(dir string) *probe.Request {
	epsilon := .65
	return &probe.Request{RunDirectory: dir, Epsilon: &epsilon, Measurements: []string{"groups", "bridges"}, Scan: &probe.ScanOptions{}}
}

func TestAnalysisOutputThinningKeepsNativeObservationsAndLifecycles(t *testing.T) {
	dir := analysisHistory(t)
	before := staticHistoryHashes(t, dir)
	request := analysisRequest(dir)
	dense, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Scan = &probe.ScanOptions{OutputStride: 4}
	thin, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if dense.Analysis == nil || dense.Analysis.Observations != 9 || dense.Analysis.Sampled || len(dense.Analysis.BridgeEvents) == 0 || len(dense.Analysis.BridgeEpisodes) == 0 {
		t.Fatalf("missing dense lifecycle analysis: %+v", dense.Analysis)
	}
	if !reflect.DeepEqual(dense.Analysis, thin.Analysis) {
		t.Fatal("output thinning changed lifecycle observation or event records")
	}
	if len(dense.Results) != 9 || len(thin.Results) != 3 || thin.Results[0].Step != 0 || thin.Results[1].Step != 4 || thin.Results[2].Step != 8 {
		t.Fatalf("unexpected thinning: %+v", thin.Results)
	}
	if !thin.RNG.IsZero() {
		t.Fatal("static lifecycle observers instantiated force RNG")
	}
	again, err := probe.Evaluate(request)
	if err != nil || !reflect.DeepEqual(thin, again) {
		t.Fatalf("independent requests leaked observer state: %v", err)
	}
	if after := staticHistoryHashes(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("analysis changed source files")
	}
	if len(request.States) != 0 {
		t.Fatal("streaming analysis mutated caller request")
	}
}

func TestAnalysisBridgeConfigurationOwnsFixedPairs(t *testing.T) {
	request := analysisRequest(analysisHistory(t))
	options := bridges.DefaultOptions(.65)
	options.FixedPairs = [][2]int64{{1, 2}}
	request.Bridges = &options
	request.BridgeCores = [][]int{{0}, {50}, {100}}
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Analysis.BridgeOptions == nil || len(response.Analysis.BridgeOptions.FixedPairs) != 1 {
		t.Fatal("missing recorded fixed-pair configuration")
	}
	response.Analysis.BridgeOptions.FixedPairs[0] = [2]int64{2, 3}
	if request.Bridges.FixedPairs[0] != [2]int64{1, 2} {
		t.Fatal("mutating response configuration changed the caller request")
	}
	again, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range again.Results {
		if len(result.Bridges.Pairs) != 1 || result.Bridges.Pairs[0].GroupIDs != [2]int64{1, 2} {
			t.Fatal("response mutation changed a later observer's fixed-pair selection")
		}
	}
	request.Bridges.FixedPairs[0] = [2]int64{1, 3}
	if again.Analysis.BridgeOptions.FixedPairs[0] != [2]int64{1, 2} {
		t.Fatal("mutating the caller request changed recorded response configuration")
	}
	if response.Analysis.BridgeOptions.FixedPairs[0] != [2]int64{2, 3} {
		t.Fatal("independent responses share recorded configuration")
	}
}

func TestAnalysisOmittedStepsIsLastStateAndHorizonCloseIsCensored(t *testing.T) {
	request := analysisRequest(analysisHistory(t))
	request.Scan = nil
	last, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Results) != 1 || last.Results[0].Step != 8 || last.Analysis.Observations != 1 {
		t.Fatalf("omitted steps ceased to mean last: %+v", last)
	}
	end := 6
	request.Scan = &probe.ScanOptions{End: &end}
	cut, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if cut.Analysis.LastStep != 6 || *cut.SourceMaxStep != 8 {
		t.Fatalf("lost horizons: %+v", cut.Analysis)
	}
	closed := 0
	for _, e := range cut.Analysis.Epochs {
		if e.State == "closed" {
			closed++
			if e.Reason != "analysis_horizon" {
				t.Fatalf("truncation mislabelled epoch close: %+v", e)
			}
		}
	}
	if closed == 0 {
		t.Fatal("test did not produce a bridge epoch close")
	}
	for _, e := range cut.Analysis.BridgeEpisodes {
		if e.RightCensored && e.Reason != "analysis_horizon" {
			t.Fatalf("truncation mislabelled episode: %+v", e)
		}
	}
	for _, e := range cut.Analysis.GroupEvents {
		if e.RightCensored && e.Reason != "analysis_horizon" {
			t.Fatalf("truncation mislabelled group close: %+v", e)
		}
	}
}

func TestAnalysisLifecycleOutputSwitchesDoNotDisableObservation(t *testing.T) {
	request := analysisRequest(analysisHistory(t))
	dense, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	include := false
	request.Scan.IncludeEvents, request.Scan.IncludeEpisodes = &include, &include
	compact, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if compact.Analysis.Observations != dense.Analysis.Observations || !reflect.DeepEqual(compact.Analysis.Summaries, dense.Analysis.Summaries) || !reflect.DeepEqual(compact.Analysis.Epochs, dense.Analysis.Epochs) {
		t.Fatal("disabling lifecycle detail changed observation or summary accounting")
	}
	if len(compact.Analysis.GroupEvents) != 0 || len(compact.Analysis.BridgeEvents) != 0 || len(compact.Analysis.BridgeEpisodes) != 0 {
		t.Fatal("disabled lifecycle detail was retained")
	}
}

func TestAnalysisSparseObservationsAreUnknownAndFixedReferencesAreSeparate(t *testing.T) {
	request := analysisRequest(analysisHistory(t))
	request.Scan.Stride = 2
	request.BridgeCores = [][]int{{0}, {50}}
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Analysis.ReferenceMode != "fixed_cohorts" || !response.Analysis.Sampled || response.Analysis.Observations != 5 {
		t.Fatalf("lost sparse/fixed contract: %+v", response.Analysis)
	}
	if len(response.Results[0].Groups.Groups) != 0 || len(response.Results[0].Bridges.Pairs) != 1 {
		t.Fatal("fixed bridge cohorts were confused with confirmed dynamic groups")
	}
	unknown := 0
	for _, event := range response.Analysis.GroupEvents {
		if event.Type == "unknown_interval" {
			unknown++
		}
		if event.Type == "birth" || event.Type == "fade" {
			t.Fatalf("gap produced observed native group lifecycle: %+v", event)
		}
	}
	if unknown != 4 {
		t.Fatalf("want four unknown intervals, got %d", unknown)
	}
	for _, event := range response.Analysis.BridgeEvents {
		if event.Type == "formation" || event.Type == "loss" || event.Type == "regeneration" {
			t.Fatalf("gap invented bridge transition: %+v", event)
		}
	}
	for _, result := range response.Results[1:] {
		if !result.Groups.Gap {
			t.Fatal("sparse state lost gap marker")
		}
	}
}

func TestAnalysisOnlySelectedMeasurementsImposeRequirements(t *testing.T) {
	epsilon := .45
	badGroups, badBridges := groups.Options{}, bridges.Options{}
	request := &probe.Request{States: []probe.FrozenState{{Step: 0, Opinions: []float64{0, 0}}}, Epsilon: &epsilon,
		Measurements: []string{"terminal"}, Groups: &badGroups, Bridges: &badBridges, BridgeCores: [][]int{{-1}}}
	if _, err := probe.Evaluate(request); err != nil {
		t.Fatalf("unselected options imposed requirements: %v", err)
	}
	request.Measurements = []string{"groups"}
	request.Groups = nil
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatalf("groups-only required graph/posts/valid unselected bridges: %v", err)
	}
	if response.Analysis.GroupOptions.Epsilon != epsilon || response.Analysis.BridgeOptions != nil {
		t.Fatal("unselected bridge options changed groups")
	}
	request.RunDirectory, request.States = staticHistory(t, "legacy"), nil
	request.Scan = &probe.ScanOptions{}
	if _, err := probe.Evaluate(request); err != nil {
		t.Fatalf("groups-only opened unavailable posts: %v", err)
	}
}

func TestAnalysisFixedCohortValidation(t *testing.T) {
	for name, cores := range map[string][][]int{"empty": {}, "single": {{0}}, "empty_core": {{0}, {}}, "negative": {{-1}, {1}}, "duplicate": {{0, 0}, {1}}, "overlap": {{0}, {0}}, "out_of_range": {{0}, {101}}} {
		t.Run(name, func(t *testing.T) {
			request := analysisRequest(analysisHistory(t))
			request.BridgeCores = cores
			if _, err := probe.Evaluate(request); err == nil || !strings.Contains(err.Error(), "bridge_cores") {
				t.Fatalf("invalid fixed cohort accepted or wrong error: %v", err)
			}
		})
	}
}

func TestAnalysisMissingPostCollectionPreservesOpinionAndUnknownPostSignals(t *testing.T) {
	dir := analysisHistory(t)
	path := filepath.Join(dir, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata["HKParams"].(map[string]any)["RepostRate"] = .5
	data, err = json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	request := analysisRequest(dir)
	options := bridges.DefaultOptions(.65)
	options.DetailOutput = true
	request.Bridges = &options
	request.BridgeCores = [][]int{{0}, {50}}
	response, err := probe.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Analysis.PostsUnavailableReason == "" {
		t.Fatal("missing collection channel lost its provenance")
	}
	last := response.Results[len(response.Results)-1].Bridges
	found := false
	for _, holder := range last.Pairs[0].Holders {
		if holder.Holder == 100 {
			found = true
			if !holder.O[0].Status.Known || !holder.O[0].Status.Active || holder.P[0].SourceKnown || holder.P[0].Status.Known {
				t.Fatalf("unknown post source was confused with negative or opinion support lost: %+v", holder)
			}
		}
	}
	if !found {
		t.Fatal("missing holder detail")
	}
}
