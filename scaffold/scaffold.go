// Package scaffold is the engine behind a tool's `init`, `update` and `check`:
// it copies an embedded file tree into a workspace, refreshes managed files
// without overwriting user edits (tracked by a manifest of content hashes), and
// validates the tree.
package scaffold

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/btdstudio/kiso/settings"
	"github.com/btdstudio/kiso/version"
	"github.com/btdstudio/kiso/workspace"
)

// Engine scaffolds one tool's workspace.
type Engine struct {
	Tool string
	// FS holds the tree to copy out, rooted at the workspace root.
	FS fs.FS
	// Marker is a folder that identifies an existing workspace, so `update` can
	// rebuild a lost manifest without writing into an unrelated directory.
	Marker string
	// Seeds generates files that `init` writes once and `update` never touches,
	// such as settings and state. Optional.
	Seeds func() ([]Seed, error)
	// VCS is the workspace's core "vcs" setting. "" and settings.VCSGit scaffold
	// the whole tree. settings.VCSNone leaves out .gitattributes and .gitkeep
	// everywhere (init and update don't write, record or report them) but keeps
	// .gitignore, and init doesn't run `git init` and records "vcs": "none" in
	// the project settings.
	VCS string
}

// IsGitFile reports whether rel, a slash-separated path, is a git file: a
// .gitignore, .gitattributes or .gitkeep at any depth. A local workspace (VCS
// none) leaves out every git file except .gitignore, which it keeps so that
// machine-local files such as .<tool>/active and .<tool>/paths.json stay out of
// an enclosing git repo, or of one created later.
func IsGitFile(rel string) bool {
	switch path.Base(rel) {
	case ".gitignore", ".gitattributes", ".gitkeep":
		return true
	}
	return false
}

// omitWhenLocal reports whether a local workspace (vcs: none) leaves rel out:
// every git file except .gitignore, which still keeps .<tool>/active and
// .<tool>/paths.json out of an enclosing or later git repo.
func omitWhenLocal(rel string) bool {
	return IsGitFile(rel) && path.Base(rel) != ".gitignore"
}

func (e Engine) local() bool { return e.VCS == settings.VCSNone }

func (e Engine) checkVCS() error {
	if e.VCS == "" {
		return nil
	}
	return settings.CheckVCS(e.Tool, e.VCS)
}

// Seed is a generated file, written only when absent.
type Seed struct {
	Rel  string // slash-separated path relative to the workspace root
	Data []byte
}

// Manifest records the tool version and a sha256 per managed file, so `update`
// can tell which files the user has edited.
type Manifest struct {
	Version string            `json:"version"`
	Files   map[string]string `json:"files"` // relative path -> sha256 hex
}

// Options configure Scaffold.
type Options struct {
	NoGit bool
	Force bool
}

// Report describes what Scaffold did.
type Report struct {
	Written []string
	GitInit bool
}

// UpdateReport describes what Update did.
type UpdateReport struct {
	Updated   []string
	Skipped   []string
	Unchanged []string
}

func (e Engine) walk(fn func(rel string, data []byte) error) error {
	return fs.WalkDir(e.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip OS junk (macOS .DS_Store) so the tree and its manifest are
		// deterministic regardless of the build machine.
		if path.Base(p) == ".DS_Store" {
			return nil
		}
		// A local workspace leaves out git files, except .gitignore.
		if e.local() && omitWhenLocal(p) {
			return nil
		}
		data, err := fs.ReadFile(e.FS, p)
		if err != nil {
			return err
		}
		return fn(p, data)
	})
}

// keptDirs creates, under a local workspace, the folders that the tree's
// .gitkeep files exist to keep, since walk leaves those files out.
func (e Engine) keptDirs(dir string) error {
	if !e.local() {
		return nil
	}
	return fs.WalkDir(e.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Base(p) != ".gitkeep" {
			return nil
		}
		return os.MkdirAll(filepath.Join(dir, filepath.FromSlash(path.Dir(p))), 0o755)
	})
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// BuildManifest walks the tree and hashes every file.
func (e Engine) BuildManifest() (Manifest, error) {
	m := Manifest{Version: version.String(), Files: map[string]string{}}
	err := e.walk(func(rel string, data []byte) error {
		m.Files[rel] = hashBytes(data)
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (e Engine) manifestPath(dir string) string {
	return filepath.Join(workspace.Dir(dir, e.Tool), "manifest.json")
}

// ReadManifest reads .<tool>/manifest.json.
func (e Engine) ReadManifest(dir string) (Manifest, error) {
	data, err := os.ReadFile(e.manifestPath(dir))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	return m, nil
}

// WriteManifest writes .<tool>/manifest.json.
func (e Engine) WriteManifest(dir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(e.manifestPath(dir)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(e.manifestPath(dir), data, 0o644)
}

// Scaffold writes the tree into dir, skipping existing files (unless Force),
// initializing git when greenfield, and writing the seeds and manifest. A local
// workspace (VCS none) gets no git init and no git files, and its project
// settings record "vcs": "none".
func (e Engine) Scaffold(dir string, opts Options) (Report, error) {
	rep := Report{}
	if err := e.checkVCS(); err != nil {
		return rep, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return rep, err
	}
	if !opts.NoGit && !e.local() {
		if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
			if err := runGit(dir, "init"); err != nil {
				return rep, err
			}
			rep.GitInit = true
		}
	}
	err := e.walk(func(rel string, data []byte) error {
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(dest); err == nil && !opts.Force {
			return nil // skip existing user-owned file
		}
		if err := writeFile(dest, data); err != nil {
			return err
		}
		rep.Written = append(rep.Written, rel)
		return nil
	})
	if err != nil {
		return rep, err
	}
	if err := e.keptDirs(dir); err != nil {
		return rep, err
	}
	seeded, err := e.writeSeeds(dir)
	if err != nil {
		return rep, err
	}
	rep.Written = append(rep.Written, seeded...)
	if e.local() {
		created, err := e.recordLocal(dir)
		if err != nil {
			return rep, err
		}
		if created != "" {
			rep.Written = append(rep.Written, created)
		}
	}
	m, err := e.BuildManifest()
	if err != nil {
		return rep, err
	}
	if err := e.WriteManifest(dir, m); err != nil {
		return rep, err
	}
	return rep, nil
}

// recordLocal sets "vcs": "none" in the project settings, creating the file when
// absent, so the workspace stays local after init. It returns the file's
// relative path when it created it.
func (e Engine) recordLocal(dir string) (string, error) {
	file := filepath.Join(workspace.Dir(dir, e.Tool), "settings.json")
	rel := filepath.ToSlash(filepath.Join(filepath.Base(workspace.Dir(dir, e.Tool)), "settings.json"))
	m := map[string]any{}
	created := true
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		created = false
		// Keep numbers as written, so re-encoding can't round them.
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.UseNumber()
		var parsed any
		if err := dec.Decode(&parsed); err != nil {
			return "", fmt.Errorf("%s init: cannot parse settings %s: %v", e.Tool, file, err)
		}
		if dec.More() {
			return "", fmt.Errorf("%s init: cannot parse settings %s: trailing data after the object", e.Tool, file)
		}
		obj, ok := parsed.(map[string]any)
		if !ok || obj == nil {
			return "", fmt.Errorf("%s init: settings must be a JSON object: %s", e.Tool, file)
		}
		if obj["vcs"] == settings.VCSNone {
			return "", nil
		}
		m = obj
	case !os.IsNotExist(err):
		return "", err
	}
	m["vcs"] = settings.VCSNone
	// Encode leaves "&", "<" and ">" as written and ends with a newline.
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return "", err
	}
	if err := writeFile(file, []byte(out.String())); err != nil {
		return "", err
	}
	if created {
		return rel, nil
	}
	return "", nil
}

func (e Engine) writeSeeds(dir string) ([]string, error) {
	if e.Seeds == nil {
		return nil, nil
	}
	seeds, err := e.Seeds()
	if err != nil {
		return nil, err
	}
	var written []string
	for _, s := range seeds {
		dest := filepath.Join(dir, filepath.FromSlash(s.Rel))
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := writeFile(dest, s.Data); err != nil {
			return nil, err
		}
		written = append(written, s.Rel)
	}
	return written, nil
}

// Update refreshes managed files to the installed version. A file is overwritten
// only when its on-disk content still matches the recorded manifest hash;
// user-edited files are skipped and reported. A missing manifest is rebuilt:
// nothing on disk is recorded, so no differing file gets overwritten. In a local
// workspace (VCS none) git files are neither written nor recorded nor reported;
// ones already on disk are left alone.
func (e Engine) Update(dir string) (UpdateReport, error) {
	rep := UpdateReport{}
	if err := e.checkVCS(); err != nil {
		return rep, err
	}
	cur, err := e.ReadManifest(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if info, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(e.Marker))); statErr != nil || !info.IsDir() {
			return rep, fmt.Errorf("%s update: %s is not a %s workspace (run `%s init`)", e.Tool, dir, e.Tool, e.Tool)
		}
		cur, err = Manifest{Files: map[string]string{}}, nil
	}
	if err != nil {
		return rep, fmt.Errorf("%s update: %v", e.Tool, err)
	}
	next, err := e.BuildManifest()
	if err != nil {
		return rep, err
	}
	if err := e.keptDirs(dir); err != nil {
		return rep, err
	}
	for rel, nextHash := range next.Files {
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		curData, err := os.ReadFile(dest)
		if err != nil {
			if err := e.writeManaged(dest, rel); err != nil {
				return rep, err
			}
			rep.Updated = append(rep.Updated, rel)
			continue
		}
		if hashBytes(curData) == nextHash {
			rep.Unchanged = append(rep.Unchanged, rel)
			continue
		}
		if prevHash, recorded := cur.Files[rel]; recorded && hashBytes(curData) == prevHash {
			if err := e.writeManaged(dest, rel); err != nil {
				return rep, err
			}
			rep.Updated = append(rep.Updated, rel)
			continue
		}
		// unrecorded (new file) or user-edited — skip to avoid clobbering
		rep.Skipped = append(rep.Skipped, rel)
	}
	if err := e.WriteManifest(dir, next); err != nil {
		return rep, err
	}
	return rep, nil
}

func (e Engine) writeManaged(dest, rel string) error {
	data, err := fs.ReadFile(e.FS, rel)
	if err != nil {
		return err
	}
	return writeFile(dest, data)
}

// Check validates the tree and returns a list of problems (empty when clean). It
// guards against template syntax leaking into the literal output.
func (e Engine) Check() []string {
	var problems []string
	if err := e.walk(func(rel string, data []byte) error {
		if strings.Contains(string(data), "{{") {
			problems = append(problems, rel+": contains template syntax")
		}
		return nil
	}); err != nil {
		problems = append(problems, err.Error())
	}
	return problems
}

func writeFile(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}
