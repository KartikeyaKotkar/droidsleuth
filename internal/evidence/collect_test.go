package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"droidsleuth/internal/adb"
)

func TestFakeADBCollectorsCancellationAndMissingHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adb")
	script := `#!/bin/sh
case "$*" in
 *random/boot_id*) printf 'boot\n';;
 *persist.sys.timezone*) printf 'UTC\n';;
 *exit-info*) printf 'Permission Denial\n';;
 *logcat*) cat "$FIXTURE_LOG"; sleep 30;;
 *) exit 1;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "logcat")
	if err := os.WriteFile(logPath, []byte(java("10123", 42, target.Package)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_LOG", logPath)
	ctx, cancel := context.WithCancel(context.Background())
	ch, stop := Start(ctx, adb.Client{Path: path, Timeout: time.Second}, target)
	gotMissing := false
	deadline := time.After(2 * time.Second)
	for !gotMissing {
		select {
		case r := <-ch:
			gotMissing = r.Source == "exit_history" && strings.Contains(r.Error, "unavailable")
		case <-deadline:
			t.Fatal("missing history not recorded")
		}
	}
	start := time.Now()
	cancel()
	stop()
	if time.Since(start) > time.Second {
		t.Fatal("source subprocess cancellation exceeded bound")
	}
	for r := range ch {
		if r.Kind == "evidence" {
			t.Fatal("partial timed-out stdout accepted as evidence")
		}
	}
}

func TestFakeADBFiniteTailDedupAndRollover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adb")
	script := `#!/bin/sh
case "$*" in
 *random/boot_id*) printf 'boot\n';;
 *persist.sys.timezone*) printf 'UTC\n';;
 *exit-info*) printf 'ACTIVITY MANAGER PROCESS EXIT INFO\n';;
 *logcat*) cat "$FIXTURE_LOG"; case "$*" in *'droidsleuth crash tail'*) printf '\n--------- droidsleuth crash tail\n';; esac;;
 *) exit 1;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "logcat")
	raw := java("10123", 42, target.Package) + "\n" + java("10124", 43, "com.private.other")
	if err := os.WriteFile(logPath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_LOG", logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ch, stop := Start(ctx, adb.Client{Path: path}, target)
	defer stop()
	count := 0
	rollover := false
	// First duplicate poll must add no evidence. Then replace the tail, simulating
	// buffer rollover/disconnection without allowing PID-only attribution.
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for !rollover {
		select {
		case r := <-ch:
			if r.Evidence != nil {
				count++
				if strings.Contains(r.Evidence.Text, "com.private.other") {
					t.Fatal("unrelated logs persisted")
				}
			}
			if strings.Contains(r.Error, "overlap lost") {
				rollover = true
			}
		case <-timer.C:
			if count != 1 {
				t.Fatalf("duplicate evidence count %d", count)
			}
			if err := os.WriteFile(logPath, []byte(logline("system", 900, "am_anr", "[0,42,com.example.app,0,timeout]")), 0600); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("rollover not reported")
		}
	}
}

func TestFakeADBDisconnectAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adb")
	script := `#!/bin/sh
case "$*" in
 *random/boot_id*) printf 'boot\n';;
 *persist.sys.timezone*) printf 'UTC\n';;
 *exit-info*) printf 'ACTIVITY MANAGER PROCESS EXIT INFO\n';;
 *logcat*)
  if [ ! -f "$FIXTURE_READY" ]; then touch "$FIXTURE_READY"; exit 1; fi
  printf '1700000000.123456 1000 900 900 I am_anr: [0,42,com.example.app,0,fixture timeout]\n'
  case "$*" in *'droidsleuth crash tail'*) printf '\n--------- droidsleuth crash tail\n';; esac;;
 *) exit 1;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_READY", filepath.Join(dir, "ready"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, stop := Start(ctx, adb.Client{Path: path}, target)
	defer stop()
	gap := false
	for {
		select {
		case r := <-ch:
			if strings.Contains(r.Error, "disconnected") {
				gap = true
			}
			if r.Evidence != nil {
				if !gap || r.Evidence.Events[0].Kind != "anr_report" {
					t.Fatal("recovery lost source gap or report")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("source failed to recover")
		}
	}
}
