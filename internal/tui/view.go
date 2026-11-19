package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func quoteRune(r rune) string {
	s := strconv.QuoteRuneToASCII(r)
	return s[1 : len(s)-1]
}

func (m *model) View() string {
	if m.width < 40 || m.height < 10 {
		return ansi.Truncate("Resize to 40×10 or press q to quit.", max(1, m.width), "")
	}
	width := min(m.width, 512)
	lines := []string{"DroidSleuth · Incident investigation"}
	footer := "↑/↓ Move  PgUp/PgDn Page  Enter Open  g Go to ID  ? Help  q Quit"
	switch {
	case m.help:
		lines = append(lines, "Keyboard help", "↑/↓ or j/k to move. Page Up/Down to page.", "Enter opens. Esc goes back. q or Ctrl+C quits.")
		footer = "Esc Back"
	case m.enteringID:
		lines = append(lines, "Incident ID: "+m.idInput)
		footer = "Enter Open  Esc Cancel"
	case m.screen == detailScreen:
		footer = "Esc Index  r Reload  ? Help  q Quit"
		if d := m.detail.Incident; d != nil {
			lines = append(lines, fmt.Sprintf("%s %s · %s · PID %d", d.Anchor.ID, d.Anchor.Kind, d.Anchor.Process, d.Anchor.PID))
			if d.Lifecycle != nil {
				lines = append(lines, "Observed disappearance. Cause unknown.")
			}
		}
	default:
		lines = append(lines, fmt.Sprintf("%s · schema %d", m.index.Metadata.Package, m.index.Metadata.Schema))
		if m.index.Incidents != nil {
			entries := *m.index.Incidents
			if len(entries) == 0 {
				lines = append(lines, "No recorded incident anchors. Use replay FILE for observations.")
			}
			if m.index.IndexTruncated {
				lines = append(lines, "Index limited to 4096 entries. Use g to open another ID.")
			}
			m.indexTop = keepVisible(m.indexTop, m.indexSelection, m.page())
			for i := m.indexTop; i < min(len(entries), m.indexTop+m.page()); i++ {
				e := entries[i]
				marker := "  "
				if i == m.indexSelection {
					marker = "> "
				}
				lines = append(lines, fmt.Sprintf("%s%s %s · %s · PID %d", marker, e.ID, e.Kind, e.Process, e.PID))
			}
		}
	}
	if m.err != nil {
		footer = "Error: " + m.err.Error() + " · Esc Back  r Retry"
	} else if m.active != nil {
		footer = "Loading…  Esc Back  q Quit"
	}
	lines = append(lines, footer)
	for i, line := range lines {
		lines[i] = ansi.Truncate(escape(line), width, "…")
	}
	lines[0] = lipgloss.NewStyle().Bold(true).Render(lines[0])
	return strings.Join(lines, "\n")
}
