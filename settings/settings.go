// Package settings merges a tool's layered settings: effective settings =
// defaults ← global (~/.config/<tool>/settings.json) ← project
// (.<tool>/settings.json). The project supersedes the global. Each tool defines
// its own settings shape and passes it in through a Spec.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/btdstudio/kiso/workspace"
)

// ValidGates lists the accepted values of the core "gates" setting.
var ValidGates = []string{"block", "prompt", "auto"}

// CheckGates returns an error unless value is one of ValidGates.
func CheckGates(tool, value string) error {
	for _, v := range ValidGates {
		if v == value {
			return nil
		}
	}
	return fmt.Errorf("%s: invalid gates %q (expected %s)", tool, value, strings.Join(ValidGates, "|"))
}

// The accepted values of the core "vcs" setting: whether the workspace lives in
// git. kiso resolves and validates it for every tool (see VCSOf).
const (
	VCSGit  = "git"
	VCSNone = "none"
)

// DefaultVCS is the "vcs" value when no layer sets one.
const DefaultVCS = VCSGit

// ValidVCS lists the accepted values of the core "vcs" setting.
var ValidVCS = []string{VCSGit, VCSNone}

// CheckVCS returns an error unless value is one of ValidVCS.
func CheckVCS(tool, value string) error {
	for _, v := range ValidVCS {
		if v == value {
			return nil
		}
	}
	return fmt.Errorf("%s: invalid vcs %q (expected %s)", tool, value, strings.Join(ValidVCS, "|"))
}

// VCSOf resolves the effective "vcs" value from raw layers, in the same order as
// Merge: the last layer that sets it wins, null values are ignored, and
// DefaultVCS applies when none sets it. The value is validated.
func VCSOf(tool string, layers ...map[string]any) (string, error) {
	vcs := DefaultVCS
	for _, layer := range layers {
		value, ok := layer["vcs"]
		if !ok || value == nil {
			continue
		}
		v, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%s: invalid vcs %v (expected %s)", tool, value, strings.Join(ValidVCS, "|"))
		}
		vcs = v
	}
	if err := CheckVCS(tool, vcs); err != nil {
		return "", err
	}
	return vcs, nil
}

// Spec describes one tool's settings.
type Spec[T any] struct {
	Tool     string
	Defaults func() T
	// Apply overlays one raw key from a layer onto the settings. Unknown keys
	// should be ignored. It is never called with a nil value.
	Apply func(s *T, key string, value any)
	// Validate checks and may normalize the merged settings. Optional.
	Validate func(s *T) error
}

// Loaded is the result of Load.
type Loaded[T any] struct {
	Global    map[string]any
	Project   map[string]any
	Effective T
	// VCS is the effective core "vcs" value: VCSGit or VCSNone.
	VCS string
}

// GlobalPath is the tool's global settings file, under $XDG_CONFIG_HOME when set
// and ~/.config otherwise.
func (s Spec[T]) GlobalPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, s.Tool, "settings.json")
}

// ProjectPath is the tool's settings file for the workspace at root.
func (s Spec[T]) ProjectPath(root string) string {
	return filepath.Join(workspace.Dir(root, s.Tool), "settings.json")
}

// ReadIfExists parses a JSON object file, returning an empty map when absent.
func (s Spec[T]) ReadIfExists(file string) (map[string]any, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%s: cannot parse settings %s: %v", s.Tool, file, err)
	}
	m, ok := parsed.(map[string]any)
	if !ok || m == nil {
		return nil, fmt.Errorf("%s: settings must be a JSON object: %s", s.Tool, file)
	}
	return m, nil
}

// Merge overlays the raw layers, in order, over the defaults. Layers are raw JSON
// objects so a present-but-empty value still overrides; null values are ignored.
// After the tool's Validate, the core "vcs" value is validated too.
func (s Spec[T]) Merge(layers ...map[string]any) (T, error) {
	out := s.Defaults()
	for _, layer := range layers {
		for key, value := range layer {
			if value != nil {
				s.Apply(&out, key, value)
			}
		}
	}
	if s.Validate != nil {
		if err := s.Validate(&out); err != nil {
			var zero T
			return zero, err
		}
	}
	if _, err := VCSOf(s.Tool, layers...); err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// Load reads the global and project layers for the workspace at root and merges
// them.
func (s Spec[T]) Load(root string) (Loaded[T], error) {
	global, err := s.ReadIfExists(s.GlobalPath())
	if err != nil {
		return Loaded[T]{}, err
	}
	project, err := s.ReadIfExists(s.ProjectPath(root))
	if err != nil {
		return Loaded[T]{}, err
	}
	effective, err := s.Merge(global, project)
	if err != nil {
		return Loaded[T]{}, err
	}
	vcs, err := VCSOf(s.Tool, global, project)
	if err != nil {
		return Loaded[T]{}, err
	}
	return Loaded[T]{Global: global, Project: project, Effective: effective, VCS: vcs}, nil
}
