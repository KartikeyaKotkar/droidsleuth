package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"droidsleuth/internal/investigate"

	tea "github.com/charmbracelet/bubbletea"
)

type request struct {
	id            string
	ref           investigate.Reference
	before, after time.Duration
}

type result struct {
	job      *job
	report   investigate.TimelineReport
	evidence *investigate.EvidenceSlice
	err      error
}

type job struct {
	cancel  context.CancelFunc
	done    chan struct{}
	ignored bool
}

// start runs one cancellable operation. A buffered result lets the worker
// finish even if the terminal program has already stopped receiving messages.
func (m *model) start(req request) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	j := &job{cancel: cancel, done: make(chan struct{})}
	m.active = j
	results := make(chan result, 1)
	go func() {
		defer close(j.done)
		defer cancel()
		r := m.load(ctx, req)
		r.job = j
		results <- r
	}()
	return func() tea.Msg {
		<-j.done
		return <-results
	}
}

func (m *model) request(req request) tea.Cmd {
	m.err = nil
	if m.active == nil {
		return m.start(req)
	}
	m.active.cancel()
	m.pending = &req
	return nil
}

func (m *model) discard() {
	m.pending = nil
	if m.active != nil {
		m.active.ignored = true
		m.active.cancel()
	}
}

var errChanged = errors.New("recording changed; quit and reopen it")

func recordingLoader(path string, original os.FileInfo) func(context.Context, request) result {
	return func(ctx context.Context, req request) result {
		check := func() error {
			current, err := os.Stat(path)
			if err != nil || !os.SameFile(original, current) || current.Size() != original.Size() || !current.ModTime().Equal(original.ModTime()) {
				return errChanged
			}
			return nil
		}
		if err := check(); err != nil {
			return result{err: err}
		}
		if req.ref.Sequence != 0 {
			slice, err := investigate.ReadEvidence(ctx, path, req.ref)
			if changed := check(); changed != nil {
				return result{err: changed}
			}
			return result{evidence: &slice, err: err}
		}
		var id *investigate.IncidentID
		if req.id != "" {
			parsed, err := investigate.ParseIncidentID(req.id)
			if err != nil {
				return result{err: err}
			}
			id = &parsed
		}
		r, err := investigate.Timeline(ctx, path, id, req.before, req.after)
		if changed := check(); changed != nil {
			return result{err: changed}
		}
		return result{report: r, err: err}
	}
}

func openRecording(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open recording %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return nil, errors.New("expected a regular recording of at most 256 MiB")
	}
	return info, nil
}
