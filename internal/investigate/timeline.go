package investigate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

const (
	contextLimit   = 100
	MaxContext     = 5 * time.Minute
	DefaultContext = 30 * time.Second
)

// IncidentID identifies an entry in one persisted observation.
type IncidentID struct {
	kind     byte
	sequence int64
	index    int
}

func ParseIncidentID(s string) (IncidentID, error) {
	if len(s) > 1 && (s[0] == 'L' || s[0] == 'E') {
		seq, idx, found := strings.Cut(s[1:], ".")
		n, nErr := strconv.ParseInt(seq, 10, 64)
		i, iErr := strconv.Atoi(idx)
		id := IncidentID{kind: s[0], sequence: n, index: i}
		if found && nErr == nil && iErr == nil && n > 0 && i >= 0 && id.String() == s {
			return id, nil
		}
	}
	return IncidentID{}, fmt.Errorf("invalid incident ID %q; use L<sequence>.<index> or E<sequence>.<index> from the timeline index", s)
}

func (id IncidentID) String() string { return fmt.Sprintf("%c%d.%d", id.kind, id.sequence, id.index) }

type Bounds struct {
	Sequence       int64     `json:"sequence"`
	StartedUTC     time.Time `json:"started_utc"`
	EndedUTC       time.Time `json:"ended_utc"`
	StartElapsedNS int64     `json:"start_elapsed_ns"`
	EndElapsedNS   int64     `json:"end_elapsed_ns"`
}

func bounds(r session.Record) Bounds {
	return Bounds{r.Sequence, r.StartedUTC, r.EndedUTC, r.StartElapsedNS, r.EndElapsedNS}
}

type IncidentSummary struct {
	ID string `json:"id"`
	Bounds
	Kind           string `json:"kind"`
	Source         string `json:"source,omitempty"`
	Process        string `json:"process"`
	PID            int    `json:"pid"`
	LabelTruncated bool   `json:"label_truncated,omitempty"`
}

type SnapshotEndpoint struct {
	Bounds
	Resources           *ResourceObservation `json:"resources,omitempty"`
	BootID              string               `json:"boot_id"`
	DeviceEpochSeconds  int64                `json:"device_epoch_seconds"`
	DeviceUptimeSeconds float64              `json:"device_uptime_seconds"`
	MainProcesses       *[]session.Identity  `json:"main_processes,omitempty"`
}

func endpoint(r session.Record, meta session.Metadata) *SnapshotEndpoint {
	s := r.Snapshot
	e := &SnapshotEndpoint{Bounds: bounds(r), BootID: s.BootID, DeviceEpochSeconds: s.DeviceEpochSeconds, DeviceUptimeSeconds: s.DeviceUptimeSeconds}
	if meta.Schema == session.PrivateSchemaVersion {
		main := []session.Identity{}
		for _, p := range s.Processes {
			if p.Name == meta.Package {
				main = append(main, p.Identity)
			}
		}
		e.MainProcesses = &main
	}
	return e
}

type LifecycleIncident struct {
	Transition   session.Transition `json:"transition"`
	LastPresence *SnapshotEndpoint  `json:"last_presence"`
	FirstAbsence SnapshotEndpoint   `json:"first_observed_absence"`
	SnapshotGaps *int               `json:"snapshot_gaps_between_endpoints"`
}

type TimelineObservation struct {
	Bounds
	Resources      *ResourceObservation  `json:"resources,omitempty"`
	Kind           string                `json:"kind"`
	Source         string                `json:"source,omitempty"`
	Snapshot       *session.Snapshot     `json:"snapshot,omitempty"`
	Transitions    []session.Transition  `json:"transitions,omitempty"`
	SourceStatus   *session.SourceStatus `json:"source_status,omitempty"`
	Error          string                `json:"error,omitempty"`
	Reports        []TimelineFinding     `json:"reports,omitempty"`
	DetailsOmitted bool                  `json:"details_omitted,omitempty"`
}

type TimelineFinding struct {
	ID               string             `json:"id"`
	Event            session.Event      `json:"reported_event"`
	Reference        Reference          `json:"reference"`
	Association      string             `json:"association"`
	Candidates       []session.Identity `json:"candidate_instances,omitempty"`
	Evidence         []Reference        `json:"related_references,omitempty"`
	Uncertainty      []string           `json:"uncertainty,omitempty"`
	Conflict         bool               `json:"conflicting_exit_reasons,omitempty"`
	Excerpt          string             `json:"source_excerpt,omitempty"`
	ExcerptTruncated bool               `json:"excerpt_truncated,omitempty"`
}

type IncidentDetail struct {
	AnchorObservation  TimelineObservation   `json:"anchor_observation"`
	Anchor             IncidentSummary       `json:"anchor"`
	Lifecycle          *LifecycleIncident    `json:"lifecycle,omitempty"`
	Report             *TimelineFinding      `json:"report,omitempty"`
	WindowStartNS      int64                 `json:"window_start_elapsed_ns"`
	WindowEndNS        int64                 `json:"window_end_elapsed_ns"`
	Context            []TimelineObservation `json:"context"`
	BeforeOmitted      int                   `json:"before_omitted"`
	AfterOmitted       int                   `json:"after_omitted"`
	AnalysisIncomplete bool                  `json:"analysis_incomplete"`
}

type TimelineReport struct {
	ReportType     string             `json:"report_type"`
	Version        int                `json:"version"`
	View           string             `json:"view"`
	Recording      string             `json:"recording"`
	Metadata       session.Metadata   `json:"metadata"`
	Incidents      *[]IncidentSummary `json:"incidents,omitempty"`
	IndexTruncated bool               `json:"index_truncated,omitempty"`
	Incident       *IncidentDetail    `json:"incident,omitempty"`
	Limitations    []string           `json:"limitations"`
}

// Timeline reads a finalized recording through one read transaction. Index and
// context ordering express collection order, independent of device wall time.
func Timeline(ctx context.Context, path string, id *IncidentID, before, after time.Duration) (TimelineReport, error) {
	if before < 0 || before > MaxContext || after < 0 || after > MaxContext {
		return TimelineReport{}, errors.New("context durations must be between 0 and 5m")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return TimelineReport{}, err
	}
	report := TimelineReport{ReportType: "incident_timeline", Version: 1, View: "index", Recording: path,
		Limitations: []string{
			"Rows are in recording sequence. Windows use host elapsed collection bounds. Device times describe source reports and may differ from collection time.",
			"Process disappearance has an unknown cause. Nearby diagnostic reports remain separate claims, and candidate instances are unproven.",
			"Logcat may lose records silently. Coverage gaps and unavailable sources can leave incidents unresolved.",
		},
	}
	err = session.WithFinalizedRecording(ctx, path, func(r session.Recording) error {
		report.Metadata = r.Metadata
		if r.Metadata.Schema == 1 {
			report.Limitations = append(report.Limitations, "Schema 1 contains no diagnostic collectors.")
		}
		if r.Metadata.Schema == session.PrivateSchemaVersion {
			report.Limitations = append(report.Limitations, "Lifecycle scope is the main process and package-private names under the verified package UID. Presence at two endpoints does not establish continuous coverage.")
		}
		if id == nil {
			index, truncated, err := timelineIndex(ctx, r)
			report.Incidents, report.IndexTruncated = &index, truncated
			return err
		}
		report.View = "incident"
		detail, err := timelineDetail(ctx, r, *id, before, after)
		report.Incident = detail
		return err
	})
	if err != nil {
		return TimelineReport{}, err
	}
	return report, nil
}

func summary(r session.Record, kind byte, index int, label, name string, pid int) IncidentSummary {
	short := strings.Clone(clip(name, 256))
	return IncidentSummary{ID: IncidentID{kind, r.Sequence, index}.String(), Bounds: bounds(r), Kind: label, Process: short, PID: pid, Source: r.Source, LabelTruncated: len(short) < len(name)}
}

func timelineIndex(ctx context.Context, r session.Recording) ([]IncidentSummary, bool, error) {
	index := []IncidentSummary{}
	seen := map[[sha256.Size]byte]struct{}{}
	truncated := false
	err := r.Scan(ctx, func(row session.Record) error {
		for i, t := range row.Transitions {
			if t.Kind != "no_longer_observed" {
				continue
			}
			if len(index) >= limit {
				truncated = true
				break
			}
			index = append(index, summary(row, 'L', i, t.Kind, t.Process.Name, t.Process.PID))
		}
		if row.Evidence != nil {
			for i, e := range row.Evidence.Events {
				key := fingerprint(row.Source, e)
				if _, ok := seen[key]; ok {
					continue
				}
				if len(index) >= limit {
					truncated = true
					break
				}
				seen[key] = struct{}{}
				index = append(index, summary(row, 'E', i, e.Kind, e.Process, e.PID))
			}
		}
		return nil
	})
	return index, truncated, err
}

func timelineDetail(ctx context.Context, r session.Recording, id IncidentID, before, after time.Duration) (*IncidentDetail, error) {
	row, err := r.ReadRecord(ctx, id.sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, missingAnchor(id)
	}
	if err != nil {
		return nil, err
	}
	d := &IncidentDetail{
		WindowStartNS: max(0, row.StartElapsedNS-before.Nanoseconds()),
		WindowEndNS:   row.EndElapsedNS + min(after.Nanoseconds(), math.MaxInt64-row.EndElapsedNS),
		Context:       []TimelineObservation{},
	}
	switch id.kind {
	case 'L':
		if id.index >= len(row.Transitions) || row.Transitions[id.index].Kind != "no_longer_observed" {
			return nil, missingAnchor(id)
		}
		t := row.Transitions[id.index]
		if row.Snapshot == nil || row.Snapshot.BootID != t.Process.BootID || containsIdentity(row.Snapshot, t.Process.Identity) {
			return nil, fmt.Errorf("incident %s contradicts its recorded snapshot", id.String())
		}
		d.Anchor = summary(row, 'L', id.index, t.Kind, t.Process.Name, t.Process.PID)
		d.Lifecycle = &LifecycleIncident{Transition: t, FirstAbsence: *endpoint(row, r.Metadata)}
	case 'E':
		if row.Evidence == nil || id.index >= len(row.Evidence.Events) {
			return nil, missingAnchor(id)
		}
		e := row.Evidence.Events[id.index]
		d.Anchor = summary(row, 'E', id.index, e.Kind, e.Process, e.PID)
		f := observedFinding(row, id.index, e)
		excerpt := referenceText(row.Evidence.Text, e.FirstLine, e.LastLine)
		f.Excerpt = clip(excerpt, 2048)
		f.ExcerptTruncated = row.Evidence.Truncated || len(f.Excerpt) < len(excerpt)
		d.Report = &f
	default:
		return nil, missingAnchor(id)
	}
	if err := collectContext(ctx, r, d); err != nil {
		return nil, err
	}
	if err := enrichContext(ctx, r, d); err != nil {
		return nil, err
	}
	return d, nil
}

func missingAnchor(id IncidentID) error {
	return fmt.Errorf("incident %s not found; list available IDs with --timeline", id.String())
}

func collectContext(ctx context.Context, r session.Recording, d *IncidentDetail) error {
	var previous [contextLimit]TimelineObservation
	var history resourceHistory
	prior, following, gaps := 0, 0, 0
	err := r.Scan(ctx, func(row session.Record) error {
		resources := history.observe(row)
		if row.Sequence == d.Anchor.Sequence {
			d.AnchorObservation = contextObservation(row, resources)
		}
		if row.Sequence < d.Anchor.Sequence && d.Lifecycle != nil {
			if row.Snapshot != nil && row.Snapshot.BootID == d.Lifecycle.Transition.Process.BootID && containsIdentity(row.Snapshot, d.Lifecycle.Transition.Process.Identity) {
				d.Lifecycle.LastPresence = endpoint(row, r.Metadata)
				d.Lifecycle.LastPresence.Resources = resources
				gaps = 0
			}
			if row.Kind == "gap" && row.Source == "" {
				gaps++
			}
		}
		if row.Sequence == d.Anchor.Sequence || row.EndElapsedNS < d.WindowStartNS || row.StartElapsedNS > d.WindowEndNS {
			return nil
		}
		if row.Sequence < d.Anchor.Sequence {
			previous[prior%contextLimit] = contextObservation(row, resources)
			prior++
		} else {
			if following < contextLimit {
				d.Context = append(d.Context, contextObservation(row, resources))
			}
			following++
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.BeforeOmitted = max(0, prior-contextLimit)
	d.AfterOmitted = max(0, following-contextLimit)
	ordered := make([]TimelineObservation, 0, min(prior, contextLimit)+len(d.Context))
	for i := d.BeforeOmitted; i < prior; i++ {
		ordered = append(ordered, previous[i%contextLimit])
	}
	d.Context = append(ordered, d.Context...)
	if d.Lifecycle != nil && d.Lifecycle.LastPresence != nil {
		d.Lifecycle.SnapshotGaps = &gaps
	}
	return nil
}

func containsIdentity(s *session.Snapshot, id session.Identity) bool {
	for _, p := range s.Processes {
		if p.Identity == id {
			return true
		}
	}
	return false
}

func contextObservation(row session.Record, resources *ResourceObservation) TimelineObservation {
	entry := TimelineObservation{Resources: resources, Bounds: bounds(row), Kind: row.Kind, Source: row.Source, Snapshot: row.Snapshot, Transitions: row.Transitions, SourceStatus: row.SourceStatus, Error: row.Error}
	if row.Evidence != nil {
		for i, e := range row.Evidence.Events {
			entry.Reports = append(entry.Reports, observedFinding(row, i, e))
		}
	}
	// A valid recording row can still be too large to repeat in a context view.
	// Its sequence remains available for exact replay.
	if body, _ := json.Marshal(entry); len(body) > 8<<10 {
		return TimelineObservation{Bounds: entry.Bounds, Kind: entry.Kind, Source: entry.Source, DetailsOmitted: true}
	}
	return entry
}

func observedFinding(row session.Record, index int, e session.Event) TimelineFinding {
	return TimelineFinding{ID: IncidentID{'E', row.Sequence, index}.String(), Event: e, Reference: Reference{row.Sequence, e.FirstLine, e.LastLine}, Association: "incomplete"}
}

func fingerprint(source string, e session.Event) [sha256.Size]byte {
	return sha256.Sum256([]byte(eventKey(source, e)))
}

func enrichContext(ctx context.Context, r session.Recording, d *IncidentDetail) error {
	needed := map[[sha256.Size]byte][]*TimelineFinding{}
	if d.Report != nil {
		key := fingerprint(d.Anchor.Source, d.Report.Event)
		needed[key] = append(needed[key], d.Report)
	}
	add := func(entry *TimelineObservation) {
		if entry.DetailsOmitted && entry.Kind == "evidence" {
			d.AnalysisIncomplete = true
		}
		for j := range entry.Reports {
			f := &entry.Reports[j]
			key := fingerprint(entry.Source, f.Event)
			needed[key] = append(needed[key], f)
		}
	}
	add(&d.AnchorObservation)
	for i := range d.Context {
		add(&d.Context[i])
	}
	if len(needed) == 0 {
		return nil
	}
	report, err := AnalyzeRecording(ctx, r)
	if err != nil {
		return err
	}
	for _, note := range report.Limitations {
		if strings.HasPrefix(note, "Analysis bounds reached") {
			d.AnalysisIncomplete = true
			break
		}
	}
	reportCapped := d.AnalysisIncomplete
	budget := 1 << 20
	// Enrich the selected report first. Duplicate reports keep their own source reference.
	if d.Report != nil {
		key := fingerprint(d.Anchor.Source, d.Report.Event)
		for _, f := range report.Findings {
			if fingerprint(f.Source, f.Event) == key {
				enriched := *d.Report
				applyFinding(&enriched, f, reportCapped)
				body, _ := json.Marshal(enriched)
				if len(body) <= budget {
					*d.Report = enriched
					budget -= len(body)
				} else {
					d.AnalysisIncomplete = true
				}
				needed[key] = needed[key][1:]
				break
			}
		}
	}
	for _, f := range report.Findings {
		key := fingerprint(f.Source, f.Event)
		for _, entry := range needed[key] {
			enriched := *entry
			applyFinding(&enriched, f, reportCapped)
			body, _ := json.Marshal(enriched)
			if len(body) > budget {
				d.AnalysisIncomplete = true
				continue
			}
			budget -= len(body)
			*entry = enriched
		}
		delete(needed, key)
	}
	if len(needed) > 0 {
		d.AnalysisIncomplete = true
	}
	return nil
}

func applyFinding(entry *TimelineFinding, f Finding, capped bool) {
	entry.Candidates, entry.Evidence, entry.Uncertainty, entry.Conflict = f.Candidates, f.Evidence, f.Uncertainty, f.Conflict
	switch {
	case len(f.Candidates) > 0:
		entry.Association = "candidate_unproven"
	case capped:
		entry.Association = "incomplete"
	default:
		entry.Association = "unbound"
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func referenceText(s string, first, last int) string {
	for i := 1; i < first; i++ {
		_, s, _ = strings.Cut(s, "\n")
	}
	end := 0
	for i := first; i <= last; i++ {
		n := strings.IndexByte(s[end:], '\n')
		if n < 0 {
			return s
		}
		end += n + 1
	}
	return s[:end-1]
}
