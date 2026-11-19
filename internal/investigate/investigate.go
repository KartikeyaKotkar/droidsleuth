// Package investigate reconstructs reported incidents without live device access.
package investigate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"droidsleuth/internal/session"
)

const limit = 4096

type Reference struct {
	Sequence  int64 `json:"sequence"`
	FirstLine int   `json:"first_line"`
	LastLine  int   `json:"last_line"`
}
type Finding struct {
	Excerpt          string             `json:"source_excerpt"`
	ExcerptTruncated bool               `json:"excerpt_truncated,omitempty"`
	Event            session.Event      `json:"reported_event"`
	Source           string             `json:"source"`
	Evidence         []Reference        `json:"evidence"`
	Candidates       []session.Identity `json:"candidate_instances,omitempty"`
	Uncertainty      []string           `json:"uncertainty"`
	Conflict         bool               `json:"conflicting_exit_reasons,omitempty"`
}
type Report struct {
	Version        int              `json:"version"`
	Package        string           `json:"package"`
	Findings       []Finding        `json:"findings"`
	Gaps           []session.Record `json:"collection_gaps"`
	Limitations    []string         `json:"limitations"`
	Disappearances int              `json:"unexplained_disappearances"`
}

func eventKey(source string, event session.Event) string {
	event.FirstLine, event.LastLine = 0, 0
	body, _ := json.Marshal(struct {
		Source string
		Event  session.Event
	}{source, event})
	return string(body)
}

// Analyze uses two bounded read-only passes. Timestamps identify reported events,
// never an inferred death time. Even a unique PID/UID/name match remains only a
// candidate: neither logcat nor exit history supplies kernel start ticks.
func Analyze(ctx context.Context, path string) (Report, error) {
	return analyze(func(header func(session.Metadata) error, visit func(session.Record) error) error {
		return session.Replay(ctx, path, header, visit)
	})
}

// AnalyzeRecording shares the investigation rules with a finalized session reader.
func AnalyzeRecording(ctx context.Context, recording session.Recording) (Report, error) {
	return analyze(func(header func(session.Metadata) error, visit func(session.Record) error) error {
		if err := header(recording.Metadata); err != nil {
			return err
		}
		return recording.Scan(ctx, visit)
	})
}

func analyze(replay func(func(session.Metadata) error, func(session.Record) error) error) (Report, error) {
	r := Report{Version: 1, Findings: []Finding{}, Gaps: []session.Record{}, Limitations: []string{
		"Reports describe source claims, not proven causes of observed disappearance.",
		"Logcat is a finite filtered tail; silent buffer loss and incidents before collection may be unobservable.",
		"Source realtime timestamps are distinct from host collection bounds; clock changes and delayed records may prevent instance association.",
	}}
	type instance struct {
		p           session.Process
		first, last int64
	}
	instances := map[session.Identity]*instance{}
	capped := false
	err := replay(func(m session.Metadata) error {
		r.Package = m.Package
		if m.Schema == 1 {
			r.Limitations = append(r.Limitations, "Schema 1 contains no diagnostic collectors.")
		}
		return nil
	}, func(record session.Record) error {
		if record.Snapshot != nil {
			for _, p := range record.Snapshot.Processes {
				v := instances[p.Identity]
				if v == nil {
					if len(instances) >= limit {
						capped = true
						continue
					}
					v = &instance{p: p, first: record.Snapshot.DeviceEpochSeconds}
					instances[p.Identity] = v
				}
				v.last = record.Snapshot.DeviceEpochSeconds
			}
		}
		for _, t := range record.Transitions {
			if t.Kind == "no_longer_observed" {
				r.Disappearances++
			}
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	identities := make([]session.Identity, 0, len(instances))
	for id := range instances {
		identities = append(identities, id)
	}
	sort.Slice(identities, func(i, j int) bool {
		a, b := identities[i], identities[j]
		if a.BootID != b.BootID {
			return a.BootID < b.BootID
		}
		if a.PID != b.PID {
			return a.PID < b.PID
		}
		if a.UID != b.UID {
			return a.UID < b.UID
		}
		return a.StartTicks < b.StartTicks
	})
	keys := map[string]int{}
	err = replay(func(session.Metadata) error { return nil }, func(record session.Record) error {
		if record.Kind == "gap" {
			if len(r.Gaps) < 256 {
				r.Gaps = append(r.Gaps, record)
			} else {
				capped = true
			}
			return nil
		}
		if record.Evidence == nil {
			return nil
		}
		for _, event := range record.Evidence.Events {
			key := eventKey(record.Source, event)
			ref := Reference{record.Sequence, event.FirstLine, event.LastLine}
			if index, ok := keys[key]; ok {
				if len(r.Findings[index].Evidence) < 16 {
					r.Findings[index].Evidence = append(r.Findings[index].Evidence, ref)
				} else {
					capped = true
				}
				continue
			}
			if len(r.Findings) >= limit {
				capped = true
				continue
			}
			excerpt := record.Evidence.Text
			short := len(excerpt) > 2048
			if short {
				excerpt = excerpt[:2048]
			}
			f := Finding{Excerpt: excerpt, ExcerptTruncated: short || record.Evidence.Truncated, Event: event, Source: record.Source, Evidence: []Reference{ref}, Uncertainty: []string{"Source does not provide kernel start ticks; exact process instance is unproven."}}
			for _, id := range identities {
				v := instances[id]
				if event.PID != id.PID || event.Process != v.p.Name || (event.UID != nil && *event.UID != id.UID) || (event.User != nil && *event.User != id.UID/100000) || (event.BootID != "" && event.BootID != id.BootID) {
					continue
				}
				// Only suggest instances actually observed at the source timestamp, with
				// second-resolution bounds. Later exit reports deliberately remain unbound.
				sec := event.DeviceTimeNS / 1e9
				if event.DeviceTimeNS != 0 && sec >= v.first && sec <= v.last {
					if len(f.Candidates) < 16 {
						f.Candidates = append(f.Candidates, id)
					} else {
						capped = true
					}
				}
			}
			sort.Slice(f.Candidates, func(i, j int) bool {
				a, b := f.Candidates[i], f.Candidates[j]
				if a.BootID != b.BootID {
					return a.BootID < b.BootID
				}
				if a.StartTicks != b.StartTicks {
					return a.StartTicks < b.StartTicks
				}
				return a.UID < b.UID
			})
			if len(f.Candidates) != 1 {
				f.Uncertainty = append(f.Uncertainty, "No unique observed instance at reported time; PID reuse, delayed records or missing snapshots remain possible.")
			}
			if event.DeviceTimeNS == 0 {
				f.Uncertainty = append(f.Uncertainty, "Source timestamp could not be placed on device realtime.")
			}
			if event.Kind == "exit_report" {
				f.Uncertainty = append(f.Uncertainty, "Exit history may predate recording or device boot; retrieval boot is not event boot.")
			}
			keys[key] = len(r.Findings)
			r.Findings = append(r.Findings, f)
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	// Only different exit reasons for the same reported exit conflict. An ANR
	// followed by a crash is not intrinsically contradictory.
	exits := map[string][]int{}
	for i, f := range r.Findings {
		e := f.Event
		if e.Kind == "exit_report" && e.DeviceTimeNS != 0 {
			uid := -1
			if e.UID != nil {
				uid = *e.UID
			}
			key := fmt.Sprintf("%d/%d/%s/%d", uid, e.PID, e.Process, e.DeviceTimeNS)
			exits[key] = append(exits[key], i)
		}
	}
	for _, indices := range exits {
		for _, i := range indices {
			for _, j := range indices {
				a, b := r.Findings[i].Event.Reason, r.Findings[j].Event.Reason
				if a != nil && b != nil && *a != *b {
					r.Findings[i].Conflict = true
					r.Findings[i].Uncertainty = append(r.Findings[i].Uncertainty, "Conflicting reported exit reasons; no single cause selected.")
					break
				}
			}
		}
	}
	if capped {
		r.Limitations = append(r.Limitations, "Analysis bounds reached; findings, references, gaps or instance candidates are incomplete.")
	}
	return r, nil
}
