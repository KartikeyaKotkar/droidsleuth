package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"droidsleuth/internal/session"
)

type Options struct {
	Path     string
	Interval time.Duration
	// Visit is called only after a record has committed.
	Visit func(session.Record) error
}

// Run uses a single collector and writer: no unbounded queue, overlapping polls,
// or live presentation state can race with persistence. Duration is controlled
// by ctx. Caller cancellation ends capture cleanly, preserving an interrupted status.
func Run(ctx context.Context, shell Shell, meta session.Metadata, opts Options) (err error) {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	command, err := SnapshotCommand(meta.Package, meta.UID)
	if err != nil {
		return err
	}
	started := time.Now()
	meta.StartedUTC = started.UTC()
	store, err := session.Create(ctx, opts.Path, meta)
	if err != nil {
		return err
	}
	status := "interrupted"
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err != nil {
			status = "failed"
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "complete"
		}
		err = errors.Join(err, store.Finish(finishCtx, status), store.Close())
	}()
	var tracker Tracker
	var sequence int64
	for {
		if ctx.Err() != nil {
			return nil
		}
		begin := time.Now()
		out, collectErr := shell.Shell(ctx, command)
		end := time.Now()
		sequence++
		record := session.Record{Sequence: sequence, StartedUTC: begin.UTC(), EndedUTC: end.UTC(), StartElapsedNS: begin.Sub(started).Nanoseconds(), EndElapsedNS: end.Sub(started).Nanoseconds()}
		var snapshot session.Snapshot
		if collectErr == nil {
			snapshot, collectErr = ParseSnapshot(out, meta.Package, meta.UID)
		}
		if collectErr != nil {
			record.Kind, record.Error = "gap", collectErr.Error()
			tracker.Gap()
		} else {
			record.Kind, record.Snapshot = "snapshot", &snapshot
			record.Transitions = tracker.Observe(snapshot)
		}
		// A canceled collector still records the interrupted observation attempt.
		writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		writeErr := store.Append(writeCtx, record)
		cancel()
		if writeErr != nil {
			return fmt.Errorf("persist observation: %w", writeErr)
		}
		if opts.Visit != nil {
			if err := opts.Visit(record); err != nil {
				return err
			}
		}
		// Leave room for completion metadata and the bounded rollback journal.
		if info, statErr := os.Stat(opts.Path); statErr != nil {
			return statErr
		} else if info.Size() >= 240<<20 {
			return errors.New("recording reached the 240 MiB stop threshold (256 MiB database limit)")
		}
		if ctx.Err() != nil {
			return nil
		}
		// Delay after work deliberately skips missed polls; no catch-up bursts.
		timer := time.NewTimer(opts.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
