package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"droidsleuth/internal/investigate"
	"droidsleuth/internal/session"
)

func runInvestigate(ctx context.Context, flags *flag.FlagSet, args []string, out, diagnostic io.Writer) error {
	asJSON := flags.Bool("json", false, "emit versioned investigation JSON")
	last := flags.Bool("last", false, "use the newest finalized managed recording")
	timeline := flags.Bool("timeline", false, "list incident anchors in a finalized recording")
	incident := flags.String("incident", "", "inspect a timeline anchor by ID")
	before := flags.Duration("before", investigate.DefaultContext, "context before the anchor (0..5m)")
	after := flags.Duration("after", investigate.DefaultContext, "context after the anchor (0..5m)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*last && flags.NArg() != 0) || (!*last && flags.NArg() != 1) {
		return errors.New("usage: droidsleuth investigate [--json] [--timeline [--incident ID [--before D --after D]]] (FILE | --last)")
	}
	var incidentSet, contextSet bool
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "incident":
			incidentSet = true
		case "before", "after":
			contextSet = true
		}
	})
	if incidentSet && !*timeline {
		return errors.New("--incident requires --timeline")
	}
	if contextSet && !incidentSet {
		return errors.New("--before and --after require --incident")
	}
	if *before < 0 || *before > investigate.MaxContext || *after < 0 || *after > investigate.MaxContext {
		return errors.New("context durations must be between 0 and 5m")
	}
	var id *investigate.IncidentID
	if incidentSet {
		parsed, err := investigate.ParseIncidentID(*incident)
		if err != nil {
			return err
		}
		id = &parsed
	}
	path := flags.Arg(0)
	if *last {
		dir, err := managedDirectory()
		if err != nil {
			return err
		}
		chosen, skipped, err := session.LatestFinalized(ctx, dir)
		if skipped > 0 {
			if _, writeErr := fmt.Fprintf(diagnostic, "Skipped %d newer unfinalized managed recording(s).\n", skipped); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			return err
		}
		path = chosen
		if !*timeline {
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if _, err = fmt.Fprintf(diagnostic, "Using recording %q.\n", abs); err != nil {
				return err
			}
		}
	}
	if *timeline {
		report, err := investigate.Timeline(ctx, path, id, *before, *after)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(report)
		}
		return printTimeline(out, report)
	}
	var report investigate.Report
	var err error
	if *last {
		err = session.WithFinalizedRecording(ctx, path, func(recording session.Recording) error {
			var readErr error
			report, readErr = investigate.AnalyzeRecording(ctx, recording)
			return readErr
		})
	} else {
		report, err = investigate.Analyze(ctx, path)
	}
	if err != nil {
		return err
	}
	return printInvestigation(out, report, *asJSON)
}

func printInvestigation(out io.Writer, report investigate.Report, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(report)
	}
	if _, err := fmt.Fprintf(out, "Package %q; %d reported events; %d observed disappearances (cause unknown)\n", report.Package, len(report.Findings), report.Disappearances); err != nil {
		return err
	}
	for _, f := range report.Findings {
		if _, err := fmt.Fprintf(out, "%s reported by %s: process=%q pid=%d device_time_ns=%d detail=%q reason=%v conflict=%t\n", f.Event.Kind, f.Source, f.Event.Process, f.Event.PID, f.Event.DeviceTimeNS, f.Event.Detail, reasonText(f.Event.Reason), f.Conflict); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "  source excerpt (truncated=%t): %q\n", f.ExcerptTruncated, f.Excerpt); err != nil {
			return err
		}
		for _, id := range f.Candidates {
			if _, err := fmt.Fprintf(out, "  candidate instance: boot=%q uid=%d pid=%d start_ticks=%d (unproven)\n", id.BootID, id.UID, id.PID, id.StartTicks); err != nil {
				return err
			}
		}
		for _, ref := range f.Evidence {
			if _, err := fmt.Fprintf(out, "  evidence: observation #%d lines %d..%d (available through replay --json)\n", ref.Sequence, ref.FirstLine, ref.LastLine); err != nil {
				return err
			}
		}
		for _, u := range f.Uncertainty {
			if _, err := fmt.Fprintf(out, "  uncertainty: %s\n", u); err != nil {
				return err
			}
		}
	}
	for _, g := range report.Gaps {
		if err := printRecord(out, g); err != nil {
			return err
		}
	}
	for _, l := range report.Limitations {
		if _, err := fmt.Fprintf(out, "Limitation: %s\n", l); err != nil {
			return err
		}
	}
	return nil

}

// timelinePrinter stops writing at the first output failure.
type timelinePrinter struct {
	out io.Writer
	err error
}

func (p *timelinePrinter) line(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.out, format+"\n", args...)
	}
}

func (p *timelinePrinter) identity(prefix string, id session.Identity) {
	p.line("%s boot=%q uid=%d pid=%d start_ticks=%d", prefix, id.BootID, id.UID, id.PID, id.StartTicks)
}

func printTimeline(out io.Writer, r investigate.TimelineReport) error {
	p := timelinePrinter{out: out}
	p.line("Recording %q, package %q, schema %d, status %s", r.Recording, r.Metadata.Package, r.Metadata.Schema, r.Metadata.Status)
	if r.Incidents != nil {
		if len(*r.Incidents) == 0 {
			p.line("No recorded incident anchors.")
		}
		for _, entry := range *r.Incidents {
			p.summary(entry)
		}
		if r.IndexTruncated {
			p.line("Incident index reached 4096 entries. Additional observation IDs can be inspected directly.")
		}
		if len(*r.Incidents) > 0 {
			p.line("Inspect an anchor with --timeline --incident ID.")
		}
	}
	if d := r.Incident; d != nil {
		p.summary(d.Anchor)
		if l := d.Lifecycle; l != nil {
			p.identity("  Observed process instance", l.Transition.Process.Identity)
			p.line("  Recorded after_gap=%t. Disappearance cause unknown.", l.Transition.AfterGap)
			if l.LastPresence == nil {
				p.line("  Last presence unavailable.")
			} else {
				p.endpoint("Last observed presence", *l.LastPresence)
				p.line("  Snapshot gaps between endpoints: %d", *l.SnapshotGaps)
			}
			p.endpoint("First observed absence", l.FirstAbsence)
		}
		if d.Report != nil {
			p.finding(d.Anchor.Source, *d.Report)
		}
		p.line("Collection context %.3f..%.3fs in recording sequence", seconds(d.WindowStartNS), seconds(d.WindowEndNS))
		if d.BeforeOmitted+d.AfterOmitted > 0 {
			p.line("  Context limit omitted %d earlier and %d later observations.", d.BeforeOmitted, d.AfterOmitted)
		}
		var anchorPrinted bool
		for _, row := range d.Context {
			if row.Sequence > d.Anchor.Sequence && !anchorPrinted {
				p.observation(d.AnchorObservation, d.Anchor.ID)
				anchorPrinted = true
			}
			p.observation(row, "")
		}
		if !anchorPrinted {
			p.observation(d.AnchorObservation, d.Anchor.ID)
		}
		if d.AnalysisIncomplete {
			p.line("  Association analysis incomplete. Some findings or candidates may be unavailable.")
		}
	}
	for _, note := range r.Limitations {
		p.line("Limitation: %s", note)
	}
	return p.err
}

func seconds(ns int64) float64 { return float64(ns) / 1e9 }

func (p *timelinePrinter) summary(e investigate.IncidentSummary) {
	p.line("%s [%.3f..%.3fs] %s source=%q process=%q pid=%d label_truncated=%t", e.ID, seconds(e.StartElapsedNS), seconds(e.EndElapsedNS), e.Kind, e.Source, e.Process, e.PID, e.LabelTruncated)
}

func (p *timelinePrinter) endpoint(label string, e investigate.SnapshotEndpoint) {
	p.line("  %s #%d [%.3f..%.3fs] device_epoch_seconds=%d", label, e.Sequence, seconds(e.StartElapsedNS), seconds(e.EndElapsedNS), e.DeviceEpochSeconds)
	p.resources(e.Resources)
}

func (p *timelinePrinter) observation(row investigate.TimelineObservation, selected string) {
	p.line("  #%d [%.3f..%.3fs] %s source=%q", row.Sequence, seconds(row.StartElapsedNS), seconds(row.EndElapsedNS), row.Kind, row.Source)
	if row.DetailsOmitted {
		p.line("    Details exceed context budget. See replay --json.")
		return
	}
	if row.Error != "" {
		p.line("    Coverage gap: %q", row.Error)
	}
	if s := row.Snapshot; s != nil {
		p.line("    Snapshot: %d main process(es), boot=%q", len(s.Processes), s.BootID)
		for _, process := range s.Processes {
			p.identity(fmt.Sprintf("    Observed %q", process.Name), process.Identity)
		}
		for _, t := range row.Transitions {
			p.identity(fmt.Sprintf("    %s %q after_gap=%t", t.Kind, t.Process.Name, t.AfterGap), t.Process.Identity)
		}
	}
	p.resources(row.Resources)
	if s := row.SourceStatus; s != nil {
		p.line("    Bounded read succeeded=%t selected_observations=%d", s.ReadSucceeded, s.SelectedObservations)
	}
	for _, f := range row.Reports {
		if f.ID != selected {
			p.finding(row.Source, f)
		}
	}
}

func (p *timelinePrinter) resources(m *investigate.ResourceObservation) {
	if m == nil {
		return
	}
	if m.Raw.Identity == nil {
		p.line("    Resource sample: %s", m.Raw.Status)
		return
	}
	p.identity("    Resources for", *m.Raw.Identity)
	if c := m.CPU; c != nil {
		p.line("      CPU %.1f%% of one logical CPU, interval %.3fs (possible %.3f..%.3fs), observations #%d and #%d", c.PercentOfOneCPU, seconds(c.ElapsedNS), seconds(c.MinElapsedNS), seconds(c.MaxElapsedNS), c.FromSequence, c.ToSequence)
	} else {
		p.line("      CPU rate unavailable: %s", m.CPUUnavailable)
	}
	if m.RSSBytes != nil {
		p.line("      Approximate RSS %.2f MiB", float64(*m.RSSBytes)/(1<<20))
	} else {
		p.line("      RSS unavailable: %s", m.Raw.RSSUnavailable)
	}
}

func (p *timelinePrinter) finding(source string, f investigate.TimelineFinding) {
	e := f.Event
	p.line("    %s %s reported by %s process=%q pid=%d detail=%q reason=%s", f.ID, e.Kind, source, e.Process, e.PID, e.Detail, reasonText(e.Reason))
	if e.UID != nil {
		p.line("      Reported UID: %d", *e.UID)
	}
	if e.User != nil {
		p.line("      Reported Android user: %d", *e.User)
	}
	if e.BootID != "" {
		p.line("      Source boot: %q", e.BootID)
	}
	if e.DeviceTimeNS == 0 {
		p.line("      Device occurrence time unknown, raw=%q", e.RawTime)
	} else {
		p.line("      Device occurrence timestamp (ns): %d, precision_ns=%d, raw=%q", e.DeviceTimeNS, e.TimeUncertaintyNS, e.RawTime)
	}
	p.line("      Evidence: observation #%d lines %d..%d", f.Reference.Sequence, f.Reference.FirstLine, f.Reference.LastLine)
	if f.Excerpt != "" {
		p.line("      Source excerpt (truncated=%t): %q", f.ExcerptTruncated, f.Excerpt)
	}
	p.line("      Instance association: %s", f.Association)
	if f.Conflict {
		p.line("      Conflicting reported exit reasons.")
	}
	for _, id := range f.Candidates {
		p.identity("      Candidate instance (unproven)", id)
	}
	for _, ref := range f.Evidence {
		if ref != f.Reference {
			p.line("      Related report: observation #%d lines %d..%d", ref.Sequence, ref.FirstLine, ref.LastLine)
		}
	}
	for _, u := range f.Uncertainty {
		p.line("      Uncertainty: %s", u)
	}
}
