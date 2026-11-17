package evidence

import (
	"fmt"
	"strings"
	"testing"

	"droidsleuth/internal/session"
)

var target = session.Metadata{Package: "com.example.app", UID: 10123, User: 0}

func logline(uid string, pid int, tag, msg string) string {
	return fmt.Sprintf("1700000000.123456 %s %d %d E %s: %s", uid, pid, pid, tag, msg)
}
func java(uid string, pid int, name string) string {
	return logline(uid, pid, "AndroidRuntime", "FATAL EXCEPTION: main") + "\n" + logline(uid, pid, "AndroidRuntime", fmt.Sprintf("Process: %s, PID: %d", name, pid)) + "\n" + logline(uid, pid, "AndroidRuntime", "java.lang.IllegalStateException: fixture")
}
func TestLogcatScopeAndReuse(t *testing.T) {
	cases := []struct {
		name, text string
		want       int
	}{
		{"java", java("u0_a123", 42, target.Package), 1},
		{"wrong UID", java("10124", 42, target.Package), 0},
		{"other user", java("110123", 42, target.Package), 0},
		{"prefix collision", java("10123", 42, target.Package+".other"), 0},
		{"PID alone", logline("10123", 42, "AndroidRuntime", "FATAL EXCEPTION: main"), 0},
		{"PID reuse", java("10123", 42, target.Package) + "\n" + java("10123", 42, "com.other.app"), 1},
		{"ANR", logline("system", 900, "am_anr", "[0,42,com.example.app,0,Input dispatch timed out, detail]"), 1},
		{"ANR other user", logline("system", 900, "am_anr", "[10,42,com.example.app,0,timeout]"), 0},
		{"ANR untrusted emitter", logline("10123", 900, "am_anr", "[0,42,com.example.app,0,timeout]"), 0},
		{"native", logline("root", 99, "DEBUG", "*** *** ***") + "\n" + logline("root", 99, "DEBUG", "pid: 42, tid: 42, name: app >>> com.example.app <<<") + "\n" + logline("root", 99, "DEBUG", "uid: 10123"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, bad := Logcat(tc.text, target, "boot")
			if bad || len(got) != tc.want {
				t.Fatalf("events=%d malformed=%t", len(got), bad)
			}
			for _, e := range got {
				if strings.Contains(e.Text, "com.other.app") {
					t.Fatal("unrelated process persisted")
				}
				if e.Events[0].DeviceTimeNS != 1700000000123456000 {
					t.Fatal("timestamp precision lost")
				}
			}
		})
	}
}
func TestMalformedAndOversizedLogcat(t *testing.T) {
	_, bad := Logcat("logd: dropped records\n"+java("10123", 42, target.Package)+"\n"+logline("10123", 42, "AndroidRuntime", strings.Repeat("x", TextLimit)), target, "boot")
	if !bad {
		t.Fatal("loss/truncation silently accepted")
	}
}
func history(reason int) string {
	return fmt.Sprintf("ACTIVITY MANAGER PROCESS EXIT INFO (dumpsys activity exit-info)\n ApplicationExitInfo #0:\n timestamp=2023-11-14 22:13:20.123 pid=42 realUid=10123 packageUid=10123 definingUid=10123\n process=com.example.app reason=%d (REPORTED) subreason=0 status=9\n description=fixture\n", reason)
}
func TestExitHistoryTimeReasonsAndMissingSupport(t *testing.T) {
	for _, reason := range []int{2, 3, 4, 5, 6, 10} {
		out, err := ExitHistory(history(reason), target, "UTC")
		if err != nil || len(out) != 1 {
			t.Fatalf("%v %v", out, err)
		}
		e := out[0].Events[0]
		if *e.Reason != reason || e.DeviceTimeNS != 1700000000123000000 || e.BootID != "" {
			t.Fatalf("bad event %+v", e)
		}
	}
	for _, text := range []string{"Permission Denial", "Unknown command", strings.ReplaceAll(history(4), "pid=42", "pid=no")} {
		if _, err := ExitHistory(text, target, "UTC"); err == nil {
			t.Fatal("unsupported/malformed history became empty success")
		}
	}
	if out, err := ExitHistory("ACTIVITY MANAGER PROCESS EXIT INFO\nLast Timestamp of Persistence Into Persistent Storage: now", target, "UTC"); err != nil || len(out) != 0 {
		t.Fatal("supported empty ring failed")
	}
	out, err := ExitHistory(strings.ReplaceAll(history(4), "realUid=10123", "realUid=10124"), target, "UTC")
	if err != nil || len(out) != 0 {
		t.Fatal("wrong UID stored")
	}
	out, err = ExitHistory(history(4), target, "missing/timezone")
	if err != nil || out[0].Events[0].DeviceTimeNS != 0 {
		t.Fatal("unknown timezone invented a timestamp")
	}
}
