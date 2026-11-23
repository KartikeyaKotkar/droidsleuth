package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

type fullGapShell struct{ message string }

func (s fullGapShell) Shell(context.Context, string) (string, error) {
	return "", errors.New(s.message)
}

func TestActualCaptureStopThreshold(t *testing.T) {
	if os.Getenv("DROIDSLEUTH_RESOURCE_TESTS") != "1" {
		t.Skip("set DROIDSLEUTH_RESOURCE_TESTS=1; temporary disk budget 241 MiB")
	}
	path := filepath.Join(t.TempDir(), "stop.db")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	count := 0
	err := Run(ctx, fullGapShell{strings.Repeat("x", 60<<10)}, session.Metadata{Package: "com.example.app", UID: 10123}, Options{Path: path, Interval: time.Nanosecond, Visit: func(session.Record) error { count++; return nil }})
	if err == nil || !strings.Contains(err.Error(), "240 MiB stop threshold") {
		t.Fatalf("did not stop at threshold: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 240<<20 || info.Size() > (240<<20)+(128<<10) {
		t.Fatalf("bad stopping size %d", info.Size())
	}
	replayed := 0
	err = session.Replay(context.Background(), path, func(m session.Metadata) error {
		if m.Status != "failed" || m.EndedUTC == nil {
			t.Fatal("size stop not finalized")
		}
		return nil
	}, func(r session.Record) error {
		replayed++
		if len(r.Transitions) != 0 {
			t.Fatal("failure invented lifecycle event")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != replayed {
		t.Fatal("size stop lost committed observations")
	}
	t.Logf("real capture stop: observations=%d bytes=%d", replayed, info.Size())
}
