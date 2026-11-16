package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"droidsleuth/internal/adb"
	"droidsleuth/internal/capture"
	"droidsleuth/internal/session"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "droidsleuth: %s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, diagnostic io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: droidsleuth devices | watch [flags] PACKAGE | replay [--json] FILE")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	client := adb.Client{}
	switch args[0] {
	case "devices":
		if len(args) != 1 {
			return errors.New("usage: droidsleuth devices")
		}
		devices, err := client.Devices(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(devices)
	case "watch":
		serial := flags.String("device", "", "ADB serial (required when several devices are connected)")
		user := flags.Int("user", -1, "Android user ID (default: current user)")
		path := flags.String("out", "", "new recording file (default: XDG state directory)")
		duration := flags.Duration("duration", 0, "stop after this duration (default: until interrupted)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 || !capture.ValidPackage(flags.Arg(0)) || *user < -1 || *duration < 0 {
			return errors.New("usage: droidsleuth watch [--device SERIAL] [--user ID] [--out FILE] [--duration 10s] PACKAGE; flags precede PACKAGE")
		}
		devices, err := client.Devices(ctx)
		if err != nil {
			return err
		}
		client.Serial, err = adb.Select(devices, *serial)
		if err != nil {
			return err
		}
		meta, err := capture.Probe(ctx, client, flags.Arg(0), *user)
		if err != nil {
			return err
		}
		meta.Serial = client.Serial
		// Verify the mandatory data sources before creating a recording.
		command, err := capture.SnapshotCommand(meta.Package, meta.UID)
		if err != nil {
			return err
		}
		probe, err := client.Shell(ctx, command)
		if err != nil {
			return fmt.Errorf("snapshot capability probe: %w", err)
		}
		if _, err = capture.ParseSnapshot(probe, meta.Package, meta.UID); err != nil {
			return fmt.Errorf("snapshot capability probe: %w", err)
		}
		if *path == "" {
			*path, err = defaultPath()
			if err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(diagnostic, "Recording main process %s on %s to %s\n", meta.Package, meta.Serial, *path); err != nil {
			return err
		}
		if *duration > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, *duration)
			defer cancel()
		}
		return capture.Run(ctx, client, meta, capture.Options{Path: *path, Visit: func(r session.Record) error { return printRecord(out, r) }})
	case "replay":
		asJSON := flags.Bool("json", false, "emit metadata followed by observation JSON lines")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("usage: droidsleuth replay [--json] FILE")
		}
		enc := json.NewEncoder(out)
		return session.Replay(ctx, flags.Arg(0), func(meta session.Metadata) error {
			if *asJSON {
				return enc.Encode(struct {
					Metadata session.Metadata `json:"metadata"`
				}{meta})
			}
			if meta.Status == "recording" {
				meta.Status = "not finalized (active or incomplete)"
			}
			_, err := fmt.Fprintf(out, "Package %q; device %q; schema %d; status %s\nTimes are host observation bounds; termination causes are unknown.\n", meta.Package, meta.Serial, meta.Schema, meta.Status)
			return err
		}, func(record session.Record) error {
			if *asJSON {
				return enc.Encode(record)
			}
			return printRecord(out, record)
		})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func defaultPath() (string, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" || !filepath.IsAbs(root) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(root, "droidsleuth")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000")+".db"), nil
}

func printRecord(out io.Writer, r session.Record) error {
	if r.Kind == "gap" {
		_, err := fmt.Fprintf(out, "#%d [%.3f..%.3fs] coverage gap: %q\n", r.Sequence, float64(r.StartElapsedNS)/1e9, float64(r.EndElapsedNS)/1e9, r.Error)
		return err
	}
	if r.Snapshot == nil {
		return errors.New("snapshot record lacks payload")
	}
	_, err := fmt.Fprintf(out, "#%d [%.3f..%.3fs] snapshot: %d main process(es), boot=%q\n", r.Sequence, float64(r.StartElapsedNS)/1e9, float64(r.EndElapsedNS)/1e9, len(r.Snapshot.Processes), r.Snapshot.BootID)
	if err != nil {
		return err
	}
	for _, p := range r.Snapshot.Processes {
		if _, err := fmt.Fprintf(out, "  observed uid=%d pid=%d start_ticks=%d name=%q\n", p.UID, p.PID, p.StartTicks, p.Name); err != nil {
			return err
		}
	}
	for _, event := range r.Transitions {
		if _, err := fmt.Fprintf(out, "  %s uid=%d pid=%d start_ticks=%d after_gap=%t (cause unknown)\n", event.Kind, event.Process.UID, event.Process.PID, event.Process.StartTicks, event.AfterGap); err != nil {
			return err
		}
	}
	return nil
}
