package session

import (
	"errors"
	"math"
)

const (
	ResourceSingle    = "single_main_process"
	ResourceAbsent    = "main_process_absent"
	ResourceAmbiguous = "ambiguous_main_process"
)

func ValidClockTicks(v uint64) bool { return v > 0 && v <= 1_000_000 }
func ValidPageSize(v uint64) bool   { return v >= 512 && v <= 1<<30 && v&(v-1) == 0 }

func (m Resources) CPUInputIssue() string {
	switch {
	case m.UserTicks == nil || m.SystemTicks == nil:
		return "cpu_counters_unavailable"
	case m.Uptime == nil:
		return "uptime_unavailable"
	case m.ClockTicksPerSecond == 0:
		return "clock_ticks_unavailable"
	}
	return ""
}

func (m Resources) RSSInputIssue() string {
	switch {
	case m.RSSPages == nil:
		return "rss_pages_unavailable"
	case m.PageSizeBytes == 0:
		return "page_size_unavailable"
	case *m.RSSPages > math.MaxUint64/m.PageSizeBytes:
		return "rss_overflow"
	}
	return ""
}

func validateResources(r Record, version int) error {
	m := r.Resources
	if version != ResourceSchemaVersion || r.Snapshot == nil {
		if m != nil {
			return errors.New("resource payload requires a schema 3 snapshot")
		}
		return nil
	}
	if m == nil {
		return errors.New("schema 3 snapshot requires resource status")
	}
	switch len(r.Snapshot.Processes) {
	case 1:
		p := r.Snapshot.Processes[0].Identity
		if m.Status != ResourceSingle || m.Identity == nil || *m.Identity != p ||
			p.BootID != r.Snapshot.BootID || p.BootID == "" || p.PID <= 0 || p.UID < 0 || p.StartTicks == 0 {
			return errors.New("resource identity does not match snapshot")
		}
	default:
		status := ResourceAbsent
		if len(r.Snapshot.Processes) > 1 {
			status = ResourceAmbiguous
		}
		if *m != (Resources{Status: status}) {
			return errors.New("resource status does not match target count")
		}
		return nil
	}
	if (m.UserTicks == nil) != (m.SystemTicks == nil) ||
		(m.ClockTicksPerSecond != 0 && !ValidClockTicks(m.ClockTicksPerSecond)) ||
		(m.PageSizeBytes != 0 && !ValidPageSize(m.PageSizeBytes)) ||
		(m.Uptime != nil && (m.Uptime.EarliestNS < 0 || m.Uptime.LatestNS <= m.Uptime.EarliestNS)) ||
		m.CPUUnavailable != m.CPUInputIssue() || m.RSSUnavailable != m.RSSInputIssue() {
		return errors.New("invalid resource inputs or availability")
	}
	return nil
}
