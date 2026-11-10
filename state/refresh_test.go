package state

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/btdstudio/kiso/workspace"
)

// writeReadme is a Refresh that rewrites a file in the change's folder from the
// gate it's given.
func writeReadme(root string, c Change) error {
	body := c.Name + " " + c.Phase + " " + c.Status + "\n"
	return os.WriteFile(filepath.Join(root, "specs", c.Name, "README.md"), []byte(body), 0o644)
}

// dirty is `git status --porcelain` without the local active-change pointer,
// which is never committed.
func dirty(t *testing.T, root string) string {
	t.Helper()
	var out []string
	for _, line := range strings.SplitAfter(run(t, root, "status", "--porcelain"), "\n") {
		if line != "" && line != "?? .sample/\n" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}

func TestRefreshCommitsWithTheTransition(t *testing.T) {
	root, s := gitRepo(t)
	s.ChangeDirs = []string{"research"}
	s.Refresh = writeReadme
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "research", "1-x"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	files := run(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "specs/1-x/README.md") {
		t.Fatalf("advance commit has:\n%s", files)
	}
	if got := run(t, root, "show", "HEAD:specs/1-x/README.md"); got != "1-x intake approved\n" {
		t.Fatalf("committed README = %q", got)
	}
	if status := dirty(t, root); status != "" {
		t.Fatalf("tree not clean after advance:\n%s", status)
	}

	// Reopen commits the README and the change's other folders.
	if err := os.WriteFile(filepath.Join(root, "research", "1-x", "notes.md"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reopen(root, "intake", "changed"); err != nil {
		t.Fatal(err)
	}
	files = run(t, root, "show", "--name-only", "--format=", "HEAD")
	for _, want := range []string{"STATE.json", "LOG.md", "specs/1-x/README.md", "research/1-x/notes.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("reopen commit missing %q:\n%s", want, files)
		}
	}
	if got := run(t, root, "show", "HEAD:specs/1-x/README.md"); got != "1-x intake draft\n" {
		t.Fatalf("committed README = %q", got)
	}
	if status := dirty(t, root); status != "" {
		t.Fatalf("tree not clean after reopen:\n%s", status)
	}
}

func TestRefreshSeesSavedStateBeforeLog(t *testing.T) {
	root, s := t.TempDir(), store()
	type seen struct{ given, onDisk, log string }
	var calls []seen
	s.Refresh = func(r string, c Change) error {
		changes, err := s.ReadChanges(r)
		if err != nil {
			return err
		}
		log, err := os.ReadFile(s.LogPath(r))
		if err != nil {
			return err
		}
		calls = append(calls, seen{c.Gates["intake"], changes[c.Name].Gates["intake"], string(log)})
		return nil
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reopen(root, "intake", "changed"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("Refresh ran %d times, want 2", len(calls))
	}
	for i, want := range []struct{ gate, notYetLogged string }{
		{Approved, "intake → approved"},
		{Draft, "reopened intake"},
	} {
		c := calls[i]
		if c.given != want.gate || c.onDisk != want.gate {
			t.Errorf("call %d: given %q, on disk %q, want %q", i, c.given, c.onDisk, want.gate)
		}
		if strings.Contains(c.log, want.notYetLogged) {
			t.Errorf("call %d: LOG.md already has %q:\n%s", i, want.notYetLogged, c.log)
		}
	}
	log, err := os.ReadFile(s.LogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"intake → approved", "reopened intake (changed)"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("LOG.md missing %q afterwards:\n%s", want, log)
		}
	}
}

func TestRefreshErrorIsAWarning(t *testing.T) {
	root, s := t.TempDir(), store()
	warnings := &bytes.Buffer{}
	s.Warn = warnings
	s.Refresh = func(string, Change) error { return errors.New("x.md: bad markers") }
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}

	c, err := s.Advance(root, "intake", Approved, "")
	if err != nil {
		t.Fatalf("Advance err = %v", err)
	}
	if c.Gates["intake"] != Approved {
		t.Fatalf("returned gates = %+v", c.Gates)
	}
	if got := warnings.String(); got != "warning: x.md: bad markers\n" {
		t.Fatalf("after Advance, warnings = %q", got)
	}

	warnings.Reset()
	if _, err := s.Reopen(root, "intake", "changed"); err != nil {
		t.Fatalf("Reopen err = %v", err)
	}
	if got := warnings.String(); got != "warning: x.md: bad markers\n" {
		t.Fatalf("after Reopen, warnings = %q", got)
	}

	changes, err := s.ReadChanges(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := changes["1-x"].Gates["intake"]; got != Draft {
		t.Fatalf("saved intake = %q, want %q", got, Draft)
	}
	log, err := os.ReadFile(s.LogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"intake → approved", "reopened intake (changed)"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("LOG.md missing %q:\n%s", want, log)
		}
	}
}

func TestRefreshUnderVCSNone(t *testing.T) {
	marker := fakeGit(t)
	root, s := t.TempDir(), store()
	s.Commit = true
	s.VCS = "none"
	calls := 0
	s.Refresh = func(string, Change) error { calls++; return nil }
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("Seed ran Refresh %d times", calls)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reopen(root, "intake", "redo"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("Refresh ran %d times, want 2", calls)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		calls, _ := os.ReadFile(marker)
		t.Fatalf("git ran under vcs none: %s", calls)
	}
}

func TestRefreshNotCalledWhenRefused(t *testing.T) {
	root, s := t.TempDir(), store()
	calls := 0
	s.Refresh = func(string, Change) error { calls++; return nil }

	if _, err := s.Advance(root, "intake", Approved, ""); err == nil {
		t.Fatal("Advance with no active change succeeded")
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	refused := []struct {
		name string
		do   func() error
	}{
		{"advance unknown phase", func() error { _, err := s.Advance(root, "bogus", Approved, ""); return err }},
		{"advance unknown status", func() error { _, err := s.Advance(root, "intake", "bogus", ""); return err }},
		{"reopen unknown phase", func() error { _, err := s.Reopen(root, "bogus", "r"); return err }},
		{"reopen not started", func() error { _, err := s.Reopen(root, "review", "r"); return err }},
		{"reopen without reason", func() error { _, err := s.Reopen(root, "intake", ""); return err }},
	}
	for _, r := range refused {
		if err := r.do(); err == nil {
			t.Fatalf("%s succeeded", r.name)
		}
	}
	if _, err := s.CloseChange(root, "1-x", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reopen(root, "intake", "r"); err == nil {
		t.Fatal("Reopen after close succeeded")
	}
	if calls != 0 {
		t.Fatalf("Refresh ran %d times on refused transitions", calls)
	}
}

func TestReopenCommitsTheChangesFolders(t *testing.T) {
	root, s := gitRepo(t)
	s.ChangeDirs = []string{"research", "missing"}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"specs/1-x/a.md":    "a\n",
		"research/1-x/n.md": "n\n",
		"notes.md":          "outside\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Reopen(root, "intake", "changed"); err != nil {
		t.Fatal(err)
	}
	files := run(t, root, "show", "--name-only", "--format=", "HEAD")
	for _, want := range []string{"STATE.json", "LOG.md", "specs/1-x/a.md", "research/1-x/n.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("reopen commit missing %q:\n%s", want, files)
		}
	}
	for _, f := range strings.Fields(files) {
		if f == "notes.md" {
			t.Errorf("reopen commit has the outside file:\n%s", files)
		}
	}
	if status := dirty(t, root); status != "?? notes.md\n" {
		t.Fatalf("status = %q", status)
	}
}

// A hand-edited pointer gets the same checks as Switch, so a transition never
// saves, refreshes or records a name that leaves the specs folder.
func TestCraftedActivePointerIsRefused(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "ws")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{outside, filepath.Join(root, "specs")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := store()
	calls := 0
	s.Refresh = func(string, Change) error { calls++; return nil }
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.StatePath(root))
	if err != nil {
		t.Fatal(err)
	}

	names := []string{"../../outside", "1-x/../../../outside"}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(root, "specs", "2-y")); err != nil {
			t.Fatal(err)
		}
		names = append(names, "2-y")
	}
	for _, name := range names {
		if err := workspace.WriteActive(root, "sample", name); err != nil {
			t.Fatal(err)
		}
		// The folder exists, so only the name and symlink checks can refuse it.
		if _, err := os.Stat(s.ChangeDir(root, name)); err != nil {
			t.Fatalf("%s: test setup: %v", name, err)
		}
		if _, err := s.Advance(root, "intake", Approved, ""); err == nil || !strings.Contains(err.Error(), "isn't a valid change folder") {
			t.Errorf("%s: Advance err = %v", name, err)
		}
		if _, err := s.Reopen(root, "intake", "r"); err == nil || !strings.Contains(err.Error(), "isn't a valid change folder") {
			t.Errorf("%s: Reopen err = %v", name, err)
		}
	}
	if calls != 0 {
		t.Fatalf("Refresh ran %d times", calls)
	}
	after, err := os.ReadFile(s.StatePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("STATE.json changed:\n%s", after)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("outside folder has %d entries", len(entries))
	}

	// A valid pointer still works.
	if err := workspace.WriteActive(root, "sample", "1-x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Refresh ran %d times after a valid pointer, want 1", calls)
	}
}
