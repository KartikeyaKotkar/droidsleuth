package tui

import (
	"context"
	"errors"
	"time"

	"droidsleuth/internal/investigate"

	tea "github.com/charmbracelet/bubbletea"
)

type screen uint8

const (
	indexScreen screen = iota
	detailScreen
	observationScreen
	referencesScreen
	evidenceScreen
)

type cursor struct{ selected, top int }

func (c *cursor) move(key string, length, page int) {
	c.selected = move(key, c.selected, length, page)
	c.reflow(page)
}

func (c *cursor) reflow(page int) {
	c.top = min(c.selected, max(c.top, c.selected-page+1))
}

type model struct {
	ctx                                     context.Context
	load                                    func(context.Context, request) result
	active                                  *job
	pending                                 *request
	wanted                                  request
	initialBefore, initialAfter             time.Duration
	window                                  int
	index, detail                           investigate.TimelineReport
	rows                                    []*investigate.TimelineObservation
	selectionSequence                       int64
	refs                                    []investigate.Reference
	evidence                                *investigate.EvidenceSlice
	inspector                               []string
	scroll, observationScroll               int
	width, height                           int
	screen                                  screen
	referencesFrom                          screen
	indexCursor, timelineCursor, refsCursor cursor
	changed                                 bool
	err, fatal                              error
	helpScroll                              int
	help, enteringID                        bool
	idInput                                 string
}

func (m *model) Init() tea.Cmd { return m.start(m.wanted) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		widthChanged := m.width != msg.Width
		m.width, m.height = msg.Width, msg.Height
		if m.usable() {
			m.indexCursor.reflow(m.page())
			m.timelineCursor.reflow(m.page())
			m.refsCursor.reflow(m.page())
			if widthChanged || m.inspector == nil {
				m.reflowInspector()
			}
		}
	case result:
		return m, m.accept(msg)
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *model) accept(msg result) tea.Cmd {
	if m.active != msg.job {
		return nil
	}
	m.active = nil
	if m.pending != nil {
		req := *m.pending
		m.pending = nil
		return m.start(req)
	}
	if msg.job.ignored || errors.Is(msg.err, context.Canceled) {
		return nil
	}
	switch {
	case msg.err != nil:
		m.err = msg.err
		m.changed = errors.Is(msg.err, errChanged)
		if msg.unreadable || (m.screen == indexScreen && m.index.Incidents == nil) {
			m.fatal = msg.err
			return tea.Quit
		}
	case msg.evidence != nil:
		m.evidence = msg.evidence
		m.reflowInspector()
	case msg.report.Incidents != nil:
		m.index = msg.report
	case msg.report.Incident != nil:
		m.setDetail(msg.report)
	}
	return nil
}

func (m *model) key(key tea.KeyMsg) tea.Cmd {
	k := key.String()
	if k == "ctrl+c" || k == "ctrl+d" || k == "q" {
		m.discard()
		return tea.Quit
	}
	if !m.usable() || m.changed {
		return nil
	}
	if m.help {
		if k == "esc" || k == "?" {
			m.help = false
		} else {
			m.helpScroll = move(k, m.helpScroll, len(helpLines), m.page())
		}
		return nil
	}
	if m.enteringID {
		if key.Type == tea.KeyRunes {
			k = string(key.Runes)
		}
		return m.editID(k)
	}
	if k == "?" {
		m.help, m.helpScroll = true, 0
		return nil
	}
	if k == "esc" {
		return m.back()
	}
	switch m.screen {
	case indexScreen:
		if m.index.Incidents == nil {
			return nil
		}
		entries := *m.index.Incidents
		switch k {
		case "g":
			m.enteringID, m.idInput, m.err = true, "", nil
		case "enter":
			if len(entries) > 0 {
				return m.open(entries[m.indexCursor.selected].ID)
			}
		default:
			m.indexCursor.move(k, len(entries), m.page())
		}
	case detailScreen:
		return m.detailKey(k)
	case observationScreen:
		if k == "e" {
			m.openReferences()
		} else {
			m.scroll = move(k, m.scroll, len(m.inspector), m.page())
		}
	case referencesScreen:
		if k == "enter" {
			m.screen, m.scroll = evidenceScreen, 0
			m.inspector = nil
			return m.request(request{ref: m.refs[m.refsCursor.selected]})
		}
		m.refsCursor.move(k, len(m.refs), m.page())
	case evidenceScreen:
		if k == "r" && m.err != nil {
			return m.request(request{ref: m.refs[m.refsCursor.selected]})
		}
		m.scroll = move(k, m.scroll, len(m.inspector), m.page())
	}
	return nil
}

func (m *model) detailKey(key string) tea.Cmd {
	switch key {
	case "r":
		return m.reloadDetail()
	case "w":
		windows := [...]time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute}
		m.window = (m.window + 1) % len(windows)
		m.wanted.before, m.wanted.after = windows[m.window], windows[m.window]
		return m.reloadDetail()
	}
	if len(m.rows) == 0 {
		return nil
	}
	switch key {
	case "enter":
		m.screen, m.scroll = observationScreen, 0
		m.reflowInspector()
	case "e":
		m.openReferences()
	default:
		m.timelineCursor.move(key, len(m.rows), m.page())
	}
	return nil
}

func (m *model) editID(key string) tea.Cmd {
	switch key {
	case "esc":
		m.enteringID, m.err = false, nil
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
		// Bubble Tea can deliver several typed or pasted runes in one message.
		for _, r := range key {
			if r != 'L' && r != 'E' && r != '.' && (r < '0' || r > '9') {
				return nil
			}
		}
		if len(m.idInput)+len(key) <= 42 {
			m.idInput += key
		}
	}
	return nil
}

func (m *model) back() tea.Cmd {
	if m.screen == indexScreen {
		return nil
	}
	m.discard()
	m.err = nil
	switch m.screen {
	case detailScreen:
		m.detail, m.rows = investigate.TimelineReport{}, nil
		m.screen = indexScreen
		if m.index.Incidents == nil {
			return m.request(request{})
		}
	case observationScreen:
		m.screen, m.inspector = detailScreen, nil
	case referencesScreen:
		m.screen, m.refs = m.referencesFrom, nil
		if m.screen == observationScreen {
			m.scroll = m.observationScroll
			m.reflowInspector()
		}
	case evidenceScreen:
		m.screen, m.evidence, m.inspector = referencesScreen, nil, nil
	}
	return nil
}

func (m *model) open(id string) tea.Cmd {
	m.screen, m.selectionSequence, m.timelineCursor = detailScreen, 0, cursor{}
	m.wanted = request{id: id, before: m.initialBefore, after: m.initialAfter}
	m.window = initialWindow(m.wanted)
	return m.request(m.wanted)
}

func initialWindow(req request) int {
	if req.before == investigate.DefaultContext && req.after == investigate.DefaultContext {
		return 0
	}
	// Custom initial bounds are followed by the first symmetric preset.
	return -1
}

func (m *model) reloadDetail() tea.Cmd {
	if row := m.selectedRow(); row != nil {
		m.selectionSequence = row.Sequence
	}
	m.detail, m.rows = investigate.TimelineReport{}, nil
	return m.request(m.wanted)
}

func (m *model) setDetail(report investigate.TimelineReport) {
	m.detail = report
	d := report.Incident
	m.rows = make([]*investigate.TimelineObservation, 0, len(d.Context)+1)
	anchor := len(d.Context)
	for i := range d.Context {
		if anchor == len(d.Context) && d.Context[i].Sequence > d.Anchor.Sequence {
			anchor = i
			m.rows = append(m.rows, &d.AnchorObservation)
		}
		m.rows = append(m.rows, &d.Context[i])
	}
	if anchor == len(d.Context) {
		m.rows = append(m.rows, &d.AnchorObservation)
	}
	m.timelineCursor = cursor{selected: anchor}
	for i, row := range m.rows {
		if m.selectionSequence > 0 && row.Sequence == m.selectionSequence {
			m.timelineCursor.selected = i
			break
		}
	}
	m.timelineCursor.reflow(m.page())
}

func (m *model) selectedRow() *investigate.TimelineObservation {
	if len(m.rows) == 0 {
		return nil
	}
	return m.rows[m.timelineCursor.selected]
}

func (m *model) openReferences() {
	row := m.selectedRow()
	if row == nil {
		return
	}
	m.refs = observationReferences(m.detail.Incident, row)
	if len(m.refs) == 0 {
		m.err = errors.New("no retained evidence references for this observation")
		return
	}
	m.observationScroll = m.scroll
	m.referencesFrom, m.screen, m.refsCursor = m.screen, referencesScreen, cursor{}
	m.err, m.inspector = nil, nil
}

func (m *model) usable() bool { return m.width >= 40 && m.height >= 10 }
func (m *model) columns() int { return min(m.width, 512) }
func (m *model) page() int    { return max(1, min(m.height, 256)-6) }

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
