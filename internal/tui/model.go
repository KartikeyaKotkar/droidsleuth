package tui

import (
	"context"
	"errors"

	"droidsleuth/internal/investigate"

	tea "github.com/charmbracelet/bubbletea"
)

type screen uint8

const (
	indexScreen screen = iota
	detailScreen
)

type model struct {
	ctx                      context.Context
	load                     func(context.Context, request) result
	active                   *job
	pending                  *request
	wanted                   request
	index, detail            investigate.TimelineReport
	width, height            int
	screen                   screen
	indexSelection, indexTop int
	err, fatal               error
	help, enteringID         bool
	idInput                  string
}

func (m *model) Init() tea.Cmd { return m.start(m.wanted) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case result:
		if m.active != msg.job {
			return m, nil
		}
		m.active = nil
		if m.pending != nil {
			req := *m.pending
			m.pending = nil
			return m, m.start(req)
		}
		if msg.job.ignored || errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			if m.index.Incidents == nil && m.screen == indexScreen {
				m.fatal = msg.err
				return m, tea.Quit
			}
		} else if msg.report.Incidents != nil {
			m.index = msg.report
		} else {
			m.detail = msg.report
		}
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *model) key(key tea.KeyMsg) tea.Cmd {
	k := key.String()
	if k == "ctrl+c" || k == "ctrl+d" || (k == "q" && !m.enteringID) {
		m.discard()
		return tea.Quit
	}
	if m.width < 40 || m.height < 10 {
		return nil
	}
	if m.help {
		if k == "esc" || k == "?" {
			m.help = false
		}
		return nil
	}
	if m.enteringID {
		switch k {
		case "esc":
			m.enteringID = false
		case "backspace":
			if n := len(m.idInput); n > 0 {
				m.idInput = m.idInput[:n-1]
			}
		case "enter":
			if _, err := investigate.ParseIncidentID(m.idInput); err != nil {
				m.err = err
				return nil
			}
			m.enteringID = false
			return m.open(m.idInput)
		default:
			if len(k) == 1 && len(m.idInput) < 42 && (k == "L" || k == "E" || k == "." || k[0] >= '0' && k[0] <= '9') {
				m.idInput += k
			}
		}
		return nil
	}
	if k == "?" {
		m.help = true
		return nil
	}
	switch m.screen {
	case indexScreen:
		if k == "g" && m.index.Incidents != nil {
			m.enteringID, m.idInput, m.err = true, "", nil
		}
		if m.index.Incidents == nil || len(*m.index.Incidents) == 0 {
			return nil
		}
		if k == "enter" {
			return m.open((*m.index.Incidents)[m.indexSelection].ID)
		}
		m.indexSelection = move(k, m.indexSelection, len(*m.index.Incidents), m.page())
	case detailScreen:
		switch k {
		case "esc":
			m.discard()
			m.detail = investigate.TimelineReport{}
			m.screen, m.err = indexScreen, nil
			if m.index.Incidents == nil {
				return m.request(request{})
			}
		case "r":
			return m.request(m.wanted)
		}
	}
	return nil
}

func (m *model) open(id string) tea.Cmd {
	m.screen = detailScreen
	m.wanted.id = id
	return m.request(m.wanted)
}

func (m *model) page() int { return max(1, min(m.height, 256)-5) }

func move(key string, selected, length, page int) int {
	switch key {
	case "up", "k":
		selected--
	case "down", "j":
		selected++
	case "pgup":
		selected -= page
	case "pgdown":
		selected += page
	}
	return min(max(0, length-1), max(0, selected))
}

func keepVisible(top, selected, page int) int {
	return min(selected, max(top, selected-page+1))
}
