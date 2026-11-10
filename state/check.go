package state

import "strconv"

// Problem is one thing an artifact check found wrong.
type Problem struct {
	Path    string // relative to the workspace root; "" if not about one file
	Line    int    // 1-based; 0 if not about one line
	Message string
}

// String renders the problem as "path:line: message", leaving out the parts
// that are empty. A line is shown only with a path.
func (p Problem) String() string {
	switch {
	case p.Path != "" && p.Line > 0:
		return p.Path + ":" + strconv.Itoa(p.Line) + ": " + p.Message
	case p.Path != "":
		return p.Path + ": " + p.Message
	default:
		return p.Message
	}
}

// CheckArtifacts runs the machine's Check for a transition of phase to status in
// the given change, and returns what it reports, unchanged and in order. It
// returns nil when Check isn't set. It applies no policy: as with CheckGate,
// whether a problem blocks the transition is the caller's decision.
func (m Machine) CheckArtifacts(root, change, phase, status string) []Problem {
	if m.Check == nil {
		return nil
	}
	return m.Check(root, change, phase, status)
}
