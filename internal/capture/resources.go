package capture

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"droidsleuth/internal/session"
)

const resourceTimingPrefix = "__DS_RESOURCE__ "
const resourceTimingBefore = `    if [ "${ds_timed:-0}" = 0 ]; then
      ds_time_before=-
      if ds_read /proc/uptime 2>/dev/null; then ds_time_before=${ds_value%% *}; fi
    fi
`
const resourceTimingAfter = `    if [ "${ds_timed:-0}" = 0 ]; then
      ds_time_after=-
      if ds_read /proc/uptime 2>/dev/null; then ds_time_after=${ds_value%% *}; fi
    fi
`
const resourceTimingEmit = `    if [ "${ds_timed:-0}" = 0 ]; then
      ds_print "__DS_RESOURCE__ $ds_pid $ds_time_before $ds_time_after"
      ds_timed=1
    fi
`

func resourceFields(snapshot session.Snapshot, stat, timing string) session.Resources {
	switch len(snapshot.Processes) {
	case 0:
		return session.Resources{Status: session.ResourceAbsent}
	case 1:
		id := snapshot.Processes[0].Identity
		m := session.Resources{Status: session.ResourceSingle, Identity: &id}
		// The caller has already matched this stat to the validated process.
		_, fields, _ := statFields(stat)
		user, uerr := strconv.ParseUint(fields[11], 10, 64)
		system, serr := strconv.ParseUint(fields[12], 10, 64)
		if uerr == nil && serr == nil {
			m.UserTicks, m.SystemTicks = &user, &system
		}
		if len(fields) > 21 {
			pages, err := strconv.ParseUint(fields[21], 10, 64)
			if err == nil {
				m.RSSPages = &pages
			}
		}
		t := strings.Fields(timing)
		if len(t) == 4 && t[1] == strconv.Itoa(id.PID) {
			before, _, ok1 := uptimeNS(t[2])
			after, precision, ok2 := uptimeNS(t[3])
			if ok1 && ok2 && before <= after {
				m.Uptime = &session.UptimeBounds{EarliestNS: before, LatestNS: after + precision}
			}
		}
		return m
	default:
		return session.Resources{Status: session.ResourceAmbiguous}
	}
}

// uptimeNS reads the displayed uptime without rounding through floats.
func uptimeNS(s string) (ns, precision int64, ok bool) {
	seconds, fraction, dot := strings.Cut(s, ".")
	if !dot || len(fraction) == 0 || len(fraction) > 9 {
		return 0, 0, false
	}
	sec, err := strconv.ParseUint(seconds, 10, 64)
	frac, ferr := strconv.ParseUint(fraction, 10, 64)
	if err != nil || ferr != nil || sec > math.MaxInt64/uint64(time.Second) {
		return 0, 0, false
	}
	precision = int64(time.Second)
	for range fraction {
		precision /= 10
	}
	ns = int64(sec) * int64(time.Second)
	extra := int64(frac) * precision
	if extra > math.MaxInt64-ns-precision {
		return 0, 0, false
	}
	return ns + extra, precision, true
}

const resourceUnitsCommand = `set -e
cat /proc/sys/kernel/random/boot_id
ticks=$(getconf CLK_TCK 2>/dev/null) || ticks=-
pages=$(getconf PAGESIZE 2>/dev/null) || pages=$(getconf PAGE_SIZE 2>/dev/null) || pages=-
printf 'ticks %s\npages %s\n' "$ticks" "$pages"
cat /proc/sys/kernel/random/boot_id
`

type resourceUnits struct {
	boot         string
	ticks, pages uint64
	retryAt      time.Time
}

func (u *resourceUnits) apply(ctx context.Context, shell Shell, m *session.Resources) {
	if m.Identity == nil {
		return
	}
	now := time.Now()
	if u.boot != m.Identity.BootID {
		*u = resourceUnits{boot: m.Identity.BootID}
	}
	if (u.ticks == 0 || u.pages == 0) && !now.Before(u.retryAt) && ctx.Err() == nil {
		u.retryAt = now.Add(time.Minute)
		if raw, err := shell.Shell(ctx, resourceUnitsCommand); err == nil {
			lines := strings.Split(strings.TrimSpace(raw), "\n")
			if len(lines) == 4 && lines[0] == u.boot && lines[3] == u.boot {
				ticks := parseUnit(lines[1], "ticks ")
				pages := parseUnit(lines[2], "pages ")
				if session.ValidClockTicks(ticks) {
					u.ticks = ticks
				}
				if session.ValidPageSize(pages) {
					u.pages = pages
				}
			}
		}
	}
	m.ClockTicksPerSecond, m.PageSizeBytes = u.ticks, u.pages
	m.CPUUnavailable, m.RSSUnavailable = m.CPUInputIssue(), m.RSSInputIssue()
}

func parseUnit(line, prefix string) uint64 {
	value, ok := strings.CutPrefix(line, prefix)
	if !ok {
		return 0
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
