package capture

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/evidence"
	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

// CollectHistory saves retained exit reports for a previously probed target.
func CollectHistory(ctx context.Context, shell Shell, meta session.Metadata, path string) (count int, created bool, err error) {
	started := time.Now()
	meta.StartedUTC = started.UTC()
	meta.Schema = session.SchemaVersion
	store, err := session.Create(ctx, path, meta)
	if err != nil {
		return 0, false, err
	}
	created = true
	defer func() {
		status := "complete"
		if errors.Is(ctx.Err(), context.Canceled) {
			status = "interrupted"
			if !errors.Is(err, context.Canceled) {
				err = errors.Join(err, context.Canceled)
			}
		} else if err != nil {
			status = "failed"
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = errors.Join(err, store.Finish(finishCtx, status), store.Close())
	}()

	var sequence int64
	appendRecord := func(writeCtx context.Context, record session.Record, begin, end time.Time) error {
		sequence++
		record.Sequence, record.Source = sequence, "exit_history"
		record.StartedUTC, record.EndedUTC = begin.UTC(), end.UTC()
		record.StartElapsedNS, record.EndElapsedNS = begin.Sub(started).Nanoseconds(), end.Sub(started).Nanoseconds()
		if err := store.Append(writeCtx, record); err != nil {
			return fmt.Errorf("persist exit-history observation: %w", err)
		}
		return nil
	}
	scope := session.Record{Kind: "gap", Error: "retained exit history only; live process, resource, and logcat observations unavailable"}
	if err := appendRecord(ctx, scope, started, started); err != nil {
		return count, created, err
	}

	begin := time.Now()
	observations, issue := evidence.ReadExitHistory(ctx, shell, meta)
	end := time.Now()
	readErr := ctx.Err()
	if readErr != nil {
		observations = nil
		issue = "exit-history read incomplete: " + readErr.Error()
	}

	// Record failed or cancelled reads even when acquisition has ended.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	status := session.Record{Kind: "source_status", SourceStatus: &session.SourceStatus{
		ReadSucceeded: issue == "", SelectedObservations: len(observations),
	}}
	if err := appendRecord(writeCtx, status, begin, end); err != nil {
		return count, created, errors.Join(readErr, err)
	}
	if issue != "" {
		if err := appendRecord(writeCtx, session.Record{Kind: "gap", Error: issue}, begin, end); err != nil {
			return count, created, errors.Join(readErr, err)
		}
		if readErr == nil {
			readErr = errors.New(issue)
		}
	}
	for _, observation := range observations {
		if err := ctx.Err(); err != nil {
			return count, created, errors.Join(readErr, err)
		}
		if err := appendRecord(writeCtx, session.Record{Kind: "evidence", Evidence: &observation}, begin, end); err != nil {
			return count, created, errors.Join(readErr, err)
		}
		count++
		if observation.Truncated {
			if err := appendRecord(writeCtx, session.Record{Kind: "gap", Error: "selected diagnostic excerpt truncated"}, begin, end); err != nil {
				return count, created, errors.Join(readErr, err)
			}
		}
	}
	if readErr == nil {
		readErr = ctx.Err()
	}
	return count, created, readErr
}
