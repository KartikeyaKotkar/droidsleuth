package evidence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
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
			return s.marker + crashTailBoundary, nil
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

func TestEmulatorProvenanceFixtures(t *testing.T) {
	for _, api := range []string{"30", "33", "35", "36", "37"} {
		kinds := []string{"java_fatal", "native_crash", "anr_report"}
		if api == "30" || api == "37" {
			kinds = append(kinds, "am_crash-rejected")
		}
		for _, kind := range kinds {
			t.Run("API"+api+"/"+kind, func(t *testing.T) {
				raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "compatibility", "api"+api+"-"+kind+".txt"))
				if err != nil {
					t.Fatal(err)
				}
				out, bad := Logcat(string(raw), target, "boot")
				if kind == "am_crash-rejected" {
					if len(out) != 0 || !bad {
						t.Fatal("unsupported layout was guessed")
					}
					return
				}
				if bad || len(out) != 1 || out[0].Events[0].Kind != kind {
					t.Fatalf("source shape changed: events=%d gap=%t", len(out), bad)
				}
			})
		}
	}
}

func TestMalformedNativeUIDCannotBeRepairedByLaterField(t *testing.T) {
	for _, field := range []string{"uid: invalid", "uid: 999999999999999999999"} {
		t.Run(field, func(t *testing.T) {
			raw := logline("root", 99, "DEBUG", "pid: 42, tid: 42, name: app >>> com.example.app <<<") + "\n" +
				logline("root", 99, "DEBUG", field) + "\n" + logline("root", 99, "DEBUG", "uid: 10123") + "\n" +
				logline("root", 99, "DEBUG", "signal 11 (SIGSEGV), code 1 (SEGV_MAPERR)")
			out, bad := Logcat(raw, target, "boot")
			if len(out) != 0 || !bad {
				t.Fatal("later field hid malformed UID evidence")
			}
		})
	}
}

func TestNativeMarkersInsideMessagesAreNotHeaders(t *testing.T) {
	cases := []struct{ name, process, signal string }{
		{"signal text", "pid: 42, tid: 42, name: app >>> com.example.app <<<", "Abort message: 'signal 11 (SIGSEGV)'"},
		{"process text", "Abort message: 'pid: 42, tid: 42, name: app >>> com.example.app <<<'", "signal 11 (SIGSEGV), code 1 (SEGV_MAPERR)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := logline("root", 99, "DEBUG", tc.process) + "\n" + logline("root", 99, "DEBUG", "uid: 10123") + "\n" + logline("root", 99, "DEBUG", tc.signal)
			out, _ := Logcat(raw, target, "boot")
			if len(out) != 0 {
				t.Fatal("quoted diagnostic text fabricated native headers")
			}
		})
	}
}
func TestInvalidUTF8SourceIsNotSilentlyRewritten(t *testing.T) {
	raw := java("10123", 42, target.Package) + "\n" + logline("10123", 42, "AndroidRuntime", "invalid byte: \xff")
	out, bad := Logcat(raw, target, "boot")
	if !bad {
		t.Fatal("malformed source encoding was not reported")
	}
	for _, o := range out {
		if !utf8.ValidString(o.Text) {
			t.Fatal("JSON persistence would silently replace source bytes")
		}
	}
	if _, err := ExitHistory(history(4)+"\xff", target, "UTC"); err == nil {
		t.Fatal("exit source encoding silently rewritten")
	}
}

func TestLogcatTimestampPrecisionRemainsExplicit(t *testing.T) {
	for _, tc := range []struct {
		fraction  string
		precision int64
	}{{"1", 100000000}, {"123", 1000000}, {"123456", 1000}, {"123456789", 1}} {
		t.Run(tc.fraction, func(t *testing.T) {
			raw := strings.ReplaceAll(java("10123", 42, target.Package), ".123456", "."+tc.fraction)
			out, bad := Logcat(raw, target, "boot")
			if bad || len(out) != 1 {
				t.Fatal("valid source timestamp rejected")
			}
			if out[0].Events[0].TimeUncertaintyNS != tc.precision {
				t.Fatal("source precision overstated")
			}
		})
	}
}

func TestAffectedPIDMustFitAndroidInteger(t *testing.T) {
	raw := logline("root", 99, "DEBUG", "pid: 2147483648, tid: 42, name: app >>> com.example.app <<<") + "\n" + logline("root", 99, "DEBUG", "uid: 10123") + "\n" + logline("root", 99, "DEBUG", "signal 11 (SIGSEGV), code 0 (SI_USER)")
	if out, bad := Logcat(raw, target, "boot"); len(out) != 0 || !bad {
		t.Fatal("oversized native affected PID accepted")
	}
	raw = logline("system", 900, "am_anr", "[0,2147483648,com.example.app,0,fixture]")
	if out, bad := Logcat(raw, target, "boot"); len(out) != 0 || !bad {
		t.Fatal("oversized event affected PID accepted")
	}
	if _, err := ExitHistory(strings.Replace(history(4), "pid=42", "pid=2147483648", 1), target, "UTC"); err == nil {
		t.Fatal("oversized exit PID accepted")
	}
}

func TestInterleavedFatalThreadsKeepSeparateEvidence(t *testing.T) {
	row := func(tid int, body string) string {
		return fmt.Sprintf("1700000000.%06d 10123 42 %d E AndroidRuntime: %s", tid, tid, body)
	}
	raw := strings.Join([]string{row(42, "FATAL EXCEPTION: thread-A"), row(43, "FATAL EXCEPTION: thread-B"), row(42, "Process: com.example.app, PID: 42"), row(43, "Process: com.example.app, PID: 42"), row(42, "java.lang.IllegalStateException: stack-A"), row(43, "java.lang.IllegalStateException: stack-B")}, "\n")
	out, bad := Logcat(raw, target, "boot")
	if bad || len(out) != 2 {
		t.Fatal("interleaved fatal threads lost a report")
	}
	if strings.Contains(out[0].Text, "stack-B") || strings.Contains(out[1].Text, "stack-A") {
		t.Fatal("unrelated thread stacks merged")
	}
}

func TestTruncatedEventVectorIsNotAnANRReport(t *testing.T) {
	for _, body := range []string{"[0,42,com.example.app,0,timeout", "0,42,com.example.app,0,timeout]"} {
		out, bad := Logcat(logline("system", 900, "am_anr", body), target, "boot")
		if len(out) != 0 || !bad {
			t.Fatal("truncated event vector accepted as complete ANR evidence")
		}
	}
}
