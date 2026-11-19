package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"droidsleuth/internal/investigate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

// Run opens a finalized recording in an interactive terminal.
func Run(ctx context.Context, input *os.File, output io.Writer, path string, id *investigate.IncidentID, before, after time.Duration) error {
	out, ok := output.(*os.File)
	if !ok || !term.IsTerminal(input.Fd()) || !term.IsTerminal(out.Fd()) {
		return errors.New("--tui requires terminal input and output; use investigate --timeline for text output")
	}
	info, err := openRecording(path)
	if err != nil {
		return err
	}
	m := &model{ctx: ctx, load: recordingLoader(path, info), wanted: request{before: before, after: after}, initialBefore: before, initialAfter: after}
	if id != nil {
		m.screen, m.wanted.id = detailScreen, id.String()
	}
	m.window = initialWindow(m.wanted)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(out), tea.WithAltScreen(), tea.WithoutSignalHandler())
	_, err = p.Run()
	m.discard()
	if m.active != nil {
		<-m.active.done
	}
	if ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	if m.fatal != nil {
		return errors.New(escape(m.fatal.Error()))
	}
	return err
}

// escape preserves printable Unicode and represents terminal control characters
// visibly. Renderers split structural newlines before calling it.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029' {
			q := strconv.QuoteRuneToASCII(r)
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
