package investigate

import (
	"context"
	"testing"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

func TestReadEvidenceVerifiesExactReference(t *testing.T) {
	f := newTimelineFixture(t)
	f.append(session.Record{Kind: "gap", Error: "disconnect"})
	f.append(session.Record{Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{
		Format: "fixture", Text: "outside\nreport\x1b[2J\n\noutside", Truncated: true,
		Events: []session.Event{{Kind: "java_fatal", Process: "com.example.app", PID: 3, FirstLine: 2, LastLine: 3}},
	}})
	f.finish()
	ref := Reference{Sequence: 2, FirstLine: 2, LastLine: 3}
	got, err := ReadEvidence(context.Background(), f.path, ref)
	if err != nil || got.Reference != ref || got.Text != "report\x1b[2J\n" || got.Source != "logcat" || got.Format != "fixture" || !got.Truncated {
		t.Fatalf("wrong stored slice: %+v, %v", got, err)
	}
	for _, ref := range []Reference{{2, 1, 3}, {2, 2, 2}, {2, 2, 5}, {2, 0, 0}, {1, 1, 1}, {3, 2, 3}} {
		if slice, err := ReadEvidence(context.Background(), f.path, ref); err == nil || slice.Text != "" {
			t.Errorf("accepted unmatched reference %+v: %+v, %v", ref, slice, err)
		}
	}
}
