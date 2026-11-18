package capture

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"droidsleuth/internal/adb"
	"droidsleuth/internal/investigate"
	"droidsleuth/internal/session"
)

// Opt-in wall-clock soak. Normal tests remain fast; accelerated event fixtures
// are not presented as elapsed-time endurance evidence.
func TestCompatibilitySoak(t *testing.T) {
	raw := os.Getenv("DROIDSLEUTH_SOAK_DURATION")
	if raw == "" {
		t.Skip("set DROIDSLEUTH_SOAK_DURATION=2h for wall-clock soak")
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 || duration > 2*time.Hour {
		t.Fatal("soak duration must be in (0, 2h]")
	}
	if _, err = exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for fake-ADB soak")
	}
	dir := t.TempDir()
	script, err := os.ReadFile("../../testdata/fake-compat-adb.py")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "adb")
	if err = os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "snapshot.txt"), []byte(fixture(t, "snapshot-present.txt")), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROIDSLEUTH_COMPAT_ROOT", dir)
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	var count atomic.Int64
	live := sha256.New()
	baseG := runtime.NumGoroutine()
	done := make(chan struct{})
	metricsDone := make(chan struct{})
	go func() {
		defer close(metricsDone)
		timer := time.NewTicker(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-timer.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				t.Logf("soak metrics observations=%d heap_bytes=%d goroutines=%d", count.Load(), m.HeapAlloc, runtime.NumGoroutine())
				if m.HeapAlloc > 256<<20 || runtime.NumGoroutine() > baseG+32 {
					t.Error("soak exceeded bounded heap/goroutine budget")
					cancel()
					return
				}
			}
		}
	}()
	started := time.Now()
	db := filepath.Join(dir, "soak.db")
	err = Run(ctx, adb.Client{Path: path}, session.Metadata{Package: "com.example.app", UID: 10123}, Options{Path: db, Evidence: true, Visit: func(r session.Record) error {
		body, e := json.Marshal(r)
		if e != nil {
			return e
		}
		live.Write(body)
		count.Add(1)
		return nil
	}})
	close(done)
	<-metricsDone
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < duration {
		t.Fatal("capture stopped before requested wall-clock duration")
	}
	replay := sha256.New()
	counts := map[string]int{}
	err = session.Replay(context.Background(), db, func(m session.Metadata) error {
		if m.Status != "complete" {
			return fmt.Errorf("status=%s", m.Status)
		}
		return nil
	}, func(r session.Record) error {
		body, e := json.Marshal(r)
		if e != nil {
			return e
		}
		replay.Write(body)
		counts[r.Kind+"/"+r.Source]++
		if r.Evidence != nil && containsPrivate(r.Evidence.Text) {
			return fmt.Errorf("unrelated fixture log persisted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", live.Sum(nil)) != fmt.Sprintf("%x", replay.Sum(nil)) {
		t.Fatal("soak replay differs from committed observations")
	}
	report, err := investigate.Analyze(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Findings {
		if len(f.Uncertainty) == 0 {
			t.Fatal("report lost attribution uncertainty")
		}
	}
	info, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= 240<<20 {
		t.Fatal("soak unexpectedly exhausted recording budget")
	}
	t.Logf("soak complete elapsed=%s observations=%d database_bytes=%d counts=%v", time.Since(started), count.Load(), info.Size(), counts)
}

func containsPrivate(s string) bool { return strings.Contains(s, "unrelated-private-sentinel") }
