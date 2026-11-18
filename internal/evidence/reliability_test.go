package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeUIDInsideMessageCannotOverrideOwner(t *testing.T) {
	raw := logline("root", 99, "DEBUG", "*** *** ***") + "\n" +
		logline("root", 99, "DEBUG", "pid: 42, tid: 42, name: app >>> com.example.app <<<") + "\n" +
		logline("root", 99, "DEBUG", "uid: 110123") + "\n" +
		logline("root", 99, "DEBUG", "signal 11 (SIGSEGV), code 1 (SEGV_MAPERR)") + "\n" +
		logline("root", 99, "DEBUG", "Abort message: 'uid: 10123'")
	out, _ := Logcat(raw, target, "boot")
	if len(out) != 0 {
		t.Fatal("another user's native report became selected evidence through message text")
	}
}
func TestConflictingNativeUIDFieldsAreRejected(t *testing.T) {
	raw := logline("root", 99, "DEBUG", "pid: 42, tid: 42, name: app >>> com.example.app <<<") + "\n" +
		logline("root", 99, "DEBUG", "uid: 110123") + "\n" +
		logline("root", 99, "DEBUG", "uid: 10123") + "\n" +
		logline("root", 99, "DEBUG", "signal 11 (SIGSEGV), code 1 (SEGV_MAPERR)")
	out, bad := Logcat(raw, target, "boot")
	if len(out) != 0 || !bad {
		t.Fatal("contradictory native UID fields accepted")
	}
}
func TestIsolatedUIDCannotSpillIntoAnotherUser(t *testing.T) {
	if uid, ok := UID("u0_i1123"); ok {
		t.Fatalf("invalid isolated UID aliased another user: %d", uid)
	}
}

type embeddedMarkerShell struct {
	calls  atomic.Int32
	marker string
}

func (s *embeddedMarkerShell) Shell(ctx context.Context, command string) (string, error) {
	switch {
	case strings.Contains(command, "boot_id"):
		return "boot", nil
	case strings.Contains(command, "timezone"):
		return "UTC", nil
	case strings.Contains(command, "exit-info"):
		return "ACTIVITY MANAGER PROCESS EXIT INFO", nil
	case strings.Contains(command, "logcat"):
		if s.calls.Add(1) == 1 {
			return s.marker, nil
		}
		return logline("10124", 202, "AndroidRuntime", "embedded marker: "+s.marker), nil
	}
	return "", ctx.Err()
}
func TestOverlapMarkerMustBeWholeRecord(t *testing.T) {
	s := &embeddedMarkerShell{marker: logline("system", 900, "am_anr", "[0,42,com.example.app,0,fixture]")}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ch, stop := Start(ctx, s, target)
	defer stop()
	for {
		select {
		case r := <-ch:
			if strings.Contains(r.Error, "overlap lost") {
				return
			}
		case <-ctx.Done():
			t.Fatal("embedded text falsely proved overlap continuity")
		}
	}
}

func TestNativePhysicalFixtureBoundaries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "logcat-device-native.txt"))
	if err != nil {
		t.Fatal(err)
	}
	out, bad := Logcat(string(raw), target, "boot")
	if bad || len(out) != 1 {
		t.Fatal("known physical shape lost")
	}
}

type oversizedHistoryShell struct{}

func (oversizedHistoryShell) Shell(ctx context.Context, command string) (string, error) {
	if strings.Contains(command, "exit-info") {
		return strings.Replace(history(4), "4 (REPORTED)", "4 ("+strings.Repeat("x", 90<<10)+")", 1), nil
	}
	if strings.Contains(command, "timezone") {
		return "UTC", nil
	}
	if strings.Contains(command, "boot_id") {
		return "boot", nil
	}
	return "", nil
}
func TestOversizedNormalizedEvidenceBecomesGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, stop := Start(ctx, oversizedHistoryShell{}, target)
	defer stop()
	for {
		select {
		case r := <-ch:
			if r.Evidence != nil {
				t.Fatal("oversized normalized event reached writer and would fail capture")
			}
			if strings.Contains(r.Error, "observation budget") {
				return
			}
		case <-ctx.Done():
			t.Fatal("oversized normalized evidence was not reported as gap")
		}
	}
}

func TestUnknownUIDFormatMarksDiagnosticGap(t *testing.T) {
	raw := java("OEM_UNKNOWN_UID", 42, target.Package)
	out, bad := Logcat(raw, target, "boot")
	if len(out) != 0 || !bad {
		t.Fatal("unreadable source UID masqueraded as a successful empty diagnostic read")
	}
}
