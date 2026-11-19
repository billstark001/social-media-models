package record_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"smp/model"
	"smp/record"
	"smp/simulation"
	"smp/trajectory"
	"smp/utils"

	"github.com/vmihailenco/msgpack/v5"
	"gonum.org/v1/gonum/graph/simple"
)

func writeSparseRun(t testing.TB, last, agents, chunkSteps, retain int) string {
	t.Helper()
	dir := t.TempDir()
	meta := metadata()
	meta["NodeCount"], meta["PostRetainCount"] = agents, retain
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	graph := simple.NewDirectedGraph()
	for id := int64(0); id < int64(agents); id++ {
		graph.AddNode(simple.Node(id))
	}
	for id := int64(0); id < int64(agents); id++ {
		graph.SetEdge(graph.NewEdge(graph.Node(id), graph.Node((id+1)%int64(agents))))
	}
	data, err = msgpack.Marshal(utils.SerializeGraph(graph))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph-0.msgpack"), data, 0644); err != nil {
		t.Fatal(err)
	}
	precision := trajectory.Precision{Opinions: "float64", OpinionSums: "float32"}
	writer, err := trajectory.Open(dir, agents, precision)
	if err != nil {
		t.Fatal(err)
	}
	buffer := trajectory.NewBuffer(agents, precision)
	opinions := make([]float64, agents)
	numbers := make([]model.AgentNumberRecord, agents)
	sums := make([]model.AgentOpinionSumRecord, agents)
	for step := 0; step <= last; step++ {
		for id := range opinions {
			opinions[id] = float64((step+id)%64) / 64
		}
		if err := buffer.Append(opinions, numbers, sums); err != nil {
			t.Fatal(err)
		}
		if (step+1)%chunkSteps == 0 || step == last {
			if err := writer.Flush(buffer); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func TestSparsePostWindowsMatchDenseReplay(t *testing.T) {
	for _, retain := range []int{0, 1, 2, 3, 10} {
		t.Run(fmt.Sprintf("retain_%d", retain), func(t *testing.T) {
			dir := writeSparseRun(t, 8, 3, 2, retain)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			dense := readStates(t, reader, []int{0, 1, 2, 3, 4, 5, 6, 7, 8}, record.Requirements{Posts: true})
			selected := []int{0, 1, 5, 7, 8}
			sparse := readStates(t, reader, selected, record.Requirements{Posts: true})
			for index, step := range selected {
				if !reflect.DeepEqual(sparse[index], dense[step]) {
					t.Fatalf("sparse state at step %d differs from dense replay", step)
				}
			}
		})
	}
}

func TestSparsePostWindowsSkipOpinionsButReplayRewiring(t *testing.T) {
	dir := writeSparseRun(t, 5, 3, 1, 1)
	meta := metadata()
	meta["PostRetainCount"], meta["PostEvent"], meta["RewiringEvent"] = 1, true, true
	meta["HKParams"].(map[string]any)["RepostRate"] = .5
	meta["HKParams"].(map[string]any)["RewiringRate"] = .5
	writeJSON(t, filepath.Join(dir, "metadata.json"), meta)
	db, err := simulation.OpenEventDB(filepath.Join(dir, "events.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []*model.EventRecord{
		{Type: "Rewiring", AgentID: 0, Step: 2, Body: model.RewiringEventBody{AgentID: 0, Unfollow: 1, Follow: 2}},
		{Type: "Post", AgentID: 1, Step: 3, Body: model.PostEventBody[float64]{IsRepost: true, Record: &model.PostRecord[float64]{AgentID: 0, Step: -1, Opinion: -.5}}},
		{Type: "Post", AgentID: 2, Step: 5, Body: model.PostEventBody[float64]{IsRepost: true, Record: &model.PostRecord[float64]{AgentID: 1, Step: 3, Opinion: .25}}},
	} {
		if err := db.StoreEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Neither selected state needs the intervening opinion blocks. Rewiring
	// events within that gap still determine the selected final topology.
	for step := 1; step < 5; step++ {
		path := filepath.Join(dir, fmt.Sprintf("trajectory-%09d-%09d.smpc", step, step))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := record.OpenRun(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	states := readStates(t, reader, []int{0, 5}, record.Requirements{Posts: true})
	if states[1].Graph.Adjacency[0][2] == nil || states[1].Graph.Adjacency[0][1] != nil {
		t.Fatal("rewiring within the omitted post window was not replayed")
	}
	post := states[1].Posts[2]
	if len(post) != 1 || post[0].AgentID != 1 || post[0].Step != 3 || post[0].Opinion != .25 {
		t.Fatalf("post cursor failed to advance across an omitted window: %+v", post)
	}
}

func BenchmarkSparsePostWindows(b *testing.B) {
	for _, last := range []int{100, 10000} {
		b.Run(fmt.Sprintf("last_%d", last), func(b *testing.B) {
			dir := writeSparseRun(b, last, 32, 100, 3)
			reader, err := record.OpenRun(dir, nil)
			if err != nil {
				b.Fatal(err)
			}
			defer reader.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := reader.ReadStates([]int{0, last}, record.Requirements{Posts: true}, func(record.State) error { return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
