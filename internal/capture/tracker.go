package capture

import "github.com/KartikeyaKotkar/droidsleuth/internal/session"

// Tracker only consumes complete snapshots. Gaps never manufacture disappearance.
type Tracker struct {
	previous []session.Process
	boot     string
	afterGap bool
}

func (t *Tracker) Gap() { t.afterGap = true }

func (t *Tracker) Observe(s session.Snapshot) []session.Transition {
	var events []session.Transition
	previous := t.previous
	if t.boot != "" && t.boot != s.BootID {
		previous = nil // A new boot is not evidence of absence in the old boot.
		t.afterGap = true
	}
	for _, old := range previous {
		found := false
		for _, p := range s.Processes {
			if old.Identity == p.Identity {
				found = true
				break
			}
		}
		if !found {
			events = append(events, session.Transition{Kind: "no_longer_observed", Process: old, AfterGap: t.afterGap})
		}
	}
	for _, p := range s.Processes {
		found := false
		for _, old := range previous {
			if old.Identity == p.Identity {
				found = true
				break
			}
		}
		if !found {
			events = append(events, session.Transition{Kind: "appeared", Process: p, AfterGap: t.afterGap})
		}
	}
	t.previous = append([]session.Process(nil), s.Processes...)
	t.boot, t.afterGap = s.BootID, false
	return events
}
