package scaffold

import (
	"os"
	"path/filepath"
	"reflect"
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
