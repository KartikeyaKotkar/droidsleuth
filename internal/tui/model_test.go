package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/KartikeyaKotkar/droidsleuth/internal/investigate"

	tea "github.com/charmbracelet/bubbletea"
)

func press(m *model, key tea.KeyMsg) tea.Cmd {
	_, cmd := m.Update(key)
	return cmd
}

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func finish(m *model, cmd tea.Cmd) tea.Cmd {
	_, next := m.Update(cmd())
	return next
}

func TestIndexDetailBack(t *testing.T) {
	index := []investigate.IncidentSummary{{ID: "E4.0"}, {ID: "L6.0", Kind: "no_longer_observed"}}
	m := &model{ctx: context.Background(), width: 80, height: 24, load: func(_ context.Context, req request) result {
		if req.id == "" {
			return result{report: investigate.TimelineReport{Incidents: &index}}
		}
		d := &investigate.IncidentDetail{Anchor: index[1], Lifecycle: &investigate.LifecycleIncident{}}
		return result{report: investigate.TimelineReport{Incident: d}}
	}}
	finish(m, m.Init())
	press(m, runeKey('j'))
	finish(m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.wanted.id != "L6.0" || !strings.Contains(m.View(), "Cause unknown") {
		t.Fatalf("wrong detail: %s", m.View())
	}
	press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.detail.Incident != nil || !strings.Contains(m.View(), "> L6.0") {
		t.Fatalf("index selection lost: %s", m.View())
	}
}

func TestIndexShowsCollectionTimeAndUncertainty(t *testing.T) {
	entries := []investigate.IncidentSummary{
		{ID: "E4.0", Kind: "exit_report", Process: "com.example.app", PID: 7},
		{ID: "L6.0", Kind: "no_longer_observed", Process: "com.example.app", PID: 7},
	}
	m := &model{width: 120, height: 24, index: investigate.TimelineReport{Incidents: &entries}}
	view := m.View()
	for _, want := range []string{"exit_report", "no_longer_observed", "association unproven", "1/2", "0.000s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %q", want, view)
		}
	}
	press(m, runeKey('j'))
	if !strings.Contains(m.View(), "2/2") || !strings.Contains(m.View(), "cause unknown") {
		t.Fatal("selection position did not update")
	}
}
