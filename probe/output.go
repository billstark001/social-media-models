package probe

// File-backed collection keeps the observer's working set independent of the
// number of emitted lifecycle records. Metadata is committed last: its presence
// is the completion marker for the database and optional archive.
import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"smp/bridges"
	"smp/groups"

	_ "github.com/mattn/go-sqlite3"
	"github.com/vmihailenco/msgpack/v5"
)

const CollectionSchemaVersion = 1

// OutputOptions selects file-backed persistence independently of collection.
// Metadata is caller provenance and cannot replace authoritative run fields.
type OutputOptions struct {
	Directory     string         `json:"directory" msgpack:"directory"`
	ArchiveNative bool           `json:"archive_native,omitempty" msgpack:"archive_native,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty" msgpack:"metadata,omitempty"`
}

// UnmarshalJSON preserves large integer provenance values without changing the
// numeric decoding of simulation graphs, recommender parameters or states.
func (o *OutputOptions) UnmarshalJSON(data []byte) error {
	var raw struct {
		Directory     string          `json:"directory"`
		ArchiveNative bool            `json:"archive_native"`
		Metadata      json.RawMessage `json:"metadata"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	var metadata map[string]any
	if len(raw.Metadata) > 0 {
		values := json.NewDecoder(bytes.NewReader(raw.Metadata))
		values.UseNumber()
		if err := values.Decode(&metadata); err != nil {
			return fmt.Errorf("output metadata: %w", err)
		}
	}
	*o = OutputOptions{Directory: raw.Directory, ArchiveNative: raw.ArchiveNative, Metadata: metadata}
	return nil
}

// OutputFile binds one completed payload by length and SHA-256.
type OutputFile struct {
	Bytes  int64  `json:"bytes" msgpack:"bytes"`
	SHA256 string `json:"sha256" msgpack:"sha256"`
}

// OutputReceipt is the small transport response for a completed collection.
type OutputReceipt struct {
	SchemaVersion int                   `json:"schema_version" msgpack:"schema_version"`
	Directory     string                `json:"directory" msgpack:"directory"`
	Database      string                `json:"database" msgpack:"database"`
	Metadata      string                `json:"metadata" msgpack:"metadata"`
	ArchiveNative string                `json:"archive_native,omitempty" msgpack:"archive_native,omitempty"`
	Rows          map[string]int64      `json:"rows" msgpack:"rows"`
	Files         map[string]OutputFile `json:"files" msgpack:"files"`
	Completed     bool                  `json:"completed" msgpack:"completed"`
}

type recordField struct {
	name                   string
	path                   []int
	kind                   reflect.Kind
	blob, symbol, nullable bool
}
type recordTable struct {
	name           string
	normalized     bool
	fields         []recordField
	identityFields []recordField
	stmt           *sql.Stmt
}
type domainKey struct {
	scope int
	ids   [2]int64
	epoch int
}
type collectionSink struct {
	directory, temporary, lock           string
	lockFile                             *os.File
	marker                               string
	db                                   *sql.DB
	tx                                   *sql.Tx
	tables                               map[string]*recordTable
	domains                              map[domainKey]int64
	symbols                              map[string]int64
	domainStmt, symbolStmt, snapshotStmt *sql.Stmt
	rows                                 map[string]int64
	owned                                []string
	finished                             bool
}

func quoteColumn(s string) string { return `"` + s + `"` }
func sqliteFile(path string, readOnly bool) string {
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	if readOnly {
		q.Set("mode", "ro")
	} else {
		q.Set("mode", "rwc")
	}
	q.Set("_foreign_keys", "on")
	q.Set("_busy_timeout", "5000")
	u.RawQuery = q.Encode()
	return u.String()
}

func newCollectionSink(request *Request) (_ *collectionSink, err error) {
	if request.Output == nil {
		return nil, nil
	}
	if request.Output.Directory == "" {
		return nil, errors.New("output directory is required")
	}
	directory, err := resolvedOutputPath(request.Output.Directory)
	if err != nil {
		return nil, err
	}
	if request.RunDirectory != "" {
		source, e := resolvedOutputPath(request.RunDirectory)
		if e != nil {
			return nil, e
		}
		if pathWithin(source, directory) || pathWithin(directory, source) {
			return nil, errors.New("source run and output directory must not overlap")
		}
	}
	if err = os.MkdirAll(directory, 0755); err != nil {
		return nil, err
	}
	s := &collectionSink{directory: directory, lock: filepath.Join(directory, ".collection.lock"), tables: map[string]*recordTable{}, domains: map[domainKey]int64{}, symbols: map[string]int64{}, rows: map[string]int64{}}
	lock, e := os.OpenFile(s.lock, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, fmt.Errorf("claim collection output: %w", e)
	}
	if e = lockCollection(lock); e != nil {
		_ = lock.Close()
		return nil, e
	}
	s.lockFile = lock
	defer func() {
		if err != nil {
			s.Abort()
		}
	}()
	if _, e = os.Stat(filepath.Join(directory, "metadata.json")); e == nil {
		return nil, errors.New("completed collection output already exists")
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	s.marker = filepath.Join(directory, ".collection.incomplete")
	if content, e := os.ReadFile(s.marker); e == nil {
		if string(content) != "smp-probe-collection-v1\n" {
			return nil, errors.New("unrecognized incomplete output marker")
		}
		for _, pattern := range []string{".collection-*.sqlite", ".collection-*.sqlite-journal", ".metadata-*", "results.sqlite", "native.msgpack.zst"} {
			matches, e := filepath.Glob(filepath.Join(directory, pattern))
			if e != nil {
				return nil, e
			}
			for _, path := range matches {
				if e = os.Remove(path); e != nil {
					return nil, e
				}
			}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	for _, name := range []string{"results.sqlite", "native.msgpack.zst"} {
		if _, e = os.Lstat(filepath.Join(directory, name)); !errors.Is(e, os.ErrNotExist) {
			if e == nil {
				return nil, fmt.Errorf("output already exists: %s", name)
			}
			return nil, e
		}
	}
	if err = os.WriteFile(s.marker, []byte("smp-probe-collection-v1\n"), 0600); err != nil {
		return nil, err
	}
	s.owned = append(s.owned, s.marker)
	temporary, e := os.CreateTemp(directory, ".collection-*.sqlite")
	if e != nil {
		return nil, e
	}
	s.temporary = temporary.Name()
	if e = temporary.Close(); e != nil {
		return nil, e
	}
	s.owned = append(s.owned, s.temporary)
	s.db, err = sql.Open("sqlite3", sqliteFile(s.temporary, false))
	if err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=DELETE", "PRAGMA synchronous=FULL", "PRAGMA cache_size=-4096", fmt.Sprintf("PRAGMA user_version=%d", CollectionSchemaVersion), `CREATE TABLE domains(id INTEGER PRIMARY KEY,scope INTEGER NOT NULL,g0 INTEGER NOT NULL,g1 INTEGER NOT NULL,epoch INTEGER NOT NULL,UNIQUE(scope,g0,g1,epoch))`, `CREATE TABLE symbols(id INTEGER PRIMARY KEY,value TEXT NOT NULL UNIQUE)`, `CREATE TABLE run_info(id INTEGER PRIMARY KEY CHECK(id=1),population INTEGER NOT NULL,source_max_step INTEGER,definition_version INTEGER NOT NULL,branch_ledger_version INTEGER NOT NULL,completed INTEGER NOT NULL CHECK(completed=1))`, `CREATE TABLE snapshots(id INTEGER PRIMARY KEY,step INTEGER NOT NULL,payload BLOB NOT NULL)`} {
		if _, err = s.db.Exec(q); err != nil {
			return nil, err
		}
	}
	s.tx, err = s.db.Begin()
	if err != nil {
		return nil, err
	}
	if s.domainStmt, err = s.tx.Prepare(`INSERT INTO domains(scope,g0,g1,epoch) VALUES(?,?,?,?)`); err != nil {
		return nil, err
	}
	if s.symbolStmt, err = s.tx.Prepare(`INSERT INTO symbols(value) VALUES(?)`); err != nil {
		return nil, err
	}
	if s.snapshotStmt, err = s.tx.Prepare(`INSERT INTO snapshots(step,payload) VALUES(?,?)`); err != nil {
		return nil, err
	}
	for _, spec := range []struct {
		name       string
		value      any
		normalized bool
	}{{"events", bridges.BranchEvent{}, true}, {"episodes", bridges.BranchEpisode{}, true}, {"summaries", bridges.BranchSummary{}, true}, {"step_stats", bridges.StepStats{}, true}, {"epochs", bridges.Epoch{}, true}, {"group_events", groups.Event{}, false}} {
		if err = s.addTable(spec.name, spec.value, spec.normalized); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func recordFields(t reflect.Type, prefix []int) []recordField {
	var result []recordField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		path := append(append([]int(nil), prefix...), i)
		if f.Anonymous {
			result = append(result, recordFields(f.Type, path)...)
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		typ := f.Type
		nullable := typ.Kind() == reflect.Pointer
		if nullable {
			typ = typ.Elem()
		}
		kind := typ.Kind()
		result = append(result, recordField{name: name, path: path, kind: kind, nullable: nullable, blob: kind == reflect.Slice || kind == reflect.Array || kind == reflect.Map || kind == reflect.Struct, symbol: kind == reflect.String})
	}
	return result
}

func (s *collectionSink) addTable(name string, value any, normalized bool) error {
	t := &recordTable{name: name, normalized: normalized}
	columns := []string{`id INTEGER PRIMARY KEY`}
	fields := recordFields(reflect.TypeOf(value), nil)
	if normalized {
		for _, field := range fields {
			switch field.name {
			case "scope", "group_ids", "epoch", "number", "channel", "side":
				t.identityFields = append(t.identityFields, field)
			}
		}
	}
	if normalized {
		columns = append(columns, `domain_id INTEGER NOT NULL REFERENCES domains(id)`, `channel INTEGER NOT NULL`, `side INTEGER NOT NULL`)
	}
	var names []string
	if normalized {
		names = append(names, "domain_id", "channel", "side")
	}
	for _, f := range fields {
		if normalized && (f.name == "scope" || f.name == "group_ids" || f.name == "epoch" || f.name == "number" || f.name == "channel" || f.name == "side") {
			continue
		}
		typ := "INTEGER"
		if f.blob {
			typ = "BLOB"
		} else if f.kind == reflect.Float64 || f.kind == reflect.Float32 {
			typ = "REAL"
		}
		declaration := quoteColumn(f.name) + " " + typ
		if !f.nullable {
			declaration += " NOT NULL"
		}
		columns = append(columns, declaration)
		names = append(names, f.name)
		t.fields = append(t.fields, f)
	}
	if _, err := s.tx.Exec(`CREATE TABLE ` + quoteColumn(name) + `(` + strings.Join(columns, ",") + `)`); err != nil {
		return err
	}
	quoted := make([]string, len(names))
	marks := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quoteColumn(n)
		marks[i] = "?"
	}
	stmt, err := s.tx.Prepare(`INSERT INTO ` + quoteColumn(name) + `(` + strings.Join(quoted, ",") + `) VALUES(` + strings.Join(marks, ",") + `)`)
	if err != nil {
		return err
	}
	t.stmt = stmt
	s.tables[name] = t
	return nil
}

func scopeCode(value string) (int, error) {
	switch value {
	case "pair":
		return 1, nil
	case "union":
		return 2, nil
	}
	return 0, fmt.Errorf("invalid record scope %q", value)
}
func channelCode(value string) (int, error) {
	switch value {
	case "", "U":
		return 0, nil
	case "O", bridges.ChannelOpinion:
		return 1, nil
	case "P", bridges.ChannelPost:
		return 2, nil
	case "S", bridges.ChannelStructural:
		return 3, nil
	}
	return 0, fmt.Errorf("invalid record channel %q", value)
}
func (s *collectionSink) symbol(value string) (int64, error) {
	if v, ok := s.symbols[value]; ok {
		return v, nil
	}
	r, err := s.symbolStmt.Exec(value)
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err == nil {
		s.symbols[value] = id
		s.rows["symbols"]++
	}
	return id, err
}
func (s *collectionSink) domain(scope string, ids [2]int64, epoch int) (int64, error) {
	code, err := scopeCode(scope)
	if err != nil {
		return 0, err
	}
	key := domainKey{code, ids, epoch}
	if id, ok := s.domains[key]; ok {
		return id, nil
	}
	r, err := s.domainStmt.Exec(code, ids[0], ids[1], epoch)
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err == nil {
		s.domains[key] = id
		s.rows["domains"]++
	}
	return id, err
}

func (s *collectionSink) write(table string, record any) error {
	t := s.tables[table]
	v := reflect.ValueOf(record)
	args := make([]any, 0, len(t.fields)+3)
	if t.normalized {
		var scope, channel string
		var ids [2]int64
		epoch, side := 0, -1
		for _, f := range t.identityFields {
			fv := v.FieldByIndex(f.path)
			switch f.name {
			case "scope":
				scope = fv.String()
			case "group_ids":
				ids = fv.Interface().([2]int64)
			case "epoch", "number":
				epoch = int(fv.Int())
			case "channel":
				channel = fv.String()
			case "side":
				if !fv.IsNil() {
					side = int(fv.Elem().Int())
				}
			}
		}
		id, err := s.domain(scope, ids, epoch)
		if err != nil {
			return err
		}
		ch, err := channelCode(channel)
		if err != nil {
			return err
		}
		args = append(args, id, ch, side)
	}
	for _, f := range t.fields {
		fv := v.FieldByIndex(f.path)
		if f.nullable {
			if fv.IsNil() {
				args = append(args, nil)
				continue
			}
			fv = fv.Elem()
		}
		var value any
		if f.blob {
			b, err := marshalCollection(fv.Interface())
			if err != nil {
				return err
			}
			value = b
		} else if f.symbol {
			id, err := s.symbol(fv.String())
			if err != nil {
				return err
			}
			value = id
		} else {
			value = fv.Interface()
		}
		args = append(args, value)
	}
	if _, err := t.stmt.Exec(args...); err != nil {
		return fmt.Errorf("write %s: %w", table, err)
	}
	s.rows[table]++
	return nil
}

func marshalCollection(value any) ([]byte, error) { return msgpack.Marshal(value) }

func (s *collectionSink) Collect(frame *bridges.Frame) error {
	for _, row := range frame.Epochs {
		if err := s.write("epochs", row); err != nil {
			return err
		}
	}
	for _, row := range frame.Events {
		if err := s.write("events", bridges.BranchEvent{Event: row}); err != nil {
			return err
		}
	}
	for _, row := range frame.BranchEvents {
		if err := s.write("events", row); err != nil {
			return err
		}
	}
	for _, row := range frame.Episodes {
		if err := s.write("episodes", bridges.BranchEpisode{Episode: row}); err != nil {
			return err
		}
	}
	for _, row := range frame.BranchEpisodes {
		if err := s.write("episodes", row); err != nil {
			return err
		}
	}
	for _, row := range frame.Summaries {
		if err := s.write("summaries", bridges.BranchSummary{Summary: row}); err != nil {
			return err
		}
	}
	for _, row := range frame.BranchSummaries {
		if err := s.write("summaries", row); err != nil {
			return err
		}
	}
	for _, row := range frame.Series {
		if err := s.write("step_stats", row); err != nil {
			return err
		}
	}
	return nil
}
func (s *collectionSink) Groups(frame groups.Frame) error {
	for _, row := range frame.Events {
		if err := s.write("group_events", row); err != nil {
			return err
		}
	}
	return nil
}
func (s *collectionSink) Snapshot(row StateResult) error {
	payload, err := marshalCollection(row)
	if err != nil {
		return err
	}
	if _, err = s.snapshotStmt.Exec(row.Step, payload); err == nil {
		s.rows["snapshots"]++
	}
	return err
}

func outputFingerprint(path string) (OutputFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return OutputFile{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return OutputFile{Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}
func atomicMetadata(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (s *collectionSink) Finish(analysis *AnalysisResult, request *Request) (_ *OutputReceipt, err error) {
	defer func() {
		if err != nil {
			s.Abort()
		}
	}()
	if s.finished {
		return nil, errors.New("collection already finished")
	}
	if _, err = s.tx.Exec(`UPDATE summaries SET population=?,unknown_holder_rounds=?*("end"-"start")-known_holder_rounds WHERE channel=0`, analysis.Population, analysis.Population); err != nil {
		return nil, err
	}
	if _, err = s.tx.Exec(`INSERT INTO run_info VALUES(1,?,?,?,?,1)`, analysis.Population, request.sourceMaxStep, analysis.DefinitionVersion, analysis.BranchLedgerVersion); err != nil {
		return nil, err
	}
	for _, t := range s.tables {
		if err = t.stmt.Close(); err != nil {
			return nil, err
		}
	}
	for _, stmt := range []*sql.Stmt{s.domainStmt, s.symbolStmt, s.snapshotStmt} {
		if err = stmt.Close(); err != nil {
			return nil, err
		}
	}
	if err = s.tx.Commit(); err != nil {
		return nil, err
	}
	s.tx = nil
	if err = s.db.Close(); err != nil {
		return nil, err
	}
	s.db = nil
	database := filepath.Join(s.directory, "results.sqlite")
	if err = os.Rename(s.temporary, database); err != nil {
		return nil, err
	}
	s.owned = append(s.owned, database)
	files := map[string]OutputFile{}
	if files["results.sqlite"], err = outputFingerprint(database); err != nil {
		return nil, err
	}
	metadata := map[string]any{"schema_version": CollectionSchemaVersion, "completed": true, "population": analysis.Population, "source_max_step": request.sourceMaxStep, "definition_version": analysis.DefinitionVersion, "branch_ledger_version": analysis.BranchLedgerVersion, "first_step": analysis.FirstStep, "last_step": analysis.LastStep, "observations": analysis.Observations, "sampled": analysis.Sampled, "collection": analysis.Collection, "resolved_selection": analysis.Collection, "analysis": analysis, "user_metadata": request.Output.Metadata, "run_metadata": request.RunMetadata, "source_compatibility": request.SourceCompatibility, "rows": s.rows, "files": files}
	receipt := &OutputReceipt{SchemaVersion: CollectionSchemaVersion, Directory: s.directory, Database: database, Metadata: filepath.Join(s.directory, "metadata.json"), Rows: s.rows, Files: files, Completed: true}
	if request.Output.ArchiveNative {
		archive := filepath.Join(s.directory, "native.msgpack.zst")
		s.owned = append(s.owned, archive)
		if err = s.archive(archive, database, analysis, request); err != nil {
			return nil, err
		}
		if files["native.msgpack.zst"], err = outputFingerprint(archive); err != nil {
			return nil, err
		}
		receipt.ArchiveNative = archive
	}
	if err = atomicMetadata(receipt.Metadata, metadata); err != nil {
		return nil, err
	}
	s.owned = append(s.owned, receipt.Metadata)
	if files["metadata.json"], err = outputFingerprint(receipt.Metadata); err != nil {
		return nil, err
	}
	if err = os.Remove(s.marker); err != nil {
		return nil, err
	}
	unlockCollection(s.lockFile)
	err = s.lockFile.Close()
	s.lockFile = nil
	if err != nil {
		return nil, err
	}
	s.finished = true
	return receipt, nil
}

func (s *collectionSink) Abort() {
	if s == nil || s.finished {
		return
	}
	if s.tx != nil {
		_ = s.tx.Rollback()
		s.tx = nil
	}
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
	for _, path := range s.owned {
		_ = os.Remove(path)
		_ = os.Remove(path + "-journal")
	}
	if s.lockFile != nil {
		unlockCollection(s.lockFile)
		_ = s.lockFile.Close()
		s.lockFile = nil
	}
}
