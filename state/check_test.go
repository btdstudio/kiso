package state

import (
	"go/build"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProblemString(t *testing.T) {
	cases := []struct {
		p    Problem
		want string
	}{
		{Problem{Path: "a.md", Line: 3, Message: "m"}, "a.md:3: m"},
		{Problem{Path: "a.md", Message: "m"}, "a.md: m"},
		{Problem{Message: "m"}, "m"},
		{Problem{Line: 3, Message: "m"}, "m"},                      // a line means nothing without a path
		{Problem{Path: "a.md", Line: -1, Message: "m"}, "a.md: m"}, // not about one line
		{Problem{Path: "dir/b.md", Line: 12, Message: "two words"}, "dir/b.md:12: two words"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("%#v.String() = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestCheckArtifacts(t *testing.T) {
	// No Check: nil.
	if got := machine.CheckArtifacts("/root", "1-x", "review", Approved); got != nil {
		t.Fatalf("without Check = %#v, want nil", got)
	}

	// A Check gets the arguments as given, and its problems come back as is.
	want := []Problem{
		{Path: "b.md", Line: 2, Message: "second file first"},
		{Message: "no file"},
	}
	var calls [][4]string
	m := machine
	m.Check = func(root, change, phase, status string) []Problem {
		calls = append(calls, [4]string{root, change, phase, status})
		// A fresh slice, so a change to the result can't also change want.
		return append([]Problem(nil), want...)
	}
	got := m.CheckArtifacts("/root", "1-x", "review", Approved)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckArtifacts = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(calls, [][4]string{{"/root", "1-x", "review", Approved}}) {
		t.Fatalf("Check calls = %v", calls)
	}

	// A copy carries the hook with it.
	copied := m
	if got := copied.CheckArtifacts("/r", "2-y", "build", Complete); len(got) != 2 || calls[1] != [4]string{"/r", "2-y", "build", Complete} {
		t.Fatalf("copy: got %#v, calls %v", got, calls)
	}

	// A Check that finds nothing: nil.
	m.Check = func(root, change, phase, status string) []Problem { return nil }
	if got := m.CheckArtifacts("/root", "1-x", "review", Approved); got != nil {
		t.Fatalf("empty Check = %#v, want nil", got)
	}
}

func TestAdvanceDoesNotRunCheck(t *testing.T) {
	root, s := t.TempDir(), store()
	calls := 0
	s.Machine.Check = func(root, change, phase, status string) []Problem {
		calls++
		return []Problem{{Message: "would block"}}
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("Check ran %d times during Seed and Advance", calls)
	}
}

// state must stay independent of ticket, directly or through another kiso
// package.
func TestStateDoesNotImportTicket(t *testing.T) {
	const module = "github.com/btdstudio/kiso/"
	seen := map[string]bool{}
	var walk func(pkg, dir string)
	walk = func(pkg, dir string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		bp, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		for _, imp := range bp.Imports {
			if !strings.HasPrefix(imp, module) {
				continue
			}
			if imp == module+"ticket" {
				t.Fatalf("%s imports %s", pkg, imp)
			}
			walk(imp, filepath.Join("..", strings.TrimPrefix(imp, module)))
		}
	}
	walk(module+"state", ".")
	for _, want := range []string{"changelog", "settings", "workspace"} {
		if !seen[module+want] {
			t.Fatalf("the walk didn't reach %s, so it isn't following imports: %v", want, seen)
		}
	}
}
