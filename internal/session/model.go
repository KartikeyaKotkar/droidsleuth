// Package session owns the recording format and SQLite persistence.
package session

import "time"

const SchemaVersion = 2

type Identity struct {
	BootID     string `json:"boot_id"`
	UID        int    `json:"uid"`
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
}

type Process struct {
	Identity
	Name string `json:"name"`
}

type Snapshot struct {
	BootID              string    `json:"boot_id"`
	DeviceEpochSeconds  int64     `json:"device_epoch_seconds"`
	DeviceUptimeSeconds float64   `json:"device_uptime_seconds"`
	Processes           []Process `json:"processes"`
}

type Transition struct {
	Kind     string  `json:"kind"`
	Process  Process `json:"process"`
	AfterGap bool    `json:"after_gap,omitempty"`
}

// Record times describe host observation bounds, never an exact process death time.
type Record struct {
	Source         string       `json:"source,omitempty"`
	Evidence       *Evidence    `json:"evidence,omitempty"`
	Sequence       int64        `json:"sequence"`
	Kind           string       `json:"kind"`
	StartedUTC     time.Time    `json:"started_utc"`
	EndedUTC       time.Time    `json:"ended_utc"`
	StartElapsedNS int64        `json:"start_elapsed_ns"`
	EndElapsedNS   int64        `json:"end_elapsed_ns"`
	Snapshot       *Snapshot    `json:"snapshot,omitempty"`
	Transitions    []Transition `json:"transitions,omitempty"`
	Error          string       `json:"error,omitempty"`
}

type Metadata struct {
	Schema     int        `json:"schema"`
	Package    string     `json:"package"`
	Serial     string     `json:"serial"`
	User       int        `json:"user"`
	UID        int        `json:"uid"`
	SDK        int        `json:"sdk"`
	StartedUTC time.Time  `json:"started_utc"`
	EndedUTC   *time.Time `json:"ended_utc,omitempty"`
	Status     string     `json:"status"`
}

// Evidence retains only selected-package source text. Event times are device
// realtime, not host collection times. Unknown timestamps and UIDs stay unknown.
type Evidence struct {
	Format    string  `json:"format"`
	Text      string  `json:"text"`
	Events    []Event `json:"events"`
	Truncated bool    `json:"truncated,omitempty"`
}

type Event struct {
	Kind              string `json:"kind"`
	Process           string `json:"process"`
	PID               int    `json:"pid"`
	UID               *int   `json:"uid,omitempty"`
	User              *int   `json:"user,omitempty"`
	DeviceTimeNS      int64  `json:"device_time_ns,omitempty"`
	TimeUncertaintyNS int64  `json:"time_uncertainty_ns,omitempty"`
	RawTime           string `json:"raw_time,omitempty"`
	BootID            string `json:"boot_id,omitempty"`
	Reason            *int   `json:"reason,omitempty"`
	Detail            string `json:"detail,omitempty"`
	// Lines are one-based references within this observation's source text.
	FirstLine int `json:"first_line"`
	LastLine  int `json:"last_line"`
}
