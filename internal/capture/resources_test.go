package capture

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"droidsleuth/internal/session"
)

func TestResourceFramingUsesValidatedStat(t *testing.T) {
	rows := "PID UID NAME\n101 10123 com.example.app\n"
	out, err := runSnapshotScript(t, scriptCase{rows: rows}, "resources")
	if err != nil {
		t.Fatal(err, out)
	}
	var m session.Resources
	snapshot, err := parseSnapshot(out, "com.example.app", 10123, &m)
	if err != nil {
		t.Fatal(err, out)
	}
	if m.Identity == nil || *m.Identity != snapshot.Processes[0].Identity || m.UserTicks == nil || *m.UserTicks != 0 ||
		m.RSSPages == nil || *m.RSSPages != 0 || m.Uptime == nil || m.Uptime.LatestNS-m.Uptime.EarliestNS != 10_000_000 {
		t.Fatalf("unexpected stat sample: %+v", m)
	}
	withoutTiming := strings.Replace(out, "__DS_RESOURCE__ 101 1059884.34 1059884.34", "__DS_RESOURCE__ 101 - -", 1)
	unchanged, err := parseSnapshot(withoutTiming, "com.example.app", 10123, &m)
	if err != nil || !reflect.DeepEqual(snapshot, unchanged) || m.Uptime != nil || m.RSSPages == nil {
		t.Fatalf("timing failure affected identity or RSS: %+v %v", m, err)
	}
	for _, incomplete := range []string{strings.Replace(out, "__DS_END__", "", 1), strings.Replace(out, "101 (main (worker))", "102 (main (worker))", 1)} {
		if _, err := parseSnapshot(incomplete, "com.example.app", 10123, &m); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	if _, err := runSnapshotScript(t, scriptCase{rows: rows, secondStat: "101 (main) S 1 1 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 2000 0 0\n"}, "resources"); err == nil {
		t.Fatal("resource mode accepted a PID reuse race")
	}
}

func TestUptimeUsesDecimalPrecision(t *testing.T) {
	n, step, ok := uptimeNS("1234.09")
	if !ok || n != 1234090000000 || step != 10000000 {
		t.Fatalf("uptime=%d step=%d ok=%t", n, step, ok)
	}
	for _, s := range []string{"-1.00", "1.", "1.0123456789", "99999999999999.1", "9223372036.854775807"} {
		if _, _, ok := uptimeNS(s); ok {
			t.Errorf("accepted %q", s)
		}
	}
}

type resourceFixtureShell struct {
	out     string
	units   int
	boot    string
	cancel  context.CancelFunc
	watches int
}

func (f *resourceFixtureShell) Shell(ctx context.Context, command string) (string, error) {
	if command == resourceUnitsCommand {
		f.units++
		return f.boot + "\nticks 100\npages 16384\n" + f.boot + "\n", nil
	}
	f.watches++
	if f.watches == 3 {
		f.cancel()
		return "", context.Canceled
	}
	return f.out, nil
}

func TestResourceCapturePreservesLifecycleAndCancels(t *testing.T) {
	plain := fixture(t, "snapshot-present.txt")
	boot := "11111111-1111-1111-1111-111111111111"
	resource := strings.Replace(plain, "__DS_END__", "__DS_RESOURCE__ 101 10.00 10.01\n__DS_END__", 1)
	var reference []session.Transition
	for _, enabled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var got []session.Transition
		shell := &resourceFixtureShell{out: plain, boot: boot, cancel: cancel}
		if enabled {
			shell.out = resource
		}
		meta := session.Metadata{Package: "com.example.app", UID: 10123}
		path := filepath.Join(t.TempDir(), "capture.db")
		err := Run(ctx, shell, meta, Options{Path: path, Resources: enabled, Interval: time.Millisecond})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if shell.units != map[bool]int{false: 0, true: 1}[enabled] {
			t.Fatalf("unit call count %d", shell.units)
		}
		if err := session.Replay(context.Background(), path, func(m session.Metadata) error {
			want := session.SchemaVersion
			if enabled {
				want = session.ResourceSchemaVersion
			}
			if m.Schema != want || m.Status != "interrupted" {
				t.Fatalf("metadata: %+v", m)
			}
			return nil
		}, func(r session.Record) error {
			got = append(got, r.Transitions...)
			if r.Sequence == 3 && (r.Kind != "gap" || r.Resources != nil) {
				t.Fatal("canceled read persisted a sample")
			}
			if enabled && r.Snapshot != nil && (r.Resources == nil || r.Resources.ClockTicksPerSecond != 100 || r.Resources.PageSizeBytes != 16384) {
				t.Fatalf("units missing: %+v", r.Resources)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !enabled {
			reference = got
		} else if !reflect.DeepEqual(reference, got) {
			t.Fatal("resource mode changed lifecycle")
		}
	}
}

func TestResourcesThroughFakeADBWithReboot(t *testing.T) {
	boot1 := "11111111-1111-1111-1111-111111111111"
	boot2 := "22222222-2222-2222-2222-222222222222"
	present := strings.Replace(fixture(t, "snapshot-present.txt"), "__DS_END__", "__DS_RESOURCE__ 101 10.00 10.00\n__DS_END__", 1)
	replacement := strings.ReplaceAll(present, "1000 0 0", "2000 0 0")
	otherBoot := strings.ReplaceAll(replacement, boot1, boot2)
	client := fakeClient(t, map[string]string{
		"1.stdout": present, "2.stdout": boot1 + "\nticks 100\npages -\n" + boot1 + "\n",
		"3.stdout": "", "3.exit": "1", "4.stdout": replacement,
		"5.stdout": otherBoot, "6.stdout": boot2 + "\nticks -\npages 16384\n" + boot2 + "\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var rows []session.Record
	err := Run(ctx, client, session.Metadata{Package: "com.example.app", UID: 10123}, Options{
		Path: filepath.Join(t.TempDir(), "resource.db"), Resources: true, Interval: time.Millisecond,
		Visit: func(r session.Record) error {
			rows = append(rows, r)
			if len(rows) == 4 {
				cancel()
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[1].Kind != "gap" || rows[1].Resources != nil || len(rows[2].Transitions) != 2 ||
		!rows[2].Transitions[0].AfterGap || len(rows[3].Transitions) != 1 || rows[3].Transitions[0].Kind != "appeared" {
		t.Fatalf("unexpected lifecycle: %+v", rows)
	}
	for _, i := range []int{0, 2} {
		m := rows[i].Resources
		if m.CPUUnavailable != "" || m.RSSUnavailable != "page_size_unavailable" {
			t.Fatalf("availability: %+v", m)
		}
	}
	last := rows[3].Resources
	if last.ClockTicksPerSecond != 0 || last.RSSUnavailable != "" || last.CPUUnavailable != "clock_ticks_unavailable" {
		t.Fatalf("cached units crossed boots or unavailable CPU affected RSS: %+v", last)
	}
}

func TestResourceUnitProbeCanceledAfterValidSnapshot(t *testing.T) {
	present := strings.Replace(fixture(t, "snapshot-present.txt"), "__DS_END__", "__DS_RESOURCE__ 101 10.00 10.01\n__DS_END__", 1)
	client := fakeClient(t, map[string]string{"1.stdout": present, "2.block": ""})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "canceled-probe.db")
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, client, session.Metadata{Package: "com.example.app", UID: 10123}, Options{Path: path, Resources: true})
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
waiting:
	for {
		select {
		case <-deadline.C:
			t.Fatal("unit probe did not start")
		case <-tick.C:
			if _, err := os.Stat(filepath.Join(filepath.Dir(client.Path), "pid")); err == nil {
				break waiting
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled unit probe did not exit")
	}
	rows := 0
	err := session.Replay(context.Background(), path, func(m session.Metadata) error {
		if m.Status != "interrupted" {
			t.Fatalf("unexpected final status %s", m.Status)
		}
		return nil
	}, func(r session.Record) error {
		rows++
		if r.Snapshot == nil || len(r.Transitions) != 1 || r.Transitions[0].Kind != "appeared" ||
			r.Resources == nil || r.Resources.CPUUnavailable != "clock_ticks_unavailable" ||
			r.Resources.RSSUnavailable != "page_size_unavailable" {
			t.Fatalf("probe cancellation changed the validated sample: %+v", r)
		}
		return nil
	})
	if err != nil || rows != 1 {
		t.Fatalf("replay rows=%d err=%v", rows, err)
	}
}
