package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const historyADB = `#!/bin/sh
set -eu
if [ "$1" = devices ]; then
    echo devices >> "$HISTORY_FIXTURE/calls"
    printf 'List of devices attached\nfixture device\n'
    if [ "${HISTORY_MULTI:-}" = 1 ]; then printf 'other device\n'; fi
    exit 0
fi
[ "$1" = -s ] && [ "$2" = fixture ] && [ "$3" = shell ] && [ "$4" = -T ]
case "$5" in
    'getprop ro.build.version.sdk') echo sdk >> "$HISTORY_FIXTURE/calls"; echo 30;;
    'am get-current-user') echo user >> "$HISTORY_FIXTURE/calls"; echo 0;;
    'cmd package list packages -U --user 0 com.example.app')
        echo package >> "$HISTORY_FIXTURE/calls"
        if [ "${HISTORY_MISSING:-}" != 1 ]; then echo 'package:com.example.app uid:10123'; fi;;
    'getprop persist.sys.timezone') echo zone >> "$HISTORY_FIXTURE/calls"; echo "${HISTORY_ZONE:-UTC}";;
    'dumpsys activity exit-info com.example.app')
        echo history >> "$HISTORY_FIXTURE/calls"
        if [ "${HISTORY_BLOCK:-}" = 1 ]; then exec /bin/sleep 30; fi
        if [ "${HISTORY_OVERFLOW:-}" = 1 ]; then exec /usr/bin/head -c 4194305 /dev/zero; fi
        exec /bin/cat "$HISTORY_FIXTURE/raw";;
    *) echo unexpected >> "$HISTORY_FIXTURE/calls"; exit 91;;
esac
`

const historyHeader = "ACTIVITY MANAGER PROCESS EXIT INFO (dumpsys activity exit-info)\n"

func historyEntry(slot, pid, uid int, pkg, timestamp, description string) string {
	return fmt.Sprintf(" ApplicationExitInfo #%d:\n timestamp=%s pid=%d realUid=%d\n process=%s reason=4 (APP CRASH(EXCEPTION)) subreason=0 status=0\n description=%s\n",
		slot, timestamp, pid, uid, pkg, description)
}

func historyFixture(t *testing.T, raw string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"adb": historyADB, "raw": raw} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("HISTORY_FIXTURE", dir)
	for _, key := range []string{"HISTORY_MULTI", "HISTORY_MISSING", "HISTORY_ZONE", "HISTORY_BLOCK", "HISTORY_OVERFLOW"} {
		t.Setenv(key, "")
	}
	return dir
}

func TestCollectHistoryOfflineHandoff(t *testing.T) {
	const stamp = "2026-01-01 12:00:00.000"
	dir := historyFixture(t, historyHeader+historyEntry(0, 42, 10123, "com.example.app", stamp, "selected")+
		historyEntry(1, 43, 10124, "com.example.app", stamp, "wrong UID")+
		historyEntry(2, 44, 10123, "com.example.other", stamp, "wrong package"))
	path := filepath.Join(dir, "incident 'report'.db")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"collect-history", "--out", path, "com.example.app"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Stored 1 Android exit report(s)") || !strings.Contains(out.String(), "investigate --timeline "+shellRecordingPath(path)) {
		t.Fatal(out.String())
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || !slices.Equal(strings.Fields(string(calls)), []string{"devices", "sdk", "user", "package", "zone", "history"}) {
		t.Fatalf("unexpected ADB calls: %q, %v", calls, err)
	}
	meta, rows := watchRecords(t, path)
	if meta.Schema != 2 || meta.Status != "complete" || len(rows) != 3 || !rows[1].SourceStatus.ReadSucceeded {
		t.Fatalf("unexpected history recording: %+v, %+v", meta, rows)
	}
	for _, r := range rows {
		if r.Source != "exit_history" || r.Snapshot != nil || len(r.Transitions) != 0 {
			t.Fatalf("historical report became live observation: %+v", r)
		}
	}
	event := rows[2].Evidence.Events[0]
	if event.BootID != "" || event.PID != 42 || event.DeviceTimeNS == 0 || *event.Reason != 4 {
		t.Fatalf("unexpected historical evidence: %+v", event)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	t.Setenv("PATH", t.TempDir())
	args := []string{"investigate", "--timeline", "--json", "--incident", "E3.0", path}
	var first, second bytes.Buffer
	for _, output := range []*bytes.Buffer{&first, &second} {
		if err := run(context.Background(), args, output, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if first.String() != second.String() || !strings.Contains(first.String(), "unbound") ||
		strings.Contains(first.String(), "wrong UID") || strings.Contains(first.String(), "wrong package") {
		t.Fatalf("bad offline report: %s", first.String())
	}
	data, err = os.ReadFile(path)
	if err != nil || hash != sha256.Sum256(data) {
		t.Fatal("offline investigation changed recording")
	}
}

func TestCollectHistoryReadOutcomes(t *testing.T) {
	entry := historyEntry(0, 42, 10123, "com.example.app", "2025-11-02 01:30:00.000", "fixture")
	for _, tc := range []struct {
		name, raw, zone, state string
		count                  int
		overflow               bool
	}{
		{"empty", historyHeader, "", "complete", 0, false},
		{"denied", "Permission Denial", "", "failed", 0, false},
		{"unsupported", "Unknown command", "", "failed", 0, false},
		{"malformed", historyHeader + strings.Replace(entry, "pid=42", "pid=bad", 1), "", "failed", 0, false},
		{"ambiguous time", historyHeader + entry, "America/New_York", "complete", 1, false},
		{"output overflow", "", "", "failed", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := historyFixture(t, tc.raw)
			t.Setenv("HISTORY_ZONE", tc.zone)
			if tc.overflow {
				t.Setenv("HISTORY_OVERFLOW", "1")
			}
			path := filepath.Join(dir, "history.db")
			var out bytes.Buffer
			err := run(context.Background(), []string{"collect-history", "--out", path, "com.example.app"}, &out, io.Discard)
			if (err != nil) != (tc.state == "failed") || !strings.Contains(out.String(), path) {
				t.Fatalf("err=%v, out=%q", err, out.String())
			}
			meta, rows := watchRecords(t, path)
			status := rows[1].SourceStatus
			if meta.Status != tc.state || status.ReadSucceeded != (tc.state == "complete") || status.SelectedObservations != tc.count {
				t.Fatalf("unexpected history state: %+v, %+v", meta, status)
			}
			if tc.state == "failed" && (rows[2].Kind != "gap" || strings.Contains(out.String(), "No matching")) {
				t.Fatal("failed read appeared empty")
			}
			if tc.count == 1 && rows[2].Evidence.Events[0].DeviceTimeNS != 0 {
				t.Fatal("ambiguous occurrence time was guessed")
			}
		})
	}
}

func TestCollectHistoryOutputAndTargetErrors(t *testing.T) {
	dir := historyFixture(t, historyHeader)
	path := filepath.Join(dir, "history.db")
	for _, args := range [][]string{{"collect-history", "com.example.app"}, {"collect-history", "--out", path, "invalid"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid arguments invoked ADB")
	}
	args := []string{"collect-history", "--out", path, "com.example.app"}
	t.Setenv("HISTORY_MULTI", "1")
	if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
		t.Fatal("ambiguous device selection accepted")
	}
	t.Setenv("HISTORY_MULTI", "")
	t.Setenv("HISTORY_MISSING", "1")
	if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
		t.Fatal("missing package accepted")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed target probe created a recording")
	}
	t.Setenv("HISTORY_MISSING", "")
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
		t.Fatal("existing recording accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "existing" {
		t.Fatal("existing output was modified")
	}
}

func TestCollectHistoryCancellationFinalizes(t *testing.T) {
	dir := historyFixture(t, historyHeader)
	t.Setenv("HISTORY_BLOCK", "1")
	path := filepath.Join(dir, "history.db")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- run(ctx, []string{"collect-history", "--out", path, "com.example.app"}, io.Discard, io.Discard)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
		if strings.Contains(string(calls), "history") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("history read did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled capture did not exit")
	}
	meta, rows := watchRecords(t, path)
	if meta.Status != "interrupted" || len(rows) != 3 || rows[1].SourceStatus.ReadSucceeded || rows[2].Kind != "gap" {
		t.Fatalf("incomplete cancellation record: %+v, %+v", meta, rows)
	}
}

func TestCollectHistoryEvidenceAndRecordingBounds(t *testing.T) {
	var raw strings.Builder
	raw.WriteString(historyHeader)
	for n := 0; n < 128; n++ {
		raw.WriteString(historyEntry(n, 42+n, 10123, "com.example.app", "2026-01-01 12:00:00.000", strings.Repeat("\x01", 7800)))
	}
	dir := historyFixture(t, raw.String())
	path := filepath.Join(dir, "bounded.db")
	if err := run(context.Background(), []string{"collect-history", "--out", path, "com.example.app"}, io.Discard, io.Discard); err == nil {
		t.Fatal("limited collection should report degradation")
	}
	meta, rows := watchRecords(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != "failed" || len(rows) != 67 || rows[1].SourceStatus.SelectedObservations != 64 || info.Size() > 5<<20 {
		t.Fatalf("bounds failed: status=%s rows=%d bytes=%d", meta.Status, len(rows), info.Size())
	}
	t.Logf("64 reports retained in %d bytes", info.Size())
}

func TestCollectHistoryDeadlineFinalizes(t *testing.T) {
	dir := historyFixture(t, historyHeader)
	t.Setenv("HISTORY_BLOCK", "1")
	path := filepath.Join(dir, "history.db")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := run(ctx, []string{"collect-history", "--out", path, "com.example.app"}, io.Discard, io.Discard); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	meta, rows := watchRecords(t, path)
	if meta.Status != "failed" || len(rows) != 3 || rows[1].SourceStatus.ReadSucceeded || rows[2].Kind != "gap" {
		t.Fatalf("incomplete deadline record: %+v, %+v", meta, rows)
	}
}
