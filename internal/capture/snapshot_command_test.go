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
func runSnapshotScript(t *testing.T, rows string, psStatus int, secondStat string, grepFailure bool) (string, error) {
	t.Helper()
	dir := t.TempDir()
	proc := filepath.Join(dir, "proc")
	tools := filepath.Join(dir, "tools")
	if err := os.MkdirAll(tools, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cat", "tr", "head", "sed", "grep"} {
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
		"ps.txt":                         rows,
		"second-stat.txt":                secondStat,
		"tools/ps":                       "#!/bin/sh\n\"$DS_REAL_CAT\" \"$DS_PS_FILE\"\nexit \"$DS_PS_STATUS\"\n",
		"tools/date":                     "#!/bin/sh\nprintf '1794836454\\n'\n",
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
	if secondStat != "" {
		if err := os.Remove(filepath.Join(tools, "cat")); err != nil {
			t.Fatal(err)
		}
		wrapper := `#!/bin/sh
if [ "$1" = "$DS_PROC_ROOT/101/stat" ]; then
  n=0
  if [ -f "$DS_COUNT" ]; then read -r n < "$DS_COUNT"; fi
  n=$((n+1))
  printf '%s\n' "$n" > "$DS_COUNT"
  if [ "$n" -gt 1 ]; then exec "$DS_REAL_CAT" "$DS_SECOND_STAT"; fi
fi
exec "$DS_REAL_CAT" "$@"
`
		if err := os.WriteFile(filepath.Join(tools, "cat"), []byte(wrapper), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if grepFailure {
		if err := os.Remove(filepath.Join(tools, "grep")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tools, "grep"), []byte("#!/bin/sh\nexit 2\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	command, err := SnapshotCommand("com.example.app", 10123)
	if err != nil {
		t.Fatal(err)
	}
	command = strings.ReplaceAll(command, "/proc/", proc+"/")
	realCat, _ := exec.LookPath("cat")
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+tools, "DS_REAL_CAT="+realCat,
		"DS_PS_FILE="+filepath.Join(dir, "ps.txt"), "DS_PS_STATUS="+fmt.Sprint(psStatus),
		"DS_PROC_ROOT="+proc, "DS_COUNT="+filepath.Join(dir, "count"),
		"DS_SECOND_STAT="+filepath.Join(dir, "second-stat.txt"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSnapshotCommandPreservesCensusAndRaceChecks(t *testing.T) {
	header := "PID UID NAME\n"
	present := header + "1 0 init\n101 10123 com.example.app\n102 10123 com.example.app:worker\n"
	for _, tc := range []struct {
		name, rows, secondStat string
		status                 int
		wantProcesses          int
		wantGap                bool
	}{
		{name: "present", rows: present, wantProcesses: 1},
		{name: "absent", rows: header + "1 0 init\n102 10123 com.example.app:worker\n"},
		{name: "failed census with valid header", rows: header, status: 1, wantGap: true},
		{name: "malformed unrelated row", rows: present + "999 nope unrelated\n", wantGap: true},
		{name: "duplicate unrelated PID", rows: present + "1 0 another\n", wantGap: true},
		{name: "changed target UID", rows: header + "101 10124 com.example.app\n", wantGap: true},
		{name: "start tick race", rows: present, secondStat: "101 (main) S 1 1 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 2000 0 0\n", wantGap: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runSnapshotScript(t, tc.rows, tc.status, tc.secondStat, false)
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
