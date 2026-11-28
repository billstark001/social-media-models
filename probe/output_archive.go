package probe

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"

	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
)

// tableRecords decodes the compact physical layout into ordinary named maps.
// It holds only one row (including a single optional snapshot) at a time.
func (s *collectionSink) tableRecords(db *sql.DB, table, where string, emit func(map[string]any) error) error {
	t := s.tables[table]
	query := `SELECT t.*`
	if t.normalized {
		query += `,d.scope AS _scope,d.g0 AS _g0,d.g1 AS _g1,d.epoch AS _epoch`
	}
	query += ` FROM ` + quoteColumn(table) + ` t`
	if t.normalized {
		query += ` JOIN domains d ON d.id=t.domain_id`
	}
	if where != "" {
		query += ` WHERE ` + where
	}
	query += ` ORDER BY t.id`
	// Read symbols before opening row cursor: the sink uses one connection.
	symbols := map[int64]string{}
	sr, err := db.Query(`SELECT id,value FROM symbols`)
	if err != nil {
		return err
	}
	for sr.Next() {
		var id int64
		var value string
		if err = sr.Scan(&id, &value); err != nil {
			sr.Close()
			return err
		}
		symbols[id] = value
	}
	if err = sr.Err(); err != nil {
		sr.Close()
		return err
	}
	if err = sr.Close(); err != nil {
		return err
	}
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return err
	}
	values := make([]any, len(names))
	pointers := make([]any, len(names))
	for i := range values {
		pointers[i] = &values[i]
	}
	for rows.Next() {
		if err = rows.Scan(pointers...); err != nil {
			return err
		}
		raw := make(map[string]any, len(names))
		for i, name := range names {
			raw[name] = values[i]
		}
		record := make(map[string]any, len(t.fields)+6)
		for _, field := range t.fields {
			value := raw[field.name]
			if value != nil {
				switch {
				case field.blob:
					bytes, ok := value.([]byte)
					if !ok {
						return fmt.Errorf("invalid %s.%s blob", table, field.name)
					}
					var decoded any
					if err = msgpack.Unmarshal(bytes, &decoded); err != nil {
						return err
					}
					value = decoded
				case field.symbol:
					id, ok := value.(int64)
					if !ok {
						return fmt.Errorf("invalid %s.%s symbol", table, field.name)
					}
					text, ok := symbols[id]
					if !ok {
						return fmt.Errorf("missing symbol %d", id)
					}
					value = text
				case field.kind == reflect.Bool:
					value = value.(int64) != 0
				}
			}
			record[field.name] = value
		}
		if t.normalized {
			scope := raw["_scope"].(int64)
			if scope == 1 {
				record["scope"] = "pair"
			} else {
				record["scope"] = "union"
			}
			record["group_ids"] = []int64{raw["_g0"].(int64), raw["_g1"].(int64)}
			epochName := "epoch"
			if table == "epochs" {
				epochName = "number"
			}
			record[epochName] = raw["_epoch"]
			channel := raw["channel"].(int64)
			record["channel"] = []string{"U", "O", "P", "S"}[channel]
			if raw["side"].(int64) >= 0 {
				record["side"] = raw["side"]
			}
		}
		if err = emit(record); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *collectionSink) archive(path, database string, analysis *AnalysisResult, request *Request) (err error) {
	db, err := sql.Open("sqlite3", sqliteFile(database, true))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	compressed, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	defer compressed.Close()
	encoder := msgpack.NewEncoder(compressed)
	encoder.SetCustomStructTag("json")
	root := map[string]any{"version": ProtocolVersion, "rng": request.RNG, "dynamics_type": request.DynamicsType, "source_max_step": request.sourceMaxStep, "source_compatibility": request.SourceCompatibility}
	if err = encoder.EncodeMapLen(len(root) + 2); err != nil {
		return err
	}
	for key, value := range root {
		if err = encoder.Encode(key); err != nil {
			return err
		}
		if err = encoder.Encode(value); err != nil {
			return err
		}
	}
	if err = encoder.Encode("results"); err != nil {
		return err
	}
	if err = encoder.EncodeArrayLen(int(s.rows["snapshots"])); err != nil {
		return err
	}
	snapshots, e := db.Query(`SELECT payload FROM snapshots ORDER BY id`)
	if e != nil {
		return e
	}
	for snapshots.Next() {
		var payload []byte
		if err = snapshots.Scan(&payload); err != nil {
			snapshots.Close()
			return err
		}
		if _, err = compressed.Write(payload); err != nil {
			snapshots.Close()
			return err
		}
	}
	if err = snapshots.Err(); err != nil {
		snapshots.Close()
		return err
	}
	if err = snapshots.Close(); err != nil {
		return err
	}
	if err = encoder.Encode("analysis"); err != nil {
		return err
	}
	scalar := map[string]any{}
	av := reflect.ValueOf(*analysis)
	at := av.Type()
	for i := 0; i < av.NumField(); i++ {
		tag := at.Field(i).Tag.Get("json")
		name := tag
		for j, c := range tag {
			if c == ',' {
				name = tag[:j]
				break
			}
		}
		if name != "" && name != "-" {
			scalar[name] = av.Field(i).Interface()
		}
	}
	books := []struct {
		name, table, where string
		branch             bool
	}{{"epochs", "epochs", "", false}, {"bridge_events", "events", "t.channel=0", false}, {"branch_events", "events", "t.channel<>0", true}, {"bridge_episodes", "episodes", "t.channel=0", false}, {"branch_episodes", "episodes", "t.channel<>0", true}, {"summaries", "summaries", "t.channel=0", false}, {"branch_summaries", "summaries", "t.channel<>0", true}, {"series", "step_stats", "", false}, {"group_events", "group_events", "", false}}
	for _, book := range books {
		delete(scalar, book.name)
	}
	if err = encoder.EncodeMapLen(len(scalar) + len(books)); err != nil {
		return err
	}
	for key, value := range scalar {
		if err = encoder.Encode(key); err != nil {
			return err
		}
		if err = encoder.Encode(value); err != nil {
			return err
		}
	}
	for _, book := range books {
		if err = encoder.Encode(book.name); err != nil {
			return err
		}
		var count int64
		query := `SELECT COUNT(*) FROM ` + quoteColumn(book.table) + ` t`
		if book.where != "" {
			query += ` WHERE ` + book.where
		}
		if err = db.QueryRow(query).Scan(&count); err != nil {
			return err
		}
		if err = encoder.EncodeArrayLen(int(count)); err != nil {
			return err
		}
		err = s.tableRecords(db, book.table, book.where, func(row map[string]any) error {
			if book.branch {
				ch := row["channel"].(string)
				row["channel"] = map[string]string{"O": "opinion", "P": "post", "S": "structural"}[ch]
			} else if book.table != "step_stats" {
				delete(row, "channel")
				delete(row, "side")
				if book.table == "summaries" {
					delete(row, "population")
					delete(row, "unknown_holder_rounds")
				}
			}
			return encoder.Encode(row)
		})
		if err != nil {
			return fmt.Errorf("archive %s: %w", book.name, err)
		}
	}
	if err = compressed.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	f = nil
	return nil
}
