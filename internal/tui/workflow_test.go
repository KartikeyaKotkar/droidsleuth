package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"droidsleuth/internal/investigate"
	"droidsleuth/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOfflineWorkflow(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recording.db")
	p := session.Process{Identity: session.Identity{BootID: "boot", UID: 10123, PID: 7, StartTicks: 100}, Name: "com.example.app"}
	s, err := session.Create(ctx, path, session.Metadata{Package: p.Name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := session.Event{Kind: "anr_report", Process: p.Name, PID: p.PID, FirstLine: 2, LastLine: 2}
	rows := []session.Record{
		{Kind: "snapshot", Snapshot: &session.Snapshot{BootID: p.BootID, Processes: []session.Process{p}}},
		{Kind: "gap", Source: "logcat", Error: "buffer overlap missing"},
		{Kind: "evidence", Source: "logcat", Evidence: &session.Evidence{Text: "untargeted line\nexplicit report\nuntargeted line", Format: "fixture", Events: []session.Event{e}, Truncated: true}},
		{Kind: "snapshot", Snapshot: &session.Snapshot{BootID: p.BootID}, Transitions: []session.Transition{{Kind: "no_longer_observed", Process: p}}},
	}
	for i, row := range rows {
		row.Sequence = int64(i + 1)
		row.StartElapsedNS = row.Sequence * int64(time.Second)
		row.EndElapsedNS = row.StartElapsedNS
		if err := s.Append(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Finish(ctx, "complete"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := openRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	req := request{id: "E3.0", before: 5 * time.Second, after: 10 * time.Second}
	m := &model{ctx: ctx, load: recordingLoader(path, info), wanted: req, window: initialWindow(req), screen: detailScreen, width: 100, height: 24}
	show := func(parts ...string) {
		t.Helper()
		for _, part := range parts {
			if !strings.Contains(m.View(), part) {
				t.Fatalf("missing %q from %s", part, m.View())
			}
		}
	}
	finish(m, m.Init())
	show("> *#3", "GAP · logcat", "5s before / 10s after", "association unproven")
	finish(m, press(m, runeKey('w')))
	show("30s before / 30s after", "> *#3")
	press(m, tea.KeyMsg{Type: tea.KeyEnter})
	show("Device occurrence unknown", "Instance association: unbound", "Uncertainty:")
	press(m, runeKey('e'))
	show("Observation #3 · lines 2–2")
	finish(m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	show("Stored evidence", "Source logcat", "2  explicit report", "truncated")
	if strings.Contains(m.View(), "untargeted line") {
		t.Fatal("evidence view spilled outside the verified range")
	}
	for i := 0; i < 3; i++ {
		press(m, tea.KeyMsg{Type: tea.KeyEsc})
	}
	show("> *#3")
	press(m, runeKey('j'))
	press(m, tea.KeyMsg{Type: tea.KeyEnter})
	show("Observed disappearance", "Resources not recorded")
	// A neighboring report is still independent of this successful absence.
	if strings.Contains(m.View(), "reported anr_report") {
		t.Fatal("diagnostic evidence became an absence finding")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("recording changed: %v", err)
	}
}

func TestCPUAndRSSRemainIndependentInInspector(t *testing.T) {
	id := session.Identity{BootID: "boot", UID: 10123, PID: 7, StartTicks: 100}
	rss := uint64(16 << 20)
	sample := &investigate.ResourceObservation{Raw: &session.Resources{Identity: &id}, RSSBytes: &rss, CPUUnavailable: "baseline_required"}
	got := strings.Join(appendResources(nil, sample, 3), "\n")
	if !strings.Contains(got, "RSS 16.00 MiB") || !strings.Contains(got, "CPU rate unavailable: baseline_required") {
		t.Fatal(got)
	}
	sample.RSSBytes, sample.Raw.RSSUnavailable = nil, "permission_denied"
	sample.CPU = &investigate.CPURate{PercentOfOneCPU: 193.5, FromSequence: 1, ToSequence: 2,
		ElapsedNS: 1e9, MinElapsedNS: 9e8, MaxElapsedNS: 11e8}
	got = strings.Join(appendResources(nil, sample, 3), "\n")
	for _, part := range []string{"193.5% of one logical CPU", "#1 to #2", "possible 0.900s–1.100s", "RSS unavailable: permission_denied"} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %s: %s", part, got)
		}
	}
}
