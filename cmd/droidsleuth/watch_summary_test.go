package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"droidsleuth/internal/session"
)

const watchFixtureADB = `#!/bin/sh
set -eu
root=$WATCH_FIXTURE
if [ "$1" = -s ]; then
    [ "$2" = fixture-phone ]; shift 2
fi
if [ "$1" = devices ]; then
    printf '%s\n' devices >> "$root/trace"
    printf 'List of devices attached\nfixture-phone device\n'
    exit 0
fi
[ "$1" = shell ] && [ "$2" = -T ]
case "$3" in
    'getprop ro.build.version.sdk') printf '%s\n' sdk >> "$root/trace"; echo 30;;
    'am get-current-user') printf '%s\n' user >> "$root/trace"; echo 0;;
    'cmd package list packages -U --user 0 com.example.app')
        printf '%s\n' package >> "$root/trace"; echo 'package:com.example.app uid:10123';;
    *'__DS_CLOCK__'*)
        printf '%s\n' snapshot >> "$root/trace"
        n=1
        if [ -e "$root/count" ]; then read -r n < "$root/count"; fi
        echo "$((n+1))" > "$root/count"
        if [ "$n" = 2 ] && [ "${WATCH_FIXTURE_FIRST_GAP:-}" = 1 ]; then exit 1; fi
        if [ "$n" -lt 3 ]; then name=present; else name=absent; fi
        /bin/cat "$root/$name";;
    'cat /proc/sys/kernel/random/boot_id')
        printf '%s\n' boot >> "$root/trace"; echo 11111111-1111-1111-1111-111111111111;;
    'getprop persist.sys.timezone') printf '%s\n' timezone >> "$root/trace"; echo UTC;;
    'dumpsys activity exit-info com.example.app')
        printf '%s\n' history >> "$root/trace"; echo 'ACTIVITY MANAGER PROCESS EXIT INFO';;
    logcat\ *)
        printf '%s\n' logcat >> "$root/trace"
        echo '1700000000.123456 1000 900 900 I am_anr: [0,101,com.example.app,0,fixture report]'
        case "$*" in *'droidsleuth crash tail'*) printf '\n--------- droidsleuth crash tail\n';; esac;;
    *) printf '%s\n' unexpected >> "$root/trace"; exit 1;;
esac
`

func watchFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "adb"), []byte(watchFixtureADB), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"present", "absent"} {
		input, err := os.ReadFile(filepath.Join("..", "..", "testdata", "snapshot-"+name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), input, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	t.Setenv("WATCH_FIXTURE", root)
	t.Setenv("WATCH_FIXTURE_FIRST_GAP", "")
	return root
}

func watchRecords(t *testing.T, path string) (session.Metadata, []session.Record) {
	t.Helper()
	var meta session.Metadata
	var records []session.Record
	err := session.Replay(context.Background(), path, func(m session.Metadata) error {
		meta = m
		return nil
	}, func(r session.Record) error {
		records = append(records, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return meta, records
}

func TestWatchSummaryCompletionAndLegacyParity(t *testing.T) {
	var legacyRecords, legacyTrace []string
	for _, summary := range []bool{false, true} {
		root := watchFixture(t)
		path := filepath.Join(root, "recording.db")
		var out, diagnostic bytes.Buffer
		args := []string{"watch", "--duration", "1800ms", "--out", path}
		if summary {
			args = append(args, "--summary")
		}
		if err := run(context.Background(), append(args, "com.example.app"), &out, &diagnostic); err != nil {
			t.Fatal(err)
		}
		meta, records := watchRecords(t, path)
		if meta.Status != "complete" || meta.Schema != 2 {
			t.Fatalf("unexpected recording metadata: %+v", meta)
		}

		// Collection order across independent diagnostic workers can differ.
		// Compare recorded facts per source with clock envelopes removed.
		bySource := map[string][]string{}
		for _, r := range records {
			r.Sequence, r.StartElapsedNS, r.EndElapsedNS = 0, 0, 0
			r.StartedUTC, r.EndedUTC = time.Time{}, time.Time{}
			bySource[r.Source] = append(bySource[r.Source], recordJSON(t, r))
		}
		facts := []string{
			strings.Join(bySource[""], "\n"),
			strings.Join(bySource["logcat"], "\n"),
			strings.Join(bySource["exit_history"], "\n"),
		}
		body, err := os.ReadFile(filepath.Join(root, "trace"))
		if err != nil {
			t.Fatal(err)
		}
		trace := strings.Fields(string(body))
		slices.Sort(trace)
		if !summary {
			legacyTrace, legacyRecords = trace, facts
			if !strings.Contains(out.String(), "snapshot: 1 main process(es)") || !strings.Contains(out.String(), "no_longer_observed") || strings.Contains(diagnostic.String(), "Capture ready") {
				t.Fatalf("legacy output changed: %q, %q", out.String(), diagnostic.String())
			}
			continue
		}
		if !slices.Equal(trace, legacyTrace) || !slices.Equal(facts, legacyRecords) {
			t.Fatalf("ADB traces or records changed\nlegacy %v %v\nnew %v %v", legacyTrace, legacyRecords, trace, facts)
		}
		for _, want := range []string{path, "schema 2, status complete", "cause unknown", "instance association unproven", "droidsleuth investigate --tui --incident L"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q in %q", want, out.String())
			}
		}
		if strings.Contains(out.String(), "snapshot: ") || strings.Count(diagnostic.String(), "Capture ready") != 1 {
			t.Fatalf("unexpected capture presentation: %q %q", out.String(), diagnostic.String())
		}
	}
}

type watchWriteFunc func([]byte) (int, error)

func (w watchWriteFunc) Write(p []byte) (int, error) { return w(p) }

func TestWatchSummaryInterruptReadinessAfterCommittedSnapshot(t *testing.T) {
	root := watchFixture(t)
	t.Setenv("WATCH_FIXTURE_FIRST_GAP", "1")
	path := filepath.Join(root, "interrupt.db")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	ready := 0
	diagnostic := watchWriteFunc(func(p []byte) (int, error) {
		if bytes.Contains(p, []byte("Capture ready")) {
			ready++
			meta, records := watchRecords(t, path)
			if out.Len() != 0 || meta.Status != "recording" || records[0].Kind != "gap" || records[0].Source != "" {
				t.Fatal("readiness did not follow a failed first poll while the capture was active")
			}
			snapshot := records[len(records)-1].Snapshot
			if snapshot == nil || len(snapshot.Processes) != 0 {
				t.Fatal("readiness was sent before the empty snapshot committed")
			}
			cancel()
		}
		return len(p), nil
	})
	if err := run(ctx, []string{"watch", "--summary", "--out", path, "com.example.app"}, &out, diagnostic); err != nil {
		t.Fatal(err)
	}
	if ready != 1 || !strings.Contains(out.String(), "status interrupted") {
		t.Fatalf("interrupted handoff failed: ready=%d, summary=%q", ready, out.String())
	}
}

func TestWatchSummaryFailures(t *testing.T) {
	for _, test := range []string{"existing file", "finalization error", "summary output error"} {
		t.Run(test, func(t *testing.T) {
			root := watchFixture(t)
			path := filepath.Join(root, "failed.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var out bytes.Buffer
			var output io.Writer = &out
			var want string
			switch test {
			case "existing file":
				if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "file exists"
			case "summary output error":
				output = failedWriter{}
				want = "summary failed"
			case "finalization error":
				want = "fixture finalization failure"
			}
			diagnostic := watchWriteFunc(func(p []byte) (int, error) {
				if bytes.Contains(p, []byte("Capture ready")) {
					if test == "finalization error" {
						watchExecSQL(t, path, `CREATE TRIGGER reject_finish BEFORE UPDATE ON metadata
							BEGIN SELECT RAISE(FAIL, 'fixture finalization failure'); END`)
					}
					cancel()
				}
				return len(p), nil
			})
			err := run(ctx, []string{"watch", "--summary", "--out", path, "com.example.app"}, output, diagnostic)
			if err == nil || !strings.Contains(err.Error(), want) || out.Len() != 0 {
				t.Fatalf("incorrect failure or partial output: %v, %q", err, out.String())
			}
			switch test {
			case "summary output error":
				meta, records := watchRecords(t, path)
				if meta.Status != "interrupted" || len(records) == 0 || !errors.Is(err, io.ErrClosedPipe) || !strings.Contains(err.Error(), path) {
					t.Fatalf("summary failure obscured recording: %+v, %v", meta, err)
				}
			case "finalization error":
				meta, records := watchRecords(t, path)
				if meta.Status != "recording" || len(records) == 0 {
					t.Fatalf("finalization failure lost committed data: %+v", meta)
				}
			default:
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "keep me" {
					t.Fatalf("existing file changed: %q, %v", data, err)
				}
			}
		})
	}
}

func watchExecSQL(t *testing.T, path, query string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func recordJSON(t *testing.T, r session.Record) string {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWatchSummaryOffline(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, schema := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "recording with 'quotes'.db")
			process := session.Process{Identity: session.Identity{BootID: "boot", UID: 10123, PID: 7, StartTicks: 100}, Name: "com.example.app"}
			replacement := process
			replacement.StartTicks = 200
			snapshot := func(p session.Process) *session.Snapshot {
				return &session.Snapshot{BootID: p.BootID, Processes: []session.Process{p}}
			}
			rows := []session.Record{
				{Kind: "snapshot", Snapshot: snapshot(process), Transitions: []session.Transition{{Kind: "appeared", Process: process}}},
				{Kind: "gap", Error: "snapshot disconnected"},
				{Kind: "snapshot", Snapshot: snapshot(replacement), Transitions: []session.Transition{
					{Kind: "no_longer_observed", Process: process, AfterGap: true}, {Kind: "appeared", Process: replacement, AfterGap: true},
				}},
			}
			if schema != 1 {
				rows = append(rows, session.Record{Kind: "gap", Source: "logcat", Error: "buffer overlap missing"},
					watchReport(10), watchReport(10), watchReport(1))
			}
			writeSummaryFixture(t, path, schema, rows)
			before := watchHash(t, path)
			var first, second bytes.Buffer
			if err := printWatchSummary(ctx, &first, path, time.Second); err != nil {
				t.Fatal(err)
			}
			if err := printWatchSummary(ctx, &second, path, time.Second); err != nil || first.String() != second.String() {
				t.Fatalf("nondeterministic summary: %v", err)
			}
			summary := first.String()
			if !strings.Contains(summary, fmt.Sprintf("schema %d, status complete", schema)) {
				t.Fatal(summary)
			}
			ids := []string{"L3.0"}
			if schema != 1 {
				ids = []string{"E7.0", "E5.0", "L3.0"}
				if strings.Contains(summary, "E6.0") {
					t.Fatal("duplicate report appeared in summary")
				}
			}
			last := -1
			for _, id := range ids {
				pos := strings.Index(summary, "  "+id+" [")
				if pos <= last {
					t.Fatalf("wrong recording order for %q in %q", id, summary)
				}
				last = pos
				var detail bytes.Buffer
				if err := run(ctx, []string{"investigate", "--timeline", "--incident", id, path}, &detail, io.Discard); err != nil {
					t.Fatalf("summary ID failed to resolve: %v", err)
				}
				if strings.HasPrefix(id, "L") && !strings.Contains(detail.String(), "after_gap=true. Disappearance cause unknown") {
					t.Fatalf("lifecycle uncertainty lost: %q", detail.String())
				}
			}
			if !strings.Contains(summary, "investigate --tui --incident "+ids[0]+" "+shellRecordingPath(path)) || watchHash(t, path) != before {
				t.Fatalf("handoff did not use the original recording unchanged: %q", summary)
			}
			if err := printWatchSummary(ctx, io.Discard, path, 0); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expired summary deadline ignored: %v", err)
			}
			watchExecSQL(t, path, "UPDATE observations SET payload='{' WHERE sequence=1")
			broken := watchHash(t, path)
			first.Reset()
			if err := printWatchSummary(ctx, &first, path, time.Second); err == nil || first.Len() != 0 || watchHash(t, path) != broken {
				t.Fatalf("malformed recording yielded output or changed: %q, %v", first.String(), err)
			}
		})
	}
}

func TestWatchSummaryEmptyAndCappedIndexes(t *testing.T) {
	for _, count := range []int{0, 4098} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "index.db")
			rows := make([]session.Record, count)
			for i := range rows {
				rows[i] = watchReport(int64(i + 1))
			}
			writeSummaryFixture(t, path, 2, rows)
			var out bytes.Buffer
			if err := printWatchSummary(context.Background(), &out, path, watchSummaryTimeout); err != nil {
				t.Fatal(err)
			}
			s := out.String()
			if count == 0 {
				if !strings.Contains(s, "No incident anchors recorded.") || !strings.Contains(s, "droidsleuth replay "+shellRecordingPath(path)) || strings.Contains(s, "--incident") {
					t.Fatal(s)
				}
			} else {
				if !strings.Contains(s, "Later anchors may be omitted.") || strings.Count(s, "instance association unproven") != 5 || strings.Contains(s, "E4097") {
					t.Fatal(s)
				}
				for i := 4096; i > 4091; i-- {
					id := fmt.Sprintf("E%d.0", i)
					if !strings.Contains(s, "  "+id+" [") {
						t.Fatalf("missing %s: %s", id, s)
					}
				}
			}
		})
	}
}

func TestWatchSummaryCommandPathQuoting(t *testing.T) {
	for _, path := range []string{
		"/tmp/ordinary path with a 'quote' and `echo unsafe`.db",
		"/tmp/x\x1b]52;unsafe\a\n\t\xff\u202e'\\.db",
	} {
		arg := shellRecordingPath(path)
		if strings.ContainsAny(arg, "\x1b\a\n\t\u202e") {
			t.Fatalf("unescaped controls in %q", arg)
		}
		// The rendered argument must survive copy/paste verbatim.
		got, err := exec.Command("/bin/bash", "-c", "printf '%s' "+arg).Output()
		if err != nil || string(got) != path {
			t.Fatalf("shell changed path bytes: %q, %v", got, err)
		}
	}
}

func watchReport(deviceNS int64) session.Record {
	return session.Record{Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{
		Format: "fixture", Text: "reported ANR", Events: []session.Event{{
			Kind: "anr_report", PID: 7, Process: "com.example.app", FirstLine: 1, LastLine: 1, DeviceTimeNS: deviceNS,
		}},
	}}
}

func writeSummaryFixture(t *testing.T, path string, schema int, rows []session.Record) {
	t.Helper()
	ctx := context.Background()
	s, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app", Schema: max(schema, 2)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, r := range rows {
		r.Sequence = int64(i + 1)
		r.StartElapsedNS = r.Sequence * int64(time.Second)
		r.EndElapsedNS = r.StartElapsedNS + int64(time.Millisecond)
		if schema == 3 && r.Snapshot != nil {
			id := r.Snapshot.Processes[0].Identity
			sample := &session.Resources{Status: session.ResourceSingle, Identity: &id}
			sample.CPUUnavailable = sample.CPUInputIssue()
			sample.RSSUnavailable = sample.RSSInputIssue()
			r.Resources = sample
		}
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
	if schema == 1 {
		watchExecSQL(t, path, `PRAGMA user_version=1; UPDATE metadata SET payload=replace(payload,'"schema":2','"schema":1')`)
	}
}

func watchHash(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}
