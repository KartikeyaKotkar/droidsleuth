package session

import (
	"errors"
	"strings"
)

const MaxPrivateProcesses = 16

func (m Metadata) validatePrivateScope() error {
	if m.Schema == PrivateSchemaVersion && (m.Package == "" || m.UID < 0 || m.UID/100000 != m.User) {
		return errors.New("invalid package or UID in private recording")
	}
	return nil
}

// PrivateProcessName defines the process names allowed under an exclusive package UID.
func PrivateProcessName(pkg, name string) bool {
	return name == pkg || (len(name) > len(pkg)+1 && strings.HasPrefix(name, pkg+":"))
}

func validatePrivateSnapshot(r Record, meta Metadata) error {
	if r.Snapshot == nil {
		return nil
	}
	s := r.Snapshot
	if s.BootID == "" || len(s.Processes) > MaxPrivateProcesses || len(r.Transitions) > 2*MaxPrivateProcesses {
		return errors.New("invalid private snapshot scope or size")
	}
	valid := func(p Process) bool {
		return p.BootID == s.BootID && p.UID == meta.UID && p.UID >= 0 &&
			p.PID > 0 && p.StartTicks > 0 && PrivateProcessName(meta.Package, p.Name)
	}
	pids := make(map[int]Process, len(s.Processes))
	for _, p := range s.Processes {
		if _, duplicate := pids[p.PID]; duplicate || !valid(p) {
			return errors.New("invalid or duplicate private process")
		}
		pids[p.PID] = p
	}
	seen := make(map[Identity]bool, len(r.Transitions))
	for _, t := range r.Transitions {
		p := t.Process
		current := pids[p.PID]
		if !valid(p) || seen[p.Identity] ||
			(t.Kind == "appeared" && current != p) ||
			(t.Kind == "no_longer_observed" && current.Identity == p.Identity) {
			return errors.New("private transition contradicts its snapshot")
		}
		seen[p.Identity] = true
	}
	return nil
}
