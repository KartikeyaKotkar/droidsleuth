package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

func TestInvestigateTimelineCLI(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(root, "droidsleuth")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	makeRecording := func(second int64, finished bool) string {
		t.Helper()
		path := filepath.Join(dir, session.ManagedFilename(time.Unix(second, 0)))
		store, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app"})
		if err != nil {
			t.Fatal(err)
		}
		p := session.Process{Identity: session.Identity{BootID: "boot", UID: 10123, PID: 17, StartTicks: 42}, Name: "com.example.app"}
		rows := []session.Record{
			{Sequence: 1, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot", Processes: []session.Process{p}}},
			{Sequence: 2, Kind: "gap", Source: "logcat", Error: "source failed\x1b[2J"},
			{Sequence: 3, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "boot"}, Transitions: []session.Transition{{Kind: "no_longer_observed", Process: p}}},
		}
		for _, r := range rows {
			if err := store.Append(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		if finished {
			if err := store.Finish(ctx, "interrupted"); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := makeRecording(2, true)
	active := makeRecording(3, false)
	var out, diagnostic bytes.Buffer
	call := func(args ...string) error {
		out.Reset()
		diagnostic.Reset()
		return run(ctx, append([]string{"investigate"}, args...), &out, &diagnostic)
	}
	if err := call("--json", path); err != nil {
		t.Fatal(err)
	}
	const legacy = `{"version":1,"package":"com.example.app","findings":[],"collection_gaps":[{"source":"logcat","sequence":2,"kind":"gap","started_utc":"0001-01-01T00:00:00Z","ended_utc":"0001-01-01T00:00:00Z","start_elapsed_ns":0,"end_elapsed_ns":0,"error":"source failed\u001b[2J"}],"limitations":["Reports describe source claims, not proven causes of observed disappearance.","Logcat is a finite filtered tail; silent buffer loss and incidents before collection may be unobservable.","Source realtime timestamps are distinct from host collection bounds; clock changes and delayed records may prevent instance association."],"unexplained_disappearances":1}`
	if out.String() != legacy+"\n" {
		t.Fatalf("legacy JSON changed: %s", out.String())
	}
	if err := call(path); err != nil {
		t.Fatal(err)
	}
	const legacyText = "Package \"com.example.app\"; 0 reported events; 1 observed disappearances (cause unknown)\n" +
		"#2 [0.000..0.000s] coverage gap (logcat): \"source failed\\x1b[2J\"\n" +
		"Limitation: Reports describe source claims, not proven causes of observed disappearance.\n" +
		"Limitation: Logcat is a finite filtered tail; silent buffer loss and incidents before collection may be unobservable.\n" +
		"Limitation: Source realtime timestamps are distinct from host collection bounds; clock changes and delayed records may prevent instance association.\n"
	if out.String() != legacyText {
		t.Fatalf("legacy text changed: %q", out.String())
	}

	if err := call("--last", "--json"); err != nil {
		t.Fatal(err)
	}
	if out.String() != legacy+"\n" || !strings.Contains(diagnostic.String(), "Skipped 1") || !strings.Contains(diagnostic.String(), path) {
		t.Fatalf("--last: out=%q diagnostic=%q", out.String(), diagnostic.String())
	}
	if err := call("--last", "--timeline", "--json"); err != nil {
		t.Fatal(err)
	}
	var r struct {
		ReportType      string `json:"report_type"`
		View, Recording string
		Incidents       []struct{ ID string }
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.ReportType != "incident_timeline" || r.View != "index" || r.Recording != path || len(r.Incidents) != 1 || r.Incidents[0].ID != "L3.0" {
		t.Fatalf("index: %s", out.String())
	}
	if err := call("--timeline", "--incident", "L3.0", path); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Last observed presence #1", "First observed absence #3", "source failed", "Disappearance cause unknown", "Snapshot gaps between endpoints: 0"} {
		if !strings.Contains(out.String(), s) {
			t.Fatalf("detail missing %q in %s", s, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("unescaped terminal control")
	}
	for _, args := range [][]string{
		{"--timeline", active}, {"--last", path}, {"--incident", "L3.0", path},
		{"--timeline", "--before", "30s", path}, {"--timeline", "--incident", "", path},
		{"--timeline", "--incident", "L3.0", "--after", "6m", path},
		{"--timeline", "--incident", "L3.0", "--before", "-1s", path},
	} {
		if err := call(args...); err == nil || out.Len() != 0 {
			t.Fatalf("expected clean flag/input error for %v", args)
		}
	}
	if err := run(ctx, []string{"investigate", "--timeline", path}, failedWriter{}, io.Discard); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost output error: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected discovery side effects: %v %v", entries, err)
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestTimelineSchemaOneWithoutADB(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "schema1.db")
	s, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	p := session.Process{Identity: session.Identity{BootID: "old", UID: 10123, PID: 12, StartTicks: 3}, Name: "com.example.app"}
	for _, r := range []session.Record{
		{Sequence: 1, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "old", Processes: []session.Process{p}}},
		{Sequence: 2, Kind: "snapshot", Snapshot: &session.Snapshot{BootID: "old"}, Transitions: []session.Transition{{Kind: "no_longer_observed", Process: p}}},
	} {
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Finish(ctx, "complete"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=1; UPDATE metadata SET payload=replace(payload,'"schema":2','"schema":1')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"investigate", "--timeline", "--incident", "L2.0", path}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"schema 1", "Last observed presence #1", "First observed absence #2", "Schema 1 contains no diagnostic collectors"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q from %s", want, out.String())
		}
	}
}

func TestInvestigateTUIFlags(t *testing.T) {
	var out bytes.Buffer
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--tui", "--json", "recording.db"}, "--tui cannot be combined with --json"},
		{[]string{"--tui", "--before", "5s", "recording.db"}, "--before and --after require --incident"},
		{[]string{"--tui", "--incident", "L1.0", "--before", "5s", "recording.db"}, "--tui requires terminal input and output"},
	} {
		err := run(context.Background(), append([]string{"investigate"}, tc.args...), &out, io.Discard)
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) || out.Len() != 0 {
			t.Errorf("%v: %v, output %q", tc.args, err, out.String())
		}
	}
}
