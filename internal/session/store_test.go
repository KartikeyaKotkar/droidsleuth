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

func TestSchemaOneCompatibilityAndEvidenceReferences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Create(ctx, path, Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, Record{Sequence: 1, Kind: "gap", Error: "offline"}); err != nil {
		t.Fatal(err)
	}
	// Recreate the exact old metadata/user_version contract without migrating it.
	if _, err = s.db.ExecContext(ctx, `PRAGMA user_version=1; UPDATE metadata SET payload=replace(payload,'"schema":2','"schema":1')`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = Replay(ctx, path, func(m Metadata) error {
		if m.Schema != 1 {
			t.Fatal("old schema changed")
		}
		return nil
	}, func(Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s, err = Create(ctx, filepath.Join(t.TempDir(), "new.db"), Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := &Evidence{Text: "report", Format: "fixture", Events: []Event{{Kind: "anr_report", Process: "com.example.app", PID: 42, FirstLine: 1, LastLine: 2}}}
	if err = s.Append(ctx, Record{Sequence: 1, Kind: "evidence", Source: "logcat", Evidence: e}); err == nil {
		t.Fatal("invalid source reference accepted")
	}
	e.Events[0].LastLine = 1
	if err = s.Append(ctx, Record{Sequence: 1, Kind: "evidence", Source: "logcat", Evidence: e}); err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, Record{Sequence: 2, Kind: "gap", Source: "logcat", Error: "loss", Transitions: []Transition{{Kind: "no_longer_observed"}}}); err == nil {
		t.Fatal("source gap changed lifecycle")
	}
}

func TestMetadataBounds(t *testing.T) {
	ctx := context.Background()
	if s, err := Create(ctx, filepath.Join(t.TempDir(), "too-large.db"), Metadata{Serial: strings.Repeat("x", 65536)}); err == nil {
		s.Close()
		t.Fatal("unbounded metadata accepted")
	}
	path := filepath.Join(t.TempDir(), "malicious.db")
	s, err := Create(ctx, path, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE metadata SET payload=?", strings.Repeat("x", 65537)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = Replay(ctx, path, func(Metadata) error { t.Fatal("oversized metadata reached caller"); return nil }, func(Record) error { return nil }); err == nil {
		t.Fatal("unbounded metadata replayed")
	}
}

func TestResourceSchemaRoundTripAndScope(t *testing.T) {
	ctx := context.Background()
	id := Identity{BootID: "boot", UID: 10123, PID: 42, StartTicks: 5}
	zero := uint64(0)
	row := Record{Sequence: 1, Kind: "snapshot", Snapshot: &Snapshot{BootID: id.BootID, Processes: []Process{{Identity: id}}},
		Resources: &Resources{Status: ResourceSingle, Identity: &id, UserTicks: &zero, SystemTicks: &zero,
			ClockTicksPerSecond: 100, Uptime: &UptimeBounds{EarliestNS: 10, LatestNS: 20}, RSSPages: &zero, PageSizeBytes: 16384}}
	for _, schema := range []int{SchemaVersion, ResourceSchemaVersion} {
		path := filepath.Join(t.TempDir(), "sample.db")
		s, err := Create(ctx, path, Metadata{Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		if schema == SchemaVersion {
			if err := s.Append(ctx, row); err == nil {
				t.Fatal("schema 2 accepted resources")
			}
		} else {
			missing := row
			missing.Resources = nil
			if err := s.Append(ctx, missing); err == nil {
				t.Fatal("schema 3 accepted missing resource status")
			}
			wrong := *row.Resources
			other := id
			other.StartTicks++
			wrong.Identity = &other
			invalid := row
			invalid.Resources = &wrong
			if err := s.Append(ctx, invalid); err == nil {
				t.Fatal("accepted wrong resource identity")
			}
			if err := s.Append(ctx, row); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		count := 0
		if err := Replay(ctx, path, func(m Metadata) error {
			if m.Schema != schema {
				t.Fatalf("schema %d", m.Schema)
			}
			return nil
		}, func(got Record) error {
			count++
			if got.Resources == nil || got.Resources.RSSPages == nil || *got.Resources.RSSPages != 0 {
				t.Fatal("lost observed zero")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != (schema == ResourceSchemaVersion) {
			t.Fatalf("rows %d for schema %d", count, schema)
		}
	}
}
