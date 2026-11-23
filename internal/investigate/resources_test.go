package investigate

import (
	"math"
	"testing"
	"time"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

func resourceRow(seq int64, at time.Duration, id session.Identity, user, system, pages uint64) session.Record {
	return session.Record{Sequence: seq, Kind: "snapshot", Resources: &session.Resources{
		Status: session.ResourceSingle, Identity: &id, UserTicks: &user, SystemTicks: &system,
		ClockTicksPerSecond: 100, RSSPages: &pages, PageSizeBytes: 16384,
		Uptime: &session.UptimeBounds{EarliestNS: at.Nanoseconds(), LatestNS: (at + 10*time.Millisecond).Nanoseconds()},
	}, Snapshot: &session.Snapshot{BootID: id.BootID, Processes: []session.Process{{Identity: id}}}}
}

func TestResourceRatesUseDeviceElapsedAndFullIdentity(t *testing.T) {
	id := session.Identity{BootID: "boot", UID: 10123, PID: 77, StartTicks: 123}
	var h resourceHistory
	row := resourceRow(2, time.Second, id, 5, 5, 0)
	if got := h.observe(row); got.CPU != nil || got.CPUUnavailable != "baseline_required" || *got.RSSBytes != 0 {
		t.Fatalf("initial view: %+v", got)
	}
	h.observe(session.Record{Kind: "gap", Source: "logcat"})
	got := h.observe(resourceRow(4, 2500*time.Millisecond, id, 250, 60, 2))
	if got.CPU == nil || got.CPU.PercentOfOneCPU != 200 || got.CPU.FromSequence != 2 || got.CPU.ToSequence != 4 ||
		got.CPU.ElapsedNS != 1500000000 || got.CPU.MinElapsedNS != 1490000000 || got.CPU.MaxElapsedNS != 1510000000 || *got.RSSBytes != 32768 {
		t.Fatalf("unexpected rate: %+v", got.CPU)
	}
	id.StartTicks++
	if got := h.observe(resourceRow(5, 3*time.Second, id, 6, 2, 2)); got.CPUUnavailable != "process_identity_changed" {
		t.Fatalf("PID reused: %+v", got)
	}
	h.observe(session.Record{Kind: "gap"})
	if got := h.observe(resourceRow(7, 4*time.Second, id, 6, 2, 2)); got.CPUUnavailable != "baseline_required" {
		t.Fatal("rate bridged gap")
	}
}

func TestResourceRateRejectionsKeepRSS(t *testing.T) {
	id := session.Identity{BootID: "boot", UID: 10123, PID: 77, StartTicks: 123}
	for _, tc := range []struct {
		name   string
		mutate func(*session.Resources)
	}{
		{"cpu_counter_decreased", func(m *session.Resources) { *m.UserTicks = 0 }},
		{"timing_bounds_overlap_or_reverse", func(m *session.Resources) { m.Uptime.EarliestNS = 1_005_000_000 }},
		{"timing_bracket_too_wide", func(m *session.Resources) { m.Uptime.LatestNS += int64(time.Second) }},
		{"sampling_interval_too_long", func(m *session.Resources) {
			m.Uptime.EarliestNS += int64(5 * time.Second)
			m.Uptime.LatestNS += int64(5 * time.Second)
		}},
		{"clock_ticks_changed", func(m *session.Resources) { m.ClockTicksPerSecond = 200 }},
		{"cpu_delta_overflow", func(m *session.Resources) { *m.UserTicks, *m.SystemTicks = math.MaxUint64, math.MaxUint64 }},
		{"uptime_unavailable", func(m *session.Resources) { m.Uptime = nil; m.CPUUnavailable = m.CPUInputIssue() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h resourceHistory
			h.observe(resourceRow(1, time.Second, id, 1, 1, 0))
			row := resourceRow(2, 2*time.Second, id, 2, 2, 5)
			if tc.name == "timing_bounds_overlap_or_reverse" {
				row.Resources.Uptime.LatestNS = 1015000000
			}
			tc.mutate(row.Resources)
			if got := h.observe(row); got.CPU != nil || got.CPUUnavailable != tc.name || got.RSSBytes == nil {
				t.Fatalf("unexpected view: %+v", got)
			}
		})
	}
}

func TestResourceTimelineKeepsBaselineOutsideWindow(t *testing.T) {
	f := newTimelineFixtureWithSchema(t, session.ResourceSchemaVersion)
	id := session.Identity{BootID: "boot", UID: 10123, PID: 5, StartTicks: 12}
	for i := int64(1); i <= 2; i++ {
		f.append(resourceRow(i, time.Duration(i)*time.Second, id, uint64(i*50), 0, 10))
	}
	f.append(session.Record{Kind: "snapshot", Snapshot: &session.Snapshot{BootID: id.BootID},
		Resources: &session.Resources{Status: session.ResourceAbsent}, Transitions: []session.Transition{{Kind: "no_longer_observed", Process: session.Process{Identity: id}}}})
	f.finish()
	d := f.timeline("L3.0", 0, 0).Incident
	if len(d.Context) != 0 || d.Lifecycle.LastPresence == nil || d.Lifecycle.LastPresence.Resources == nil ||
		d.Lifecycle.LastPresence.Resources.CPU == nil || d.Lifecycle.LastPresence.Resources.CPU.FromSequence != 1 ||
		d.AnchorObservation.Resources.Raw.Status != session.ResourceAbsent {
		t.Fatalf("unexpected detail: %+v", d)
	}
}

func TestRSSFailureDoesNotHideCPURate(t *testing.T) {
	id := session.Identity{BootID: "boot", UID: 10123, PID: 77, StartTicks: 123}
	var h resourceHistory
	row := resourceRow(1, time.Second, id, 0, 0, 17)
	row.Resources.PageSizeBytes = 4096
	if got := h.observe(row); got.RSSBytes == nil || *got.RSSBytes != 17*4096 {
		t.Fatalf("wrong 4 KiB page conversion: %+v", got)
	}
	row = resourceRow(2, 2*time.Second, id, 0, 0, math.MaxUint64)
	row.Resources.RSSUnavailable = row.Resources.RSSInputIssue()
	if got := h.observe(row); got.RSSBytes != nil || got.Raw.RSSUnavailable != "rss_overflow" ||
		got.CPU == nil || got.CPU.PercentOfOneCPU != 0 {
		t.Fatalf("RSS failure hid an observed zero CPU rate: %+v", got)
	}
}
