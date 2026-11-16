package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDurablePrefixReadOnlyReplayAndNoOverwrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.db")
	store, err := Create(ctx, path, Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, path, Metadata{}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite accepted: %v", err)
	}
	if err := store.Append(ctx, Record{Sequence: 1, Kind: "gap", Error: "device offline"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, Record{Sequence: 1, Kind: "gap", Error: "must not overwrite"}); err == nil {
		t.Fatal("duplicate sequence overwrote evidence")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	} // No Finish: simulate abrupt capture loss after a committed prefix.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("recording is not private")
	}
	seen := 0
	if err := Replay(ctx, path, func(m Metadata) error {
		if m.Status != "recording" || m.EndedUTC != nil {
			t.Fatal("incomplete session looked complete")
		}
		return nil
	}, func(r Record) error {
		seen++
		if r.Error != "device offline" {
			t.Fatal("committed prefix changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if seen != 1 || string(before) != string(after) {
		t.Fatal("replay modified or lost evidence")
	}
}

func TestSchemaAndStorageBounds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.db")
	store, err := Create(ctx, path, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Append(ctx, Record{Sequence: 1, Kind: "gap", Error: strings.Repeat("x", 64<<10)}); err == nil {
		t.Fatal("unbounded record accepted")
	}
	if err := store.Append(ctx, Record{Sequence: 1, Kind: "snapshot"}); err == nil {
		t.Fatal("missing snapshot payload accepted")
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA max_page_count=8"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, Record{Sequence: 1, Kind: "gap", Error: strings.Repeat("x", 40000)}); err == nil {
		t.Fatal("database size limit ignored")
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	if err := Replay(ctx, path, func(Metadata) error { t.Fatal("unsupported schema reached renderer"); return nil }, func(Record) error { return nil }); err == nil {
		t.Fatal("future schema accepted")
	}
}
