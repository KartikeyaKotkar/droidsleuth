package investigate

import (
	"context"
	"fmt"

	"droidsleuth/internal/session"
)

// EvidenceSlice is the exact stored line range of a normalized report.
type EvidenceSlice struct {
	Reference Reference
	Source    string
	Format    string
	Text      string
	Truncated bool
}

// ReadEvidence returns only ranges referenced by events in a finalized
// recording. ReadRecord validates the stored line bounds and 8 KiB text limit.
func ReadEvidence(ctx context.Context, path string, ref Reference) (EvidenceSlice, error) {
	var slice EvidenceSlice
	err := session.WithFinalizedRecording(ctx, path, func(r session.Recording) error {
		row, err := r.ReadRecord(ctx, ref.Sequence)
		if err != nil {
			return fmt.Errorf("read evidence observation #%d: %w", ref.Sequence, err)
		}
		if e := row.Evidence; e != nil {
			for _, event := range e.Events {
				if event.FirstLine == ref.FirstLine && event.LastLine == ref.LastLine {
					slice = EvidenceSlice{
						Reference: ref, Source: row.Source, Format: e.Format,
						Text: referenceText(e.Text, ref.FirstLine, ref.LastLine), Truncated: e.Truncated,
					}
					return nil
				}
			}
		}
		return fmt.Errorf("observation #%d has no evidence reference for lines %d–%d", ref.Sequence, ref.FirstLine, ref.LastLine)
	})
	if err != nil {
		return EvidenceSlice{}, err
	}
	return slice, nil
}
