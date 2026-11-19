package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func managedTestRecording(t *testing.T, dir string, second int, status string) string {
	t.Helper()
	path := filepath.Join(dir, ManagedFilename(time.Unix(int64(second), 0)))
	ctx := context.Background()
	s, err := Create(ctx, path, Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if status != "recording" {
		if err := s.Finish(ctx, status); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestLatestFinalized(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first := managedTestRecording(t, dir, 1, "complete")
	want := managedTestRecording(t, dir, 2, "failed")
	active := managedTestRecording(t, dir, 3, "recording")
	clock := time.Now()
	if err := os.Chtimes(first, clock, clock); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ManagedFilename(time.Unix(4, 0)))
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom.db"), []byte("not managed"), 0600); err != nil {
		t.Fatal(err)
	}
	got, skipped, err := LatestFinalized(ctx, dir)
	if err != nil || got != want || skipped != 1 {
		t.Fatalf("path=%q skipped=%d err=%v", got, skipped, err)
	}
	// A corrupt managed file cannot make an earlier recording look newest.
	if err := os.WriteFile(active, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LatestFinalized(ctx, dir); err == nil || !strings.Contains(err.Error(), active) {
		t.Fatalf("corrupt candidate: %v", err)
	}
}

func TestDiscoveryLimitsAndMissingDirectory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	if _, _, err := LatestFinalized(ctx, missing); err == nil {
		t.Fatal("missing directory was accepted")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discovery created the directory")
	}
	if _, _, err := LatestFinalized(ctx, dir); err == nil || !strings.Contains(err.Error(), "no finalized") {
		t.Fatalf("empty directory: %v", err)
	}
	for i := 0; i < 4097; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := LatestFinalized(ctx, dir); err == nil || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("directory limit: %v", err)
	}
}

func TestFinalizedReaderSchemaOneAndPathReplacement(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	s, err := Create(ctx, path, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, Record{Sequence: 4, Kind: "gap", Error: "disconnected"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, "complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version=1; UPDATE metadata SET payload=replace(payload,'\"schema\":2','\"schema\":1')"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	replacement := managedTestRecording(t, dir, 1, "complete")
	err = WithFinalizedRecording(ctx, path, func(r Recording) error {
		if r.Metadata.Schema != 1 {
			t.Fatalf("schema %d", r.Metadata.Schema)
		}
		row, err := r.ReadRecord(ctx, 4)
		if err != nil || row.Error != "disconnected" {
			t.Fatalf("row=%+v err=%v", row, err)
		}
		if err := os.Rename(replacement, path); err != nil {
			return err
		}
		return r.Scan(ctx, func(row Record) error {
			if row.Sequence != 4 {
				t.Fatal("reader switched databases")
			}
			return nil
		})
	})
	if err == nil || !strings.Contains(err.Error(), "recording changed") {
		t.Fatalf("replacement not detected: %v", err)
	}
}
