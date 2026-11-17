package capture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the generated script against a controlled proc tree, rather than
// stubbing the complete ADB response. This exercises shell race/error handling.
type scriptCase struct {
	name, rows, secondStat string
	status, wantProcesses  int
	wantGap                bool
	files                  map[string]string
}

func runSnapshotScript(t *testing.T, tc scriptCase, mode string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	proc := filepath.Join(dir, "proc")
	tools := filepath.Join(dir, "tools")
	if err := os.MkdirAll(tools, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cat", "tr", "head", "sed"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("shell fixture needs %s", name)
		}
		if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	stat := "101 (main (worker)) S 1 1 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 1000 0 0\n"
	files := map[string]string{
		"proc/uptime":                    "1059884.34 2649004.01\n",
		"proc/sys/kernel/random/boot_id": "11111111-1111-1111-1111-111111111111\n",
		"proc/101/stat":                  stat,
		"proc/101/cmdline":               "com.example.app\x00",
		"proc/101/status":                "Uid:\t10123\t10123\t10123\t10123\n",
		"ps.txt":                         tc.rows,
		"second-stat.txt":                tc.secondStat,
		"tools/ps":                       "#!/bin/sh\n\"$DS_REAL_CAT\" \"$DS_PS_FILE\"\nexit \"$DS_PS_STATUS\"\n",
		"tools/date":                     "#!/bin/sh\nprintf '1794836454\\n'\n",
	}
	for name, body := range tc.files {
		files[name] = body
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	command, err := SnapshotCommand("com.example.app", 10123)
	if err != nil {
		t.Fatal(err)
	}
	if tc.secondStat != "" {
		// Change the controlled file immediately before the second stat read;
		// bulk shell reads do not pass through the fake cat executable.
		boundary := "    ds_read \"/proc/$ds_pid/stat\" || exit 1\n    ds_after=$ds_value"
		if !strings.Contains(command, boundary) {
			t.Fatal("second stat read boundary changed")
		}
		command = strings.Replace(command, boundary, "    \"$DS_REAL_CAT\" \"$DS_SECOND_STAT\" > \"$DS_PROC_ROOT/101/stat\"\n"+boundary, 1)
	}
	if mode == "native" {
		// Supply a raw-print builtin equivalent; verify the optimized branch uses
		// -r and --, rather than interpreting process text as flags or escapes.
		command = `print() {
  [ "$1" = -r ] && [ "$2" = -- ] || exit 99
  shift 2
  printf '.' >> "$DS_PRINT_CALLS"
  printf '%s\n' "$1"
}
` + command
	} else {
		// Exercise portable fallbacks even when the host shell implements the
		// same redirection/NUL-read extensions as Android mksh.
		command = strings.ReplaceAll(command, `ds_value=$(< "$1")`, "ds_value=")
		command = strings.ReplaceAll(command, `if IFS= read -r -d '' ds_actual_name < "/proc/$ds_pid/cmdline" 2>/dev/null; then`, "if false; then")
	}
	command = strings.ReplaceAll(command, "/proc/", proc+"/")
	realCat, _ := exec.LookPath("cat")
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+tools, "DS_REAL_CAT="+realCat,
		"DS_PS_FILE="+filepath.Join(dir, "ps.txt"), "DS_PS_STATUS="+fmt.Sprint(tc.status),
		"DS_PROC_ROOT="+proc, "DS_PRINT_CALLS="+filepath.Join(dir, "print-calls"),
		"DS_SECOND_STAT="+filepath.Join(dir, "second-stat.txt"))
	out, err := cmd.CombinedOutput()
	if mode == "native" && err == nil {
		if _, statErr := os.Stat(filepath.Join(dir, "print-calls")); statErr != nil {
			t.Fatal("native printer branch was not exercised")
		}
	}
	return string(out), err
}

func TestSnapshotCommandPreservesCensusAndRaceChecks(t *testing.T) {
	header := "PID UID NAME\n"
	present := header + "1 0 init\n101 10123 com.example.app\n102 10123 com.example.app:worker\n"
	for _, tc := range []scriptCase{
		{name: "present", rows: present, wantProcesses: 1},
		{name: "absent", rows: header + "1 0 init\n102 10123 com.example.app:worker\n"},
		{name: "failed census with valid header", rows: header, status: 1, wantGap: true},
		{name: "malformed unrelated row", rows: present + "999 nope unrelated\n", wantGap: true},
		{name: "duplicate unrelated PID", rows: present + "1 0 another\n", wantGap: true},
		{name: "changed target UID", rows: header + "101 10124 com.example.app\n", wantGap: true},
		{name: "start tick race", rows: present, secondStat: "101 (main) S 1 1 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 2000 0 0\n", wantGap: true},
		{name: "missing target proc", rows: header + "201 10123 com.example.app\n", wantGap: true},
		{name: "empty stat", rows: present, files: map[string]string{"proc/101/stat": ""}, wantGap: true},
		{name: "missing UID evidence", rows: present, files: map[string]string{"proc/101/status": ""}, wantGap: true},
		{name: "UID evidence mismatch", rows: present, files: map[string]string{"proc/101/status": "Uid:\t10124\t10124\t10124\t10124\n"}, wantGap: true},
		{name: "cmdline mismatch", rows: present, files: map[string]string{"proc/101/cmdline": "com.example.other\x00"}, wantGap: true},
		{name: "empty cmdline", rows: present, files: map[string]string{"proc/101/cmdline": ""}, wantGap: true},
		{name: "cmdline arguments", rows: present, files: map[string]string{"proc/101/cmdline": "com.example.app\x00--argument\x00"}, wantProcesses: 1},
		{name: "literal process text", rows: present + "999 10000 other\\name\n", wantProcesses: 1},
		{name: "multiple main instances", rows: present + "202 10123 com.example.app\n", wantProcesses: 2, files: map[string]string{
			"proc/202/stat":    "202 (main) S 1 1 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 2000 0 0\n",
			"proc/202/cmdline": "com.example.app\x00",
			"proc/202/status":  "Uid:\t10123\t10123\t10123\t10123\n",
		}},
	} {
		for _, mode := range []string{"native", "fallback"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				out, err := runSnapshotScript(t, tc, mode)
				var count int
				if err == nil {
					snapshot, parseErr := ParseSnapshot(out, "com.example.app", 10123)
					err = parseErr
					count = len(snapshot.Processes)
				}
				if (err != nil) != tc.wantGap {
					t.Fatalf("gap=%t want %t; error=%v output=%q", err != nil, tc.wantGap, err, out)
				}
				if err == nil && count != tc.wantProcesses {
					t.Fatalf("process count %d want %d", count, tc.wantProcesses)
				}
			})
		}
	}
}
