// Package capture collects and tracks main-process observations.
package capture

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"droidsleuth/internal/session"
)

// Shell is the subprocess boundary used by the collector and its fixtures.
type Shell interface {
	Shell(context.Context, string) (string, error)
}

var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
var bootID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ValidPackage(name string) bool { return packageName.MatchString(name) }

func Probe(ctx context.Context, shell Shell, pkg string, user int) (session.Metadata, error) {
	meta := session.Metadata{Package: pkg}
	if !ValidPackage(pkg) {
		return meta, errors.New("invalid package name")
	}
	if user < -1 {
		return meta, errors.New("invalid Android user ID")
	}
	out, err := shell.Shell(ctx, "getprop ro.build.version.sdk")
	if err != nil {
		return meta, err
	}
	meta.SDK, err = strconv.Atoi(strings.TrimSpace(out))
	if err != nil || meta.SDK < 30 {
		return meta, errors.New("Android API 30+ is required")
	}
	if user == -1 {
		out, err = shell.Shell(ctx, "am get-current-user")
		if err != nil {
			return meta, err
		}
		user, err = strconv.Atoi(strings.TrimSpace(out))
		if err != nil || user < 0 {
			return meta, errors.New("cannot resolve current Android user")
		}
	}
	meta.User = user
	out, err = shell.Shell(ctx, fmt.Sprintf("cmd package list packages -U --user %d %s", user, pkg))
	if err != nil {
		return meta, err
	}
	found := false
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "package:"+pkg {
			continue
		}
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "uid:") || found {
			return meta, errors.New("ambiguous package UID response")
		}
		meta.UID, err = strconv.Atoi(strings.TrimPrefix(fields[1], "uid:"))
		if err != nil || meta.UID < 0 || meta.UID/100000 != user {
			return meta, errors.New("invalid package UID for Android user")
		}
		found = true
	}
	if !found {
		return meta, errors.New("package is not installed for the selected user")
	}
	return meta, nil
}

// SnapshotCommand performs one bounded, batched shell invocation. The process
// list and stat reads are observations across an interval, not an atomic census.
func SnapshotCommand(pkg string, uid int) (string, error) {
	if !ValidPackage(pkg) || uid < 0 {
		return "", errors.New("invalid snapshot target")
	}
	// Android mksh's printf is external. Raw print and bulk file substitution
	// avoid repeated execs; shells without those fast paths retain safe fallbacks.
	return fmt.Sprintf(`set -e
if ds_printer=$(command -v print) && [ "$ds_printer" = print ]; then
  ds_print() { print -r -- "$1"; }
else
  ds_print() { printf '%%s\n' "$1"; }
fi
ds_read() {
  ds_value=$(< "$1") || return 1
  if [ -z "$ds_value" ]; then ds_value=$(cat "$1") || return 1; fi
  [ -n "$ds_value" ]
}
ds_ticks() {
  ds_fields=${1##*) }
  set -- $ds_fields
  [ "$#" -ge 20 ] || return 1
  shift 19
  ds_start_ticks=$1
}
ds_print '__DS_CLOCK__'
date +%%s
ds_read /proc/uptime || exit 1
ds_print "$ds_value"
ds_print '__DS_BOOT__'
ds_read /proc/sys/kernel/random/boot_id || exit 1
ds_print "$ds_value"
ds_print '__DS_PS__'
ds_ps=$(ps -A -o PID,UID,NAME)
ds_print "$ds_ps"
ds_print '__DS_STAT__'
ds_print "$ds_ps" | while read -r ds_pid ds_uid ds_name; do
  if [ "$ds_uid" = '%d' ] && [ "$ds_name" = '%s' ]; then
    case "$ds_pid" in ''|*[!0-9]*) exit 1;; esac
    ds_read "/proc/$ds_pid/stat" || exit 1
    ds_before=$ds_value
    if IFS= read -r -d '' ds_actual_name < "/proc/$ds_pid/cmdline" 2>/dev/null; then
      :
    else
      ds_actual_name=$(tr '\000' '\n' < "/proc/$ds_pid/cmdline" | head -n 1)
    fi
    ds_actual_uid=$(sed -n 's/^Uid:[[:space:]]*\([0-9]*\).*/\1/p' "/proc/$ds_pid/status")
    [ "$ds_actual_name" = "$ds_name" ] && [ "$ds_actual_uid" = "$ds_uid" ] || exit 1
    ds_read "/proc/$ds_pid/stat" || exit 1
    ds_after=$ds_value
    ds_ticks "$ds_before" || exit 1
    ds_start_before=$ds_start_ticks
    ds_ticks "$ds_after" || exit 1
    [ "$ds_start_before" = "$ds_start_ticks" ] || exit 1
    ds_print "$ds_after"
  fi
done
ds_print '__DS_END__'
`, uid, pkg), nil
}

func ParseSnapshot(out, pkg string, uid int) (session.Snapshot, error) {
	var snapshot session.Snapshot
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 9 || lines[0] != "__DS_CLOCK__" || lines[3] != "__DS_BOOT__" || lines[5] != "__DS_PS__" || lines[len(lines)-1] != "__DS_END__" {
		return snapshot, errors.New("incomplete snapshot framing")
	}
	var err error
	snapshot.DeviceEpochSeconds, err = strconv.ParseInt(lines[1], 10, 64)
	if err != nil || snapshot.DeviceEpochSeconds <= 0 {
		return snapshot, errors.New("invalid device realtime")
	}
	uptime := strings.Fields(lines[2])
	if len(uptime) != 2 {
		return snapshot, errors.New("invalid device uptime")
	}
	snapshot.DeviceUptimeSeconds, err = strconv.ParseFloat(uptime[0], 64)
	if err != nil || !(snapshot.DeviceUptimeSeconds >= 0) || snapshot.DeviceUptimeSeconds > 1e12 {
		return snapshot, errors.New("invalid device uptime")
	}
	snapshot.BootID = lines[4]
	if !bootID.MatchString(snapshot.BootID) {
		return snapshot, errors.New("boot ID is unavailable")
	}
	if strings.Join(strings.Fields(lines[6]), " ") != "PID UID NAME" {
		return snapshot, errors.New("unsupported ps columns")
	}
	processes := make(map[int]session.Process)
	seen := make(map[int]bool)
	statAt := -1
	for i := 7; i < len(lines)-1; i++ {
		if lines[i] == "__DS_STAT__" {
			statAt = i
			break
		}
		fields := strings.Fields(lines[i])
		if len(fields) < 3 {
			return snapshot, errors.New("incomplete ps row")
		}
		pid, pErr := strconv.Atoi(fields[0])
		rowUID, uErr := strconv.Atoi(fields[1])
		if pErr != nil || uErr != nil || pid <= 0 || rowUID < 0 || seen[pid] {
			return snapshot, errors.New("invalid or duplicate ps identity")
		}
		seen[pid] = true
		name := strings.Join(fields[2:], " ")
		if name != pkg {
			continue
		}
		// Never silently interpret a changed package UID as target absence.
		if rowUID != uid {
			if rowUID/100000 == uid/100000 {
				return snapshot, errors.New("target UID changed; start a new recording")
			}
			continue
		}
		processes[pid] = session.Process{Identity: session.Identity{BootID: snapshot.BootID, UID: uid, PID: pid}, Name: pkg}
	}
	if statAt == -1 {
		return snapshot, errors.New("missing stat section")
	}
	stats := make(map[int]uint64)
	for _, line := range lines[statAt+1 : len(lines)-1] {
		pid, ticks, err := ParseStat(line)
		if err != nil {
			return snapshot, err
		}
		if _, ok := processes[pid]; !ok {
			return snapshot, errors.New("stat does not match target process list")
		}
		if _, ok := stats[pid]; ok {
			return snapshot, errors.New("duplicate stat identity")
		}
		stats[pid] = ticks
	}
	snapshot.Processes = []session.Process{}
	// Preserve process-list order for reproducible recordings.
	for _, line := range lines[7:statAt] {
		fields := strings.Fields(line)
		pid, _ := strconv.Atoi(fields[0])
		p, ok := processes[pid]
		if !ok {
			continue
		}
		ticks, ok := stats[pid]
		if !ok {
			return snapshot, errors.New("target was listed but start ticks are unavailable")
		}
		p.StartTicks = ticks
		snapshot.Processes = append(snapshot.Processes, p)
	}
	return snapshot, nil
}

// ParseStat reads field 22 after the final ')' because comm may contain spaces
// and parentheses. Start ticks are an identity token, never converted to time.
func ParseStat(line string) (int, uint64, error) {
	open, close := strings.IndexByte(line, '('), strings.LastIndexByte(line, ')')
	if open < 1 || close < open {
		return 0, 0, errors.New("malformed process stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil || pid <= 0 {
		return 0, 0, errors.New("invalid stat PID")
	}
	fields := strings.Fields(line[close+1:])
	if len(fields) < 20 || len(fields[0]) != 1 {
		return 0, 0, errors.New("truncated process stat")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return 0, 0, errors.New("invalid process start ticks")
	}
	return pid, ticks, nil
}
