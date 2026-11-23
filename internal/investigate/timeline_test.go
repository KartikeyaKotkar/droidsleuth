package investigate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

type timelineFixture struct {
	t     *testing.T
	store *session.Store
	path  string
	seq   int64
}

func newTimelineFixture(t *testing.T) *timelineFixture {
	return newTimelineFixtureWithSchema(t, session.SchemaVersion)
}

func TestPrivateIncidentMainObservedAtBothEndpoints(t *testing.T) {
	f := newTimelineFixtureWithSchema(t, session.PrivateSchemaVersion)
	main := session.Process{Name: "com.example.app", Identity: session.Identity{BootID: "boot", UID: 10123, PID: 101, StartTicks: 1000}}
	worker := session.Process{Name: main.Name + ":remote", Identity: session.Identity{BootID: main.BootID, UID: main.UID, PID: 102, StartTicks: 1001}}
	f.append(session.Record{Kind: "snapshot", Snapshot: &session.Snapshot{BootID: main.BootID, Processes: []session.Process{main, worker}}})
	f.append(session.Record{Kind: "gap", Source: "logcat", Error: "unavailable"})
	f.append(session.Record{Kind: "gap", Error: "disconnect"})
	f.append(session.Record{Kind: "snapshot", Snapshot: &session.Snapshot{BootID: main.BootID, Processes: []session.Process{main}},
		Transitions: []session.Transition{{Kind: "no_longer_observed", Process: worker, AfterGap: true}}})
	f.finish()
	index := f.timeline("", DefaultContext, DefaultContext)
	if len(*index.Incidents) != 1 || (*index.Incidents)[0].Process != worker.Name {
		t.Fatalf("missing worker anchor: %+v", index.Incidents)
	}
	l := f.timeline("L4.0", DefaultContext, DefaultContext).Incident.Lifecycle
	if l.LastPresence == nil || l.SnapshotGaps == nil || *l.SnapshotGaps != 1 || !l.Transition.AfterGap {
		t.Fatalf("worker coverage lost: %+v", l)
	}
	for _, end := range []*SnapshotEndpoint{l.LastPresence, &l.FirstAbsence} {
		if end.MainProcesses == nil || len(*end.MainProcesses) != 1 || (*end.MainProcesses)[0] != main.Identity {
			t.Fatalf("missing matched main identity at snapshot: %+v", end)
		}
	}
}

func newTimelineFixtureWithSchema(t *testing.T, schema int) *timelineFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timeline.db")
	s, err := session.Create(context.Background(), path, session.Metadata{Schema: schema, Package: "com.example.app", UID: 10123})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return &timelineFixture{t: t, store: s, path: path}
}

func (f *timelineFixture) append(row session.Record) {
	f.t.Helper()
	f.seq++
	row.Sequence = f.seq
	if row.StartElapsedNS == 0 {
		row.StartElapsedNS = f.seq * int64(time.Second)
	}
	if row.EndElapsedNS == 0 {
		row.EndElapsedNS = row.StartElapsedNS
	}
	if err := f.store.Append(context.Background(), row); err != nil {
		f.t.Fatal(err)
	}
}

func (f *timelineFixture) finish() {
	f.t.Helper()
	if err := f.store.Finish(context.Background(), "interrupted"); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *timelineFixture) timeline(id string, before, after time.Duration) TimelineReport {
	f.t.Helper()
	var anchor *IncidentID
	if id != "" {
		v, err := ParseIncidentID(id)
		if err != nil {
			f.t.Fatal(err)
		}
		anchor = &v
	}
	report, err := Timeline(context.Background(), f.path, anchor, before, after)
	if err != nil {
		f.t.Fatal(err)
	}
	return report
}

func TestTimelineFactsAndUncertainty(t *testing.T) {
	f := newTimelineFixture(t)
	old := session.Process{Identity: session.Identity{BootID: "boot", UID: 10123, PID: 42, StartTicks: 10}, Name: "com.example.app"}
	replacement := old
	replacement.StartTicks = 20
	snapshot := func(epoch int64, p session.Process) *session.Snapshot {
		return &session.Snapshot{BootID: p.BootID, DeviceEpochSeconds: epoch, Processes: []session.Process{p}}
	}
	f.append(session.Record{Kind: "snapshot", Snapshot: snapshot(100, old), Transitions: []session.Transition{{Kind: "appeared", Process: old}}})
	e := session.Event{Kind: "anr_report", Process: old.Name, PID: 42, UID: &old.UID, BootID: old.BootID, DeviceTimeNS: 100_123_456_000, TimeUncertaintyNS: 1000, FirstLine: 2, LastLine: 2}
	f.append(session.Record{Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{Format: "fixture", Text: "prefix\nexplicit ANR\nsuffix", Events: []session.Event{e}}})
	f.append(session.Record{Kind: "gap", Source: "logcat", Error: "rolled over"})
	f.append(session.Record{Kind: "gap", Error: "disconnected"})
	// Same PID, distinct start ticks. Device clock is unchanged here and moves
	// backwards later. Collection order is still unambiguous.
	f.append(session.Record{Kind: "snapshot", Snapshot: snapshot(100, replacement), Transitions: []session.Transition{
		{Kind: "no_longer_observed", Process: old, AfterGap: true}, {Kind: "appeared", Process: replacement, AfterGap: true},
	}})
	f.append(session.Record{Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{Format: "fixture", Text: "prefix\nexplicit ANR\nsuffix", Events: []session.Event{e}}})
	for _, reason := range []int{4, 10} {
		e := session.Event{Kind: "exit_report", Process: old.Name, PID: 42, UID: &old.UID, DeviceTimeNS: 90_123_000_000, TimeUncertaintyNS: 1_000_000, Reason: &reason, FirstLine: 1, LastLine: 1}
		f.append(session.Record{Kind: "evidence", Source: "exit_history", Evidence: &session.Evidence{Format: "fixture", Text: "exit report", Events: []session.Event{e}}})
	}
	replacement.BootID = "other-boot"
	f.append(session.Record{Kind: "snapshot", Snapshot: snapshot(91, replacement), Transitions: []session.Transition{{Kind: "appeared", Process: replacement}}})
	f.finish()
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	idx := f.timeline("", 0, 0)
	if idx.Incidents == nil {
		t.Fatal("missing index")
	}
	var ids []string
	for _, a := range *idx.Incidents {
		ids = append(ids, a.ID)
	}
	if got := strings.Join(ids, ","); got != "E2.0,L5.0,E7.0,E8.0" {
		t.Fatalf("anchors: %s", got)
	}
	d := f.timeline("L5.0", 30*time.Second, 30*time.Second).Incident
	if d.Lifecycle.LastPresence.Sequence != 1 || d.Lifecycle.FirstAbsence.Sequence != 5 ||
		*d.Lifecycle.SnapshotGaps != 1 || !d.Lifecycle.Transition.AfterGap {
		t.Fatalf("incorrect lifecycle endpoints: %+v", d.Lifecycle)
	}
	if len(d.AnchorObservation.Transitions) != 2 || d.AnchorObservation.Transitions[1].Process.StartTicks != 20 {
		t.Fatal("same-observation replacement lost")
	}
	for i, row := range d.Context {
		if i > 0 && d.Context[i-1].Sequence >= row.Sequence {
			t.Fatal("context moved out of sequence")
		}
	}
	a := f.timeline("E6.0", 0, 0).Incident.Report
	if a.Association != "candidate_unproven" || len(a.Candidates) != 2 || a.Excerpt != "explicit ANR" || a.Reference.Sequence != 6 || len(a.Evidence) != 2 {
		t.Fatalf("duplicate anchor detail: %+v", a)
	}
	exit := f.timeline("E8.0", 0, 0).Incident.Report
	if exit.Event.DeviceTimeNS != 90_123_000_000 || exit.Association != "unbound" || !exit.Conflict {
		t.Fatalf("exit report: %+v", exit)
	}
	first, _ := json.Marshal(d)
	second, _ := json.Marshal(f.timeline("L5.0", 30*time.Second, 30*time.Second).Incident)
	after, err := os.ReadFile(f.path)
	if err != nil || !bytes.Equal(first, second) || !bytes.Equal(before, after) {
		t.Fatal("analysis is not deterministic and read-only")
	}
}

func TestTimelineAnchorAndContextLimits(t *testing.T) {
	f := newTimelineFixture(t)
	p := session.Process{Identity: session.Identity{BootID: "boot", PID: 42, StartTicks: 1}, Name: "com.example.app"}
	for i := 0; i < limit+1; i++ {
		f.append(session.Record{Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot"}, Transitions: []session.Transition{{Kind: "no_longer_observed", Process: p}, {Kind: "no_longer_observed", Process: p}}})
	}
	f.finish()
	idx := f.timeline("", 0, 0)
	if len(*idx.Incidents) != limit || !idx.IndexTruncated || (*idx.Incidents)[1].ID != "L1.1" {
		t.Fatal("index bounds or stable IDs lost")
	}
	d := f.timeline(fmt.Sprintf("L%d.1", limit-150), MaxContext, MaxContext).Incident
	if len(d.Context) != 200 || d.BeforeOmitted != 200 || d.AfterOmitted != 51 || d.Lifecycle.LastPresence != nil || d.Lifecycle.SnapshotGaps != nil {
		t.Fatalf("context bound or absent endpoints: %d / %d / %d", len(d.Context), d.BeforeOmitted, d.AfterOmitted)
	}
}

func TestTimelineUnfinalizedAndInvalidAnchor(t *testing.T) {
	f := newTimelineFixture(t)
	ctx := context.Background()
	if _, err := Timeline(ctx, f.path, nil, 0, 0); err == nil || !strings.Contains(err.Error(), "finalized") {
		t.Fatalf("active input: %v", err)
	}
	f.finish()
	empty := f.timeline("", 0, 0)
	if empty.Incidents == nil || len(*empty.Incidents) != 0 {
		t.Fatal("expected empty incident index")
	}
	for _, s := range []string{"", "L-1.0", "L01.0", "E1.+0", "L1.-1", "S1.0", "E1.0.0"} {
		if _, err := ParseIncidentID(s); err == nil {
			t.Fatalf("invalid ID accepted: %q", s)
		}
	}
	id, _ := ParseIncidentID("E1.0")
	if _, err := Timeline(ctx, f.path, &id, 0, 0); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing anchor: %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Timeline(ctx, f.path, nil, 0, 0); err == nil {
		t.Fatal("canceled read succeeded")
	}
}

func TestTimelineWindowAndMultipleEvents(t *testing.T) {
	f := newTimelineFixture(t)
	unknown := session.Event{Kind: "exit_report", Process: "com.example.app", PID: 5, RawTime: "ambiguous wall time", FirstLine: 1, LastLine: 1}
	second := unknown
	second.FirstLine, second.LastLine = 2, 2
	second.PID = 9
	f.append(session.Record{Kind: "gap", Source: "logcat", Error: "outside window"})
	f.append(session.Record{Kind: "gap", Source: "logcat", StartElapsedNS: 2_000_000_000, EndElapsedNS: 3_000_000_000, Error: "intersects point"})
	f.append(session.Record{Kind: "evidence", Source: "exit_history", Evidence: &session.Evidence{Format: "fixture", Text: "first\nsecond", Events: []session.Event{unknown, second}}})
	f.append(session.Record{Kind: "gap", Error: strings.Repeat("x", 9<<10)})
	f.finish()
	index := f.timeline("", 0, 0)
	if len(*index.Incidents) != 2 || (*index.Incidents)[1].ID != "E3.1" {
		t.Fatal("lost sibling incident ID")
	}
	d := f.timeline("E3.1", 0, time.Second).Incident
	if len(d.Context) != 2 || d.Context[0].Sequence != 2 || d.Context[1].Sequence != 4 || !d.Context[1].DetailsOmitted {
		t.Fatalf("bad overlap/cap semantics: %+v", d.Context)
	}
	if len(d.AnchorObservation.Reports) != 2 || d.Report.Excerpt != "second" || d.Report.Reference.FirstLine != 2 || d.Report.Event.RawTime != "ambiguous wall time" || d.Report.Association != "unbound" {
		t.Fatalf("lost selected report context: %+v", d.Report)
	}
}
