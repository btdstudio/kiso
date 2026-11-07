package repos

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	root := args[0]
	var out, errOut bytes.Buffer
	code := Run(root, "sample", args[1:], &out, &errOut)
	return code, out.String(), errOut.String()
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// remoteRepo returns a local repo with one commit, usable as a remote.
func remoteRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestAddSplitsCommittedAndLocal(t *testing.T) {
	root := t.TempDir()
	code, _, errOut := run(t, root, "add", "actions", "git@github.com:org/actions.git", "--path", "/work/actions", "--branch", "trunk")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, errOut)
	}
	committed, err := os.ReadFile(filepath.Join(root, "REPOS.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(committed), "/work/actions") {
		t.Fatalf("REPOS.json holds a local path:\n%s", committed)
	}
	var file struct {
		Repos map[string]Repo `json:"repos"`
	}
	if err := json.Unmarshal(committed, &file); err != nil {
		t.Fatal(err)
	}
	if got := file.Repos["actions"]; got != (Repo{"git@github.com:org/actions.git", "trunk"}) {
		t.Fatalf("repo = %+v", got)
	}
	local, err := os.ReadFile(filepath.Join(root, ".sample", "paths.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(local), "/work/actions") {
		t.Fatalf("paths.json = %s", local)
	}
	r, err := Load(root, "sample")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := r.Resolve("actions"); err != nil || p != "/work/actions" {
		t.Fatalf("Resolve = %q, %v", p, err)
	}
}

func TestAddRefusals(t *testing.T) {
	root := t.TempDir()
	if code, _, _ := run(t, root, "add", "good", "https://github.com/org/good"); code != 0 {
		t.Fatal("first add failed")
	}
	r, _ := Load(root, "sample")
	if r.Repos["good"].DefaultBranch != DefaultBranch {
		t.Fatalf("branch = %q", r.Repos["good"].DefaultBranch)
	}
	for name, c := range map[string]struct{ repo, remote, want string }{
		"duplicate":  {"good", "https://github.com/org/x", "already registered"},
		"traversal":  {"../x", "https://github.com/org/x", "invalid repo name"},
		"separator":  {"a/b", "https://github.com/org/x", "invalid repo name"},
		"helper":     {"ext", "ext::sh -c x", "remote helpers"},
		"token":      {"tok", "https://x-access-token:secret@github.com/org/x", "credentials"},
		"bare token": {"tok", "https://secret@github.com/org/x", "credentials"},
	} {
		code, _, errOut := run(t, root, "add", c.repo, c.remote)
		if code != 1 || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: code = %d, err = %q", name, code, errOut)
		}
	}
}

func TestCheckRemoteRefusesLeadingDash(t *testing.T) {
	if err := checkRemote("sample", "-oProxyCommand=x"); err == nil {
		t.Fatal("want refusal")
	}
}

func TestLoadRefusesCorruptFilesAndLeavesThem(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "REPOS.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := run(t, root, "add", "x", "https://github.com/org/x")
	if code != 1 || !strings.Contains(errOut, "cannot parse") {
		t.Fatalf("code = %d, err = %q", code, errOut)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "REPOS.json")); string(got) != "{" {
		t.Fatalf("overwritten: %s", got)
	}
}

func TestSetPathMakesAbsoluteButKeepsTilde(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root, "sample")
	if err := r.Add("x", "https://github.com/org/x", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPath("x", "~/code/x"); err != nil {
		t.Fatal(err)
	}
	if r.Paths["x"] != "~/code/x" {
		t.Fatalf("path = %q", r.Paths["x"])
	}
	home, _ := os.UserHomeDir()
	if p, _ := r.Resolve("x"); p != filepath.Join(home, "code", "x") {
		t.Fatalf("Resolve = %q", p)
	}
	if err := r.SetPath("x", "relative/x"); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(r.Paths["x"]) {
		t.Fatalf("path = %q", r.Paths["x"])
	}
	if err := r.SetPath("nope", "/x"); err == nil || !strings.Contains(err.Error(), "unknown repo") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveErrors(t *testing.T) {
	r, _ := Load(t.TempDir(), "sample")
	if err := r.Add("x", "https://github.com/org/x", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("x"); err == nil || !strings.Contains(err.Error(), "no local path") {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.Resolve("nope"); err == nil || !strings.Contains(err.Error(), "unknown repo") {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeRemote(t *testing.T) {
	want := "github.com/org/x"
	for _, in := range []string{
		"git@github.com:org/x.git",
		"git@github.com:org/x",
		"https://github.com/org/x",
		"https://github.com/org/x.git",
		"https://GitHub.com/org/x/",
		"ssh://git@github.com/org/x.git",
	} {
		if got := normalizeRemote(in); got != want {
			t.Errorf("normalizeRemote(%q) = %q", in, got)
		}
	}
	if normalizeRemote("https://github.com/org/x") == normalizeRemote("https://github.com/org/y") {
		t.Error("different repos compare equal")
	}
	if got := normalizeRemote("/local/path/x.git"); got != "/local/path/x" {
		t.Errorf("local path = %q", got)
	}
}

func TestDoctorFindsEachProblem(t *testing.T) {
	root := t.TempDir()
	origin := remoteRepo(t)
	good := t.TempDir()
	git(t, good, "clone", "-q", origin, ".")
	other := t.TempDir()
	git(t, other, "clone", "-q", origin, ".")
	r, _ := Load(root, "sample")
	for name, remote := range map[string]string{"a-good": origin, "b-unmapped": origin, "c-missing": origin, "d-notgit": origin, "e-mismatch": "https://github.com/org/elsewhere"} {
		if err := r.Add(name, remote, ""); err != nil {
			t.Fatal(err)
		}
	}
	r.Paths["a-good"] = good
	r.Paths["c-missing"] = filepath.Join(root, "gone")
	r.Paths["d-notgit"] = t.TempDir()
	r.Paths["e-mismatch"] = other
	got := map[string]string{}
	for _, p := range r.Doctor() {
		got[p.Repo] = p.Detail
	}
	if _, ok := got["a-good"]; ok || len(got) != 4 {
		t.Fatalf("problems = %+v", got)
	}
	for name, want := range map[string]string{"b-unmapped": "no local path", "c-missing": "missing on disk", "d-notgit": "not a git checkout", "e-mismatch": "remote mismatch"} {
		if !strings.Contains(got[name], want) {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

func TestDoctorCloneFetchesMissingRepos(t *testing.T) {
	root := t.TempDir()
	origin := remoteRepo(t)
	into := t.TempDir()
	mapped := filepath.Join(into, "elsewhere", "mapped")
	r, _ := Load(root, "sample")
	for _, name := range []string{"mapped", "unmapped"} {
		if err := r.Add(name, origin, ""); err != nil {
			t.Fatal(err)
		}
	}
	r.Paths["mapped"] = mapped
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	// Without --into the unmapped repo stays a problem, and the mapped one clones.
	code, _, errOut := run(t, root, "doctor", "--clone")
	if code != 1 || !strings.Contains(errOut, "pass --into") {
		t.Fatalf("code = %d, err = %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(mapped, "README.md")); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, root, "doctor", "--clone", "--into", into)
	if code != 0 || !strings.Contains(out, "2 repos ok") {
		t.Fatalf("code = %d, out = %q, err = %q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(into, "unmapped", "README.md")); err != nil {
		t.Fatal(err)
	}
	r, _ = Load(root, "sample")
	if p, _ := r.Resolve("unmapped"); p != filepath.Join(into, "unmapped") {
		t.Fatalf("path not recorded: %q", p)
	}
}

func TestCloneRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root, "sample")
	if err := r.Add("x", remoteRepo(t), ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Clone("x", t.TempDir()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

// A refused clone must not leave doctor believing the repo has a path.
func TestFailedCloneRecordsNoPath(t *testing.T) {
	root := t.TempDir()
	origin := remoteRepo(t)
	into := t.TempDir()
	if err := os.MkdirAll(filepath.Join(into, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, filepath.Join(into, "x"), "clone", "-q", origin, ".")
	if code, _, _ := run(t, root, "add", "x", origin); code != 0 {
		t.Fatal("add failed")
	}
	code, out, errOut := run(t, root, "doctor", "--clone", "--into", into)
	if code != 1 || !strings.Contains(errOut, "already exists") || !strings.Contains(out, "x: no local path") {
		t.Fatalf("code = %d, out = %q, err = %q", code, out, errOut)
	}
	r, _ := Load(root, "sample")
	if _, ok := r.Paths["x"]; ok {
		t.Fatalf("paths = %+v", r.Paths)
	}
}

func TestCloneRefusesHostileCommittedRemote(t *testing.T) {
	root := t.TempDir()
	body := `{"repos":{"x":{"remote":"--upload-pack=touch pwned","defaultBranch":"main"}}}`
	if err := os.WriteFile(filepath.Join(root, "REPOS.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Load(root, "sample")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Clone("x", filepath.Join(root, "x")); err == nil || !strings.Contains(err.Error(), "invalid remote") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvFlags(t *testing.T) {
	root := t.TempDir()
	spaced := filepath.Join(t.TempDir(), "my repo's")
	plain := t.TempDir()
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	r, _ := Load(root, "sample")
	for _, name := range []string{"plain", "spaced", "unmapped", "gone"} {
		if err := r.Add(name, "https://github.com/org/"+name, ""); err != nil {
			t.Fatal(err)
		}
	}
	r.Paths["plain"] = plain
	r.Paths["spaced"] = spaced
	r.Paths["gone"] = filepath.Join(root, "gone")
	flags, err := r.EnvFlags([]string{"plain", "spaced"})
	if err != nil {
		t.Fatal(err)
	}
	want := "--add-dir " + plain + " --add-dir '" + strings.ReplaceAll(spaced, "'", `'\''`) + "'"
	if flags != want {
		t.Fatalf("flags = %q, want %q", flags, want)
	}
	// The shell has to read it back as the original path.
	out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(spaced)).Output()
	if err != nil || string(out) != spaced {
		t.Fatalf("round trip = %q, %v", out, err)
	}
	for _, name := range []string{"unmapped", "gone", "nope"} {
		if _, err := r.EnvFlags([]string{"plain", name}); err == nil {
			t.Errorf("%s: want an error, not a partial list", name)
		}
	}
}

func TestRunEnvPrintsNothingForNoRepos(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := RunEnv(t.TempDir(), "sample", nil, &out, &errOut); code != 0 || out.Len() != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
}

func TestListAndUsage(t *testing.T) {
	root := t.TempDir()
	if code, _, _ := run(t, root, "add", "x", "https://github.com/org/x"); code != 0 {
		t.Fatal("add failed")
	}
	_, out, _ := run(t, root, "list")
	if !strings.Contains(out, "x  https://github.com/org/x  main  (no local path)") {
		t.Fatalf("list = %q", out)
	}
	if code, _, errOut := run(t, root, "bogus"); code != 1 || !strings.Contains(errOut, "sample repo add") {
		t.Fatalf("code = %d, err = %q", code, errOut)
	}
}
