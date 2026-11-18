package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This allocates one real 256 MiB disposable SQLite database, not a smaller
// configured surrogate. It is opt-in to keep normal/race CI lightweight.
func TestActualSQLiteSizeLimit(t *testing.T) {
	if os.Getenv("DROIDSLEUTH_RESOURCE_TESTS") != "1" {
		t.Skip("set DROIDSLEUTH_RESOURCE_TESTS=1; temporary disk budget 257 MiB")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "limit.db")
	s, err := Create(ctx, path, Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	payload := strings.Repeat("x", 60<<10)
	var committed int64
	for seq := int64(1); seq <= 5000; seq++ {
		err = s.Append(ctx, Record{Sequence: seq, Kind: "gap", Error: payload})
		if err != nil {
			break
		}
		committed = seq
	}
	var full interface{ Code() int }
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatalf("expected real SQLITE_FULL after %d rows, got %v", committed, err)
	}
	var pages, maxPages, count int64
	if err = s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("PRAGMA max_page_count").Scan(&maxPages); err != nil {
		t.Fatal(err)
	}
	if pages > 65536 || maxPages != 65536 {
		t.Fatalf("page limit lost: %d/%d", pages, maxPages)
	}
	if err = s.db.QueryRow("SELECT count(*) FROM observations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != committed {
		t.Fatal("failed insertion corrupted committed prefix")
	}
	if err = s.Finish(ctx, "failed"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	var replayed int64
	if err = Replay(ctx, path, func(m Metadata) error {
		if m.Status != "failed" {
			t.Fatal("failure not finalized")
		}
		return nil
	}, func(r Record) error {
		replayed++
		if r.Sequence != replayed || r.Error != payload {
			t.Fatal("prefix changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replayed != committed {
		t.Fatal("replay lost committed rows")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 256<<20 {
		t.Fatal("database exceeded 256 MiB")
	}
	t.Logf("real SQLite limit: committed=%d pages=%d bytes=%d", committed, pages, info.Size())
}
