// Package state is a generic per-change phase machine. A tool supplies its
// ordered phases; the machine enforces their order, and a Store keeps every
// change's record in a committed STATE.json.
package state

import "fmt"

// PhaseKind says how a phase progresses.
type PhaseKind int

const (
	// Gated phases move draft → await-approval → approved, or are skipped.
	Gated PhaseKind = iota
	// Run phases move "" → in-progress → complete.
	Run
)

// Statuses a phase can hold. "" means not started.
const (
	Draft         = "draft"
	AwaitApproval = "await-approval"
	Approved      = "approved"
	Skipped       = "skipped"
	InProgress    = "in-progress"
	Complete      = "complete"
	// Closed is a change's status once it's finished with; it isn't a gate.
	Closed = "closed"
)

// Phase is one step of a tool's loop.
type Phase struct {
	Name      string
	Kind      PhaseKind
	Skippable bool // may be set to Skipped, with a reason
}

// Machine is a tool's ordered phases.
type Machine struct {
	Tool   string // prefixes order violations
	Phases []Phase
	// Check validates a phase's artifacts before a transition to status. It's
	// optional. CheckArtifacts runs it; Advance and SetGate never do, so the
	// caller decides when to check and what a problem means.
	Check func(root, change, phase, status string) []Problem
}

// Change is one change's progress through the machine. Gates holds only phases
// that have a status, so a record stays small and merges cleanly.
type Change struct {
	Name      string            `json:"-"`
	Phase     string            `json:"phase"`
	Status    string            `json:"status"`
	Gates     map[string]string `json:"gates"`
	Sources   []string          `json:"sources"`
	Repos     []string          `json:"repos,omitempty"` // logical names from REPOS.json
	UpdatedAt string            `json:"updatedAt"`       // per change, so branches touching different changes merge
}

// IsClosed reports whether the change has been closed.
func (c Change) IsClosed() bool { return c.Status == Closed }

func (m Machine) find(name string) (int, Phase, bool) {
	for i, p := range m.Phases {
		if p.Name == name {
			return i, p, true
		}
	}
	return -1, Phase{}, false
}

func validStatus(p Phase, status string) bool {
	switch status {
	case "":
		return true
	case Draft, AwaitApproval, Approved:
		return p.Kind == Gated
	case Skipped:
		return p.Kind == Gated && p.Skippable
	case InProgress, Complete:
		return p.Kind == Run
	}
	return false
}

// withGate returns a copy of gates with phase set, so a Change copied by value
// never shares updates with the original. "" removes the entry.
func withGate(gates map[string]string, phase, status string) map[string]string {
	out := make(map[string]string, len(gates)+1)
	for k, v := range gates {
		out[k] = v
	}
	if status == "" {
		delete(out, phase)
	} else {
		out[phase] = status
	}
	return out
}

// CheckGate reports whether setting phase to status respects the loop: every
// earlier phase must be finished, meaning approved or skipped when gated and
// complete when run. Clearing a gate (status "") is always allowed. It returns
// the violation, or nil; whether a violation blocks is the caller's policy.
func (m Machine) CheckGate(c Change, phase, status string) error {
	if status == "" {
		return nil
	}
	for _, p := range m.Phases {
		if p.Name == phase {
			return nil
		}
		got := c.Gates[p.Name]
		switch p.Kind {
		case Run:
			if got != Complete {
				return fmt.Errorf("%s: %s requires %s to be complete (currently %q)", m.Tool, phase, p.Name, got)
			}
		default:
			if got != Approved && got != Skipped {
				return fmt.Errorf("%s: %s requires %s to be approved (currently %q)", m.Tool, phase, p.Name, got)
			}
		}
	}
	return nil
}

// SetGate starts the given phase and sets its status. It validates names and the
// reason a skip requires, and refuses a closed change; CheckGate is the order
// rule.
func (m Machine) SetGate(c *Change, phase, status, reason string) error {
	if c.IsClosed() {
		return fmt.Errorf("%s: %s is closed", m.Tool, c.Name)
	}
	_, p, ok := m.find(phase)
	if !ok {
		return fmt.Errorf("unknown phase: %s", phase)
	}
	if !validStatus(p, status) {
		if status == Skipped && p.Kind == Gated {
			return fmt.Errorf("%s: %s can't be skipped", m.Tool, phase)
		}
		return fmt.Errorf("unknown gate status: %s", status)
	}
	if status == Skipped && reason == "" {
		return fmt.Errorf("%s: skipping %s requires a reason", m.Tool, phase)
	}
	c.Phase = phase
	c.Status = status
	c.Gates = withGate(c.Gates, phase, status)
	return nil
}

// Reopen moves back to a phase that has already started: it returns to draft (or
// in-progress for a run phase), and every later phase is cleared because its
// approval no longer holds. Skipped phases stay skipped. A reason is required.
func (m Machine) Reopen(c *Change, phase, reason string) error {
	if c.IsClosed() {
		return fmt.Errorf("%s: %s is closed", m.Tool, c.Name)
	}
	idx, p, ok := m.find(phase)
	if !ok {
		return fmt.Errorf("unknown phase: %s", phase)
	}
	if reason == "" {
		return fmt.Errorf("%s: reopening %s requires a reason", m.Tool, phase)
	}
	if c.Gates[phase] == "" {
		return fmt.Errorf("%s: %s hasn't started", m.Tool, phase)
	}
	status := Draft
	if p.Kind == Run {
		status = InProgress
	}
	gates := withGate(c.Gates, phase, status)
	for _, later := range m.Phases[idx+1:] {
		if gates[later.Name] != Skipped {
			delete(gates, later.Name)
		}
	}
	c.Phase = phase
	c.Status = status
	c.Gates = gates
	return nil
}

// Close marks a change as finished with. A reason is required. Its gates and
// artifacts stay as history.
func (m Machine) Close(c *Change, reason string) error {
	if c.IsClosed() {
		return fmt.Errorf("%s: %s is already closed", m.Tool, c.Name)
	}
	if reason == "" {
		return fmt.Errorf("%s: closing %s requires a reason", m.Tool, c.Name)
	}
	c.Status = Closed
	return nil
}
