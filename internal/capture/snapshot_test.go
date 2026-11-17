package capture

import (
	"os"
	"strings"
	"testing"

	"droidsleuth/internal/session"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotEvidenceAndIncompleteData(t *testing.T) {
	present := fixture(t, "snapshot-present.txt")
	s, err := ParseSnapshot(present, "com.example.app", 10123)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Processes) != 1 || s.Processes[0].PID != 101 || s.Processes[0].StartTicks != 1000 {
		t.Fatalf("bad target identity: %+v", s)
	}
	absent, err := ParseSnapshot(fixture(t, "snapshot-absent.txt"), "com.example.app", 10123)
	if err != nil || len(absent.Processes) != 0 {
		t.Fatalf("absence: %+v, %v", absent, err)
	}
	cases := map[string]string{
		"truncated":      strings.ReplaceAll(present, "__DS_END__", ""),
		"missing stat":   present[:strings.Index(present, "101 (main")] + "__DS_END__\n",
		"wrong UID":      strings.ReplaceAll(present, "101 10123", "101 10125"),
		"malformed ps":   strings.ReplaceAll(present, "103 10124", "103 nope"),
		"wrong columns":  strings.ReplaceAll(present, "PID UID NAME", "PID USER NAME"),
		"duplicate PID":  strings.ReplaceAll(present, "103 10124 com.example.other", "101 10124 com.example.other"),
		"wrong stat PID": strings.ReplaceAll(present, "101 (main", "999 (main"),
		"invalid ticks":  strings.ReplaceAll(present, "1000 0 0", "nope 0 0"),
		"boot denied":    strings.ReplaceAll(present, s.BootID, "Permission denied"),
		"NaN clock":      strings.ReplaceAll(present, "1059884.34", "NaN"),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSnapshot(text, "com.example.app", 10123); err == nil {
				t.Fatal("incomplete evidence accepted")
			}
		})
	}
}

func TestPackageValidation(t *testing.T) {
	for _, pkg := range []string{"com.example.app;id", "com.x$(id)", "com.x\n", "../app", "-p", ""} {
		if _, err := SnapshotCommand(pkg, 10123); err == nil {
			t.Fatalf("accepted %q", pkg)
		}
	}
}

func TestIdentityTracking(t *testing.T) {
	base := session.Process{Identity: session.Identity{BootID: "boot-a", UID: 10123, PID: 101, StartTicks: 1000}, Name: "com.example.app"}
	for _, field := range []string{"pid", "start_ticks", "uid", "boot"} {
		t.Run(field, func(t *testing.T) {
			tracker := Tracker{}
			tracker.Observe(session.Snapshot{BootID: base.BootID, Processes: []session.Process{base}})
			next := base
			switch field {
			case "pid":
				next.PID++
			case "start_ticks":
				next.StartTicks++ // Same PID reused.
			case "uid":
				next.UID++
			case "boot":
				next.BootID = "boot-b"
			}
			events := tracker.Observe(session.Snapshot{BootID: next.BootID, Processes: []session.Process{next}})
			if len(events) == 0 || events[len(events)-1].Kind != "appeared" {
				t.Fatalf("replacement was not observed: %+v", events)
			}
			if field == "boot" && len(events) != 1 {
				t.Fatal("boot change invented old-boot absence")
			}
			if field != "boot" && (len(events) != 2 || events[0].Kind != "no_longer_observed") {
				t.Fatalf("old identity persisted: %+v", events)
			}
		})
	}
	t.Run("gaps do not establish absence", func(t *testing.T) {
		tracker := Tracker{}
		s := session.Snapshot{BootID: base.BootID, Processes: []session.Process{base}}
		tracker.Observe(s)
		tracker.Gap()
		if events := tracker.Observe(s); len(events) != 0 {
			t.Fatalf("gap invented transitions: %+v", events)
		}
		tracker.Gap()
		events := tracker.Observe(session.Snapshot{BootID: base.BootID})
		if len(events) != 1 || events[0].Kind != "no_longer_observed" || !events[0].AfterGap {
			t.Fatalf("absence evidence lost: %+v", events)
		}
	})
	t.Run("replacement after gap preserves uncertainty", func(t *testing.T) {
		tracker := Tracker{}
		tracker.Observe(session.Snapshot{BootID: base.BootID, Processes: []session.Process{base}})
		tracker.Gap()
		next := base
		next.PID++
		next.StartTicks++
		events := tracker.Observe(session.Snapshot{BootID: next.BootID, Processes: []session.Process{next}})
		if len(events) != 2 || events[0].Kind != "no_longer_observed" || events[1].Kind != "appeared" {
			t.Fatalf("replacement evidence changed: %+v", events)
		}
		for _, event := range events {
			if !event.AfterGap {
				t.Fatalf("post-gap transition lost uncertainty: %+v", event)
			}
		}
		if events[0].Process.Identity != base.Identity || events[1].Process.Identity != next.Identity {
			t.Fatal("replacement joined different process instances")
		}
	})
}

func FuzzParseSnapshot(f *testing.F) {
	f.Add("__DS_CLOCK__\n")
	f.Fuzz(func(t *testing.T, out string) { _, _ = ParseSnapshot(out, "com.example.app", 10123) })
}
