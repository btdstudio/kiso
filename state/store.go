package state

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/btdstudio/kiso/changelog"
	"github.com/btdstudio/kiso/settings"
	"github.com/btdstudio/kiso/workspace"
)

// Schema is the STATE.json version a Store writes unless it sets its own.
const Schema = 1

// Store persists changes for one tool at the workspace root: a committed
// STATE.json and LOG.md, change folders under SpecsDir, and a local active-change
// pointer in .<tool>/active.
type Store struct {
	Machine  Machine
	SpecsDir string // change folders, relative to the workspace root
	// ChangeDirs are other folders holding a <dir>/<change>/ per change, relative
	// to the workspace root. Gates commit these along with the change's folder.
	ChangeDirs []string
	// Commit makes each transition commit STATE.json and LOG.md, so an approval
	// can't sit uncommitted.
	Commit bool
	// VCS is the workspace's core "vcs" setting. "" and settings.VCSGit keep the
	// behavior above; settings.VCSNone means the workspace is plain local files:
	// transitions never run git, never warn, and change folders get no .gitkeep.
	VCS string
	// Now is the clock for timestamps. Optional; defaults to time.Now.
	Now func() time.Time
	// Warn receives warnings. Optional; defaults to os.Stderr.
	Warn io.Writer
	// Schema is this tool's STATE.json version. A tool bumps it when its phases
	// change, and upgrades older files in Migrate. Optional; defaults to the
	// package's Schema.
	Schema int
	// Migrate upgrades STATE.json bytes older than the store's schema. Optional.
	// It should return bytes at the current schema.
	Migrate func(data []byte) ([]byte, error)
	// Preflight runs before STATE.json is read, to refuse layouts the tool no
	// longer supports. Optional.
	Preflight func(root string) error
}

type stateFile struct {
	Schema  int                `json:"schema"`
	Changes map[string]*Change `json:"changes"`
}

func (s Store) tool() string { return s.Machine.Tool }

func (s Store) schema() int {
	if s.Schema > 0 {
		return s.Schema
	}
	return Schema
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Store) warn() io.Writer {
	if s.Warn != nil {
		return s.Warn
	}
	return os.Stderr
}

// local reports whether the workspace is declared local (vcs: none).
func (s Store) local() bool { return s.VCS == settings.VCSNone }

// commits reports whether transitions commit: Commit is on and the workspace is
// not local.
func (s Store) commits() bool { return s.Commit && !s.local() }

func (s Store) checkVCS() error {
	if s.VCS == "" {
		return nil
	}
	return settings.CheckVCS(s.tool(), s.VCS)
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// StatePath is the committed state file.
func (s Store) StatePath(root string) string { return filepath.Join(root, "STATE.json") }

// LogPath is the committed log.
func (s Store) LogPath(root string) string { return filepath.Join(root, "LOG.md") }

// SpecsPath is the folder holding change folders.
func (s Store) SpecsPath(root string) string { return filepath.Join(root, s.SpecsDir) }

// ChangeDir is one change's folder. An empty change resolves to the specs folder
// itself.
func (s Store) ChangeDir(root, change string) string {
	if change == "" {
		return s.SpecsPath(root)
	}
	return filepath.Join(s.SpecsPath(root), change)
}

var changeIDRe = regexp.MustCompile(`^(\d+)-`)

// changeNameRe is the strict grammar for a change folder: a numeric id, a hyphen,
// then the kebab-case slug Slugify produces. It keeps a name from escaping the
// specs folder via `..` or a separator.
var changeNameRe = regexp.MustCompile(`^(\d+)-[a-z0-9]+(?:-[a-z0-9]+)*$`)

// NextID is the next sequential change id: the highest numeric `<id>-` prefix
// under the specs folder, plus one.
func (s Store) NextID(root string) int {
	entries, err := os.ReadDir(s.SpecsPath(root))
	if err != nil {
		return 1
	}
	max := 0
	for _, e := range entries {
		if m := changeIDRe.FindStringSubmatch(e.Name()); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify lowercases text to a short kebab-case slug, so change folders read as
// <id>-<slug>.
func Slugify(text string) string {
	slug := slugNonAlnum.ReplaceAllString(strings.ToLower(text), "-")
	slug = strings.Trim(slug, "-")
	if r := []rune(slug); len(r) > 40 {
		slug = string(r[:40])
	}
	return slug
}

// NewChange returns an empty change.
func NewChange(name string) Change {
	return Change{Name: name, Gates: map[string]string{}, Sources: []string{}}
}

// ReadChanges returns every recorded change (empty when STATE.json is absent). An
// unreadable or unparseable file is an error, never "no changes": a save writes
// this map back, so treating it as empty would wipe every record.
func (s Store) ReadChanges(root string) (map[string]*Change, error) {
	if s.Preflight != nil {
		if err := s.Preflight(root); err != nil {
			return nil, err
		}
	}
	p := s.StatePath(root)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Change{}, nil
		}
		return nil, fmt.Errorf("%s: cannot read %s: %v", s.tool(), p, err)
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("%s: cannot parse %s: %v", s.tool(), p, err)
	}
	if sf.Schema > s.schema() {
		return nil, fmt.Errorf("%s: %s has schema %d, newer than this version supports (%d) — upgrade %s", s.tool(), p, sf.Schema, s.schema(), s.tool())
	}
	if sf.Schema < s.schema() && s.Migrate != nil {
		upgraded, err := s.Migrate(data)
		if err != nil {
			return nil, fmt.Errorf("%s: cannot migrate %s: %v", s.tool(), p, err)
		}
		sf = stateFile{}
		if err := json.Unmarshal(upgraded, &sf); err != nil {
			return nil, fmt.Errorf("%s: cannot parse migrated %s: %v", s.tool(), p, err)
		}
	}
	if sf.Changes == nil {
		return map[string]*Change{}, nil
	}
	for name, c := range sf.Changes {
		if c == nil {
			delete(sf.Changes, name)
			continue
		}
		c.Name = name
		if c.Gates == nil {
			c.Gates = map[string]string{}
		}
		for phase, status := range c.Gates {
			if status == "" {
				delete(c.Gates, phase)
			}
		}
		if c.Sources == nil {
			c.Sources = []string{}
		}
	}
	return sf.Changes, nil
}

func (s Store) resolve(name string, changes map[string]*Change) Change {
	if c := changes[name]; c != nil {
		return *c
	}
	return NewChange(name)
}

// Load returns the active change. Its Name is "" when none is active.
func (s Store) Load(root string) (Change, error) {
	changes, err := s.ReadChanges(root)
	if err != nil {
		return Change{}, err
	}
	name, err := workspace.ReadActive(root, s.tool())
	if err != nil {
		return Change{}, err
	}
	return s.resolve(name, changes), nil
}

func (s Store) write(root string, c *Change) error {
	c.UpdatedAt = stamp(s.now())
	changes, err := s.ReadChanges(root)
	if err != nil {
		return err
	}
	if c.Gates == nil {
		c.Gates = map[string]string{}
	}
	if c.Sources == nil {
		c.Sources = []string{}
	}
	changes[c.Name] = c
	buf, err := json.MarshalIndent(stateFile{Schema: s.schema(), Changes: changes}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.StatePath(root), append(buf, '\n'), 0o644)
}

// Save stamps updatedAt, writes the change's record (preserving every other
// change's), and makes it the active change.
func (s Store) Save(root string, c *Change) error {
	if err := s.write(root, c); err != nil {
		return err
	}
	return workspace.WriteActive(root, s.tool(), c.Name)
}

// KnownChange is one change folder annotated with any recorded progress.
type KnownChange struct {
	Name   string            `json:"name"`
	ID     int               `json:"id"`
	Phase  string            `json:"phase"`
	Status string            `json:"status"`
	Gates  map[string]string `json:"gates"`
}

// Known lists the numbered folders under the specs folder, sorted by id.
func (s Store) Known(root string) ([]KnownChange, error) {
	changes, err := s.ReadChanges(root)
	if err != nil {
		return nil, err
	}
	out := []KnownChange{}
	entries, err := os.ReadDir(s.SpecsPath(root))
	if err != nil {
		return out, nil
	}
	for _, e := range entries {
		m := changeIDRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		id, _ := strconv.Atoi(m[1])
		kc := KnownChange{Name: e.Name(), ID: id, Gates: map[string]string{}}
		if c := changes[e.Name()]; c != nil {
			kc.Phase, kc.Status, kc.Gates = c.Phase, c.Status, c.Gates
		}
		out = append(out, kc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s Store) checkFolder(root, name string) error {
	unknown := fmt.Errorf(`%s: unknown change "%s"`, s.tool(), name)
	if !changeNameRe.MatchString(name) {
		return unknown
	}
	info, err := os.Lstat(s.ChangeDir(root, name))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return unknown
	}
	return nil
}

// Switch points the workspace at a change folder and resumes its machine. The
// folder must exist and match the change-name grammar. Switching to a recorded
// change moves only the local pointer, leaving STATE.json untouched.
func (s Store) Switch(root, name string) (Change, error) {
	if err := s.checkFolder(root, name); err != nil {
		return Change{}, err
	}
	changes, err := s.ReadChanges(root)
	if err != nil {
		return Change{}, err
	}
	c := s.resolve(name, changes)
	if changes[name] != nil {
		return c, workspace.WriteActive(root, s.tool(), name)
	}
	return c, s.Save(root, &c)
}

// SeedOptions configure Seed.
type SeedOptions struct {
	Sources []string
	// Repos are logical repo names this change works on.
	Repos []string
	// Skip maps skippable phases to the reason they don't apply to this change.
	Skip map[string]string
	// Paths are extra files to commit with the new change, relative to root, such
	// as an intake file its record points at.
	Paths []string
}

// Seed creates a change folder and records its initial machine (the first phase
// at draft), making it the active change.
func (s Store) Seed(root, name string, opts SeedOptions) (Change, error) {
	if !changeNameRe.MatchString(name) {
		return Change{}, fmt.Errorf("%s: invalid change name %q", s.tool(), name)
	}
	if err := s.checkVCS(); err != nil {
		return Change{}, err
	}
	if len(s.Machine.Phases) == 0 {
		return Change{}, fmt.Errorf("%s: machine has no phases", s.tool())
	}
	c := NewChange(name)
	if opts.Sources != nil {
		c.Sources = opts.Sources
	}
	c.Repos = opts.Repos
	first := s.Machine.Phases[0].Name
	lines := []string{name + ": created"}
	for _, p := range s.Machine.Phases {
		if reason, ok := opts.Skip[p.Name]; ok {
			if err := s.Machine.SetGate(&c, p.Name, Skipped, reason); err != nil {
				return Change{}, err
			}
			lines = append(lines, transitionLine(name, p.Name, Skipped, reason))
		}
	}
	if err := s.Machine.SetGate(&c, first, Draft, ""); err != nil {
		return Change{}, err
	}
	dir := s.ChangeDir(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Change{}, err
	}
	var paths []string
	if !s.local() {
		// Git drops empty folders, and a clone needs this one to switch to the change.
		keep := filepath.Join(dir, ".gitkeep")
		if err := os.WriteFile(keep, nil, 0o644); err != nil {
			return Change{}, err
		}
		paths = append(paths, filepath.ToSlash(filepath.Join(s.SpecsDir, name, ".gitkeep")))
	}
	if err := s.Save(root, &c); err != nil {
		return Change{}, err
	}
	paths = append(paths, opts.Paths...)
	return c, s.Record(root, lines, paths...)
}

func transitionLine(name, phase, status, reason string) string {
	line := name + ": " + phase + " → " + status
	if reason != "" {
		line += " (" + reason + ")"
	}
	return line
}

func (s Store) active(root string) (Change, error) {
	c, err := s.Load(root)
	if err != nil {
		return Change{}, err
	}
	if c.Name == "" {
		return Change{}, fmt.Errorf("%s: no active change — run `%s switch <change>` first", s.tool(), s.tool())
	}
	// The pointer is local, so a branch switch can leave it naming a change that
	// doesn't exist here.
	if _, err := os.Stat(s.ChangeDir(root, c.Name)); err != nil {
		return Change{}, fmt.Errorf("%s: active change %q has no folder under %s/ — run `%s switch <change>`", s.tool(), c.Name, s.SpecsDir, s.tool())
	}
	return c, nil
}

// Advance sets a gate on the active change, logs it along with any notes, and
// commits, including the change's folder, so an approval can't be recorded while
// the document it approved stays uncommitted. Enforcing the loop order is the caller's job, through CheckGate, since
// whether a violation blocks is its policy.
func (s Store) Advance(root, phase, status, reason string, notes ...string) (Change, error) {
	c, err := s.active(root)
	if err != nil {
		return Change{}, err
	}
	if err := s.Machine.SetGate(&c, phase, status, reason); err != nil {
		return Change{}, err
	}
	if err := s.Save(root, &c); err != nil {
		return Change{}, err
	}
	lines := []string{transitionLine(c.Name, phase, status, reason)}
	for _, note := range notes {
		lines = append(lines, c.Name+": "+note)
	}
	paths := []string{filepath.ToSlash(filepath.Join(s.SpecsDir, c.Name))}
	for _, dir := range s.ChangeDirs {
		if _, err := os.Stat(filepath.Join(root, dir, c.Name)); err == nil {
			paths = append(paths, filepath.ToSlash(filepath.Join(dir, c.Name)))
		}
	}
	return c, s.Record(root, lines, paths...)
}

// Reopen moves the active change back to a phase, logs it and commits.
func (s Store) Reopen(root, phase, reason string) (Change, error) {
	c, err := s.active(root)
	if err != nil {
		return Change{}, err
	}
	if err := s.Machine.Reopen(&c, phase, reason); err != nil {
		return Change{}, err
	}
	if err := s.Save(root, &c); err != nil {
		return Change{}, err
	}
	return c, s.Record(root, []string{c.Name + ": reopened " + phase + " (" + reason + ")"})
}

// CloseChange closes a change by name, logs it and commits. Closing the active
// change clears the local pointer.
func (s Store) CloseChange(root, name, reason string) (Change, error) {
	if err := s.checkFolder(root, name); err != nil {
		return Change{}, err
	}
	changes, err := s.ReadChanges(root)
	if err != nil {
		return Change{}, err
	}
	c := s.resolve(name, changes)
	if err := s.Machine.Close(&c, reason); err != nil {
		return Change{}, err
	}
	if err := s.write(root, &c); err != nil {
		return Change{}, err
	}
	active, err := workspace.ReadActive(root, s.tool())
	if err != nil {
		return Change{}, err
	}
	if active == name {
		if err := os.Remove(workspace.ActivePath(root, s.tool())); err != nil {
			return Change{}, err
		}
	}
	return c, s.Record(root, []string{name + ": closed (" + reason + ")"})
}

// Log appends one timestamped line to LOG.md without committing.
func (s Store) Log(root, line string) error {
	return changelog.Append(s.LogPath(root), s.now(), line)
}

// Record logs lines and, when Commit is on and VCS is not none, commits them with
// STATE.json and any extra paths, relative to root. The first line becomes the
// commit subject.
func (s Store) Record(root string, lines []string, paths ...string) error {
	if err := s.checkVCS(); err != nil {
		return err
	}
	for _, line := range lines {
		if err := s.Log(root, line); err != nil {
			return err
		}
	}
	if !s.commits() {
		return nil
	}
	return s.commit(root, s.tool()+": "+lines[0], append([]string{"STATE.json", "LOG.md"}, paths...))
}

// commit commits exactly paths. Anything else already staged stays staged and out
// of this commit. Outside a git repo the transition stands and this only warns;
// a failed commit is an error, because the state on disk is now ahead of git.
func (s Store) commit(root, message string, paths []string) error {
	if _, err := s.git(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		fmt.Fprintf(s.warn(), "warning: %s is not in a git repo — this state change was not committed; set \"vcs\": \"none\" if this workspace is meant to be local\n", root)
		return nil
	}
	failed := func(err error) error {
		return fmt.Errorf("%s: state saved but not committed: %v", s.tool(), err)
	}
	args := append([]string{"add", "--"}, paths...)
	if _, err := s.git(root, args...); err != nil {
		return failed(err)
	}
	// `git commit --only` refuses a path git knows nothing about, such as an empty
	// folder, so keep only the paths that have something to commit.
	var changed []string
	for _, p := range paths {
		if _, err := s.git(root, "diff", "--cached", "--quiet", "--", p); err != nil {
			changed = append(changed, p)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	args = append([]string{"commit", "--quiet", "--only", "-m", message, "--"}, changed...)
	if _, err := s.git(root, args...); err != nil {
		return failed(err)
	}
	return nil
}

func (s Store) git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
