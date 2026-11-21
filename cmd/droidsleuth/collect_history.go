package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"droidsleuth/internal/adb"
	"droidsleuth/internal/capture"
)

func runCollectHistory(ctx context.Context, flags *flag.FlagSet, args []string, out io.Writer) error {
	serial := flags.String("device", "", "ADB serial (required when several devices are connected)")
	user := flags.Int("user", -1, "Android user ID (default: current user)")
	path := flags.String("out", "", "new recording file (required)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 || !capture.ValidPackage(flags.Arg(0)) || *user < -1 || *path == "" {
		return errors.New("usage: droidsleuth collect-history --out FILE [--device SERIAL] [--user ID] PACKAGE; flags precede PACKAGE")
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	client := adb.Client{}
	meta, err := probeTarget(ctx, &client, *serial, flags.Arg(0), *user)
	if err != nil {
		return err
	}
	count, created, err := capture.CollectHistory(ctx, client, meta, abs)
	if !created {
		return fmt.Errorf("create history recording at %q: %w", abs, err)
	}
	if err != nil {
		err = fmt.Errorf("history recording at %q: %w", abs, err)
	}
	p := timelinePrinter{out: out}
	p.line("Recording %q, package %q. Retained exit history only.", abs, meta.Package)
	p.line("Stored %d Android exit report(s). Process instance association is unproven.", count)
	if count == 0 && err == nil {
		p.line("No matching reports in the retained history.")
	}
	p.line("  droidsleuth investigate --timeline %s", shellRecordingPath(abs))
	return errors.Join(err, p.err)
}
