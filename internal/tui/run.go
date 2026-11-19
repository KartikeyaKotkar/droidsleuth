package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"droidsleuth/internal/investigate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// Run opens a finalized recording in an interactive terminal.
func Run(ctx context.Context, input *os.File, output io.Writer, path string, id *investigate.IncidentID, before, after time.Duration) (runErr error) {
	defer func() {
		if runErr != nil {
			runErr = errors.New(escape(runErr.Error()))
		}
	}()
	out, ok := output.(*os.File)
	if !ok || input == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(out.Fd()) {
		return errors.New("--tui requires terminal input and output; use investigate --timeline for text output")
	}
	info, err := openRecording(path)
	if err != nil {
		return err
	}
	state, err := term.GetState(input.Fd())
	if err != nil {
		return fmt.Errorf("save terminal settings: %w", err)
	}
	// Bubble Tea normally restores the TTY. Keep a fallback for setup errors
	// after raw mode has been entered and surface failures to restore settings.
	defer func() {
		if err := term.Restore(input.Fd(), state); err != nil && runErr == nil {
			runErr = fmt.Errorf("restore terminal settings: %w", err)
		}
	}()

	programCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	terminal := &terminalOutput{File: out, cancel: cancel}
	m := &model{ctx: programCtx, load: recordingLoader(path, info), wanted: request{before: before, after: after}, initialBefore: before, initialAfter: after}
	if id != nil {
		m.screen, m.wanted.id, m.wanted.direct = detailScreen, id.String(), true
	}
	m.window = initialWindow(m.wanted)
	p := tea.NewProgram(m, tea.WithContext(programCtx), tea.WithInput(terminalInput{input}), tea.WithOutput(terminal),
		tea.WithAltScreen(), tea.WithoutSignalHandler(), tea.WithoutBracketedPaste())
	_, err = p.Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		// v1.3.7 can return from input setup before its normal teardown.
		_, _ = io.WriteString(out, ansi.ResetAltScreenSaveCursorMode+ansi.ShowCursor)
	}
	m.discard()
	if m.active != nil {
		<-m.active.done
	}
	if outputErr := terminal.err(); outputErr != nil {
		return fmt.Errorf("write terminal frame: %w", outputErr)
	}
	if errors.Is(err, errTerminalEOF) || (ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled)) {
		return nil
	}
	if m.fatal != nil {
		return m.fatal
	}
	return err
}

var errTerminalEOF = errors.New("terminal input closed")

type terminalInput struct{ *os.File }

func (in terminalInput) Read(p []byte) (int, error) {
	n, err := in.File.Read(p)
	if errors.Is(err, io.EOF) {
		// Bubble Tea ignores io.EOF. Report terminal closure so Run unwinds.
		return n, errTerminalEOF
	}
	return n, err
}

type terminalOutput struct {
	*os.File
	cancel   context.CancelFunc
	mu       sync.Mutex
	writeErr error
}

func (out *terminalOutput) Write(p []byte) (int, error) {
	n, err := out.File.Write(p)
	if err != nil {
		out.mu.Lock()
		if out.writeErr == nil {
			out.writeErr = err
		}
		out.mu.Unlock()
		// The Bubble Tea renderer does not propagate write failures.
		out.cancel()
	}
	return n, err
}

func (out *terminalOutput) WriteString(s string) (int, error) { return out.Write([]byte(s)) }

func (out *terminalOutput) err() error {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.writeErr
}

// escape preserves printable Unicode and represents terminal control characters
// visibly. Renderers split structural newlines before calling it.
func escape(s string) string {
	if !strings.ContainsFunc(s, terminalControl) {
		return strings.ToValidUTF8(s, "�")
	}
	var b strings.Builder
	for _, r := range s {
		if terminalControl(r) {
			q := strconv.QuoteRuneToASCII(r)
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func terminalControl(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029'
}
