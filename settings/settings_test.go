package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sample struct {
	Gates string
	Name  string
}

func spec() Spec[sample] {
	return Spec[sample]{
		Tool:     "sample",
		Defaults: func() sample { return sample{Gates: "block", Name: "default"} },
		Apply: func(s *sample, key string, value any) {
			v, ok := value.(string)
			if !ok {
				return
			}
			switch key {
			case "gates":
				s.Gates = v
			case "name":
				s.Name = v
			}
		},
		Validate: func(s *sample) error { return CheckGates("sample", s.Gates) },
	}
}

func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLayersDefaultsGlobalProject(t *testing.T) {
	xdg, root := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	s := spec()
	writeJSON(t, filepath.Join(xdg, "sample", "settings.json"), `{"gates":"prompt","name":"global"}`)
	writeJSON(t, filepath.Join(root, ".sample", "settings.json"), `{"name":"project"}`)
	loaded, err := s.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := (sample{Gates: "prompt", Name: "project"}); loaded.Effective != want {
		t.Fatalf("effective = %+v, want %+v", loaded.Effective, want)
	}
	if loaded.Global["name"] != "global" || loaded.Project["name"] != "project" {
		t.Fatalf("layers not preserved: %+v", loaded)
	}
}

func TestLoadDefaultsWhenNoFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	loaded, err := spec().Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := (sample{Gates: "block", Name: "default"}); loaded.Effective != want {
		t.Fatalf("effective = %+v", loaded.Effective)
	}
}

func TestMergeEmptyOverridesAndNullIgnored(t *testing.T) {
	got, err := spec().Merge(map[string]any{"name": ""}, map[string]any{"gates": nil})
	if err != nil {
		t.Fatal(err)
	}
	if want := (sample{Gates: "block", Name: ""}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMergeValidationFails(t *testing.T) {
	_, err := spec().Merge(map[string]any{"gates": "nope"})
	if err == nil || !strings.Contains(err.Error(), `sample: invalid gates "nope" (expected block|prompt|auto)`) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadIfExistsErrors(t *testing.T) {
	s := spec()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	writeJSON(t, bad, `{`)
	if _, err := s.ReadIfExists(bad); err == nil || !strings.Contains(err.Error(), "cannot parse settings") {
		t.Fatalf("bad JSON err = %v", err)
	}
	list := filepath.Join(dir, "list.json")
	writeJSON(t, list, `[]`)
	if _, err := s.ReadIfExists(list); err == nil || !strings.Contains(err.Error(), "must be a JSON object") {
		t.Fatalf("non-object err = %v", err)
	}
}

func TestGlobalPathHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := spec().GlobalPath(), filepath.Join("/xdg", "sample", "settings.json"); got != want {
		t.Fatalf("GlobalPath = %q, want %q", got, want)
	}
}

func TestVCSDefaultsToGit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	loaded, err := spec().Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VCS != VCSGit {
		t.Fatalf("VCS = %q, want git", loaded.VCS)
	}
}

func TestVCSProjectSupersedesGlobal(t *testing.T) {
	for _, tc := range []struct{ global, project, want string }{
		{"none", "git", "git"},
		{"git", "none", "none"},
		{"none", "", "none"},
	} {
		xdg, root := t.TempDir(), t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		writeJSON(t, filepath.Join(xdg, "sample", "settings.json"), `{"vcs":"`+tc.global+`"}`)
		project := `{}`
		if tc.project != "" {
			project = `{"vcs":"` + tc.project + `"}`
		}
		writeJSON(t, filepath.Join(root, ".sample", "settings.json"), project)
		loaded, err := spec().Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.VCS != tc.want {
			t.Fatalf("global %q, project %q: VCS = %q, want %q", tc.global, tc.project, loaded.VCS, tc.want)
		}
	}
}

func TestVCSValidationFails(t *testing.T) {
	_, err := spec().Merge(map[string]any{"vcs": "svn"})
	if err == nil || !strings.Contains(err.Error(), `sample: invalid vcs "svn" (expected git|none)`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := spec().Merge(map[string]any{"vcs": true}); err == nil || !strings.Contains(err.Error(), "invalid vcs") {
		t.Fatalf("non-string err = %v", err)
	}
	xdg, root := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	writeJSON(t, filepath.Join(root, ".sample", "settings.json"), `{"vcs":"svn"}`)
	if _, err := spec().Load(root); err == nil || !strings.Contains(err.Error(), "invalid vcs") {
		t.Fatalf("Load err = %v", err)
	}
}

func TestVCSOfIgnoresNull(t *testing.T) {
	got, err := VCSOf("sample", map[string]any{"vcs": "none"}, map[string]any{"vcs": nil})
	if err != nil || got != VCSNone {
		t.Fatalf("VCSOf = %q, %v", got, err)
	}
}
