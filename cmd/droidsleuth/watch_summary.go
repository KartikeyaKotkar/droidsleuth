package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"droidsleuth/internal/investigate"
	"droidsleuth/internal/session"
)

const (
	watchSummaryTimeout = 10 * time.Second
	watchSummaryAnchors = 5
)

func announceCaptureReady(diagnostic io.Writer) func(session.Record) error {
	ready := false
	return func(r session.Record) error {
		if ready || r.Snapshot == nil {
			return nil
		}
		ready = true
		_, err := fmt.Fprintln(diagnostic, "Capture ready (first successful snapshot committed).")
		return err
	}
}

func printWatchSummary(ctx context.Context, out io.Writer, path string, timeout time.Duration) error {
	// Capture cancellation is a normal way to finish a recording. Give the
	// offline scan its own deadline after the writer and collectors exit.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	report, err := investigate.Timeline(ctx, path, nil, investigate.DefaultContext, investigate.DefaultContext)
	if err != nil {
		return err
	}

	p := timelinePrinter{out: out}
	p.line("Recording %q, package %q, schema %d, status %s", report.Recording, report.Metadata.Package, report.Metadata.Schema, report.Metadata.Status)
	entries := *report.Incidents
	arg := shellRecordingPath(report.Recording)
	var incident string
	if len(entries) == 0 {
		p.line("No incident anchors recorded.")
	} else {
		p.line("Newest indexed anchors (host collection elapsed time)")
		for i := len(entries) - 1; i >= max(0, len(entries)-watchSummaryAnchors); i-- {
			e := entries[i]
			if e.Source == "" {
				p.line("  %s [%.3f..%.3fs] observed disappearance, PID %d (cause unknown)", e.ID, seconds(e.StartElapsedNS), seconds(e.EndElapsedNS), e.PID)
			} else {
				p.line("  %s [%.3f..%.3fs] %q reported by %q, PID %d (instance association unproven)", e.ID, seconds(e.StartElapsedNS), seconds(e.EndElapsedNS), e.Kind, e.Source, e.PID)
			}
		}
		incident = " --incident " + entries[len(entries)-1].ID
	}
	if report.IndexTruncated {
		p.line("Incident index capped at 4096 entries. Later anchors may be omitted.")
	}
	p.line("Capture can miss events. Review stored observations and coverage with replay.")
	p.line("  droidsleuth investigate --tui%s %s", incident, arg)
	p.line("  droidsleuth investigate --timeline%s %s", incident, arg)
	p.line("  droidsleuth replay %s", arg)
	return p.err
}

// shellRecordingPath produces a copyable argument without emitting terminal
// controls. Ordinary paths use POSIX single quotes. Control characters and
// invalid UTF-8 use Bash/Zsh ANSI-C quoting with byte-wise octal escapes.
func shellRecordingPath(path string) string {
	if utf8.ValidString(path) && !strings.ContainsFunc(path, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, c := range []byte(path) {
		if c >= ' ' && c <= '~' && c != '\'' && c != '\\' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
