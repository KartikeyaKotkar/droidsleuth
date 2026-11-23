package investigate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

func TestInvestigationFindingReferenceAndGapLimits(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bounded.db")
	s, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(0)
	appendRecord := func(r session.Record) {
		t.Helper()
		seq++
		r.Sequence = seq
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	uid := 10123
	event := func(i int) session.Event {
		return session.Event{Kind: "anr_report", Process: "com.example.app", PID: 42, UID: &uid, DeviceTimeNS: 1700000000000000000 + int64(i), FirstLine: 1, LastLine: 1}
	}
	evidence := func(e session.Event) session.Record {
		return session.Record{Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{Format: "fixture", Text: "synthetic report", Events: []session.Event{e}}}
	}
	for i := 0; i < limit+1; i++ {
		appendRecord(evidence(event(i)))
	}
	for i := 0; i < 17; i++ {
		appendRecord(evidence(event(0)))
	}
	for i := 0; i < 257; i++ {
		appendRecord(session.Record{Kind: "gap", Source: "logcat", Error: "synthetic gap"})
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Analyze(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 4096 || len(r.Findings[0].Evidence) != 16 || len(r.Gaps) != 256 {
		t.Fatalf("bounds lost: findings=%d refs=%d gaps=%d", len(r.Findings), len(r.Findings[0].Evidence), len(r.Gaps))
	}
	if !strings.Contains(strings.Join(r.Limitations, " "), "bounds reached") {
		t.Fatal("analysis truncation hidden")
	}
	for _, f := range r.Findings {
		if len(f.Uncertainty) == 0 {
			t.Fatal("unknown instance became certain")
		}
	}
}
func TestInvestigationInstanceAndCandidateLimits(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "instances.db")
	s, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	uid := 10123
	for i := 1; i <= limit+1; i++ {
		p := session.Process{Identity: session.Identity{BootID: "boot", UID: uid, PID: 42, StartTicks: uint64(i)}, Name: "com.example.app"}
		if err = s.Append(ctx, session.Record{Sequence: int64(i), Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot", DeviceEpochSeconds: 1700000000, Processes: []session.Process{p}}}); err != nil {
			t.Fatal(err)
		}
	}
	e := session.Event{Kind: "java_fatal", Process: "com.example.app", PID: 42, UID: &uid, BootID: "boot", DeviceTimeNS: 1700000000123456000, FirstLine: 1, LastLine: 1}
	if err = s.Append(ctx, session.Record{Sequence: limit + 2, Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{Format: "fixture", Text: "synthetic fatal", Events: []session.Event{e}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || len(report.Findings[0].Candidates) != 16 {
		t.Fatal("candidate bound lost")
	}
	for i, id := range report.Findings[0].Candidates {
		if id.StartTicks != uint64(i+1) {
			t.Fatalf("candidate selection nondeterministic at %d: %s", i, fmt.Sprint(id))
		}
	}
	if len(report.Findings[0].Uncertainty) < 2 || !strings.Contains(strings.Join(report.Limitations, " "), "bounds reached") {
		t.Fatal("clipped candidates lost explicit ambiguity")
	}
}
