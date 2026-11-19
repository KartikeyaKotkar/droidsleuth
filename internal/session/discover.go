package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const managedLayout = "20060102T150405.000000000"

func ManagedFilename(t time.Time) string {
	return t.UTC().Format(managedLayout) + ".db"
}

// LatestFinalized examines managed names newest first. Invalid newest
// candidates are errors. Only a known unfinalized status permits skipping.
func LatestFinalized(ctx context.Context, dir string) (path string, skipped int, err error) {
	f, err := os.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, errors.New("no managed recording directory; pass a recording filename")
	}
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	const entryLimit = 4096
	entries, err := f.ReadDir(entryLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", 0, err
	}
	if len(entries) > entryLimit {
		return "", 0, errors.New("managed recording directory exceeds 4096 entries; pass a recording filename")
	}
	var names []string
	for _, e := range entries {
		stamp, ok := strings.CutSuffix(e.Name(), ".db")
		if !ok || e.Type()&os.ModeSymlink != 0 || e.IsDir() {
			continue
		}
		t, err := time.Parse(managedLayout, stamp)
		if err == nil && ManagedFilename(t) == e.Name() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for i := len(names) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return "", skipped, err
		}
		path := filepath.Join(dir, names[i])
		meta, err := managedMetadata(ctx, path)
		if err != nil {
			return "", skipped, fmt.Errorf("read managed recording %q: %w", path, err)
		}
		if meta.Status == "recording" {
			skipped++
			continue
		}
		return path, skipped, nil
	}
	return "", skipped, errors.New("no finalized managed recording; pass a recording filename")
}

func managedMetadata(ctx context.Context, path string) (Metadata, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Metadata{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return Metadata{}, errors.New("expected a regular recording of at most 256 MiB")
	}
	db, err := open(path, "ro")
	if err != nil {
		return Metadata{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Metadata{}, err
	}
	defer tx.Rollback()
	meta, err := readMetadata(ctx, tx)
	if err != nil {
		return Metadata{}, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) {
		return Metadata{}, errors.New("managed recording changed during discovery")
	}
	return meta, nil
}
