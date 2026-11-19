package investigate

import (
	"math"
	"time"

	"droidsleuth/internal/session"
)

// ResourceObservation includes the measured inputs and any derived values.
type ResourceObservation struct {
	Raw            *session.Resources `json:"raw"`
	RSSBytes       *uint64            `json:"rss_bytes,omitempty"`
	CPU            *CPURate           `json:"cpu,omitempty"`
	CPUUnavailable string             `json:"cpu_unavailable,omitempty"`
}

type CPURate struct {
	PercentOfOneCPU float64 `json:"percent_of_one_cpu"`
	FromSequence    int64   `json:"from_sequence"`
	ToSequence      int64   `json:"to_sequence"`
	ElapsedNS       int64   `json:"elapsed_ns"`
	MinElapsedNS    int64   `json:"min_elapsed_ns"`
	MaxElapsedNS    int64   `json:"max_elapsed_ns"`
}

const (
	maxSampleBracket  = 250 * time.Millisecond
	maxSampleInterval = 5 * time.Second
)

type resourceHistory struct {
	previous *session.Resources
	sequence int64
}

// observe runs in recording sequence before filtering context. Diagnostic gaps
// have no bearing on continuity of the process snapshot stream.
func (h *resourceHistory) observe(row session.Record) *ResourceObservation {
	if row.Source != "" {
		return nil
	}
	m := row.Resources
	if m == nil || m.Identity == nil {
		h.previous = nil
		if m != nil {
			return &ResourceObservation{Raw: m}
		}
		return nil
	}
	view := &ResourceObservation{Raw: m, CPUUnavailable: m.CPUUnavailable}
	if m.RSSUnavailable == "" {
		bytes := *m.RSSPages * m.PageSizeBytes
		view.RSSBytes = &bytes
	}
	if m.CPUUnavailable != "" {
		h.previous = nil
		return view
	}
	if m.Uptime.LatestNS-m.Uptime.EarliestNS > int64(maxSampleBracket) {
		h.previous = nil
		view.CPUUnavailable = "timing_bracket_too_wide"
		return view
	}
	old, seq := h.previous, h.sequence
	h.previous, h.sequence = m, row.Sequence
	view.CPU, view.CPUUnavailable = cpuRate(old, seq, m, row.Sequence)
	return view
}

func cpuRate(old *session.Resources, from int64, now *session.Resources, to int64) (*CPURate, string) {
	if old == nil {
		return nil, "baseline_required"
	}
	if *old.Identity != *now.Identity {
		return nil, "process_identity_changed"
	}
	if old.ClockTicksPerSecond != now.ClockTicksPerSecond {
		return nil, "clock_ticks_changed"
	}
	if *now.UserTicks < *old.UserTicks || *now.SystemTicks < *old.SystemTicks {
		return nil, "cpu_counter_decreased"
	}
	minNS := now.Uptime.EarliestNS - old.Uptime.LatestNS
	maxNS := now.Uptime.LatestNS - old.Uptime.EarliestNS
	if minNS <= 0 {
		return nil, "timing_bounds_overlap_or_reverse"
	}
	if maxNS > int64(maxSampleInterval) {
		return nil, "sampling_interval_too_long"
	}
	user, system := *now.UserTicks-*old.UserTicks, *now.SystemTicks-*old.SystemTicks
	if user > math.MaxUint64-system {
		return nil, "cpu_delta_overflow"
	}
	elapsed := minNS + (maxNS-minNS)/2
	percent := 100 * (float64(user+system) / float64(now.ClockTicksPerSecond)) / (float64(elapsed) / float64(time.Second))
	return &CPURate{PercentOfOneCPU: percent, FromSequence: from, ToSequence: to, ElapsedNS: elapsed, MinElapsedNS: minNS, MaxElapsedNS: maxNS}, ""
}
