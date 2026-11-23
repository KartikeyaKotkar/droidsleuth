package capture

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

func TestPrivateSnapshotCommand(t *testing.T) {
	present := "PID UID NAME\n101 10123 com.example.app\n103 10123 com.example.app:service\n102 10123 com.example.app:remote\n" +
		"104 99001 com.example.app:isolated\n105 110123 com.example.app:remote\n106 10123 global.name\n107 10123 com.example.appish:service\n"
	unique := "#!/bin/sh\n[ \"$*\" = 'package list packages -U --user 0 --uid 10123' ] || exit 1\necho 'package:com.example.app uid:10123'\n"
	workerStat := " (%s) S 1 1 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 1000 0 0\n"
	many := present
	for pid := 200; pid < 215; pid++ {
		many += fmt.Sprintf("%d 10123 com.example.app:other%d\n", pid, pid)
	}
	for _, tc := range []struct {
		name, rows, lookup string
		unreadable         bool
		want               int
	}{
		{"complete", present, unique, false, 3},
		{"shared UID", present, unique + "echo 'package:com.example.peer uid:10123'\n", false, -1},
		{"unsupported UID lookup", present, "#!/bin/sh\nexit 1\n", false, -1},
		{"unreadable worker", present, unique, true, -1},
		{"selected process cap", many, unique, false, -1},
		{"complete absence", "PID UID NAME\n1 0 init\n", unique, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"tools/cmd": tc.lookup}
			for pid, name := range map[int]string{102: "remote", 103: "service"} {
				base := fmt.Sprintf("proc/%d/", pid)
				files[base+"stat"] = fmt.Sprint(pid) + fmt.Sprintf(workerStat, name)
				files[base+"cmdline"] = "com.example.app:" + name + "\x00"
				files[base+"status"] = "Uid:\t10123\t10123\t10123\t10123\n"
			}
			if tc.unreadable {
				delete(files, "proc/102/stat")
			}
			if tc.rows == many {
				for pid := 200; pid < 215; pid++ {
					base := fmt.Sprintf("proc/%d/", pid)
					name := fmt.Sprintf("other%d", pid)
					files[base+"stat"] = fmt.Sprint(pid) + fmt.Sprintf(workerStat, name)
					files[base+"cmdline"] = "com.example.app:" + name + "\x00"
					files[base+"status"] = "Uid:\t10123\t10123\t10123\t10123\n"
				}
			}
			out, runErr := runSnapshotScript(t, scriptCase{rows: tc.rows, files: files}, "private")
			s, parseErr := parseScopedSnapshot(out, "com.example.app", 10123, nil, true)
			if tc.want < 0 {
				if runErr == nil && parseErr == nil {
					t.Fatalf("incomplete or ambiguous rows accepted: %+v", s)
				}
				return
			}
			if runErr != nil || parseErr != nil || len(s.Processes) != tc.want {
				t.Fatalf("run=%v parse=%v observed=%+v; output=%q", runErr, parseErr, s.Processes, out)
			}
			if tc.want == 3 && (s.Processes[1].Name != "com.example.app:remote" || s.Processes[2].Name != "com.example.app:service") {
				t.Fatalf("private processes should have stable name order: %+v", s.Processes)
			}
		})
	}
}

func TestPrivateCaptureReplacesWorkerAcrossGap(t *testing.T) {
	main := fixture(t, "snapshot-present.txt")
	lines := strings.Split(main, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "101 (") {
			main = strings.Replace(main, line, line+"\n"+strings.Replace(line, "101 ", "102 ", 1), 1)
		}
	}
	private := "__DS_OWNER__\npackage:com.example.app uid:10123\n" +
		strings.Replace(main, "com.example.app:worker", "com.example.app:remote", 1)
	replaced := private
	for _, line := range strings.Split(private, "\n") {
		if strings.HasPrefix(line, "102 (") {
			replaced = strings.Replace(private, line, strings.Replace(line, "1000 0 0", "2000 0 0", 1), 1)
		}
	}
	client := fakeClient(t, map[string]string{
		"1.stdout": private,
		"2.stdout": strings.Replace(private, "__DS_CLOCK__", "package:com.example.peer uid:10123\n__DS_CLOCK__", 1),
		"3.exit":   "1",
		"3.stdout": "",
		"4.stdout": replaced,
		"5.stdout": strings.ReplaceAll(replaced, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	meta := session.Metadata{Package: "com.example.app", UID: 10123}
	path := filepath.Join(t.TempDir(), "private.db")
	var rows []session.Record
	err := Run(ctx, client, meta, Options{Path: path, Private: true, Interval: time.Millisecond, Visit: func(r session.Record) error {
		rows = append(rows, r)
		if len(rows) == 5 {
			cancel()
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 || len(rows[0].Transitions) != 2 ||
		rows[1].Kind != "gap" || len(rows[1].Transitions) != 0 || !strings.Contains(rows[1].Error, "exclusive package UID") ||
		rows[2].Kind != "gap" || len(rows[2].Transitions) != 0 {
		t.Fatalf("unexpected private capture: %+v", rows)
	}
	transitions := rows[3].Transitions
	if len(transitions) != 2 || transitions[0].Kind != "no_longer_observed" || transitions[1].Kind != "appeared" ||
		!transitions[0].AfterGap || !transitions[1].AfterGap || transitions[1].Process.PID != 102 ||
		transitions[0].Process.StartTicks != 1000 || transitions[1].Process.StartTicks != 2000 ||
		transitions[0].Process.Name != "com.example.app:remote" || transitions[1].Process.Name != transitions[0].Process.Name {
		t.Fatalf("worker replacement lost or attributed to peer: %+v", transitions)
	}
	if len(rows[4].Transitions) != 2 {
		t.Fatalf("new boot should observe two appearances: %+v", rows[4].Transitions)
	}
	for _, tr := range rows[4].Transitions {
		if tr.Kind != "appeared" || !tr.AfterGap {
			t.Fatalf("new boot inferred an old-boot disappearance: %+v", tr)
		}
	}
	err = session.Replay(context.Background(), path, func(m session.Metadata) error {
		if m.Schema != session.PrivateSchemaVersion {
			t.Fatalf("schema = %d", m.Schema)
		}
		return nil
	}, func(session.Record) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}
