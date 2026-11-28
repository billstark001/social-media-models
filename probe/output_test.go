package probe

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
	"smp/bridges"
	"smp/groups"
)

func sinkFixture(t *testing.T, archive bool) (*collectionSink, *Request, *AnalysisResult) {
	t.Helper()
	selection, e := bridges.ResolveCollection(&bridges.Collection{Profile: "lifecycle"})
	if e != nil {
		t.Fatal(e)
	}
	terminal := 2
	request := &Request{Output: &OutputOptions{Directory: filepath.Join(t.TempDir(), "output with space #hash &percent%"), ArchiveNative: archive, Metadata: map[string]any{"dataset": "fixture"}}, sourceMaxStep: &terminal}
	sink, e := newCollectionSink(request)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(sink.Abort)
	return sink, request, &AnalysisResult{DefinitionVersion: 2, BranchLedgerVersion: 1, Population: 3, Observations: 3, LastStep: 2, Collection: selection}
}

func TestCollectionSinkSQLiteAndOptionalArchive(t *testing.T) {
	s, request, analysis := sinkFixture(t, true)
	ids := [2]int64{0, 0}
	end := 2
	frame := bridges.Frame{Epochs: []bridges.Epoch{{Scope: "union", GroupIDs: ids, Number: 1, State: "closed", Start: 0, End: 2, ClosedAt: 2, Reason: "record_end", ReferencePairs: [][3]int64{{1, 2, 1}}}}, Summaries: []bridges.Summary{{Scope: "union", GroupIDs: ids, Epoch: 1, End: 2, InitialPresent: 1, Losses: 1, ActiveHolderRounds: 1, KnownHolderRounds: 6, NeverSeenHolderRounds: 4, SeenInactiveHolderRounds: 1, Reason: "record_end"}}, Events: []bridges.Event{{Scope: "union", GroupIDs: ids, Epoch: 1, Step: 1, IntervalEnd: 1, Holder: 0, Type: "loss", FromRole: 1, FromKnownBits: 1, ToKnownBits: 1}}, Episodes: []bridges.Episode{{Scope: "union", GroupIDs: ids, Epoch: 1, Holder: 0, ObservedStart: 0, End: &end, ObservationEnd: 2, LeftCensored: true, Reason: "record_end"}}, Series: []bridges.StepStats{{Scope: "union", GroupIDs: ids, Epoch: 1, Channel: "U", Step: 1, IntervalEnd: 1, Population: 3, Known: 3, NeverSeen: 2, SeenInactive: 1, Losses: 1, ActiveHolderRounds: 1, KnownHolderRounds: 3, NeverSeenHolderRounds: 2}}}
	frame.BranchEvents = []bridges.BranchEvent{{Event: frame.Events[0], Channel: bridges.ChannelPost}}
	if e := s.Collect(&frame); e != nil {
		t.Fatal(e)
	}
	if e := s.Groups(groups.Frame{Events: []groups.Event{{Type: "birth", Step: 0, GroupIDs: []int64{1}, Reason: "initial"}}}); e != nil {
		t.Fatal(e)
	}
	if e := s.Snapshot(StateResult{Step: 1, Precision: "float64"}); e != nil {
		t.Fatal(e)
	}
	receipt, e := s.Finish(analysis, request)
	if e != nil {
		t.Fatal(e)
	}
	if !receipt.Completed || len(receipt.Files) != 3 || receipt.Rows["events"] != 2 || receipt.Rows["step_stats"] != 1 {
		t.Fatalf("bad receipt: %+v", receipt)
	}
	db, e := sql.Open("sqlite3", sqliteFile(receipt.Database, true))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var population, unknown int
	if e = db.QueryRow(`SELECT population,unknown_holder_rounds FROM summaries`).Scan(&population, &unknown); e != nil || population != 3 || unknown != 0 {
		t.Fatalf("U summary backfill: %d %d %v", population, unknown, e)
	}
	var check string
	if e = db.QueryRow(`PRAGMA integrity_check`).Scan(&check); e != nil || check != "ok" {
		t.Fatalf("integrity: %s %v", check, e)
	}
	b, e := os.ReadFile(receipt.Metadata)
	if e != nil {
		t.Fatal(e)
	}
	var meta map[string]any
	if e = json.Unmarshal(b, &meta); e != nil {
		t.Fatal(e)
	}
	if meta["population"] != float64(3) || meta["completed"] != true {
		t.Fatal("missing authoritative metadata")
	}
	compressed, e := os.Open(receipt.ArchiveNative)
	if e != nil {
		t.Fatal(e)
	}
	defer compressed.Close()
	reader, e := zstd.NewReader(compressed)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	decoder := msgpack.NewDecoder(reader)
	decoder.SetCustomStructTag("json")
	var archived Response
	if e = decoder.Decode(&archived); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(archived.Analysis.BridgeEvents, frame.Events) || !reflect.DeepEqual(archived.Analysis.BranchEvents, frame.BranchEvents) || !reflect.DeepEqual(archived.Analysis.BridgeEpisodes, frame.Episodes) || !reflect.DeepEqual(archived.Analysis.Epochs, frame.Epochs) || !reflect.DeepEqual(archived.Analysis.Series, frame.Series) || len(archived.Results) != 1 || archived.Results[0].Step != 1 {
		t.Fatalf("archive lost native records: %+v", archived.Analysis)
	}
	if _, e = newCollectionSink(request); e == nil {
		t.Fatal("completed output was overwritten")
	}
}

func TestCollectionSinkAbortAndCrashRecovery(t *testing.T) {
	s, request, _ := sinkFixture(t, false)
	s.Abort()
	if _, e := os.Stat(filepath.Join(request.Output.Directory, "metadata.json")); !os.IsNotExist(e) {
		t.Fatal("aborted output was marked complete")
	}
	// Simulate files left by a terminated process, whose OS lock is released.
	for name, value := range map[string]string{".collection.incomplete": "smp-probe-collection-v1\n", ".collection-abandoned.sqlite": "partial", "results.sqlite": "unpublished", "native.msgpack.zst": "partial"} {
		if e := os.WriteFile(filepath.Join(request.Output.Directory, name), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	recovered, e := newCollectionSink(request)
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.Abort()
	if _, e = os.Stat(filepath.Join(request.Output.Directory, ".collection-abandoned.sqlite")); !os.IsNotExist(e) {
		t.Fatal("abandoned temporary database survived recovery")
	}
	if _, e = newCollectionSink(request); e == nil {
		t.Fatal("concurrent output writer acquired held lock")
	}
}

func TestCollectionSinkRejectsOverlappingSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if e := os.Mkdir(source, 0755); e != nil {
		t.Fatal(e)
	}
	for _, output := range []string{source, filepath.Join(source, "nested"), root} {
		if s, e := newCollectionSink(&Request{RunDirectory: source, Output: &OutputOptions{Directory: output}}); e == nil {
			s.Abort()
			t.Fatal("source/output overlap accepted")
		}
	}
}

func TestCollectionSQLiteURIKeepsPathCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "space # percent% question? name.sqlite")
	uri, err := url.Parse(sqliteFile(path, true))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(path)
	if len(want) > 0 && want[0] != '/' {
		want = "/" + want
	}
	if uri.Host != "" || uri.Path != want || uri.RawQuery != "_busy_timeout=5000&_foreign_keys=on&mode=ro" {
		t.Fatalf("URI changed filename: %s", uri)
	}
}

func TestCollectionSinkResolvesSourceAliases(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if s, err := newCollectionSink(&Request{RunDirectory: source, Output: &OutputOptions{Directory: filepath.Join(alias, "output")}}); err == nil {
		s.Abort()
		t.Fatal("source alias accepted as output")
	}
}

func TestCollectionOutputMetadataRetainsLargeIntegers(t *testing.T) {
	const exact int64 = 1800000000000000001
	for _, format := range []string{"json", "msgpack"} {
		t.Run(format, func(t *testing.T) {
			var options OutputOptions
			if format == "json" {
				if err := json.Unmarshal([]byte(`{"directory":"unused","metadata":{"mtime_ns":1800000000000000001,"nested":{"number":1800000000000000001}}}`), &options); err != nil {
					t.Fatal(err)
				}
			} else {
				encoded, err := msgpack.Marshal(OutputOptions{Directory: "unused", Metadata: map[string]any{"mtime_ns": exact, "nested": map[string]any{"number": exact}}})
				if err != nil {
					t.Fatal(err)
				}
				decoder := msgpack.NewDecoder(bytes.NewReader(encoded))
				decoder.SetCustomStructTag("json")
				if err = decoder.Decode(&options); err != nil {
					t.Fatal(err)
				}
			}
			s, request, analysis := sinkFixture(t, false)
			request.Output.Metadata = options.Metadata
			receipt, err := s.Finish(analysis, request)
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(receipt.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			decoder := json.NewDecoder(file)
			decoder.UseNumber()
			var stored map[string]any
			if err = decoder.Decode(&stored); err != nil {
				t.Fatal(err)
			}
			metadata := stored["user_metadata"].(map[string]any)
			for _, value := range []any{metadata["mtime_ns"], metadata["nested"].(map[string]any)["number"]} {
				if value.(json.Number).String() != "1800000000000000001" {
					t.Fatalf("%s lost integer precision: %v", format, value)
				}
			}
		})
	}
	for _, bad := range []string{`{"directory":"x","unknown":true}`, `{"directory":"x","metadata":[]}`} {
		var options OutputOptions
		if err := json.Unmarshal([]byte(bad), &options); err == nil {
			t.Fatalf("invalid output metadata accepted: %s", bad)
		}
	}
}
