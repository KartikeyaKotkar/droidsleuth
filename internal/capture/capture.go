package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/evidence"
	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

type Options struct {
	Evidence  bool
	Resources bool
	Path      string
	Interval  time.Duration
	// Visit is called only after a record has committed.
	Visit func(session.Record) error
}

// Run uses one snapshot collector and one writer: no unbounded queue, overlapping snapshot polls,
// or live presentation state can race with persistence. Duration is controlled
// by ctx. Caller cancellation ends capture cleanly, preserving an interrupted status.
func Run(ctx context.Context, shell Shell, meta session.Metadata, opts Options) (err error) {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	command, err := snapshotCommand(meta.Package, meta.UID, opts.Resources)
	if err != nil {
		return err
	}
	started := time.Now()
	meta.StartedUTC = started.UTC()
	meta.Schema = session.SchemaVersion
	if opts.Resources {
		meta.Schema = session.ResourceSchemaVersion
	}
	var units resourceUnits
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
	appendRecord := func(record session.Record) error {
		sequence++
		record.Sequence = sequence
		if record.StartedUTC.IsZero() {
			record.StartedUTC = time.Now()
		}
		if record.EndedUTC.IsZero() {
			record.EndedUTC = record.StartedUTC
		}
		// Snapshot bounds were computed directly from monotonic begin/end clocks.
		// Diagnostic workers keep time.Time's monotonic component until here.
		if record.Source != "" {
			record.StartElapsedNS = record.StartedUTC.Sub(started).Nanoseconds()
			record.EndElapsedNS = record.EndedUTC.Sub(started).Nanoseconds()
		}
		record.StartedUTC = record.StartedUTC.UTC()
		record.EndedUTC = record.EndedUTC.UTC()
		writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := store.Append(writeCtx, record); err != nil {
			return fmt.Errorf("persist observation: %w", err)
		}
		if opts.Visit != nil {
			if err := opts.Visit(record); err != nil {
				return err
			}
		}
		if info, err := os.Stat(opts.Path); err != nil {
			return err
		} else if info.Size() >= 240<<20 {
			return errors.New("recording reached the 240 MiB stop threshold (256 MiB database limit)")
		}
		return nil
	}
	var diagnostics <-chan session.Record
	if opts.Evidence {
		var stop func()
		diagnostics, stop = evidence.Start(ctx, shell, meta)
		drain := diagnostics
		defer func() {
			stop()
			// Preserve the bounded committed-prefix queue on cancellation.
			for record := range drain {
				if e := appendRecord(record); e != nil {
					err = errors.Join(err, e)
					break
				}
			}
			for _, source := range []string{"logcat", "exit_history"} {
				e := appendRecord(session.Record{Kind: "gap", Source: source, Error: "collection ended; final in-flight or delayed source records may be unavailable"})
				err = errors.Join(err, e)
			}
		}()
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		begin := time.Now()
		out, collectErr := shell.Shell(ctx, command)
		end := time.Now()
		record := session.Record{Sequence: sequence, StartedUTC: begin.UTC(), EndedUTC: end.UTC(), StartElapsedNS: begin.Sub(started).Nanoseconds(), EndElapsedNS: end.Sub(started).Nanoseconds()}
		var snapshot session.Snapshot
		if collectErr == nil {
			if opts.Resources {
				record.Resources = &session.Resources{}
			}
			snapshot, collectErr = parseSnapshot(out, meta.Package, meta.UID, record.Resources)
		}
		if collectErr != nil {
			record.Kind, record.Error = "gap", collectErr.Error()
			record.Resources = nil
			tracker.Gap()
		} else {
			record.Kind, record.Snapshot = "snapshot", &snapshot
			record.Transitions = tracker.Observe(snapshot)
			if record.Resources != nil {
				units.apply(ctx, shell, record.Resources)
			}
		}
		if err := appendRecord(record); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		// Delay after work deliberately skips missed polls; no catch-up bursts.
		timer := time.NewTimer(opts.Interval)
	waiting:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case record, ok := <-diagnostics:
				if !ok {
					diagnostics = nil
					continue
				}
				if err := appendRecord(record); err != nil {
					timer.Stop()
					return err
				}
			case <-timer.C:
				break waiting
			}
		}
	}
}
