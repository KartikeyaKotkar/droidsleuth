package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/investigate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestLoaderCancelsAndCoalesces(t *testing.T) {
	ctx := context.Background()
	entered := make(chan request, 2)
	cancelled := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var workers, peak atomic.Int32
	m := &model{ctx: ctx, width: 80, height: 24, screen: detailScreen,
		wanted: request{id: "E1.0", before: investigate.DefaultContext, after: investigate.DefaultContext},
		load: func(ctx context.Context, req request) result {
			current := workers.Add(1)
			if current > peak.Load() {
				peak.Store(current)
			}
			defer workers.Add(-1)
			entered <- req
			if req.before == investigate.DefaultContext {
				<-ctx.Done()
				close(cancelled)
				<-release
				return result{err: errors.New("stale failure")}
			}
			return result{report: investigate.TimelineReport{Incident: &investigate.IncidentDetail{
				Anchor: investigate.IncidentSummary{ID: req.id},
			}}}
		},
	}
	first := m.Init()
	<-entered
	started := time.Now()
	for i := 0; i < 5; i++ {
		if cmd := press(m, runeKey('w')); cmd != nil {
			t.Fatal("started a second load")
		}
	}
	select {
	case <-cancelled:
		t.Logf("synthetic worker observed cancellation in %v", time.Since(started))
	case <-time.After(time.Second):
		t.Fatal("superseded worker did not receive cancellation")
	}
	if m.pending == nil || m.pending.before != 5*time.Minute {
		t.Fatalf("latest load was not retained: %v", m.pending)
	}
	close(release)
	old := first()
	_, next := m.Update(old)
	if req := <-entered; req.before != 5*time.Minute {
		t.Fatalf("loaded stale window: %+v", req)
	}
	m.Update(old)
	finish(m, next)
	if m.detail.Incident == nil || m.err != nil || peak.Load() != 1 || workers.Load() != 0 {
		t.Fatalf("failed to apply current window or exceeded concurrency limit: workers=%d peak=%d err=%v", workers.Load(), peak.Load(), m.err)
	}
}

func TestBackRejectsCompletedButStaleLoad(t *testing.T) {
	entries := []investigate.IncidentSummary{{ID: "E1.0"}}
	m := &model{ctx: context.Background(), width: 80, height: 24, index: investigate.TimelineReport{Incidents: &entries},
		load: func(_ context.Context, req request) result {
			return result{report: investigate.TimelineReport{Incident: &investigate.IncidentDetail{Anchor: entries[0]}}}
		},
	}
	cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	stale := cmd()
	press(m, tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(stale)
	if m.detail.Incident != nil || m.screen != indexScreen || m.active != nil {
		t.Fatalf("stale detail accepted on index: %s", m.View())
	}
}

func TestRecordingReplacementRequiresReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.db")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := openRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	m := &model{ctx: context.Background(), width: 80, height: 24, screen: detailScreen, load: recordingLoader(path, original), wanted: request{id: "E1.0"}}
	finish(m, m.Init())
	if !m.changed || !strings.Contains(m.View(), "Quit and reopen") || press(m, runeKey('r')) != nil {
		t.Fatalf("replacement was not rejected: %s", m.View())
	}
}

func TestTinyResizeAndTerminalEscaping(t *testing.T) {
	payload := "界\x1b]52;c;BAD\a\u009b2J\x7f\u202e\u2066\u2028"
	entries := []investigate.IncidentSummary{{ID: "E1.0"}, {ID: "E2.0", Process: payload}}
	m := &model{ctx: context.Background(), width: 100, height: 24, index: investigate.TimelineReport{Incidents: &entries}}
	press(m, runeKey('j'))
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	press(m, runeKey('k'))
	if !strings.Contains(m.View(), "resize") || m.indexCursor.selected != 1 {
		t.Fatalf("tiny view modified selection: %s", m.View())
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	got := m.View()
	for _, escaped := range []string{`\x1b`, `\a`, `\u009b`, `\x7f`, `\u202e`, `\u2066`, `\u2028`} {
		if !strings.Contains(got, escaped) {
			t.Errorf("escaped %q missing: %q", escaped, got)
		}
	}
	if !strings.Contains(got, "> E2.0") || strings.Contains(got, payload) {
		t.Fatalf("unexpected resized view: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("line exceeds terminal width: %q", line)
		}
	}
	m.screen = evidenceScreen
	m.refs = []investigate.Reference{{Sequence: 2, FirstLine: 1, LastLine: 1}}
	m.evidence = &investigate.EvidenceSlice{Reference: m.refs[0], Text: payload}
	m.reflowInspector()
	if got := m.View(); strings.Contains(got, payload) || !strings.Contains(got, `\u202e`) {
		t.Fatalf("source text bypassed trust boundary: %q", got)
	}
}

func TestBoundedIDEntryAndRecovery(t *testing.T) {
	entries := []investigate.IncidentSummary{}
	m := &model{ctx: context.Background(), width: 80, height: 24, index: investigate.TimelineReport{Incidents: &entries},
		load: func(_ context.Context, req request) result {
			if req.id != "E9000.15" {
				t.Errorf("wrong direct ID %q", req.id)
			}
			return result{err: errors.New("test lookup failure")}
		},
	}
	press(m, runeKey('g'))
	press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("1", 1000)), Paste: true})
	press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.idInput != "" || m.err == nil || m.screen != indexScreen {
		t.Fatal("oversize or empty ID accepted")
	}
	press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E9000.15"), Paste: true})
	finish(m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if !strings.Contains(m.View(), "test lookup failure") {
		t.Fatal("lookup error hidden")
	}
	finish(m, press(m, runeKey('r')))
	press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(m.View(), "No recorded incident anchors") {
		t.Fatalf("back failed: %s", m.View())
	}
}

func TestTerminalEOFIsReportedToBubbleTea(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	w.Close()
	if _, err := (terminalInput{r}).Read(make([]byte, 1)); !errors.Is(err, errTerminalEOF) || errors.Is(err, io.EOF) {
		t.Fatalf("EOF will be ignored by terminal runner: %v", err)
	}
}
