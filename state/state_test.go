package state

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var machine = Machine{Tool: "sample", Phases: []Phase{
	{Name: "intake", Kind: Gated},
	{Name: "explore", Kind: Gated, Skippable: true},
	{Name: "build", Kind: Run},
	{Name: "review", Kind: Gated},
	{Name: "ship", Kind: Run},
}}

func store() Store {
	return Store{
		Machine:  machine,
		SpecsDir: "specs",
		Now:      func() time.Time { return time.Date(2026, 11, 7, 1, 2, 3, 4_000_000, time.UTC) },
		Warn:     &bytes.Buffer{},
	}
}

func TestCheckGateOrder(t *testing.T) {
	cases := []struct {
		name          string
		gates         map[string]string
		phase, status string
		want          string // substring of the violation; "" = allowed
	}{
		{"first phase", nil, "intake", Draft, ""},
		{"gated must be approved", map[string]string{"intake": AwaitApproval}, "explore", Draft, "requires intake to be approved"},
		{"skipped satisfies", map[string]string{"intake": Approved, "explore": Skipped}, "build", InProgress, ""},
		{"not started blocks", map[string]string{"intake": Approved}, "build", InProgress, "requires explore"},
		{"run in progress blocks", map[string]string{"intake": Approved, "explore": Skipped, "build": InProgress}, "review", Approved, "requires build to be complete"},
		{"run complete allows", map[string]string{"intake": Approved, "explore": Approved, "build": Complete}, "review", Approved, ""},
		{"clearing always allowed", nil, "ship", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := machine.CheckGate(Change{Gates: c.gates}, c.phase, c.status)
			if c.want == "" {
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func TestSetGateValidation(t *testing.T) {
	cases := []struct {
		name                  string
		phase, status, reason string
		want                  string
	}{
		{"unknown phase", "bogus", Draft, "", "unknown phase"},
		{"unknown status", "intake", "nope", "", "unknown gate status"},
		{"run status on gated", "intake", InProgress, "", "unknown gate status"},
		{"gated status on run", "build", Approved, "", "unknown gate status"},
		{"skip not skippable", "intake", Skipped, "r", "can't be skipped"},
		{"skip run phase", "build", Skipped, "r", "unknown gate status"},
		{"skip without reason", "explore", Skipped, "", "requires a reason"},
		{"skip with reason", "explore", Skipped, "r", ""},
		{"run status on run", "build", InProgress, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := NewChange("1-x")
			err := machine.SetGate(&ch, c.phase, c.status, c.reason)
			if c.want == "" {
				if err != nil || ch.Phase != c.phase || ch.Status != c.status || ch.Gates[c.phase] != c.status {
					t.Fatalf("err = %v, change = %+v", err, ch)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func TestChangeCopyDoesNotShareGateUpdates(t *testing.T) {
	orig := NewChange("1-x")
	probe := orig
	if err := machine.SetGate(&probe, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if len(orig.Gates) != 0 {
		t.Fatalf("SetGate on a copy changed the original: %+v", orig.Gates)
	}
	probe = orig
	orig.Gates = map[string]string{"intake": Draft}
	probe = orig
	if err := machine.Reopen(&probe, "intake", "r"); err != nil {
		t.Fatal(err)
	}
	probe.Gates["intake"] = "changed"
	if orig.Gates["intake"] != Draft {
		t.Fatalf("Reopen on a copy shares gates: %+v", orig.Gates)
	}
}

func TestReopenClearsLaterApprovalsButNotSkips(t *testing.T) {
	ch := Change{Name: "1-x", Gates: map[string]string{
		"intake": Approved, "explore": Skipped, "build": Complete, "review": Approved, "ship": InProgress,
	}}
	if err := machine.Reopen(&ch, "intake", "requirements changed"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"intake": Draft, "explore": Skipped}
	if !reflect.DeepEqual(ch.Gates, want) || ch.Phase != "intake" || ch.Status != Draft {
		t.Fatalf("change = %+v", ch)
	}
}

func TestReopenRunPhaseReturnsToInProgress(t *testing.T) {
	ch := Change{Name: "1-x", Gates: map[string]string{"intake": Approved, "explore": Approved, "build": Complete, "review": AwaitApproval}}
	if err := machine.Reopen(&ch, "build", "review found a bug"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"intake": Approved, "explore": Approved, "build": InProgress}
	if !reflect.DeepEqual(ch.Gates, want) || ch.Status != InProgress {
		t.Fatalf("change = %+v", ch)
	}
}

func TestReopenUnskipsAPhase(t *testing.T) {
	ch := Change{Name: "1-x", Gates: map[string]string{"intake": Approved, "explore": Skipped}}
	if err := machine.Reopen(&ch, "explore", "there is a repo after all"); err != nil {
		t.Fatal(err)
	}
	if ch.Gates["explore"] != Draft {
		t.Fatalf("gates = %+v", ch.Gates)
	}
}

func TestReopenRefusals(t *testing.T) {
	ch := Change{Name: "1-x", Gates: map[string]string{"intake": Approved}}
	for name, c := range map[string]struct{ phase, reason, want string }{
		"no reason":   {"intake", "", "requires a reason"},
		"not started": {"review", "r", "hasn't started"},
		"unknown":     {"bogus", "r", "unknown phase"},
	} {
		if err := machine.Reopen(&ch, c.phase, c.reason); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		}
	}
}

func TestCloseRefusesFurtherTransitions(t *testing.T) {
	ch := Change{Name: "1-x", Status: Draft, Gates: map[string]string{"intake": Approved}}
	if err := machine.Close(&ch, ""); err == nil || !strings.Contains(err.Error(), "requires a reason") {
		t.Fatalf("err = %v", err)
	}
	if err := machine.Close(&ch, "abandoned"); err != nil {
		t.Fatal(err)
	}
	if !ch.IsClosed() || ch.Gates["intake"] != Approved {
		t.Fatalf("change = %+v", ch)
	}
	if err := machine.SetGate(&ch, "explore", Draft, ""); err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("SetGate err = %v", err)
	}
	if err := machine.Reopen(&ch, "intake", "r"); err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("Reopen err = %v", err)
	}
	if err := machine.Close(&ch, "again"); err == nil || !strings.Contains(err.Error(), "already closed") {
		t.Fatalf("Close err = %v", err)
	}
}

func TestSeedRecordsSkipsAndLogs(t *testing.T) {
	root, s := t.TempDir(), store()
	c, err := s.Seed(root, "1-x", SeedOptions{Sources: []string{"00_intake/1.md"}, Skip: map[string]string{"explore": "no repo"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Phase != "intake" || c.Status != Draft || c.Gates["explore"] != Skipped {
		t.Fatalf("change = %+v", c)
	}
	if _, err := os.Stat(filepath.Join(root, "specs", "1-x", ".gitkeep")); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(s.LogPath(root))
	for _, want := range []string{"1-x: created", "1-x: explore → skipped (no repo)"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
	loaded, err := s.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "1-x" || !reflect.DeepEqual(loaded.Sources, []string{"00_intake/1.md"}) {
		t.Fatalf("loaded = %+v", loaded)
	}
}

func TestSeedRecordsRepos(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Seed(root, "1-x", SeedOptions{Repos: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Repos, []string{"a", "b"}) {
		t.Fatalf("repos = %v", c.Repos)
	}
	raw, _ := os.ReadFile(s.StatePath(root))
	if _, err := s.Seed(root, "2-y", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.StatePath(root))
	if strings.Count(string(after), `"repos"`) != strings.Count(string(raw), `"repos"`) {
		t.Fatalf("a change without repos wrote the key:\n%s", after)
	}
}

func TestSeedRefusesBadNameAndUnskippablePhase(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Seed(root, "../evil", SeedOptions{}); err == nil || !strings.Contains(err.Error(), "invalid change name") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{Skip: map[string]string{"review": "r"}}); err == nil {
		t.Fatal("skipping an unskippable phase should fail")
	}
	if _, err := os.Stat(filepath.Join(root, "specs")); err == nil {
		t.Fatal("refused seed wrote files")
	}
}

func TestStateFileShape(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.StatePath(root))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	if string(top["schema"]) != "1" || len(top) != 2 || top["changes"] == nil {
		t.Fatalf("shape: %s", raw)
	}
	var sf stateFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatal(err)
	}
	if got := sf.Changes["1-x"].UpdatedAt; got != "2026-11-07T01:02:03.004Z" {
		t.Fatalf("updatedAt = %q", got)
	}
}

func TestReadMigratesOldFileWithoutRewriting(t *testing.T) {
	root, s := t.TempDir(), store()
	old := []byte(`{"changes":{"1-x":{"phase":"intake","status":"approved","mode":"old","gates":{"intake":"approved","explore":""}}}}`)
	if err := os.WriteFile(s.StatePath(root), old, 0o644); err != nil {
		t.Fatal(err)
	}
	s.Migrate = func(data []byte) ([]byte, error) {
		return bytes.Replace(data, []byte(`"explore":""`), []byte(`"explore":"skipped"`), 1), nil
	}
	changes, err := s.ReadChanges(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := changes["1-x"].Gates; !reflect.DeepEqual(got, map[string]string{"intake": Approved, "explore": Skipped}) {
		t.Fatalf("gates = %+v", got)
	}
	if after, _ := os.ReadFile(s.StatePath(root)); !bytes.Equal(after, old) {
		t.Fatalf("a read rewrote the file: %s", after)
	}
}

func TestReadRefusesNewerSchemaAndCorruptFile(t *testing.T) {
	root, s := t.TempDir(), store()
	if err := os.WriteFile(s.StatePath(root), []byte(`{"schema":99,"changes":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadChanges(root); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v", err)
	}
	corrupt := []byte(`{"changes":{`)
	if err := os.WriteFile(s.StatePath(root), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err == nil {
		t.Fatal("seed must fail on corrupt state")
	}
	if got, _ := os.ReadFile(s.StatePath(root)); !bytes.Equal(got, corrupt) {
		t.Fatalf("corrupt file overwritten: %s", got)
	}
}

func TestStoreSchemaOverridesDefault(t *testing.T) {
	root, s := t.TempDir(), store()
	s.Schema = 3
	migrated := 0
	s.Migrate = func(data []byte) ([]byte, error) {
		migrated++
		return []byte(`{"schema":3,"changes":{}}`), nil
	}
	if err := os.WriteFile(s.StatePath(root), []byte(`{"schema":2,"changes":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadChanges(root); err != nil || migrated != 1 {
		t.Fatalf("migrated = %d, err = %v", migrated, err)
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.StatePath(root))
	if !strings.Contains(string(raw), `"schema": 3`) {
		t.Fatalf("wrote:\n%s", raw)
	}
	s.Schema = 2
	if _, err := s.ReadChanges(root); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v", err)
	}
}

func TestPreflightRefuses(t *testing.T) {
	s := store()
	s.Preflight = func(string) error { return os.ErrPermission }
	if _, err := s.Load(t.TempDir()); err != os.ErrPermission {
		t.Fatalf("err = %v", err)
	}
}

func TestSwitch(t *testing.T) {
	root, s := t.TempDir(), store()
	for _, name := range []string{"nope", "", "1-../x", "1-Weird_Name"} {
		if _, err := s.Switch(root, name); err == nil || !strings.Contains(err.Error(), "unknown change") {
			t.Errorf("switch(%q) err = %v", name, err)
		}
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seed(root, "2-y", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.StatePath(root))
	c, err := s.Switch(root, "1-x")
	if err != nil || c.Name != "1-x" {
		t.Fatalf("c = %+v, err = %v", c, err)
	}
	if after, _ := os.ReadFile(s.StatePath(root)); !bytes.Equal(after, before) {
		t.Fatal("switching to a recorded change rewrote STATE.json")
	}
}

func TestKnownSortsByIDAndCarriesProgress(t *testing.T) {
	root, s := t.TempDir(), store()
	for _, dir := range []string{"10-later", "2-earlier"} {
		if err := os.MkdirAll(filepath.Join(root, "specs", dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "specs", "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Switch(root, "10-later"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	known, err := s.Known(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 2 || known[0].Name != "2-earlier" || known[1].Name != "10-later" || known[1].Status != Approved {
		t.Fatalf("known = %+v", known)
	}
}

func TestAdvanceAndReopenRequireActiveFolder(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Advance(root, "intake", Approved, ""); err == nil || !strings.Contains(err.Error(), "no active change") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.ChangeDir(root, "1-x")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err == nil || !strings.Contains(err.Error(), "no folder") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Reopen(root, "intake", "r"); err == nil || !strings.Contains(err.Error(), "no folder") {
		t.Fatalf("err = %v", err)
	}
}

func TestAdvanceLogsNotes(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "review", Approved, "", "override: forced"); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(s.LogPath(root))
	for _, want := range []string{"1-x: review → approved", "1-x: override: forced"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}

func TestCloseChangeClearsActivePointerOnlyForActive(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seed(root, "2-y", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloseChange(root, "1-x", "dropped"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Load(root); c.Name != "2-y" {
		t.Fatalf("closing another change moved the pointer: %+v", c)
	}
	if _, err := s.CloseChange(root, "2-y", "done"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Load(root); c.Name != "" {
		t.Fatalf("active = %+v", c)
	}
	changes, _ := s.ReadChanges(root)
	if !changes["1-x"].IsClosed() || !changes["2-y"].IsClosed() {
		t.Fatalf("changes = %+v", changes)
	}
	if _, err := s.CloseChange(root, "9-missing", "r"); err == nil || !strings.Contains(err.Error(), "unknown change") {
		t.Fatalf("err = %v", err)
	}
}

// gitRepo returns a repo with one commit, and a Store that commits.
func gitRepo(t *testing.T) (string, Store) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	root := t.TempDir()
	run(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "add", "README.md")
	run(t, root, "commit", "-q", "-m", "init")
	s := store()
	s.Commit = true
	return root, s
}

func run(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestTransitionsCommitStateAndLog(t *testing.T) {
	root, s := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "00_intake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "00_intake", "1.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{Paths: []string{"00_intake/1.md"}}); err != nil {
		t.Fatal(err)
	}
	seeded := run(t, root, "show", "--name-only", "--format=%s", "HEAD")
	for _, want := range []string{"sample: 1-x: created", "STATE.json", "LOG.md", "specs/1-x/.gitkeep", "00_intake/1.md"} {
		if !strings.Contains(seeded, want) {
			t.Errorf("seed commit missing %q:\n%s", want, seeded)
		}
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(t, root, "log", "-1", "--format=%s")); got != "sample: 1-x: intake → approved" {
		t.Fatalf("subject = %q", got)
	}
	if _, err := s.Reopen(root, "intake", "changed"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(t, root, "log", "-1", "--format=%s")); got != "sample: 1-x: reopened intake (changed)" {
		t.Fatalf("subject = %q", got)
	}
	if _, err := s.CloseChange(root, "1-x", "done"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(t, root, "log", "-1", "--format=%s")); got != "sample: 1-x: closed (done)" {
		t.Fatalf("subject = %q", got)
	}
	if status := run(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("tree not clean:\n%s", status)
	}
}

func TestGateCommitsTheChangesFolder(t *testing.T) {
	root, s := gitRepo(t)
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(root, "specs", "1-x", "requirements.md")
	if err := os.WriteFile(spec, []byte("reqs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "notes.md")
	if err := os.WriteFile(outside, []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	files := run(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "specs/1-x/requirements.md") || strings.Contains(files, "notes.md") {
		t.Fatalf("commit has:\n%s", files)
	}
	if status := run(t, root, "status", "--porcelain"); !strings.Contains(status, "?? notes.md") {
		t.Fatalf("status = %q", status)
	}
	// A deleted document is part of the record too.
	if err := os.Remove(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "explore", Draft, ""); err != nil {
		t.Fatal(err)
	}
	if files := run(t, root, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "specs/1-x/requirements.md") {
		t.Fatalf("deletion not committed:\n%s", files)
	}
}

func TestGateOnEmptyUntrackedFolderStillCommitsState(t *testing.T) {
	root, s := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "specs", "1-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Switch(root, "1-x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatalf("empty folder broke the commit: %v", err)
	}
	if got := strings.TrimSpace(run(t, root, "log", "-1", "--format=%s")); got != "sample: 1-x: intake → approved" {
		t.Fatalf("subject = %q", got)
	}
}

func TestCommitLeavesOtherStagedWorkAlone(t *testing.T) {
	root, s := gitRepo(t)
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "other.md")
	if err := os.WriteFile(other, []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "add", "other.md")
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if files := run(t, root, "show", "--name-only", "--format=", "HEAD"); strings.Contains(files, "other.md") {
		t.Fatalf("commit took staged work:\n%s", files)
	}
	if status := run(t, root, "status", "--porcelain"); !strings.Contains(status, "A  other.md") {
		t.Fatalf("staged work lost:\n%s", status)
	}
}

func TestNoCommitWhenDisabled(t *testing.T) {
	root, s := gitRepo(t)
	s.Commit = false
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(t, root, "log", "-1", "--format=%s")); got != "init" {
		t.Fatalf("unexpected commit: %q", got)
	}
}

func TestOutsideGitWarnsAndStillApplies(t *testing.T) {
	root, s := t.TempDir(), store()
	s.Commit = true
	warnings := &bytes.Buffer{}
	s.Warn = warnings
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "not committed") {
		t.Fatalf("warnings = %q", warnings)
	}
	if c, err := s.Load(root); err != nil || c.Name != "1-x" {
		t.Fatalf("c = %+v, err = %v", c, err)
	}
}

func TestFailedCommitIsAnErrorButStateStands(t *testing.T) {
	root, s := gitRepo(t)
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err == nil || !strings.Contains(err.Error(), "state saved but not committed") {
		t.Fatalf("err = %v", err)
	}
	c, err := s.Load(root)
	if err != nil || c.Gates["intake"] != Approved {
		t.Fatalf("c = %+v, err = %v", c, err)
	}
}

func TestSlugifyAndNextID(t *testing.T) {
	for in, want := range map[string]string{"Auth Flow!": "auth-flow", "  ": "", "Refactor DB layer": "refactor-db-layer"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slugify(strings.Repeat("x", 100)); len(got) != 40 {
		t.Errorf("slug length = %d", len(got))
	}
	root, s := t.TempDir(), store()
	if s.NextID(root) != 1 {
		t.Errorf("NextID without specs = %d", s.NextID(root))
	}
	if err := os.MkdirAll(filepath.Join(root, "specs", "7-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s.NextID(root) != 8 {
		t.Errorf("NextID = %d", s.NextID(root))
	}
}
