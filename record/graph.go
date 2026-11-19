package record

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"smp/utils"

	"github.com/pierrec/lz4/v4"
	"github.com/vmihailenco/msgpack/v5"
)

type graphReader struct {
	reader  *Reader
	graph   *utils.NetworkXGraph
	current int
	steps   []int
	events  *rewiringCursor
}

func (g *graphReader) close() {
	if g.events != nil {
		g.events.close()
	}
}

func (g *graphReader) at(step int) (utils.NetworkXGraph, error) {
	r := g.reader
	anchor := sort.Search(len(r.anchors), func(i int) bool { return r.anchors[i] > step }) - 1
	if anchor < 0 {
		return utils.NetworkXGraph{}, fmt.Errorf("cannot reconstruct graph at step %d: no earlier graph dump", step)
	}
	if r.anchors[anchor] > g.current {
		graph, err := r.loadGraph(r.graphs[r.anchors[anchor]])
		if err != nil {
			return utils.NetworkXGraph{}, err
		}
		g.graph, g.current = graph, r.anchors[anchor]
	}
	if g.current < step {
		rate, known, err := r.behaviorRate("RewiringRate")
		if err != nil {
			return utils.NetworkXGraph{}, err
		}
		if !known || rate != 0 {
			if !r.flag("RewiringEvent") {
				return utils.NetworkXGraph{}, fmt.Errorf("cannot reconstruct graph at step %d: RewiringRate is nonzero or unknown, no exact graph dump, and RewiringEvent was not explicitly enabled", step)
			}
			if g.events == nil {
				// One filtered query spans every needed anchor interval. Reopening
				// a query at each dump would repeatedly scan old unindexed tables.
				g.events, err = r.openRewiring(g.replayWindows())
				if err != nil {
					return utils.NetworkXGraph{}, err
				}
			}
			if err := g.events.apply(g.graph, g.current, step, r.agents); err != nil {
				return utils.NetworkXGraph{}, err
			}
		}
		g.current = step
	}
	return cloneGraph(g.graph), nil
}

func (g *graphReader) replayWindows() [][2]int {
	var windows [][2]int
	for _, step := range g.steps {
		anchor := sort.Search(len(g.reader.anchors), func(i int) bool { return g.reader.anchors[i] > step }) - 1
		if anchor < 0 || g.reader.anchors[anchor] == step {
			continue
		}
		start := g.reader.anchors[anchor] + 1
		if last := len(windows) - 1; last >= 0 && start <= windows[last][1]+1 {
			windows[last][1] = step
		} else {
			windows = append(windows, [2]int{start, step})
		}
	}
	return windows
}

func (r *Reader) loadGraph(path string) (*utils.NetworkXGraph, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read graph: %w", err)
	}
	defer file.Close()
	var source io.Reader = file
	if strings.HasSuffix(path, ".lz4") {
		source = lz4.NewReader(file)
	}
	var graph utils.NetworkXGraph
	decoder := msgpack.NewDecoder(source)
	if err := decoder.Decode(&graph); err != nil {
		return nil, fmt.Errorf("decode graph %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("graph %s has trailing or corrupt data: %v", path, err)
	}
	// The LZ4 reader ends at one frame, so also check the underlying file for
	// appended frames or bytes that the decompressor would otherwise ignore.
	var trailing [1]byte
	if n, err := file.Read(trailing[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("graph %s has trailing bytes or an unreadable footer: %v", path, err)
	}
	if !graph.Directed {
		return nil, fmt.Errorf("graph dump is not directed: %s", path)
	}
	valid := func(id int64) bool { return id >= 0 && id < int64(r.agents) }
	for id := range graph.Nodes {
		if !valid(id) {
			return nil, fmt.Errorf("graph node %d outside trajectory agents", id)
		}
	}
	for source, targets := range graph.Adjacency {
		if !valid(source) {
			return nil, fmt.Errorf("graph node %d outside trajectory agents", source)
		}
		for target := range targets {
			if !valid(target) || source == target {
				return nil, fmt.Errorf("invalid graph edge %d to %d", source, target)
			}
		}
	}
	if graph.Nodes == nil {
		graph.Nodes = make(map[int64]map[string]any)
	}
	if graph.Adjacency == nil {
		graph.Adjacency = make(map[int64]map[int64]any)
	}
	// Old graph dumps omitted isolated nodes. The dense trajectory agent axis
	// still identifies every node; an absent adjacency contains no outgoing edge.
	for id := int64(0); id < int64(r.agents); id++ {
		if graph.Nodes[id] == nil {
			graph.Nodes[id] = make(map[string]any)
		}
		if graph.Adjacency[id] == nil {
			graph.Adjacency[id] = make(map[int64]any)
		}
	}
	return &graph, nil
}

func cloneGraph(graph *utils.NetworkXGraph) utils.NetworkXGraph {
	copy := utils.NetworkXGraph{Directed: graph.Directed, Nodes: make(map[int64]map[string]any, len(graph.Nodes)), Adjacency: make(map[int64]map[int64]any, len(graph.Adjacency))}
	for id, attributes := range graph.Nodes {
		copy.Nodes[id] = cloneAttributes(attributes)
	}
	for id, targets := range graph.Adjacency {
		copy.Adjacency[id] = make(map[int64]any, len(targets))
		for target, attributes := range targets {
			copy.Adjacency[id][target] = cloneValue(attributes)
		}
	}
	copy.Graph = cloneAttributes(graph.Graph)
	return copy
}

func cloneAttributes(attributes map[string]any) map[string]any {
	if attributes == nil {
		return nil
	}
	copy := make(map[string]any, len(attributes))
	for key, value := range attributes {
		copy[key] = cloneValue(value)
	}
	return copy
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneAttributes(value)
	case map[any]any:
		copy := make(map[any]any, len(value))
		for key, item := range value {
			copy[key] = cloneValue(item)
		}
		return copy
	case []any:
		copy := make([]any, len(value))
		for index, item := range value {
			copy[index] = cloneValue(item)
		}
		return copy
	case []byte:
		return append([]byte(nil), value...)
	default:
		return value
	}
}
