package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"droidsleuth/internal/adb"
	"droidsleuth/internal/session"
)

func fakeClient(t *testing.T, files map[string]string) adb.Client {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "adb")
	if err := os.WriteFile(script, []byte(fixture(t, "fake-adb.sh")), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROIDSLEUTH_FIXTURES", dir)
	return adb.Client{Path: script, Serial: "fixture-phone", Timeout: time.Second}
}

func TestFakeADBPipelineAndOfflineReplay(t *testing.T) {
	present := fixture(t, "snapshot-present.txt")
	// No device interaction: fake subprocesses supply the complete protocol.
	client := fakeClient(t, map[string]string{
		"1.stdout": present,
		"2.stdout": "", "2.stderr": "device offline", "2.exit": "1",
		"3.stdout": present,
		"4.stdout": strings.ReplaceAll(present, "1000 0 0", "2000 0 0"),
		"5.stdout": fixture(t, "snapshot-absent.txt"),
		"6.stdout": strings.ReplaceAll(present, "101 ", "201 "),
		"7.stdout": "__DS_CLOCK__\npartial snapshot",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	devices, err := client.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adb.Select(devices, client.Serial); err != nil {
		t.Fatal(err)
	}
	meta, err := Probe(ctx, client, "com.example.app", -1)
	if err != nil {
		t.Fatal(err)
	}
	meta.Serial = client.Serial
	path := filepath.Join(t.TempDir(), "session.db")
	var committed []session.Record
	err = Run(ctx, client, meta, Options{Path: path, Interval: time.Millisecond, Visit: func(r session.Record) error {
		committed = append(committed, r)
		if r.Sequence == 7 {
			cancel()
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 7 {
		t.Fatalf("got %d observations", len(committed))
	}
	if committed[1].Kind != "gap" || committed[1].Snapshot != nil || len(committed[1].Transitions) != 0 {
		t.Fatal("disconnect was treated as absence")
	}
	if len(committed[2].Transitions) != 0 {
		t.Fatal("reconnection of same strong identity invented a restart")
	}
	if len(committed[3].Transitions) != 2 || committed[3].Snapshot.Processes[0].StartTicks != 2000 {
		t.Fatal("PID reuse was missed")
	}
	if len(committed[4].Snapshot.Processes) != 0 || len(committed[4].Transitions) != 1 {
		t.Fatal("successful absence was not retained")
	}
	if committed[5].Snapshot.Processes[0].PID != 201 {
		t.Fatal("replacement PID was missed")
	}
	if committed[6].Kind != "gap" {
		t.Fatal("partial output was treated as successful")
	}
	// Remove the fake executable. Offline replay must not invoke even fake ADB.
	if err := os.Remove(client.Path); err != nil {
		t.Fatal(err)
	}
	var replayed []session.Record
	err = session.Replay(context.Background(), path, func(m session.Metadata) error {
		if m.Status != "interrupted" || m.UID != 10123 {
			return fmt.Errorf("wrong metadata: %+v", m)
		}
		return nil
	}, func(r session.Record) error { replayed = append(replayed, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	// Compare serialized values, including timestamps, identity and gap text.
	if len(replayed) != len(committed) {
		t.Fatal("replay lost observations")
	}
	for i := range committed {
		before, _ := json.Marshal(committed[i])
		after, _ := json.Marshal(replayed[i])
		if string(before) != string(after) {
			t.Fatalf("observation %d changed", i)
		}
	}
}

func TestCancellationDuringFakeADBRead(t *testing.T) {
	client := fakeClient(t, map[string]string{"1.block": ""})
	meta := session.Metadata{Package: "com.example.app", UID: 10123}
	path := filepath.Join(t.TempDir(), "canceled.db")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, client, meta, Options{Path: path}) }()
	deadline := time.Now().Add(2 * time.Second)
	pidPath := filepath.Join(os.Getenv("DROIDSLEUTH_FIXTURES"), "pid")
	for {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake ADB never started")
		}
		time.Sleep(time.Millisecond)
	}
	pid, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reap subprocess")
	}
	if _, err := os.Stat("/proc/" + strings.TrimSpace(string(pid))); !os.IsNotExist(err) {
		t.Fatal("owned adb subprocess still exists")
	}
	var records []session.Record
	err = session.Replay(context.Background(), path, func(m session.Metadata) error {
		if m.Status != "interrupted" {
			t.Fatalf("status: %s", m.Status)
		}
		return nil
	}, func(r session.Record) error { records = append(records, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "gap" || !strings.Contains(records[0].Error, "canceled") {
		t.Fatalf("lost interrupted attempt: %+v", records)
	}
}

func TestEvidenceFakeADBPipelineKeepsLifecycleIndependent(t *testing.T) {
	present := fixture(t, "snapshot-present.txt")
	client := fakeClient(t, map[string]string{"1.stdout": present, "2.stdout": strings.ReplaceAll(present, "1000 0 0", "2000 0 0")})
	original := client.Path + "-snapshots"
	if err := os.Rename(client.Path, original); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(client.Path)
	raw := "1700000000.123456 10123 101 101 E AndroidRuntime: FATAL EXCEPTION: main\n1700000000.123456 10123 101 101 E AndroidRuntime: Process: com.example.app, PID: 101\n1700000000.123457 1000 900 900 I am_anr: [0,101,com.example.app,0,fixture timeout]\n1700000000.123458 10124 102 102 E AndroidRuntime: Process: com.private.other, PID: 102\n"
	if err := os.WriteFile(filepath.Join(dir, "logcat"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/bin/sh
case "$*" in
 *__DS_CLOCK__*) exec "$DROIDSLEUTH_FIXTURES/adb-snapshots" "$@";;
 *random/boot_id*) printf 'fixture-boot\n';;
 *persist.sys.timezone*) printf 'UTC\n';;
 *exit-info*) printf 'Unknown command\n';;
 *logcat*) cat "$DROIDSLEUTH_FIXTURES/logcat";;
 *) exec "$DROIDSLEUTH_FIXTURES/adb-snapshots" "$@";;
esac
`
	if err := os.WriteFile(client.Path, []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "evidence.db")
	var committed []session.Record
	events, snapshots := 0, 0
	err := Run(ctx, client, session.Metadata{Package: "com.example.app", UID: 10123}, Options{Path: path, Evidence: true, Interval: time.Millisecond, Visit: func(r session.Record) error {
		committed = append(committed, r)
		if r.Evidence != nil {
			events += len(r.Evidence.Events)
		}
		if r.Snapshot != nil {
			snapshots++
			for _, tr := range r.Transitions {
				if tr.AfterGap {
					t.Fatal("diagnostic gap contaminated process tracker")
				}
			}
		}
		if events >= 2 && snapshots >= 2 {
			cancel()
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if events != 2 || snapshots < 2 {
		t.Fatalf("events=%d snapshots=%d", events, snapshots)
	}
	if err = os.Remove(client.Path); err != nil {
		t.Fatal(err)
	}
	var replayed []session.Record
	err = session.Replay(context.Background(), path, func(session.Metadata) error { return nil }, func(r session.Record) error { replayed = append(replayed, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(committed)
	after, _ := json.Marshal(replayed)
	if string(before) != string(after) {
		t.Fatal("offline evidence replay differs")
	}
	if strings.Contains(string(after), "com.private.other") {
		t.Fatal("unrelated log persisted")
	}
}

// These are synthetic capability-contract cases, not Android runtime evidence.
func TestSDKContractFixtureMatrix(t *testing.T) {
	for _, sdk := range []int{29, 30, 33, 35, 36, 37} {
		t.Run(fmt.Sprint(sdk), func(t *testing.T) {
			client := fakeClient(t, map[string]string{})
			t.Setenv("DROIDSLEUTH_FIXTURE_SDK", fmt.Sprint(sdk))
			meta, err := Probe(context.Background(), client, "com.example.app", 0)
			if sdk < 30 {
				if err == nil {
					t.Fatal("unsupported SDK accepted")
				}
				return
			}
			if err != nil || meta.SDK != sdk || meta.UID != 10123 {
				t.Fatalf("capability contract: %+v %v", meta, err)
			}
		})
	}
}
