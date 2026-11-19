package tui

import (
	"fmt"
	"strings"
	"time"

	"droidsleuth/internal/investigate"
	"droidsleuth/internal/session"

	"github.com/charmbracelet/x/ansi"
)

func seconds(ns int64) float64 { return float64(ns) / 1e9 }

func sourceName(row *investigate.TimelineObservation) string {
	if row.Source == "" {
		return "snapshot"
	}
	return row.Source
}

func observationLabel(row *investigate.TimelineObservation) string {
	label := row.Kind
	switch {
	case row.Kind == "gap":
		label = "GAP · " + sourceName(row)
	case len(row.Reports) > 0:
		label = row.Reports[0].Event.Kind + " · " + row.Source
	case row.Source != "":
		label += " · " + row.Source
	}
	for _, transition := range row.Transitions {
		if transition.Kind == "no_longer_observed" {
			label += " · observed disappearance"
			break
		}
	}
	if row.DetailsOmitted {
		label += " · details omitted"
	}
	if sample := row.Resources; sample != nil {
		if sample.CPU != nil {
			label += fmt.Sprintf(" · CPU %.1f%%", sample.CPU.PercentOfOneCPU)
		}
		if sample.RSSBytes != nil {
			label += fmt.Sprintf(" · RSS %.1f MiB", float64(*sample.RSSBytes)/(1<<20))
		}
	}
	return fmt.Sprintf("#%d %.3fs %s", row.Sequence, seconds(row.StartElapsedNS), label)
}

func referenceLabel(ref investigate.Reference) string {
	return fmt.Sprintf("Observation #%d · lines %d–%d", ref.Sequence, ref.FirstLine, ref.LastLine)
}

func observationReferences(d *investigate.IncidentDetail, row *investigate.TimelineObservation) []investigate.Reference {
	var refs []investigate.Reference
	seen := make(map[investigate.Reference]bool)
	add := func(ref investigate.Reference) {
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	// Existing analysis caps a source row at 16 reports and each report at
	// 16 related references. Keep primary links ahead of related reports.
	if d.Report != nil && row == &d.AnchorObservation {
		add(d.Report.Reference)
	}
	for _, f := range row.Reports {
		add(f.Reference)
	}
	if d.Report != nil && row == &d.AnchorObservation {
		for _, ref := range d.Report.Evidence {
			add(ref)
		}
	}
	for _, f := range row.Reports {
		for _, ref := range f.Evidence {
			add(ref)
		}
	}
	return refs
}

func (m *model) reflowInspector() {
	if !m.usable() {
		return
	}
	var lines []string
	switch m.screen {
	case evidenceScreen:
		if m.evidence == nil {
			return
		}
		e := m.evidence
		lines = []string{fmt.Sprintf("Source %s · format %s", e.Source, e.Format), "Line numbers refer to the recorded source.", ""}
		for i, line := range strings.Split(e.Text, "\n") {
			lines = append(lines, fmt.Sprintf("%d  %s", e.Reference.FirstLine+i, strings.ReplaceAll(line, "\\", "\\\\")))
		}
	case observationScreen:
		row := m.selectedRow()
		lines = observationLines(m.detail.Incident, row, m.detail.Metadata.Schema)
	default:
		return
	}
	m.inspector = nil
	for _, line := range lines {
		m.inspector = append(m.inspector, strings.Split(ansi.Hardwrap(escape(line), m.columns(), true), "\n")...)
	}
	m.scroll = min(m.scroll, max(0, len(m.inspector)-1))
}

func observationLines(d *investigate.IncidentDetail, row *investigate.TimelineObservation, schema int) []string {
	lines := []string{
		fmt.Sprintf("Host collection %.3f–%.3fs · %s", seconds(row.StartElapsedNS), seconds(row.EndElapsedNS), sourceName(row)),
		fmt.Sprintf("Host UTC %s to %s", row.StartedUTC.Format(time.RFC3339Nano), row.EndedUTC.Format(time.RFC3339Nano)),
	}
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	if row == &d.AnchorObservation {
		add("Incident anchor %s · %s", d.Anchor.ID, d.Anchor.Kind)
		if d.AnalysisIncomplete {
			add("Association analysis incomplete. Findings or candidates may be missing.")
		}
		if d.BeforeOmitted+d.AfterOmitted > 0 {
			add("Context omitted %d earlier and %d later observations.", d.BeforeOmitted, d.AfterOmitted)
		}
		if l := d.Lifecycle; l != nil {
			add("Observed disappearance. Cause unknown.")
			add("Recorded after_gap=%t · %s", l.Transition.AfterGap, identity(l.Transition.Process.Identity))
			if l.LastPresence == nil {
				add("Last observed presence unavailable.")
			} else {
				lines = appendEndpoint(lines, "Last observed presence", *l.LastPresence, schema)
				add("Snapshot coverage gaps between endpoints: %d", *l.SnapshotGaps)
			}
			lines = appendEndpoint(lines, "First observed absence", l.FirstAbsence, schema)
		}
		if d.Report != nil {
			lines = appendFinding(lines, *d.Report)
		}
	}
	if row.DetailsOmitted {
		add("Observation details exceed the context budget. See replay --json.")
		return lines
	}
	if row.Error != "" {
		add("Coverage gap (%s): %s", sourceName(row), row.Error)
	}
	if s := row.Snapshot; s != nil {
		add("Snapshot · %d main processes · boot %s", len(s.Processes), s.BootID)
		add("Snapshot device epoch %ds · uptime %.3fs", s.DeviceEpochSeconds, s.DeviceUptimeSeconds)
		for _, p := range s.Processes {
			add("Observed %s · %s", p.Name, identity(p.Identity))
		}
		for _, t := range row.Transitions {
			if t.Kind == "no_longer_observed" && !(row == &d.AnchorObservation && d.Lifecycle != nil) {
				add("Observed disappearance. Cause unknown.")
			}
			add("%s · after_gap=%t · %s", t.Kind, t.AfterGap, identity(t.Process.Identity))
		}
		lines = appendResources(lines, row.Resources, schema)
	}
	if s := row.SourceStatus; s != nil {
		add("Bounded source read succeeded=%t · %d selected observations", s.ReadSucceeded, s.SelectedObservations)
	}
	for _, f := range row.Reports {
		if row != &d.AnchorObservation || f.ID != d.Anchor.ID {
			lines = appendFinding(lines, f)
		}
	}
	return lines
}

func identity(id session.Identity) string {
	return fmt.Sprintf("boot=%s uid=%d pid=%d start_ticks=%d", id.BootID, id.UID, id.PID, id.StartTicks)
}

func appendEndpoint(lines []string, label string, e investigate.SnapshotEndpoint, schema int) []string {
	lines = append(lines, fmt.Sprintf("%s #%d · host %.3f–%.3fs · device epoch %ds", label, e.Sequence, seconds(e.StartElapsedNS), seconds(e.EndElapsedNS), e.DeviceEpochSeconds))
	return appendResources(lines, e.Resources, schema)
}

func appendResources(lines []string, r *investigate.ResourceObservation, schema int) []string {
	if r == nil {
		if schema < 3 {
			return append(lines, "Resources not recorded (schema 1–2).")
		}
		return append(lines, "Resource sample unavailable.")
	}
	if r.Raw.Identity == nil {
		return append(lines, "Resource sample: "+r.Raw.Status)
	}
	lines = append(lines, "Observed resource context · "+identity(*r.Raw.Identity))
	if c := r.CPU; c != nil {
		lines = append(lines, fmt.Sprintf("CPU %.1f%% of one logical CPU · %.3fs (possible %.3fs–%.3fs) · #%d to #%d", c.PercentOfOneCPU, seconds(c.ElapsedNS), seconds(c.MinElapsedNS), seconds(c.MaxElapsedNS), c.FromSequence, c.ToSequence))
	} else {
		lines = append(lines, "CPU rate unavailable: "+r.CPUUnavailable)
	}
	if r.RSSBytes != nil {
		return append(lines, fmt.Sprintf("Approximate RSS %.2f MiB", float64(*r.RSSBytes)/(1<<20)))
	}
	return append(lines, "RSS unavailable: "+r.Raw.RSSUnavailable)
}

func appendFinding(lines []string, f investigate.TimelineFinding) []string {
	e := f.Event
	lines = append(lines, fmt.Sprintf("%s · reported %s · %s · PID %d", f.ID, e.Kind, e.Process, e.PID))
	if e.Reason != nil {
		lines = append(lines, fmt.Sprintf("Reported Android exit reason code %d", *e.Reason))
	}
	if e.UID != nil {
		lines = append(lines, fmt.Sprintf("Reported UID %d", *e.UID))
	}
	if e.User != nil {
		lines = append(lines, fmt.Sprintf("Reported Android user %d", *e.User))
	}
	if e.BootID != "" {
		lines = append(lines, "Reported boot "+e.BootID)
	}
	if e.DeviceTimeNS == 0 {
		lines = append(lines, "Device occurrence unknown · raw "+e.RawTime)
	} else {
		lines = append(lines, fmt.Sprintf("Device occurrence %d ns · precision %d ns · raw %s", e.DeviceTimeNS, e.TimeUncertaintyNS, e.RawTime))
	}
	lines = append(lines, "Instance association: "+f.Association, "Evidence: "+referenceLabel(f.Reference))
	if e.Detail != "" {
		lines = append(lines, "Reported detail: "+e.Detail)
	}
	if f.Conflict {
		lines = append(lines, "Conflicting reported exit reasons.")
	}
	for _, id := range f.Candidates {
		lines = append(lines, "Candidate (unproven) · "+identity(id))
	}
	for _, note := range f.Uncertainty {
		lines = append(lines, "Uncertainty: "+note)
	}
	return lines
}
