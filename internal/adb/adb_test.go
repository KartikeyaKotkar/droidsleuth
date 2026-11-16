package adb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureClient(t *testing.T, stdout, stderr string, block bool) Client {
	t.Helper()
	dir := t.TempDir()
	script, err := os.ReadFile("../../testdata/fake-adb.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "adb")
	files := map[string]string{"adb": string(script), "1.stdout": stdout, "1.stderr": stderr}
	if block {
		files["1.block"] = ""
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DROIDSLEUTH_FIXTURES", dir)
	return Client{Path: path, Serial: "fixture-phone", Timeout: 500 * time.Millisecond}
}

func TestDeviceSelection(t *testing.T) {
	for _, state := range []string{"unauthorized", "offline", "no"} {
		if _, err := Select([]Device{{"a", state}}, "a"); err == nil {
			t.Fatalf("accepted %s", state)
		}
	}
	if _, err := Select([]Device{{"a", "device"}, {"b", "device"}}, ""); err == nil {
		t.Fatal("selected arbitrary device")
	}
	if _, err := Select(nil, "a"); err == nil {
		t.Fatal("selected absent device")
	}
	if serial, err := Select([]Device{{"a", "device"}}, ""); err != nil || serial != "a" {
		t.Fatalf("%q %v", serial, err)
	}
}

func TestSubprocessDeadline(t *testing.T) {
	client := fixtureClient(t, "", "", true)
	begin := time.Now()
	if _, err := client.Shell(context.Background(), "fixture"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if time.Since(begin) > 2*time.Second {
		t.Fatal("subprocess did not terminate promptly")
	}
}

func TestBoundedSubprocessOutput(t *testing.T) {
	t.Run("stdout overflow cancels collection", func(t *testing.T) {
		client := fixtureClient(t, strings.Repeat("x", (4<<20)+1), "", false)
		client.Timeout = 3 * time.Second
		if _, err := client.Shell(context.Background(), "fixture"); !errors.Is(err, ErrOutputLimit) {
			t.Fatalf("output limit: %v", err)
		}
	})
	t.Run("stderr is drained and success is not hidden", func(t *testing.T) {
		client := fixtureClient(t, "ok", strings.Repeat("x", 200000), false)
		out, err := client.Shell(context.Background(), "fixture")
		if err != nil || out != "ok" {
			t.Fatalf("%q %v", out, err)
		}
	})
}
