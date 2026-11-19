package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	schema int
}

func open(path, mode string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(1000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Create reserves a new file exclusively; existing recordings are never overwritten.
func Create(ctx context.Context, path string, meta Metadata) (*Store, error) {
	if meta.Schema == 0 {
		meta.Schema = SchemaVersion
	}
	if meta.Schema != SchemaVersion && meta.Schema != ResourceSchemaVersion {
		return nil, fmt.Errorf("unsupported write schema %d", meta.Schema)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := open(path, "rw")
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			db.Close()
		}
	}()
	// Single-writer, durable, bounded database. No growing WAL companion.
	_, err = db.ExecContext(ctx, fmt.Sprintf(`PRAGMA page_size=4096;
PRAGMA journal_mode=DELETE;
PRAGMA synchronous=FULL;
PRAGMA max_page_count=65536;
PRAGMA cache_size=-4096;
PRAGMA user_version=%d;
CREATE TABLE metadata (id INTEGER PRIMARY KEY CHECK(id=1), payload TEXT NOT NULL);
CREATE TABLE observations (sequence INTEGER PRIMARY KEY, payload TEXT NOT NULL);`, meta.Schema))
	if err != nil {
		return nil, err
	}
	meta.Status = "recording"
	body, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	if len(body) > 64<<10 {
		return nil, errors.New("metadata exceeds 64 KiB")
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO metadata VALUES (1, ?)", string(body)); err != nil {
		return nil, err
	}
	failed = false
	return &Store{db: db, schema: meta.Schema}, nil
}

// Append commits one observation and all its derived transitions atomically.
func (s *Store) Append(ctx context.Context, record Record) error {
	if err := validateRecord(record, s.schema); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(body) > 64<<10 {
		return errors.New("observation exceeds 64 KiB")
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO observations VALUES (?, ?)", record.Sequence, string(body))
	return err
}

func validateRecord(record Record, version int) error {
	if err := validateResources(record, version); err != nil {
		return err
	}
	if record.Sequence <= 0 {
		return errors.New("observation sequence must be positive")
	}
	if record.Kind != "snapshot" && record.Kind != "gap" && record.Kind != "evidence" && record.Kind != "source_status" {
		return errors.New("unknown observation kind")
	}
	if (record.Kind == "snapshot") != (record.Snapshot != nil) {
		return errors.New("snapshot payload does not match observation kind")
	}
	if record.StartElapsedNS < 0 || record.EndElapsedNS < record.StartElapsedNS {
		return errors.New("invalid observation bounds")
	}
	if record.Kind == "gap" && (record.Error == "" || len(record.Transitions) != 0) {
		return errors.New("gap must contain an error and no lifecycle transitions")
	}
	if record.Source != "" && record.Source != "logcat" && record.Source != "exit_history" {
		return errors.New("unknown observation source")
	}
	if (record.Kind == "evidence") != (record.Evidence != nil) {
		return errors.New("evidence payload does not match observation kind")
	}
	if (record.Kind == "source_status") != (record.SourceStatus != nil) || (record.SourceStatus != nil && (record.Source == "" || record.SourceStatus.SelectedObservations < 0 || record.SourceStatus.SelectedObservations > 512)) {
		return errors.New("invalid source read status")
	}
	if record.Source != "" && (record.Snapshot != nil || len(record.Transitions) != 0) {
		return errors.New("diagnostic source cannot change lifecycle")
	}
	if e := record.Evidence; e != nil {
		if record.Source == "" || len(e.Text) > 8192 || len(e.Events) == 0 || len(e.Events) > 16 {
			return errors.New("invalid or excessive evidence")
		}
		lines := strings.Count(e.Text, "\n") + 1
		for _, event := range e.Events {
			switch event.Kind {
			case "java_fatal", "native_crash", "crash_report", "anr_report", "exit_report":
			default:
				return errors.New("unknown diagnostic event")
			}
			if event.PID <= 0 || event.Process == "" || event.FirstLine < 1 || event.LastLine < event.FirstLine || event.LastLine > lines {
				return errors.New("invalid evidence reference")
			}
		}
	}
	for _, event := range record.Transitions {
		if event.Kind != "appeared" && event.Kind != "no_longer_observed" {
			return errors.New("unknown lifecycle transition")
		}
	}
	return nil
}

func (s *Store) Finish(ctx context.Context, status string) error {
	if status != "complete" && status != "interrupted" && status != "failed" {
		return errors.New("invalid completion status")
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT payload FROM metadata WHERE id=1").Scan(&raw); err != nil {
		return err
	}
	var meta Metadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return err
	}
	now := time.Now().UTC()
	meta.Status, meta.EndedUTC = status, &now
	body, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE metadata SET payload=? WHERE id=1", string(body))
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// Replay streams a recording read-only and never depends on ADB.
// A hot rollback journal requires SQLite recovery; errors are reported without
// changing the source recording. Automatic recovery is outside this first slice.
func Replay(ctx context.Context, path string, header func(Metadata) error, visit func(Record) error) error {
	db, err := open(path, "ro")
	if err != nil {
		return err
	}
	defer db.Close()
	meta, err := readMetadata(ctx, db)
	if err != nil {
		return err
	}
	if err := header(meta); err != nil {
		return err
	}
	return scanRecords(ctx, db, meta.Schema, visit)
}

type recordingQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readMetadata(ctx context.Context, q recordingQuery) (Metadata, error) {
	var version int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return Metadata{}, err
	}
	if version != 1 && version != SchemaVersion && version != ResourceSchemaVersion {
		return Metadata{}, fmt.Errorf("unsupported session schema %d", version)
	}
	var raw sql.NullString
	if err := q.QueryRowContext(ctx, "SELECT CASE WHEN length(CAST(payload AS BLOB)) <= 65536 THEN payload END FROM metadata WHERE id=1").Scan(&raw); err != nil {
		return Metadata{}, err
	}
	if !raw.Valid {
		return Metadata{}, errors.New("stored metadata exceeds 64 KiB")
	}
	var meta Metadata
	if err := json.Unmarshal([]byte(raw.String), &meta); err != nil {
		return Metadata{}, err
	}
	if meta.Schema != version {
		return Metadata{}, errors.New("inconsistent metadata schema")
	}
	switch meta.Status {
	case "recording", "interrupted", "complete", "failed":
	default:
		return Metadata{}, errors.New("unknown recording status")
	}
	return meta, nil
}

const selectRecords = `SELECT sequence,
CASE WHEN length(CAST(payload AS BLOB)) <= 65536 THEN payload END
FROM observations`

func scanRecords(ctx context.Context, q recordingQuery, version int, visit func(Record) error) error {
	rows, err := q.QueryContext(ctx, selectRecords+" ORDER BY sequence")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		record, err := readRecord(rows, version)
		if err != nil {
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return rows.Err()
}

func readRecord(row interface{ Scan(...any) error }, version int) (Record, error) {
	var seq int64
	var payload sql.NullString
	if err := row.Scan(&seq, &payload); err != nil {
		return Record{}, err
	}
	if !payload.Valid {
		return Record{}, errors.New("stored observation exceeds 64 KiB")
	}
	var record Record
	if err := json.Unmarshal([]byte(payload.String), &record); err != nil {
		return Record{}, fmt.Errorf("observation %d: %w", seq, err)
	}
	if record.Sequence != seq {
		return Record{}, fmt.Errorf("inconsistent observation sequence %d", seq)
	}
	if version == 1 && (record.Source != "" || record.Evidence != nil || record.SourceStatus != nil || record.Kind == "evidence") {
		return Record{}, errors.New("schema 2 record in schema 1 recording")
	}
	if err := validateRecord(record, version); err != nil {
		return Record{}, fmt.Errorf("observation %d: %w", seq, err)
	}
	return record, nil
}
