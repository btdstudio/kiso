// Package repos is a registry of the repos a workspace works with. Logical names
// and remotes live in a committed REPOS.json. Where each repo sits on this machine
// lives in a local .<tool>/paths.json, so committed state never holds an absolute
// path and a workspace stays portable across machines and people.
package repos

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/btdstudio/kiso/workspace"
)

// DefaultBranch is used when a repo is added without one.
const DefaultBranch = "main"

// Repo is one registered repo.
type Repo struct {
	Remote        string `json:"remote"`
	DefaultBranch string `json:"defaultBranch"`
}

// Registry is a workspace's repos and their local paths.
type Registry struct {
	Repos map[string]Repo
	Paths map[string]string // name -> absolute or ~/ path
	root  string
	tool  string
}

// Problem is one thing Doctor found wrong with a repo.
type Problem struct {
	Repo   string
	Detail string
}

func registryPath(root string) string { return filepath.Join(root, "REPOS.json") }

func pathsPath(root, tool string) string {
	return filepath.Join(workspace.Dir(root, tool), "paths.json")
}

// Load reads the registry. Absent files mean an empty registry; an unparseable
// file is an error, never "no repos", because a save writes the map back.
func Load(root, tool string) (Registry, error) {
	r := Registry{Repos: map[string]Repo{}, Paths: map[string]string{}, root: root, tool: tool}
	var file struct {
		Repos map[string]Repo `json:"repos"`
	}
	if err := readJSON(tool, registryPath(root), &file); err != nil {
		return Registry{}, err
	}
	if file.Repos != nil {
		r.Repos = file.Repos
	}
	if err := readJSON(tool, pathsPath(root, tool), &r.Paths); err != nil {
		return Registry{}, err
	}
	if r.Paths == nil {
		r.Paths = map[string]string{}
	}
	return r, nil
}

func readJSON(tool, path string, into any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: cannot read %s: %v", tool, path, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s: cannot parse %s: %v", tool, path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Save writes REPOS.json and .<tool>/paths.json.
func (r Registry) Save() error {
	if err := writeJSON(registryPath(r.root), map[string]any{"repos": r.Repos}); err != nil {
		return err
	}
	return writeJSON(pathsPath(r.root, r.tool), r.Paths)
}

// Names lists the registered repos in order.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r.Repos))
	for name := range r.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// checkRemote refuses remotes that shouldn't be committed or handed to git. The
// registry comes from a shared repo, so a remote can reach `git clone` on another
// person's machine.
func checkRemote(tool, remote string) error {
	switch {
	case remote == "":
		return fmt.Errorf("%s: a remote is required", tool)
	case strings.HasPrefix(remote, "-"):
		return fmt.Errorf("%s: invalid remote %q", tool, remote)
	case regexp.MustCompile(`^[A-Za-z0-9+.-]+::`).MatchString(remote):
		return fmt.Errorf("%s: remote helpers aren't allowed: %q", tool, remote)
	}
	if u, err := url.Parse(remote); err == nil && u.User != nil && (u.Scheme == "http" || u.Scheme == "https") {
		return fmt.Errorf("%s: remote has credentials in it, and REPOS.json is committed — remove them", tool)
	}
	return nil
}

// Add registers a repo. An empty branch means DefaultBranch.
func (r *Registry) Add(name, remote, branch string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%s: invalid repo name %q (use lowercase letters, digits, '.', '_' and '-')", r.tool, name)
	}
	if _, exists := r.Repos[name]; exists {
		return fmt.Errorf("%s: repo %q is already registered", r.tool, name)
	}
	if err := checkRemote(r.tool, remote); err != nil {
		return err
	}
	if branch == "" {
		branch = DefaultBranch
	}
	r.Repos[name] = Repo{Remote: remote, DefaultBranch: branch}
	return nil
}

// SetPath records where a registered repo sits on this machine. A ~/ path is kept
// as written; anything else is made absolute.
func (r *Registry) SetPath(name, path string) error {
	if _, ok := r.Repos[name]; !ok {
		return r.unknown(name)
	}
	if path == "" {
		return fmt.Errorf("%s: a path is required", r.tool)
	}
	stored, err := storedPath(path)
	if err != nil {
		return err
	}
	r.Paths[name] = stored
	return nil
}

// storedPath is how a path is kept in paths.json: ~/ paths as written, anything
// else absolute.
func storedPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return path, nil
	}
	return filepath.Abs(path)
}

func (r Registry) unknown(name string) error {
	return fmt.Errorf("%s: unknown repo %q — run `%s repo add`", r.tool, name, r.tool)
}

func expand(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
	}
	return path, nil
}

// Resolve returns a repo's path on this machine. It doesn't check the path exists;
// Doctor and EnvFlags do.
func (r Registry) Resolve(name string) (string, error) {
	if _, ok := r.Repos[name]; !ok {
		return "", r.unknown(name)
	}
	p, ok := r.Paths[name]
	if !ok {
		return "", fmt.Errorf("%s: repo %q has no local path — run `%s repo path %s <path>`", r.tool, name, r.tool, name)
	}
	return expand(p)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Doctor reports repos with no local path, a path missing on disk, or a checkout
// whose origin doesn't match the registered remote.
func (r Registry) Doctor() []Problem {
	var problems []Problem
	for _, name := range r.Names() {
		path, err := r.Resolve(name)
		if err != nil {
			problems = append(problems, Problem{name, "no local path"})
			continue
		}
		if !isDir(path) {
			problems = append(problems, Problem{name, "missing on disk: " + path})
			continue
		}
		cmd := exec.Command("git", "remote", "get-url", "origin")
		cmd.Dir = path
		out, err := cmd.Output()
		if err != nil {
			problems = append(problems, Problem{name, "not a git checkout with an origin: " + path})
			continue
		}
		origin := strings.TrimSpace(string(out))
		if normalizeRemote(origin) != normalizeRemote(r.Repos[name].Remote) {
			problems = append(problems, Problem{name, fmt.Sprintf("remote mismatch: %s has origin %s, registered as %s", path, origin, r.Repos[name].Remote)})
		}
	}
	return problems
}

var scpRemoteRe = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):([^/].*)$`)

// normalizeRemote makes the SSH and HTTPS spellings of one repo compare equal.
func normalizeRemote(remote string) string {
	s := strings.TrimSpace(remote)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" {
		s = strings.ToLower(u.Hostname()) + u.Path
	} else if m := scpRemoteRe.FindStringSubmatch(s); m != nil {
		s = strings.ToLower(m[1]) + "/" + m[2]
	}
	s = strings.TrimRight(s, "/")
	return strings.TrimSuffix(s, ".git")
}

// Clone clones a repo from its registered remote and, once that succeeds, records
// its path. It refuses an existing destination.
func (r *Registry) Clone(name, dest string) error {
	repo, ok := r.Repos[name]
	if !ok {
		return r.unknown(name)
	}
	if err := checkRemote(r.tool, repo.Remote); err != nil {
		return err
	}
	stored, err := storedPath(dest)
	if err != nil {
		return err
	}
	path, err := expand(stored)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s: %s already exists", r.tool, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := exec.Command("git", "clone", "--quiet", "--", repo.Remote, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: cloning %s: %v: %s", r.tool, name, err, strings.TrimSpace(string(out)))
	}
	r.Paths[name] = stored
	return nil
}

// EnvFlags returns harness flags that give an agent access to the named repos, one
// `--add-dir <path>` per repo, shell-quoted. Every repo must exist on this machine:
// quietly leaving one out would leave an agent working without it.
func (r Registry) EnvFlags(names []string) (string, error) {
	var flags []string
	for _, name := range names {
		path, err := r.Resolve(name)
		if err != nil {
			return "", err
		}
		if !isDir(path) {
			return "", fmt.Errorf("%s: repo %q is missing on disk: %s — run `%s repo doctor`", r.tool, name, path, r.tool)
		}
		flags = append(flags, "--add-dir", shellQuote(path))
	}
	return strings.Join(flags, " "), nil
}

var shellSafeRe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafeRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
