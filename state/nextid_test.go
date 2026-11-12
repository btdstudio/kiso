package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeRecords writes a STATE.json recording the named changes, closed, with no
// folders, as if each folder had been removed after the change was closed.
func writeRecords(t *testing.T, s Store, root string, names ...string) {
	t.Helper()
	changes := map[string]*Change{}
	for _, name := range names {
		c := NewChange(name)
		c.Phase, c.Status = "ship", Closed
		changes[name] = &c
	}
	buf, err := json.Marshal(stateFile{Schema: Schema, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.StatePath(root), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeFolders(t *testing.T, s Store, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(s.ChangeDir(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNextIDCountsFoldersAndRecords(t *testing.T) {
	cases := []struct {
		name             string
		records, folders []string
		noSpecs          bool
		want             int
	}{
		{name: "nothing", noSpecs: true, want: 1},
		{name: "empty specs", want: 1},
		{name: "records only", records: []string{"5-old"}, want: 6},
		{name: "record higher than folder", records: []string{"5-old"}, folders: []string{"2-x"}, want: 6},
		{name: "folders only", folders: []string{"1-a", "2-b"}, want: 3},
		{name: "folders higher", records: []string{"4-r"}, folders: []string{"7-f"}, want: 8},
		{name: "records higher", records: []string{"7-r"}, folders: []string{"4-f"}, want: 8},
		{name: "agree", records: []string{"1-a", "2-b"}, folders: []string{"1-a", "2-b"}, want: 3},
		{name: "no prefix ignored", records: []string{"notes", "2-x"}, folders: []string{"README", "1-a"}, want: 3},
		{name: "no specs with a record", records: []string{"3-x"}, noSpecs: true, want: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, s := t.TempDir(), store()
			if !tc.noSpecs {
				if err := os.MkdirAll(s.SpecsPath(root), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.records != nil {
				writeRecords(t, s, root, tc.records...)
			}
			makeFolders(t, s, root, tc.folders...)
			got, err := s.NextID(root)
			if err != nil || got != tc.want {
				t.Fatalf("NextID = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestNextIDRefusesBadState(t *testing.T) {
	cases := map[string]func(t *testing.T, s Store, root string){
		"corrupt": func(t *testing.T, s Store, root string) {
			if err := os.WriteFile(s.StatePath(root), []byte(`{"changes":{`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"unreadable": func(t *testing.T, s Store, root string) {
			if err := os.Mkdir(s.StatePath(root), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"newer schema": func(t *testing.T, s Store, root string) {
			if err := os.WriteFile(s.StatePath(root), []byte(`{"schema":99,"changes":{}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			root, s := t.TempDir(), store()
			makeFolders(t, s, root, "1-a")
			setup(t, s, root)
			if id, err := s.NextID(root); err == nil || id != 0 {
				t.Fatalf("NextID = %d, %v; want an error", id, err)
			}
		})
	}
	t.Run("preflight", func(t *testing.T) {
		root, s := t.TempDir(), store()
		s.Preflight = func(string) error { return os.ErrPermission }
		if id, err := s.NextID(root); !errors.Is(err, os.ErrPermission) || id != 0 {
			t.Fatalf("NextID = %d, %v; want the preflight error", id, err)
		}
	})
}

func TestNextIDRefusesUnreadableSpecs(t *testing.T) {
	root, s := t.TempDir(), store()
	if err := os.WriteFile(s.SpecsPath(root), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if id, err := s.NextID(root); err == nil || !strings.Contains(err.Error(), "cannot read") || id != 0 {
		t.Fatalf("NextID = %d, %v; want a cannot read error", id, err)
	}
}

func TestSeedRefusesRecordedName(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Seed(root, "2-beta", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloseChange(root, "2-beta", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.ChangeDir(root, "2-beta")); err != nil {
		t.Fatal(err)
	}
	state, _ := os.ReadFile(s.StatePath(root))
	log, _ := os.ReadFile(s.LogPath(root))

	_, err := s.Seed(root, "2-beta", SeedOptions{})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(s.StatePath(root)); !bytes.Equal(got, state) {
		t.Errorf("STATE.json changed:\n%s", got)
	}
	if got, _ := os.ReadFile(s.LogPath(root)); !bytes.Equal(got, log) {
		t.Errorf("LOG.md changed:\n%s", got)
	}
	if _, err := os.Stat(s.ChangeDir(root, "2-beta")); !os.IsNotExist(err) {
		t.Errorf("refused seed created the folder: %v", err)
	}
	changes, err := s.ReadChanges(root)
	if err != nil {
		t.Fatal(err)
	}
	if c := changes["2-beta"]; c == nil || c.Status != Closed || c.Gates["intake"] != Approved {
		t.Fatalf("record = %+v", c)
	}
}

func TestSeedWritesNothingOnBadState(t *testing.T) {
	root, s := t.TempDir(), store()
	if err := os.WriteFile(s.StatePath(root), []byte(`{"changes":{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err == nil {
		t.Fatal("seed must fail on corrupt state")
	}
	if _, err := os.Stat(s.SpecsPath(root)); !os.IsNotExist(err) {
		t.Errorf("refused seed created the specs folder: %v", err)
	}
	if _, err := os.Stat(s.LogPath(root)); !os.IsNotExist(err) {
		t.Errorf("refused seed wrote LOG.md: %v", err)
	}
}

// seedNext seeds a change the way the tools do: NextID, then Seed.
func seedNext(t *testing.T, s Store, root, title string) string {
	t.Helper()
	id, err := s.NextID(root)
	if err != nil {
		t.Fatal(err)
	}
	name := strconv.Itoa(id) + "-" + Slugify(title)
	if _, err := s.Seed(root, name, SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	return name
}

// TestIssueReproNoReuse replays the reported repro: a closed change whose folder
// was removed must not give its id to the next change.
func TestIssueReproNoReuse(t *testing.T) {
	root, s := t.TempDir(), store()
	for _, title := range []string{"alpha", "beta"} {
		seedNext(t, s, root, title)
	}
	if _, err := s.CloseChange(root, "2-beta", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.ChangeDir(root, "2-beta")); err != nil {
		t.Fatal(err)
	}
	if got := seedNext(t, s, root, "gamma"); got != "3-gamma" {
		t.Fatalf("next change = %q, want 3-gamma", got)
	}

	// Folders and records agree: the same names, folders and log lines as before.
	root, s = t.TempDir(), store()
	for i, title := range []string{"alpha", "beta", "gamma"} {
		want := strconv.Itoa(i+1) + "-" + title
		if got := seedNext(t, s, root, title); got != want {
			t.Fatalf("change %d = %q, want %q", i+1, got, want)
		}
		if _, err := os.Stat(filepath.Join(s.ChangeDir(root, want), ".gitkeep")); err != nil {
			t.Fatal(err)
		}
	}
	log, _ := os.ReadFile(s.LogPath(root))
	if n := strings.Count(string(log), ": created\n"); n != 3 {
		t.Fatalf("log has %d created lines:\n%s", n, log)
	}
	for _, want := range []string{"1-alpha: created", "2-beta: created", "3-gamma: created"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}
