package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var helpLines = []string{
	"↑/↓ or j/k move. Page Up/Down pages.",
	"Enter opens. Esc goes back.",
	"g opens an incident by ID from the index.",
	"w cycles ±30s, ±2m and ±5m on the timeline.",
	"e lists evidence for the selected observation.",
	"r reloads a timeline or retries a failed read.",
	"q or Ctrl+C quits.",
	"Host collection and device occurrence use separate clocks.",
	"Reports are source claims. Instance associations are unproven.",
	"Resources are context. Disappearance has unknown cause.",
	"Logcat can lose records silently.",
}

func (m *model) View() string {
	if !m.usable() {
		notice := "Resize to 40×10 or q to quit"
		if m.width > 0 && m.width < 28 {
			notice = "q quit · resize"
		}
		return ansi.Truncate(notice, max(1, m.width), "")
	}
	header := []string{"DroidSleuth · Incident investigation"}
	var body []string
	var keys string
	switch {
	case m.help:
		header = append(header, "Keyboard help")
		body = helpLines
		keys = "↑/↓ Scroll  Esc Back  q Quit"
	case m.enteringID:
		header = append(header, "Open incident by ID", "Example  E12.0 or L42.0")
		body = []string{"> " + m.idInput}
		keys = "Enter Open  Esc Cancel"
	case m.screen == indexScreen:
		header = append(header, "Incident index")
		if m.index.Incidents != nil {
			header = append(header, fmt.Sprintf("%s · schema %d · %d incidents", m.index.Metadata.Package, m.index.Metadata.Schema, len(*m.index.Incidents)))
			if len(*m.index.Incidents) == 0 {
				body = []string{"No recorded incident anchors.", "Use replay FILE for observations."}
			}
			if m.index.IndexTruncated {
				header = append(header, "Index capped at 4096. Use g for other IDs.")
			}
			for i, end := m.indexCursor.top, min(len(*m.index.Incidents), m.indexCursor.top+m.page()); i < end; i++ {
				e := (*m.index.Incidents)[i]
				name := e.Process
				if e.LabelTruncated {
					name += "…"
				}
				body = append(body, marker(i == m.indexCursor.selected)+fmt.Sprintf("%s %s · PID %d · %s", e.ID, e.Kind, e.PID, name))
			}
		}
		keys = "↑/↓ Move  Enter Open  g ID  ? Help  q Quit"
	case m.screen == detailScreen:
		header = append(header, "Timeline · "+m.wanted.id, fmt.Sprintf("Host window: %s before / %s after", m.wanted.before, m.wanted.after))
		if d := m.detail.Incident; d != nil {
			note := "Source reports · association unproven"
			if d.Lifecycle != nil {
				note = "Observed disappearance. Cause unknown."
			}
			header = append(header, note)
			for i, end := m.timelineCursor.top, min(len(m.rows), m.timelineCursor.top+m.page()); i < end; i++ {
				row := m.rows[i]
				anchor := " "
				if row == &d.AnchorObservation {
					anchor = "*"
				}
				body = append(body, marker(i == m.timelineCursor.selected)+anchor+observationLabel(row))
			}
		}
		keys = "Enter Detail  e Evidence  w Window  Esc Index"
	case m.screen == observationScreen:
		header = append(header, fmt.Sprintf("Observation #%d · %s", m.selectedRow().Sequence, m.wanted.id))
		body = m.inspectorPage()
		keys = "↑/↓ Scroll  e Evidence  Esc Timeline"
	case m.screen == referencesScreen:
		header = append(header, fmt.Sprintf("Evidence references · observation #%d", m.selectedRow().Sequence))
		for i, end := m.refsCursor.top, min(len(m.refs), m.refsCursor.top+m.page()); i < end; i++ {
			body = append(body, marker(i == m.refsCursor.selected)+referenceLabel(m.refs[i]))
		}
		keys = "↑/↓ Move  Enter Read  Esc Back"
	case m.screen == evidenceScreen:
		ref := m.refs[m.refsCursor.selected]
		header = append(header, "Stored evidence", referenceLabel(ref))
		if m.evidence != nil {
			note := "Retained range"
			if m.evidence.Truncated {
				note = "Source text truncated at collection"
			}
			header = append(header, note)
		}
		body = m.inspectorPage()
		keys = "↑/↓ Scroll  PgUp/PgDn Page  Esc Back"
	}
	status := ""
	switch {
	case errors.Is(m.err, errChanged):
		status = "Recording changed. Quit and reopen."
	case m.err != nil:
		status = m.err.Error()
		if m.screen == evidenceScreen || (m.screen == detailScreen && m.detail.Incident == nil) {
			keys = "Esc Back  r Retry  q Quit"
		}
	case m.active != nil && !m.active.ignored:
		status = "Loading…"
	case m.screen == detailScreen && m.detail.Incident != nil:
		d := m.detail.Incident
		if d.BeforeOmitted+d.AfterOmitted > 0 {
			status = fmt.Sprintf("Omitted %d earlier / %d later", d.BeforeOmitted, d.AfterOmitted)
		}
		if d.AnalysisIncomplete {
			if status != "" {
				status += " · "
			}
			status += "Analysis incomplete"
		}
	}
	if m.help {
		start := min(m.helpScroll, len(body)-1)
		body = body[start:min(len(body), start+m.page())]
	}
	width := m.columns()
	lines := make([]string, 4+m.page()+2)
	for i, line := range header {
		lines[i] = ansi.Truncate(escape(line), width, "…")
	}
	for i, line := range body {
		lines[4+i] = ansi.Truncate(lineForScreen(m.screen, m.help, line), width, "…")
	}
	lines[len(lines)-2] = ansi.Truncate(escape(status), width, "…")
	lines[len(lines)-1] = ansi.Truncate(keys, width, "…")
	lines[0] = lipgloss.NewStyle().Bold(true).Render(lines[0])
	return strings.Join(lines, "\n")
}

func marker(selected bool) string {
	if selected {
		return "> "
	}
	return "  "
}

// Inspector lines are escaped before cell wrapping. All other bodies are
// plain text until this boundary.
func lineForScreen(screen screen, help bool, line string) string {
	if !help && (screen == observationScreen || screen == evidenceScreen) {
		return line
	}
	return escape(line)
}

func (m *model) inspectorPage() []string {
	start := min(m.scroll, max(0, len(m.inspector)-1))
	return m.inspector[start:min(len(m.inspector), start+m.page())]
}
