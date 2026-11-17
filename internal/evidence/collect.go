package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"droidsleuth/internal/session"
)

const LogcatCommand = `logcat -d -t 512 -v threadtime -v epoch -v uid -v usec -b main -b crash -b system -b events 'AndroidRuntime:E' 'DEBUG:F' 'am_crash:I' 'am_anr:I' '*:S'`

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
				return emit(session.Record{Kind: "gap", Error: message, StartedUTC: begin.UTC(), EndedUTC: time.Now().UTC()})
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
					boot, err := s.Shell(ctx, "cat /proc/sys/kernel/random/boot_id")
					if err != nil {
						issue = "logcat boot identity unavailable"
					} else {
						raw, err := s.Shell(ctx, LogcatCommand)
						if err != nil {
							issue = "logcat read failed or disconnected"
							previous = ""
						} else {
							lines := strings.Split(strings.TrimSpace(raw), "\n")
							if previous != "" && !strings.Contains(raw, previous) {
								issue = "logcat tail overlap lost: rollover, truncation or source reset"
							}
							if len(lines) >= 512 {
								issue = "logcat bounded tail saturated; records may be lost"
							}
							var bad bool
							observations, bad = Logcat(raw, meta, strings.TrimSpace(boot))
							if bad {
								issue = "logcat malformed or oversized records; diagnostic coverage incomplete"
							}
							previous = ""
							for i := len(lines) - 1; i >= 0; i-- {
								if _, ok := decode(lines[i]); ok {
									previous = lines[i]
									break
								}
							}
						}
					}
				} else {
					zone, ze := s.Shell(ctx, "getprop persist.sys.timezone")
					raw, err := s.Shell(ctx, "dumpsys activity exit-info "+meta.Package)
					if err != nil {
						issue = "exit-history read failed or disconnected"
					} else {
						if ze != nil {
							zone = ""
						}
						observations, err = ExitHistory(raw, meta, strings.TrimSpace(zone))
						if err != nil {
							issue = err.Error()
						}
					}
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
					if !emit(session.Record{Kind: "evidence", Evidence: &o, StartedUTC: begin.UTC(), EndedUTC: time.Now().UTC()}) {
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
