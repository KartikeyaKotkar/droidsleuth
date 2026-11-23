package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// Recording exposes a consistent, read-only SQLite snapshot for one analysis.
type Recording struct {
	tx       *sql.Tx
	Metadata Metadata
}

func (r Recording) ReadRecord(ctx context.Context, sequence int64) (Record, error) {
	return readRecord(r.tx.QueryRowContext(ctx, selectRecords+" WHERE sequence = ?", sequence), r.Metadata)
}

func (r Recording) Scan(ctx context.Context, visit func(Record) error) error {
	return scanRecords(ctx, r.tx, r.Metadata, visit)
}

// WithFinalizedRecording keeps all reads on the same transaction. The path check
// additionally catches replacement of the database while the transaction is open.
func WithFinalizedRecording(ctx context.Context, path string, use func(Recording) error) error {
	before, err := recordingInfo(path)
	if err != nil {
		return err
	}
	db, err := open(path, "ro")
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	meta, err := readMetadata(ctx, tx)
	if err != nil {
		return err
	}
	if meta.Status == "recording" {
		return errors.New("timeline requires a finalized recording; this recording is active or incomplete")
	}
	if err := use(Recording{tx: tx, Metadata: meta}); err != nil {
		return err
	}
	return checkRecordingInfo(path, before)
}

func recordingInfo(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return nil, errors.New("expected a regular recording of at most 256 MiB")
	}
	return info, nil
}

func checkRecordingInfo(path string, before os.FileInfo) error {
	after, err := recordingInfo(path)
	if err != nil {
		return fmt.Errorf("recording changed during read: %w", err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("recording changed during read")
	}
	return nil
}
