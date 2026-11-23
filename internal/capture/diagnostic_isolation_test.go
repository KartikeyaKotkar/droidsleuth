package capture

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

type isolationShell struct {
	snapshots  []string
	index      atomic.Int32
	diagnostic string
}

func (s *isolationShell) Shell(ctx context.Context, command string) (string, error) {
	switch {
	case strings.Contains(command, "__DS_CLOCK__"):
		i := int(s.index.Add(1)) - 1
		if i >= len(s.snapshots) {
			i = len(s.snapshots) - 1
		}
		if s.snapshots[i] == "failure" {
			return "", errors.New("snapshot fixture failure")
		}
		return s.snapshots[i], nil
	case strings.Contains(command, "boot_id"):
		return "11111111-1111-1111-1111-111111111111", nil
	case strings.Contains(command, "timezone"):
		return "UTC", nil
	case strings.Contains(command, "exit-info"):
		if s.diagnostic == "oversized" {
			return "ACTIVITY MANAGER PROCESS EXIT INFO\nApplicationExitInfo #0:\n timestamp=2023-11-14 22:13:20.123 pid=101 realUid=10123\n process=com.example.app reason=4 (" + strings.Repeat("x", 90<<10) + ") status=0\n", nil
		}
		return "Permission Denial", nil
	case strings.Contains(command, "logcat"):
		boundary := ""
		if strings.Contains(command, "droidsleuth crash tail") {
			boundary = "\n--------- droidsleuth crash tail\n"
		}
		if s.diagnostic == "unknown UID" {
			return "1700000000.123456 OEM_UID 101 101 E AndroidRuntime: FATAL EXCEPTION: main\n1700000000.123456 OEM_UID 101 101 E AndroidRuntime: Process: com.example.app, PID: 101" + boundary, nil
		}
		return "logd: truncated fixture" + boundary, nil
	}
	return "", ctx.Err()
}
func TestDiagnosticFailuresLeaveLifecycleIdentical(t *testing.T) {
	present := fixture(t, "snapshot-present.txt")
	replacement := strings.ReplaceAll(present, "1000 0 0", "2000 0 0")
	inputs := []string{present, "failure", replacement, fixture(t, "snapshot-absent.txt"), strings.ReplaceAll(replacement, "101 ", "201 "), strings.ReplaceAll(replacement, "101 ", "201 ")}
	captureTransitions := func(kind string, diagnostics bool) string {
		t.Helper()
		s := &isolationShell{snapshots: inputs, diagnostic: kind}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var transitions []session.Transition
		snapshots, reads := 0, 0
		err := Run(ctx, s, session.Metadata{Package: "com.example.app", UID: 10123}, Options{Path: filepath.Join(t.TempDir(), "session.db"), Interval: time.Millisecond, Evidence: diagnostics, Visit: func(r session.Record) error {
			if r.SourceStatus != nil {
				reads++
			}
			if r.Source == "" {
				snapshots++
				transitions = append(transitions, r.Transitions...)
			}
			if snapshots >= len(inputs) && (!diagnostics || reads >= 2) {
				cancel()
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if snapshots < len(inputs) || diagnostics && reads < 2 {
			t.Fatal("capture did not exercise both diagnostics")
		}
		body, err := json.Marshal(transitions)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	baseline := captureTransitions("", false)
	for _, kind := range []string{"malformed", "unknown UID", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			if got := captureTransitions(kind, true); got != baseline {
				t.Fatalf("diagnostics changed lifecycle: %s versus %s", got, baseline)
			}
		})
	}
}
