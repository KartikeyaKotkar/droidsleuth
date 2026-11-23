package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

// Exercise the exit status and output seen by someone trying a fresh install.
func TestCommandHelp(t *testing.T) {
	if os.Getenv("DROIDSLEUTH_TEST_HELP") == "1" {
		os.Args = os.Args[2:] // "--" stands in for argv[0].
		main()
		os.Exit(0)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "collect-history"},
		{[]string{"-h"}, "watch"},
		{[]string{"devices", "--help"}, "devices"},
		{[]string{"collect-history", "--help"}, "-out"},
		{[]string{"watch", "-h"}, "-summary"},
		{[]string{"replay", "--help"}, "-json"},
		{[]string{"investigate", "--help"}, "-timeline"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			args := append([]string{"-test.run=^TestCommandHelp$", "--"}, tc.args...)
			cmd := exec.Command(os.Args[0], args...)
			cmd.Env = append(os.Environ(), "DROIDSLEUTH_TEST_HELP=1", "PATH="+t.TempDir())
			output, err := cmd.Output()
			if err != nil || !bytes.Contains(output, []byte(tc.want)) {
				t.Fatalf("help: %v, output %q", err, output)
			}
		})
	}
}

func TestReplayHasNoADBDependency(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "offline.db")
	store, err := session.Create(ctx, path, session.Metadata{Package: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Append(ctx, session.Record{Sequence: 1, Kind: "gap", Error: "device disconnected\x1b[2J"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // No adb executable or connected-device access.
	var out, stderr bytes.Buffer
	if err := run(ctx, []string{"replay", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "coverage gap") || strings.Contains(out.String(), "\x1b") {
		t.Fatalf("unsafe or missing gap output: %q", out.String())
	}
	out.Reset()
	if err := run(ctx, []string{"investigate", "--json", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"findings":[]`) {
		t.Fatalf("disappearance/gap invented incident: %s", out.String())
	}
	out.Reset()
	if err := run(ctx, []string{"replay", "--json", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\n") != 2 || !strings.Contains(out.String(), `"sequence":1`) {
		t.Fatalf("unexpected JSONL: %s", out.String())
	}
}

func TestDefaultDirectoryIsPrivate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	path, err := defaultPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("unsafe default directory: %v", err)
	}
}
