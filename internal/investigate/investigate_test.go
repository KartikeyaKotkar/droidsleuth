package investigate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"droidsleuth/internal/session"
)

func TestOfflineReuseContradictionsAndDeterminism(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recording.db")
	s, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	uid := 10123
	p := session.Process{Identity: session.Identity{BootID: "boot", UID: uid, PID: 42, StartTicks: 10}, Name: "com.example.app"}
	appendRecord := func(r session.Record) {
		t.Helper()
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	appendRecord(session.Record{Sequence: 1, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot", DeviceEpochSeconds: 100, Processes: []session.Process{p}}})
	p.StartTicks = 20
	appendRecord(session.Record{Sequence: 2, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot", DeviceEpochSeconds: 100, Processes: []session.Process{p}}})
	for i, reason := range []int{4, 10} {
		reason := reason
		e := session.Event{Kind: "exit_report", Process: p.Name, PID: 42, UID: &uid, DeviceTimeNS: 100000000000, Reason: &reason, FirstLine: 1, LastLine: 1}
		appendRecord(session.Record{Sequence: int64(3 + i), Kind: "evidence", Source: "exit_history", Evidence: &session.Evidence{Format: "fixture", Text: "explicit exit report", Events: []session.Event{e}}})
	}
	appendRecord(session.Record{Sequence: 5, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot", DeviceEpochSeconds: 102}, Transitions: []session.Transition{{Kind: "no_longer_observed", Process: p}}})
	s.Close()
	t.Setenv("PATH", t.TempDir())
	before, _ := os.ReadFile(path)
	first, err := Analyze(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Findings) != 2 || first.Disappearances != 1 {
		t.Fatalf("wrong report %+v", first)
	}
	for _, f := range first.Findings {
		if !f.Conflict || len(f.Candidates) != 2 || len(f.Uncertainty) < 2 {
			t.Fatalf("lost ambiguity %+v", f)
		}
	}
	b, _ := json.Marshal(first)
	for i := 0; i < 10; i++ {
		next, err := Analyze(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		other, _ := json.Marshal(next)
		if string(b) != string(other) {
			t.Fatal("nondeterministic investigation")
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("investigation changed recording")
	}
}
func TestDisappearanceAloneDoesNotProduceCrash(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "empty.db")
	s, err := session.Create(ctx, path, session.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, session.Record{Sequence: 1, Kind: "snapshot", Snapshot: &session.Snapshot{}, Transitions: []session.Transition{{Kind: "no_longer_observed"}}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	r, err := Analyze(ctx, path)
	if err != nil || len(r.Findings) != 0 || r.Disappearances != 1 {
		t.Fatalf("fabricated diagnosis %+v %v", r, err)
	}
}

func TestTimestampOutsideObservedSecondStaysUnbound(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "late.db")
	s, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	uid := 10123
	p := session.Process{Identity: session.Identity{BootID: "boot", UID: uid, PID: 42, StartTicks: 10}, Name: "com.example.app"}
	if err = s.Append(ctx, session.Record{Sequence: 1, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot", DeviceEpochSeconds: 100, Processes: []session.Process{p}}}); err != nil {
		t.Fatal(err)
	}
	event := session.Event{Kind: "java_fatal", Process: p.Name, PID: 42, UID: &uid, BootID: "boot", DeviceTimeNS: 101999999000, TimeUncertaintyNS: 1000, FirstLine: 1, LastLine: 1}
	if err = s.Append(ctx, session.Record{Sequence: 2, Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{Text: "delayed report", Format: "fixture", Events: []session.Event{event}}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	report, err := Analyze(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || len(report.Findings[0].Candidates) != 0 {
		t.Fatal("timestamp outside observed second received an instance candidate")
	}
}
