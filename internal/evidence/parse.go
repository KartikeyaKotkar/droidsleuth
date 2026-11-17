// Package evidence decodes bounded Android diagnostic observations. A report is
// evidence of what Android reported, not proof of a particular kernel instance.
package evidence

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"droidsleuth/internal/session"
)

const TextLimit = 8192

var header = regexp.MustCompile(`^\s*([0-9]+\.[0-9]+)\s+(\S+)\s+([0-9]+)\s+([0-9]+)\s+([VDIWEFAS])\s+([^:]+):\s?(.*)$`)
var androidUID = regexp.MustCompile(`^u([0-9]+)_([ai])([0-9]+)$`)
var processLine = regexp.MustCompile(`^Process: ([^,]+), PID: ([0-9]+)$`)
var nativeProcess = regexp.MustCompile(`pid: ([0-9]+), (?:ppid: [0-9]+, )?tid: [0-9]+, name: .* >>> ([^ ]+) <<<'?`)
var nativeSignal = regexp.MustCompile(`\bsignal [1-9][0-9]* \(SIG[A-Z0-9]+\)`)
var nativeUID = regexp.MustCompile(`\buid: ([0-9]+)`)

func UID(s string) (int, bool) {
	if s == "root" {
		return 0, true
	}
	if s == "system" {
		return 1000, true
	}
	if s == "shell" {
		return 2000, true
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return n, true
	}
	m := androidUID.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	user, _ := strconv.Atoi(m[1])
	n, _ := strconv.Atoi(m[3])
	base := 10000
	if m[2] == "i" {
		base = 99000
	}
	return user*100000 + base + n, true
}

func selected(name, pkg string) bool { return name == pkg || strings.HasPrefix(name, pkg+":") }
func epoch(s string) int64 {
	parts := strings.SplitN(s, ".", 2)
	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || sec > 9000000000 {
		return 0
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 9 {
		return 0
	}
	frac += strings.Repeat("0", 9-len(frac))
	ns, _ := strconv.ParseInt(frac, 10, 64)
	return sec*1e9 + ns
}

type line struct {
	raw, body, tag string
	pid, uid       int
	known          bool
	ns             int64
}

func decode(raw string) (line, bool) {
	m := header.FindStringSubmatch(raw)
	if m == nil {
		return line{}, false
	}
	pid, _ := strconv.Atoi(m[3])
	uid, known := UID(m[2])
	return line{raw: raw, body: m[7], tag: strings.TrimSpace(m[6]), pid: pid, uid: uid, known: known, ns: epoch(m[1])}, true
}
func observation(format, text string, event session.Event) session.Evidence {
	truncated := len(text) > TextLimit
	if truncated {
		text = text[:TextLimit]
	}
	event.FirstLine = 1
	event.LastLine = strings.Count(text, "\n") + 1
	return session.Evidence{Format: format, Text: text, Events: []session.Event{event}, Truncated: truncated}
}

// Logcat accepts epoch/usec/uid/threadtime output only. Unscoped and unrelated
// records are discarded before persistence, including another user's package.
// Blocks require an explicit process name; a log header PID alone is insufficient.
func Logcat(text string, meta session.Metadata, boot string) ([]session.Evidence, bool) {
	var result []session.Evidence
	groups := map[string][]line{}
	generations := map[string]int{}
	var order []string
	malformed := false
	for _, raw := range strings.Split(text, "\n") {
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(raw, "---------") {
			continue
		}
		l, ok := decode(raw)
		if !ok {
			malformed = true
			continue
		}
		if l.tag == "am_crash" || l.tag == "am_anr" {
			if !l.known || (l.uid != 1000 && l.uid != 0) {
				continue
			}
			fields := strings.SplitN(strings.Trim(l.body, "[]"), ",", 5)
			if len(fields) < 5 {
				malformed = true
				continue
			}
			user, e1 := strconv.Atoi(strings.TrimSpace(fields[0]))
			pid, e2 := strconv.Atoi(strings.TrimSpace(fields[1]))
			name := strings.TrimSpace(fields[2])
			if e1 != nil || e2 != nil || pid <= 0 {
				malformed = true
				continue
			}
			if user != meta.User || !selected(name, meta.Package) {
				continue
			}
			kind := "crash_report"
			if l.tag == "am_anr" {
				kind = "anr_report"
			}
			result = append(result, observation("logcat-epoch-uid", raw, session.Event{Kind: kind, Process: name, PID: pid, User: &user, DeviceTimeNS: l.ns, TimeUncertaintyNS: 1000, BootID: boot, Detail: "Android event-buffer report"}))
			continue
		}
		if l.tag != "AndroidRuntime" && l.tag != "DEBUG" {
			continue
		}
		base := fmt.Sprintf("%s/%d", l.tag, l.pid)
		if l.tag == "AndroidRuntime" && (!l.known || l.uid != meta.UID) {
			continue
		}
		if strings.HasPrefix(l.body, "FATAL EXCEPTION:") || (l.tag == "DEBUG" && strings.HasPrefix(l.body, "*** ***")) {
			generations[base]++
		}
		key := fmt.Sprintf("%s/%d", base, generations[base])
		if _, exists := groups[key]; !exists {
			if len(order) >= 16 {
				malformed = true
				continue
			}
			order = append(order, key)
		}
		group := groups[key]
		size := 0
		for _, old := range group {
			size += len(old.raw) + 1
		}
		if size+len(raw) > TextLimit || len(group) >= 128 {
			malformed = true
			continue
		}
		groups[key] = append(group, l)
	}
	for _, key := range order {
		group := groups[key]
		if len(group) == 0 {
			continue
		}
		first := group[0]
		name := ""
		pid := 0
		uid := 0
		known := false
		fatal := false
		signal := false
		conflicting := false
		kind := "java_fatal"
		var raws []string
		for _, l := range group {
			raws = append(raws, l.raw)
			if first.tag == "AndroidRuntime" {
				if strings.HasPrefix(l.body, "FATAL EXCEPTION:") {
					fatal = true
				}
				if m := processLine.FindStringSubmatch(l.body); m != nil {
					nextPID, _ := strconv.Atoi(m[2])
					if name != "" && (name != m[1] || pid != nextPID) {
						conflicting = true
					}
					name = m[1]
					pid = nextPID
					uid = l.uid
					known = l.known && pid == l.pid
				}
			} else {
				kind = "native_crash"
				if nativeSignal.MatchString(l.body) {
					signal = true
				}
				if m := nativeProcess.FindStringSubmatch(l.body); m != nil {
					nextPID, _ := strconv.Atoi(m[1])
					if name != "" && (name != m[2] || pid != nextPID) {
						conflicting = true
					}
					pid = nextPID
					name = m[2]
					fatal = true
				}
				if m := nativeUID.FindStringSubmatch(l.body); m != nil {
					uid, _ = strconv.Atoi(m[1])
					known = true
				}
			}
		}
		if first.tag == "AndroidRuntime" && fatal && (name == "" || !known) {
			malformed = true
		}
		if kind == "native_crash" && signal && selected(name, meta.Package) && !known {
			malformed = true
		}
		if conflicting {
			malformed = true
			continue
		}
		if fatal && (kind != "native_crash" || (signal && first.known && (first.uid == meta.UID || first.uid == 0 || first.uid == 1000))) && known && uid == meta.UID && selected(name, meta.Package) && pid > 0 {
			result = append(result, observation("logcat-epoch-uid", strings.Join(raws, "\n"), session.Event{Kind: kind, Process: name, PID: pid, UID: &uid, DeviceTimeNS: first.ns, TimeUncertaintyNS: 1000, BootID: boot, Detail: "Explicit diagnostic report; instance identity not supplied by logcat"}))
		}
	}
	return result, malformed
}

var exitStart = regexp.MustCompile(`(?m)^\s*ApplicationExitInfo #\d+:?`)
var fields = regexp.MustCompile(`(?:^|\s)([A-Za-z]+)=`)

func field(block, key string) string {
	indices := fields.FindAllStringSubmatchIndex(block, -1)
	for i, m := range indices {
		if block[m[2]:m[3]] == key {
			end := len(block)
			if i+1 < len(indices) {
				end = indices[i+1][0]
			}
			return strings.TrimSpace(block[m[1]:end])
		}
	}
	return ""
}
func number(s string) (int, bool) {
	v := strings.Fields(s)
	if len(v) == 0 {
		return 0, false
	}
	n, e := strconv.Atoi(v[0])
	return n, e == nil
}

// ExitHistory handles the public dumpsys text surface, not private trace files.
// OEM formats not recognized here are a coverage gap rather than empty history.
func ExitHistory(text string, meta session.Metadata, zone string) ([]session.Evidence, error) {
	if !strings.Contains(text, "ACTIVITY MANAGER PROCESS EXIT INFO") || strings.Contains(text, "Permission Denial") {
		return nil, fmt.Errorf("exit-history unavailable or unrecognized")
	}
	starts := exitStart.FindAllStringIndex(text, -1)
	if len(starts) > 128 {
		return nil, fmt.Errorf("exit-history entry limit exceeded")
	}
	var out []session.Evidence
	for i, start := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := text[start[0]:end]
		name := field(block, "process")
		if name == "" {
			name = field(block, "processName")
		}
		if name == "" {
			return nil, fmt.Errorf("unrecognized exit-history process field")
		}
		if !selected(name, meta.Package) {
			continue
		}
		uid, ok := number(field(block, "realUid"))
		if !ok {
			return nil, fmt.Errorf("exit-history UID missing")
		}
		if uid != meta.UID {
			continue
		}
		pid, pok := number(field(block, "pid"))
		reason, rok := number(field(block, "reason"))
		if !pok || !rok || pid <= 0 {
			return nil, fmt.Errorf("malformed selected exit-history entry")
		}
		rawTime := field(block, "timestamp")
		var ns int64
		if loc, err := time.LoadLocation(zone); err == nil {
			if t, e := time.ParseInLocation("2006-01-02 15:04:05.000", rawTime, loc); e == nil {
				ns = unambiguousLocalTime(t, rawTime, loc)
			}
		}
		// Retrieval boot is deliberately absent: exit history can survive a reboot.
		out = append(out, observation("activity-exit-info", strings.TrimSpace(block), session.Event{Kind: "exit_report", Process: name, PID: pid, UID: &uid, DeviceTimeNS: ns, TimeUncertaintyNS: int64(time.Millisecond), RawTime: rawTime, Reason: &reason, Detail: field(block, "reason")}))
	}
	// A nonempty unrecognized layout must not masquerade as a supported empty ring.
	if len(starts) == 0 && strings.Contains(text, "reason=") {
		return nil, fmt.Errorf("unrecognized exit-history layout")
	}
	return out, nil
}

// Dumpsys renders local wall time without an offset. During a DST fold it can
// name two instants; retain raw text and leave occurrence time unknown then.
func unambiguousLocalTime(t time.Time, raw string, loc *time.Location) int64 {
	const layout = "2006-01-02 15:04:05.000"
	wall, err := time.Parse(layout, raw)
	if err != nil {
		return 0
	}
	offsets := map[int]bool{}
	for h := -36; h <= 36; h++ {
		_, offset := t.Add(time.Duration(h) * time.Hour).Zone()
		offsets[offset] = true
	}
	var result int64
	matches := 0
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		if candidate.In(loc).Format(layout) == raw {
			result = candidate.UnixNano()
			matches++
		}
	}
	if matches != 1 {
		return 0
	}
	return result
}
