// Package record reads persisted simulation histories without constructing a
// simulator, changing files, or advancing random streams.
package record

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"smp/model"
	"smp/simulation"
	"smp/trajectory"
	"smp/utils"
)

// Requirements selects the microscopic channels needed by an observation.
// Posts implies Graph, as recommendation evaluation needs both.
type Requirements struct {
	Graph bool
	Posts bool
}

// State is a post-commit snapshot. Its slices and maps belong to the caller.
// Precision describes opinions in the history, not full checkpoint arithmetic.
type State struct {
	Step      int
	Graph     utils.NetworkXGraph
	Opinions  []float64
	Posts     map[int64][]model.PostRecord[float64]
	Precision string
}

// Reader keeps at most one decoded trajectory block. It is not safe for
// concurrent use; independent readers may inspect the same run concurrently.
type Reader struct {
	Metadata       *simulation.ScenarioMetadata
	MetadataFields map[string]json.RawMessage
	MaxStep        int

	dir       string
	agents    int
	manifest  *trajectory.Manifest
	legacy    [][]float64
	graphs    map[int]string
	anchors   []int
	db        *sql.DB
	closed    bool
	block     [][]float64
	blockAt   int
	precision string
}

var graphName = regexp.MustCompile(`^graph-(\d+)\.msgpack(?:\.lz4)?$`)
var accumName = regexp.MustCompile(`^acc-state-.+\.lz4$`)

// OpenRun opens one output directory, not its parent. Collection flags retain
// their persisted values; MetadataFields distinguishes missing fields from false.
// Graphs and event databases are opened only if an observation needs them.
// Explicit metadata is accepted only when metadata.json does not exist; it is
// never written to the run or combined with implicit defaults.
func OpenRun(dir string, metadata map[string]any) (*Reader, error) {
	var err error
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r := &Reader{dir: dir, graphs: make(map[int]string), blockAt: -1}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err == nil && metadata != nil {
		return nil, errors.New("run already has metadata.json; explicit metadata cannot override it")
	}
	if errors.Is(err, os.ErrNotExist) && metadata != nil {
		data, err = json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("encode explicit run metadata: %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read run metadata: %w", err)
	}
	if err := json.Unmarshal(data, &r.MetadataFields); err != nil || r.MetadataFields == nil {
		return nil, fmt.Errorf("run metadata must be a JSON object")
	}
	r.Metadata = new(simulation.ScenarioMetadata)
	if err := json.Unmarshal(data, r.Metadata); err != nil {
		return nil, fmt.Errorf("decode run metadata: %w", err)
	}
	// Historical HK runs predate the DynamicsType field. An explicit HKParams
	// object supplies that format's interpretation without altering the saved
	// metadata or replacing an explicitly supplied dynamics type.
	if _, explicit := r.MetadataFields["DynamicsType"]; !explicit {
		var hk map[string]json.RawMessage
		if json.Unmarshal(r.MetadataFields["HKParams"], &hk) == nil && hk != nil {
			r.Metadata.DynamicsType = simulation.DynamicsTypeHK
		}
	}
	if raw, explicit := r.MetadataFields["DataVersion"]; explicit {
		var version *int
		if err := json.Unmarshal(raw, &version); err != nil || version == nil {
			return nil, errors.New("run DataVersion must be an integer when present")
		}
	}
	if r.Metadata.DataVersion < 0 || r.Metadata.DataVersion > simulation.CurrentDataVersion {
		return nil, fmt.Errorf("unsupported run DataVersion %d", r.Metadata.DataVersion)
	}
	if err := simulation.ValidateRecsysParamNames(r.Metadata.RecSysParams); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	latest := ""
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if match := graphName.FindStringSubmatch(name); match != nil {
			step, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, fmt.Errorf("invalid graph step in %q", name)
			}
			if _, exists := r.graphs[step]; exists {
				return nil, fmt.Errorf("multiple graph dumps at step %d", step)
			}
			r.graphs[step] = filepath.Join(dir, name)
			r.anchors = append(r.anchors, step)
		}
		if accumName.MatchString(name) && name > latest {
			latest = name
		}
	}
	sort.Ints(r.anchors)
	manifest, err := os.ReadFile(filepath.Join(dir, "trajectory.json"))
	if err == nil {
		r.manifest = new(trajectory.Manifest)
		if err := json.Unmarshal(manifest, r.manifest); err != nil {
			return nil, fmt.Errorf("decode trajectory manifest: %w", err)
		}
		if err := r.validateManifest(); err != nil {
			return nil, err
		}
		r.agents, r.MaxStep = r.manifest.Agents, r.manifest.NextStep-1
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else {
		if latest == "" {
			return nil, errors.New("run has no trajectory or accumulated state")
		}
		r.legacy, err = simulation.LoadAccumulativeOpinions(filepath.Join(dir, latest))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", latest, err)
		}
		r.MaxStep, r.agents = len(r.legacy)-1, len(r.legacy[0])
	}
	if _, explicit := r.MetadataFields["NodeCount"]; explicit && r.Metadata.NodeCount != r.agents {
		return nil, fmt.Errorf("metadata NodeCount %d differs from trajectory agents %d", r.Metadata.NodeCount, r.agents)
	}
	return r, nil
}

func (r *Reader) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	r.block, r.legacy = nil, nil
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

func (r *Reader) checkSteps(steps []int) error {
	if r == nil || r.closed || (r.manifest == nil && r.legacy == nil) {
		return errors.New("run reader is closed")
	}
	if len(steps) == 0 {
		return errors.New("at least one completed step is required")
	}
	previous := -1
	for _, step := range steps {
		if step <= previous || step > r.MaxStep {
			return fmt.Errorf("steps must increase without duplicates within [0, %d]; got %d after %d", r.MaxStep, step, previous)
		}
		previous = step
	}
	return nil
}

func (r *Reader) flag(name string) bool {
	var value bool
	return json.Unmarshal(r.MetadataFields[name], &value) == nil && value
}

func (r *Reader) behaviorRate(name string) (float64, bool, error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(r.MetadataFields[r.Metadata.DynamicsType+"Params"], &params); err != nil {
		return 0, false, nil
	}
	raw, found := params[name]
	if !found {
		return 0, false, nil
	}
	var value *float64
	if err := json.Unmarshal(raw, &value); err != nil || value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1 {
		return 0, false, fmt.Errorf("invalid %s.%s", r.Metadata.DynamicsType+"Params", name)
	}
	return *value, true, nil
}

// ReadStates observes increasing completed steps. It performs only the history
// reconstruction required by requirements and yields independently owned states.
func (r *Reader) ReadStates(steps []int, requirements Requirements, yield func(State) error) error {
	if err := r.checkSteps(steps); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("state callback is nil")
	}
	graphs := graphReader{reader: r, current: -1, steps: steps}
	defer graphs.close()
	first, last := steps[0], steps[len(steps)-1]
	var posts map[int64][]model.PostRecord[float64]
	var logged *postCursor
	retain, start := 1, first
	if requirements.Posts {
		requirements.Graph = true
		if last > 0 {
			var persistedRetain *int
			if err := json.Unmarshal(r.MetadataFields["PostRetainCount"], &persistedRetain); err != nil || persistedRetain == nil || *persistedRetain < 0 {
				return errors.New("cannot reconstruct posts: PostRetainCount is missing or invalid")
			}
			retain = max(*persistedRetain, 1)
			rate, known, err := r.behaviorRate("RepostRate")
			if err != nil {
				return err
			}
			if !known || rate > 0 {
				if !r.flag("PostEvent") {
					return errors.New("cannot reconstruct posts: RepostRate is nonzero or unknown and PostEvent was not explicitly enabled")
				}
				start = max(0, first-retain+1)
				logged, err = r.openPosts(postWindows(steps, retain))
				if err != nil {
					return err
				}
				defer logged.close()
			} else {
				start = max(0, first-retain+1)
			}
		}
		posts = make(map[int64][]model.PostRecord[float64], r.agents)
	}
	selected := 0
	for step := start; step <= last; {
		if r.closed {
			return errors.New("run reader was closed during observation")
		}
		opinions, precision, err := r.opinionRow(step)
		if err != nil {
			return err
		}
		if requirements.Posts {
			var actual map[int64]model.PostRecord[float64]
			if step > 0 && logged != nil {
				actual, err = logged.at(step, r.agents)
				if err != nil {
					return err
				}
			}
			for id, opinion := range opinions {
				post := model.PostRecord[float64]{AgentID: int64(id), Step: step, Opinion: opinion}
				if step == 0 {
					post.Step = -1
				} else if observed, found := actual[int64(id)]; found {
					post = observed
				}
				history := posts[int64(id)]
				if len(history) == retain {
					copy(history, history[1:])
					history = history[:retain-1]
				}
				posts[int64(id)] = append(history, post)
			}
		}
		if step == steps[selected] {
			state := State{Step: step, Opinions: append([]float64(nil), opinions...), Precision: precision}
			if requirements.Graph {
				state.Graph, err = graphs.at(step)
				if err != nil {
					return err
				}
			}
			if requirements.Posts {
				state.Posts = make(map[int64][]model.PostRecord[float64], len(posts))
				for id, history := range posts {
					state.Posts[id] = append([]model.PostRecord[float64](nil), history...)
				}
			}
			if err := yield(state); err != nil {
				return err
			}
			selected++
			if selected == len(steps) {
				break
			}
		}
		if requirements.Posts {
			step++
			// Overlapping or adjacent retention windows share the current
			// history. A disjoint window needs none of the intervening rows.
			if nextStart := max(0, steps[selected]-retain+1); nextStart > step {
				step = nextStart
				for id, history := range posts {
					posts[id] = history[:0]
				}
			}
		} else {
			step = steps[selected]
		}
	}
	return nil
}
