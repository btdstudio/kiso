package scaffold

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func engine() Engine {
	return Engine{
		Tool: "sample",
		FS: fstest.MapFS{
			"AGENTS.md":          {Data: []byte("agents\n")},
			"README.md":          {Data: []byte("readme\n")},
			".gitignore":         {Data: []byte(".sample/active\n")},
			"00_intake/.gitkeep": {Data: nil},
			"sub/.DS_Store":      {Data: []byte("junk")},
		},
		Marker: "00_intake",
		Seeds: func() ([]Seed, error) {
			return []Seed{{Rel: ".sample/settings.json", Data: []byte("{}\n")}, {Rel: "LOG.md"}}, nil
		},
	}
}

func scaffoldDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := engine().Scaffold(dir, Options{NoGit: true}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestScaffoldWritesTreeSeedsAndManifest(t *testing.T) {
	dir := t.TempDir()
	rep, err := engine().Scaffold(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.GitInit {
		t.Error("expected git init on empty dir")
	}
	for _, rel := range []string{"AGENTS.md", ".gitignore", "00_intake/.gitkeep", ".sample/settings.json", "LOG.md", ".sample/manifest.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Errorf(".DS_Store should not be scaffolded: %v", err)
	}
}

func TestScaffoldSkipsExistingUnlessForced(t *testing.T) {
	dir := scaffoldDir(t)
	agents := filepath.Join(dir, "AGENTS.md")
	seed := filepath.Join(dir, ".sample", "settings.json")
	write(t, agents, "mine")
	write(t, seed, "my settings")
	if _, err := engine().Scaffold(dir, Options{NoGit: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, agents); got != "mine" {
		t.Errorf("AGENTS.md clobbered: %q", got)
	}
	if _, err := engine().Scaffold(dir, Options{NoGit: true, Force: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, agents); got != "agents\n" {
		t.Errorf("Force did not rewrite AGENTS.md: %q", got)
	}
	if got := read(t, seed); got != "my settings" {
		t.Errorf("seed must never be overwritten: %q", got)
	}
}

func TestManifestIsDeterministicAndRoundTrips(t *testing.T) {
	e := engine()
	m, err := e.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files["sub/.DS_Store"]; ok {
		t.Error("manifest includes .DS_Store")
	}
	dir := t.TempDir()
	if err := e.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	got, err := e.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("round trip = %+v, want %+v", got, m)
	}
}

func TestUpdateSkipsEditedAndRestoresMissing(t *testing.T) {
	dir := scaffoldDir(t)
	write(t, filepath.Join(dir, "AGENTS.md"), "mine")
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	rep, err := engine().Update(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Skipped, []string{"AGENTS.md"}) || !reflect.DeepEqual(rep.Updated, []string{"README.md"}) {
		t.Fatalf("rep = %+v", rep)
	}
	if got := read(t, filepath.Join(dir, "AGENTS.md")); got != "mine" {
		t.Fatalf("edit clobbered: %q", got)
	}
}

func TestUpdateReplacesUneditedOlderFile(t *testing.T) {
	dir := scaffoldDir(t)
	e := engine()
	readme := filepath.Join(dir, "README.md")
	write(t, readme, "older")
	m, err := e.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Files["README.md"] = hashBytes([]byte("older"))
	if err := e.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	rep, err := e.Update(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Updated, []string{"README.md"}) || read(t, readme) != "readme\n" {
		t.Fatalf("rep = %+v, readme = %q", rep, read(t, readme))
	}
}

func TestUpdateFileAlreadyAtNextVersionIsUnchanged(t *testing.T) {
	dir := scaffoldDir(t)
	e := engine()
	m, err := e.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Files["README.md"] = hashBytes([]byte("an older hash"))
	if err := e.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	rep, err := e.Update(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 0 || len(rep.Updated) != 0 {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestUpdateRebuildsMissingManifestWithoutClobbering(t *testing.T) {
	dir := scaffoldDir(t)
	if err := os.RemoveAll(filepath.Join(dir, ".sample")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "AGENTS.md"), "mine")
	rep, err := engine().Update(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Skipped, []string{"AGENTS.md"}) || len(rep.Updated) != 0 {
		t.Fatalf("rep = %+v", rep)
	}
	if _, err := engine().ReadManifest(dir); err != nil {
		t.Fatalf("manifest not rebuilt: %v", err)
	}
}

func TestUpdateRefusesNonWorkspace(t *testing.T) {
	dir := t.TempDir()
	if _, err := engine().Update(dir); err == nil || !strings.Contains(err.Error(), "not a sample workspace") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("wrote into a non-workspace: %v", entries)
	}
}

func TestUpdateReportsCorruptManifest(t *testing.T) {
	dir := scaffoldDir(t)
	write(t, filepath.Join(dir, ".sample", "manifest.json"), "{")
	if _, err := engine().Update(dir); err == nil || !strings.HasPrefix(err.Error(), "sample update: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheck(t *testing.T) {
	if problems := engine().Check(); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	e := Engine{Tool: "sample", FS: fstest.MapFS{"bad.md": {Data: []byte("hello {{ name }}")}}}
	if problems := e.Check(); len(problems) != 1 || !strings.HasPrefix(problems[0], "bad.md:") {
		t.Fatalf("problems = %v", problems)
	}
}

func localEngine() Engine {
	e := engine()
	e.VCS = "none"
	return e
}

// fakeGit puts a `git` that records each call into a marker file alone on PATH,
// and returns the marker's path. The marker exists only if git was run.
func fakeGit(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake git needs /bin/sh")
	}
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "git-calls")
	script := "#!/bin/sh\necho \"$@\" >> '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return marker
}

var gitFiles = []string{".gitignore", "00_intake/.gitkeep"}

func TestIsGitFile(t *testing.T) {
	for rel, want := range map[string]bool{
		".gitignore":     true,
		".gitattributes": true,
		".gitkeep":       true,
		"a/b/.gitkeep":   true,
		"sub/.gitignore": true,
		".gitignore.bak": false,
		"x.gitkeep":      false,
		".git/config":    false,
		"AGENTS.md":      false,
		"gitignore":      false,
	} {
		if got := IsGitFile(rel); got != want {
			t.Errorf("IsGitFile(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestManifestUnderVCSNoneHasNoGitFiles(t *testing.T) {
	m, err := localEngine().BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range gitFiles {
		if _, ok := m.Files[rel]; ok {
			t.Errorf("manifest lists %s", rel)
		}
	}
	if _, ok := m.Files["AGENTS.md"]; !ok {
		t.Error("manifest lost AGENTS.md")
	}
	for _, vcs := range []string{"", "git"} {
		e := engine()
		e.VCS = vcs
		got, err := e.BuildManifest()
		if err != nil {
			t.Fatal(err)
		}
		want, _ := engine().BuildManifest()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("vcs %q: manifest = %+v, want %+v", vcs, got, want)
		}
	}
}

func TestScaffoldLocalWorkspace(t *testing.T) {
	marker := fakeGit(t)
	dir := t.TempDir()
	rep, err := localEngine().Scaffold(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.GitInit {
		t.Error("GitInit reported for a local workspace")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		calls, _ := os.ReadFile(marker)
		t.Fatalf("git was run: %q", calls)
	}
	for _, rel := range append([]string{".git", ".gitattributes"}, gitFiles...) {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s exists or err = %v", rel, err)
		}
	}
	for _, rel := range rep.Written {
		if IsGitFile(rel) {
			t.Errorf("reported git file %s", rel)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "00_intake")); err != nil || !info.IsDir() {
		t.Errorf("00_intake missing: %v", err)
	}
	if got := read(t, filepath.Join(dir, ".sample", "settings.json")); got != "{\n  \"vcs\": \"none\"\n}\n" {
		t.Errorf("settings = %q", got)
	}
	m, err := localEngine().ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for rel := range m.Files {
		if IsGitFile(rel) {
			t.Errorf("manifest lists %s", rel)
		}
	}
}

func TestScaffoldLocalKeepsOtherSettingsAndCreatesMissingFile(t *testing.T) {
	e := localEngine()
	e.Seeds = func() ([]Seed, error) {
		return []Seed{{Rel: ".sample/settings.json", Data: []byte("{\"gates\": \"block\"}\n")}}, nil
	}
	dir := t.TempDir()
	if _, err := e.Scaffold(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, ".sample", "settings.json")); got != "{\n  \"gates\": \"block\",\n  \"vcs\": \"none\"\n}\n" {
		t.Errorf("settings = %q", got)
	}

	e.Seeds = nil
	dir = t.TempDir()
	rep, err := e.Scaffold(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, ".sample", "settings.json")); got != "{\n  \"vcs\": \"none\"\n}\n" {
		t.Errorf("settings = %q", got)
	}
	found := false
	for _, rel := range rep.Written {
		found = found || rel == ".sample/settings.json"
	}
	if !found {
		t.Errorf("created settings not reported: %v", rep.Written)
	}
}

func TestScaffoldLocalRefusesNonObjectSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".sample", "settings.json"), "[]\n")
	if _, err := localEngine().Scaffold(dir, Options{}); err == nil || !strings.Contains(err.Error(), "must be a JSON object") {
		t.Fatalf("err = %v", err)
	}
	if got := read(t, filepath.Join(dir, ".sample", "settings.json")); got != "[]\n" {
		t.Errorf("settings overwritten: %q", got)
	}
}

func TestInvalidVCSIsAnError(t *testing.T) {
	e := engine()
	e.VCS = "svn"
	if _, err := e.Scaffold(t.TempDir(), Options{NoGit: true}); err == nil || !strings.Contains(err.Error(), `invalid vcs "svn"`) {
		t.Fatalf("Scaffold err = %v", err)
	}
	if _, err := e.Update(scaffoldDir(t)); err == nil || !strings.Contains(err.Error(), `invalid vcs "svn"`) {
		t.Fatalf("Update err = %v", err)
	}
}

func TestUpdateLocalWritesAndReportsNoGitFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := localEngine().Scaffold(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "00_intake")); err != nil {
		t.Fatal(err)
	}
	rep, err := localEngine().Update(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]string{rep.Updated, rep.Skipped, rep.Unchanged} {
		for _, rel := range list {
			if IsGitFile(rel) {
				t.Errorf("reported git file %s: %+v", rel, rep)
			}
		}
	}
	for _, rel := range gitFiles {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s created or err = %v", rel, err)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "00_intake")); err != nil || !info.IsDir() {
		t.Errorf("00_intake not recreated: %v", err)
	}
	m, err := localEngine().ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for rel := range m.Files {
		if IsGitFile(rel) {
			t.Errorf("manifest lists %s", rel)
		}
	}
}

func TestUpdateSwitchToNoneLeavesGitFilesOnDisk(t *testing.T) {
	dir := scaffoldDir(t)
	write(t, filepath.Join(dir, ".gitignore"), "edited\n")
	before := map[string]string{}
	for _, rel := range gitFiles {
		before[rel] = read(t, filepath.Join(dir, filepath.FromSlash(rel)))
	}
	if _, err := localEngine().Update(dir); err != nil {
		t.Fatal(err)
	}
	m, err := localEngine().ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range gitFiles {
		if _, ok := m.Files[rel]; ok {
			t.Errorf("manifest still lists %s", rel)
		}
		if got := read(t, filepath.Join(dir, filepath.FromSlash(rel))); got != before[rel] {
			t.Errorf("%s changed: %q, want %q", rel, got, before[rel])
		}
	}
}

func TestUpdateSwitchToGitRestoresGitFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := localEngine().Scaffold(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	rep, err := engine().Update(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := engine().ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range gitFiles {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s not restored: %v", rel, err)
		}
		if _, ok := m.Files[rel]; !ok {
			t.Errorf("manifest missing %s", rel)
		}
		found := false
		for _, u := range rep.Updated {
			found = found || u == rel
		}
		if !found {
			t.Errorf("%s not in Updated: %v", rel, rep.Updated)
		}
	}
}

// richEngine has every git file kind, including nested ones.
func richEngine(vcs string) Engine {
	e := engine()
	e.FS = fstest.MapFS{
		"AGENTS.md":          {Data: []byte("agents\n")},
		".gitignore":         {Data: []byte("x\n")},
		".gitattributes":     {Data: []byte("* text=auto\n")},
		"00_intake/.gitkeep": {Data: nil},
		"a/b/.gitkeep":       {Data: nil},
		"docs/.gitignore":    {Data: []byte("tmp\n")},
		"docs/guide.md":      {Data: []byte("guide\n")},
	}
	e.VCS = vcs
	return e
}

// AC8: every git file kind, at any depth, is left out; their folders exist.
func TestScaffoldLocalOmitsEveryGitFileKind(t *testing.T) {
	fakeGit(t)
	dir := t.TempDir()
	if _, err := richEngine("none").Scaffold(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".gitignore", ".gitattributes", "00_intake/.gitkeep", "a/b/.gitkeep", "docs/.gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s exists or err = %v", rel, err)
		}
	}
	for _, d := range []string{"00_intake", "a/b", "docs"} {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(d))); err != nil || !info.IsDir() {
			t.Errorf("folder %s missing: %v", d, err)
		}
	}
	if got := read(t, filepath.Join(dir, "docs", "guide.md")); got != "guide\n" {
		t.Errorf("guide.md = %q", got)
	}
}

// snapshot returns every file under dir with its contents.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// AC13: an explicit "git" scaffolds and updates exactly like an unset vcs.
func TestVCSGitMatchesUnset(t *testing.T) {
	unsetDir, gitDir := t.TempDir(), t.TempDir()
	repUnset, err := richEngine("").Scaffold(unsetDir, Options{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	repGit, err := richEngine("git").Scaffold(gitDir, Options{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repUnset, repGit) {
		t.Fatalf("Scaffold reports differ: %+v vs %+v", repUnset, repGit)
	}
	if a, b := snapshot(t, unsetDir), snapshot(t, gitDir); !reflect.DeepEqual(a, b) {
		t.Fatalf("trees differ:\n%v\n%v", a, b)
	}
	for _, d := range []string{unsetDir, gitDir} {
		if err := os.Remove(filepath.Join(d, ".gitattributes")); err != nil {
			t.Fatal(err)
		}
	}
	upUnset, err := richEngine("").Update(unsetDir)
	if err != nil {
		t.Fatal(err)
	}
	upGit, err := richEngine("git").Update(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	sort := func(r *UpdateReport) {
		for _, l := range []*[]string{&r.Updated, &r.Skipped, &r.Unchanged} {
			slices.Sort(*l)
		}
	}
	sort(&upUnset)
	sort(&upGit)
	if !reflect.DeepEqual(upUnset, upGit) {
		t.Fatalf("Update reports differ: %+v vs %+v", upUnset, upGit)
	}
	if a, b := snapshot(t, unsetDir), snapshot(t, gitDir); !reflect.DeepEqual(a, b) {
		t.Fatalf("trees differ after update:\n%v\n%v", a, b)
	}
}

// Review #1: recording "vcs": "none" leaves the other values exactly as written.
func TestScaffoldLocalKeepsValuesVerbatim(t *testing.T) {
	e := localEngine()
	e.Seeds = func() ([]Seed, error) {
		return []Seed{{Rel: ".sample/settings.json", Data: []byte(`{"url": "https://x.test/?a=1&b=<2>", "n": 12345678901234567890, "f": 1.50}` + "\n")}}, nil
	}
	dir := t.TempDir()
	if _, err := e.Scaffold(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"f\": 1.50,\n  \"n\": 12345678901234567890,\n  \"url\": \"https://x.test/?a=1&b=<2>\",\n  \"vcs\": \"none\"\n}\n"
	if got := read(t, filepath.Join(dir, ".sample", "settings.json")); got != want {
		t.Errorf("settings = %q, want %q", got, want)
	}
}

func TestScaffoldLocalRefusesTrailingData(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".sample", "settings.json"), "{} x\n")
	if _, err := localEngine().Scaffold(dir, Options{}); err == nil || !strings.Contains(err.Error(), "cannot parse settings") {
		t.Fatalf("err = %v", err)
	}
}
