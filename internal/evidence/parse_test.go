package evidence

import (
	"fmt"
	"os"
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
		{"native", logline("root", 99, "DEBUG", "*** *** ***") + "\n" + logline("root", 99, "DEBUG", "pid: 42, ppid: 123, tid: 42, name: app >>> com.example.app <<<") + "\n" + logline("root", 99, "DEBUG", "uid: 10123") + "\n" + logline("root", 99, "DEBUG", "signal 11 (SIGSEGV), code -1 (SI_QUEUE), fault addr --------"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, bad := Logcat(tc.text, target, "boot")
			if bad != (tc.name == "PID alone") || len(got) != tc.want {
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
func TestNonstandardCrashLayoutIsGap(t *testing.T) {
	out, bad := Logcat(logline("system", 900, "am_crash", "[42,0,com.example.app,0,Native crash,Segmentation fault]"), target, "boot")
	if !bad || len(out) != 0 {
		t.Fatal("nonstandard event layout guessed as a process instance")
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
	for _, text := range []string{"Permission Denial", "Unknown command", strings.ReplaceAll(history(4), "process=com.example.app", "unknownName=com.example.app"), strings.ReplaceAll(history(4), "pid=42", "pid=no")} {
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
	for _, zone := range []string{"missing/timezone", "", "Local"} {
		out, err = ExitHistory(history(4), target, zone)
		if err != nil || len(out) != 1 || out[0].Events[0].DeviceTimeNS != 0 || out[0].Events[0].RawTime == "" {
			t.Fatalf("unknown device timezone %q changed occurrence evidence: %+v, %v", zone, out, err)
		}
	}
}

func TestNativeMetadataWithoutSignalIsNotCrash(t *testing.T) {
	raw := logline("root", 99, "DEBUG", "pid: 42, tid: 42, name: app >>> com.example.app <<<") + "\n" + logline("root", 99, "DEBUG", "uid: 10123")
	out, bad := Logcat(raw, target, "boot")
	if bad || len(out) != 0 {
		t.Fatal("nonfatal process dump inferred as crash")
	}
}
func TestAmbiguousExitHistoryWallTime(t *testing.T) {
	raw := strings.Replace(history(4), "2023-11-14 22:13:20.123", "2023-11-05 01:30:00.123", 1)
	out, err := ExitHistory(raw, target, "America/New_York")
	if err != nil || len(out) != 1 || out[0].Events[0].DeviceTimeNS != 0 {
		t.Fatal("ambiguous DST wall time became exact instant")
	}
}

func TestExitHistoryDeviceZoneIsIndependentOfHostZone(t *testing.T) {
	const original = "2023-11-14 22:13:20.123"
	const springGap = "2023-03-12 02:30:00.123"
	for _, hostZone := range []string{"UTC", "Pacific/Honolulu"} {
		t.Setenv("TZ", hostZone)
		for _, tc := range []struct {
			stamp string
			want  int64
		}{
			{original, 1700018000123000000}, // 03:13:20.123 UTC the next day
			{springGap, 0},
		} {
			raw := strings.Replace(history(4), original, tc.stamp, 1)
			out, err := ExitHistory(raw, target, "America/New_York")
			if err != nil || len(out) != 1 {
				t.Fatalf("expected retained report, got %+v, %v", out, err)
			}
			if event := out[0].Events[0]; event.RawTime != tc.stamp || event.DeviceTimeNS != tc.want {
				t.Fatalf("host TZ %s: expected occurrence %d, got %+v", hostZone, tc.want, event)
			}
		}
	}
}

func TestExitHistoryRejectsUnrepresentableOccurrenceTimes(t *testing.T) {
	for _, stamp := range []string{
		"1677-09-21 00:12:43.145",
		"2262-04-11 23:47:16.855",
		"2500-01-01 00:00:00.000",
		"2023-02-29 00:00:00.000",
	} {
		t.Run(stamp, func(t *testing.T) {
			raw := strings.Replace(history(4), "2023-11-14 22:13:20.123", stamp, 1)
			out, err := ExitHistory(raw, target, "UTC")
			if err != nil || len(out) != 1 {
				t.Fatalf("expected retained report, got %+v, %v", out, err)
			}
			event := out[0].Events[0]
			if event.RawTime != stamp || event.DeviceTimeNS != 0 {
				t.Fatalf("expected unknown occurrence time, got %+v", event)
			}
		})
	}
}

func TestExitHistoryDoesNotAcceptUnrecognizedBodyAsEmpty(t *testing.T) {
	const heading = "ACTIVITY MANAGER PROCESS EXIT INFO (dumpsys activity exit-info)\n"
	for _, body := range []string{
		" ApplicationExitInfo #?:\n timestamp=2023-11-14 22:13:20.123\n",
		" Exit details are unavailable\n",
	} {
		if _, err := ExitHistory(heading+body, target, "UTC"); err == nil {
			t.Fatalf("unrecognized history appeared empty: %q", body)
		}
	}
}

func TestExitHistoryDoesNotIncludeUnrecognizedEntryInExcerpt(t *testing.T) {
	raw := history(4) + " ApplicationExitInfo #?:\n process=com.private.other privacy-sentinel\n"
	reports, err := ExitHistory(raw, target, "UTC")
	if err == nil {
		t.Fatal("malformed history appeared complete")
	}
	for _, report := range reports {
		if strings.Contains(report.Text, "privacy-sentinel") {
			t.Fatal("unrecognized entry was appended to selected evidence")
		}
	}
}

func TestExitHistoryExcerptStopsAtNextPackageSection(t *testing.T) {
	raw := history(4) + "  package: com.private.other\n    Historical Process Exit for uid=10123\n"
	raw += strings.Replace(strings.SplitN(history(4), "\n", 2)[1], "process=com.example.app", "process=com.private.other", 1)
	out, err := ExitHistory(raw, target, "UTC")
	if err != nil || len(out) != 1 {
		t.Fatalf("expected only the selected report, got %+v, %v", out, err)
	}
	if strings.Contains(out[0].Text, "com.private.other") {
		t.Fatalf("excerpt included a different package: %q", out[0].Text)
	}
}

func TestContradictoryProcessBlockIsNotPersisted(t *testing.T) {
	raw := java("10123", 42, target.Package) + "\n" + logline("10123", 42, "AndroidRuntime", "Process: com.private.other, PID: 42") + "\n" + logline("10123", 42, "AndroidRuntime", "Process: com.example.app, PID: 42")
	out, bad := Logcat(raw, target, "boot")
	if !bad || len(out) != 0 {
		t.Fatal("contradictory block leaked unrelated process text")
	}
}

func TestSanitizedPhysicalNativeFixture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/logcat-device-native.txt")
	if err != nil {
		t.Fatal(err)
	}
	out, bad := Logcat(string(raw), target, "boot")
	if bad || len(out) != 1 || out[0].Events[0].Kind != "native_crash" {
		t.Fatal("physical native-crash shape not recognized")
	}
}

func TestNumericOverflowIsMalformed(t *testing.T) {
	for _, raw := range []string{"9999999999999999999999999", "u999999999999999999_a123", "u21474_a99999"} {
		if _, ok := UID(raw); ok {
			t.Fatalf("overflow UID accepted: %s", raw)
		}
	}
	_, bad := Logcat("1700000000.123456 10123 9999999999999999999999 42 E AndroidRuntime: FATAL EXCEPTION: main", target, "boot")
	if !bad {
		t.Fatal("overflow PID accepted")
	}
}
