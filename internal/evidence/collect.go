package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"droidsleuth/internal/session"
)

const LogcatCommand = `logcat -d -t 512 -v threadtime -v epoch -v uid -v usec -b main -b crash -b system -b events 'AndroidRuntime:E' 'DEBUG:F' 'am_crash:I' 'am_anr:I' '*:S'`

const (
	crashTailBoundary = "\n--------- droidsleuth crash tail\n"
	crashTailFailure  = "\n--------- droidsleuth crash tail failed\n"
)

type shell interface {
	Shell(context.Context, string) (string, error)
}

// Start has two bounded polling workers and a bounded backpressure queue. Only
// the caller writes SQLite. Each ADB call inherits cancellation. Polls overlap
// finite tails; they do not claim lossless streaming or an empty-history proof.
func Start(ctx context.Context, s shell, meta session.Metadata) (<-chan session.Record, func()) {
	ctx, cancel := context.WithCancel(ctx)
	out := make(chan session.Record, 16)
	var wg sync.WaitGroup
	for _, source := range []string{"logcat", "exit_history"} {
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			seen := make(map[[32]byte]bool)
			var order [][32]byte
			emit := func(r session.Record) bool {
				r.Source = source
				select {
				case out <- r:
					return true
				case <-ctx.Done():
					return false
				}
			}
			gap := func(message string, begin time.Time) bool {
				return emit(session.Record{Kind: "gap", Error: message, StartedUTC: begin, EndedUTC: time.Now()})
			}
			if !gap("collection begins with finite retained buffers; earlier coverage unknown", time.Now()) {
				return
			}
			previous := ""
			interval := 3 * time.Second
			if source == "exit_history" {
				interval = 15 * time.Second
			}
			for ctx.Err() == nil {
				begin := time.Now()
				var observations []session.Evidence
				var issue string
				if source == "logcat" {
					observations, issue = readLogcat(ctx, s, meta, &previous)
					observations, issue = boundObservations(observations, issue)
				} else {
					observations, issue = ReadExitHistory(ctx, s, meta)
				}
				if !emit(session.Record{Kind: "source_status", SourceStatus: &session.SourceStatus{ReadSucceeded: issue == "", SelectedObservations: len(observations)}, StartedUTC: begin, EndedUTC: time.Now()}) {
					return
				}
				if issue != "" && !gap(issue, begin) {
					return
				}
				for _, o := range observations {
					// History slot numbers shift; semantic event keys remain stable. Keep
					// differing reported reasons rather than overwriting contradictory reports.
					keyText := o.Text
					if source == "exit_history" {
						keyText = exitStart.ReplaceAllString(keyText, "")
					}
					body, _ := json.Marshal(o.Events)
					key := sha256.Sum256(append(body, []byte(keyText)...))
					if seen[key] {
						continue
					}
					if len(order) == 512 {
						delete(seen, order[0])
						order = order[1:]
					}
					seen[key] = true
					order = append(order, key)
					if !emit(session.Record{Kind: "evidence", Evidence: &o, StartedUTC: begin, EndedUTC: time.Now()}) {
						return
					}
					if o.Truncated && !gap("selected diagnostic excerpt truncated", begin) {
						return
					}
				}
				timer := time.NewTimer(interval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}(source)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(out); close(done) }()
	return out, func() { cancel(); <-done }
}

// readLogcat keeps the crash buffer's numeric tail independent of traffic in
// the other buffers. Both bounded reads share one ADB call and boot bracket.
func readLogcat(ctx context.Context, s shell, meta session.Metadata, previous *string) ([]session.Evidence, string) {
	boot, err := s.Shell(ctx, "cat /proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, "logcat boot identity unavailable"
	}
	command := LogcatCommand
	numeric := true
	if last, ok := decode(*previous); ok && last.ns > 1000 {
		cursor := last.ns - 1000
		command = strings.Replace(command, "-t 512", fmt.Sprintf("-T '%d.%06d'", cursor/1e9, (cursor%1e9)/1000), 1)
		numeric = false
	}
	if numeric {
		crash := strings.Replace(LogcatCommand, "-b main -b crash -b system -b events", "-b crash", 1)
		command += ` && printf '\n--------- droidsleuth crash tail\n' && { ` + crash + ` || printf '\n--------- droidsleuth crash tail failed\n'; }`
	}
	raw, err := s.Shell(ctx, command)
	if err != nil {
		*previous = ""
		return nil, "logcat read failed or disconnected"
	}
	endBoot, err := s.Shell(ctx, "cat /proc/sys/kernel/random/boot_id")
	boot = strings.TrimSpace(boot)
	if err != nil || boot == "" || strings.TrimSpace(endBoot) != boot {
		*previous = ""
		return nil, "boot changed or became unavailable during logcat read"
	}
	issue := ""
	crashFailed := false
	reads := []string{raw}
	if numeric {
		merged, crash, ok := strings.Cut(raw, crashTailBoundary)
		if !ok || strings.Contains(crash, crashTailBoundary) {
			*previous = ""
			return nil, "logcat crash tail framing incomplete"
		}
		if strings.HasSuffix(crash, crashTailFailure) {
			reads = []string{merged}
			crashFailed = true
		} else {
			reads = []string{merged, crash}
		}
	}
	marker := ""
	var observations []session.Evidence
	for i, read := range reads {
		lines := strings.Split(strings.TrimSuffix(read, "\n"), "\n")
		if !numeric && *previous != "" && !slices.Contains(lines, *previous) {
			issue = "logcat tail overlap lost: rollover, truncation or source reset"
		}
		if numeric && len(lines) >= 512 {
			issue = "logcat bounded tail saturated; records may be lost"
		}
		found, malformed := Logcat(read, meta, boot)
		if malformed {
			issue = "logcat malformed or oversized records; diagnostic coverage incomplete"
		}
		if i == 0 {
			observations = found
		} else {
			// Identical source text from this boot normalizes to the same event.
			seen := make(map[string]struct{}, len(observations))
			for _, observation := range observations {
				seen[observation.Text] = struct{}{}
			}
			for _, observation := range found {
				if _, exists := seen[observation.Text]; !exists {
					observations = append(observations, observation)
				}
			}
		}
		// Keep the merged marker when available. The crash copy can seed a
		// cursor if the merged read yields no decodable records.
		if marker == "" {
			for i := len(lines) - 1; i >= 0; i-- {
				if _, ok := decode(lines[i]); ok {
					marker = lines[i]
					break
				}
			}
		}
	}
	if crashFailed {
		if issue != "" {
			issue += ". "
		}
		issue += "logcat crash-only tail read failed"
	}
	*previous = marker
	return observations, issue
}

// ReadExitHistory retrieves one bounded batch for a previously validated target.
// A timezone failure leaves occurrence time unknown while retaining the report.
func ReadExitHistory(ctx context.Context, s shell, meta session.Metadata) ([]session.Evidence, string) {
	zone, zoneErr := s.Shell(ctx, "getprop persist.sys.timezone")
	raw, err := s.Shell(ctx, "dumpsys activity exit-info "+meta.Package)
	if err != nil {
		return nil, "exit-history read failed or disconnected"
	}
	if zoneErr != nil {
		zone = ""
	}
	observations, err := ExitHistory(raw, meta, strings.TrimSpace(zone))
	if err != nil {
		return nil, err.Error()
	}
	return boundObservations(observations, "")
}

func boundObservations(observations []session.Evidence, issue string) ([]session.Evidence, string) {
	// Reserve room for the writer's sequence and collection-time envelope.
	bounded := observations[:0]
	for _, observation := range observations {
		body, err := json.Marshal(observation)
		if err != nil || len(body) > 60<<10 {
			issue = "normalized evidence exceeds observation budget; coverage incomplete"
			continue
		}
		bounded = append(bounded, observation)
	}
	if len(bounded) > 64 {
		bounded = bounded[:64]
		issue = "selected diagnostic observation limit exceeded; coverage incomplete"
	}
	return bounded, issue
}
