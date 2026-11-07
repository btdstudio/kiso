// Package workspace owns a tool's local .<tool>/ directory and its active-change
// pointer.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirName is the name of the tool's workspace directory (".<tool>").
func DirName(tool string) string { return "." + tool }

// Dir is the path to the tool's workspace directory under root.
func Dir(root, tool string) string { return filepath.Join(root, DirName(tool)) }

// ActivePath is the path to the file naming the active change.
func ActivePath(root, tool string) string { return filepath.Join(Dir(root, tool), "active") }

// ReadActive returns the active change's name ("" when none is set).
func ReadActive(root, tool string) (string, error) {
	p := ActivePath(root, tool)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("%s: cannot read %s: %v", tool, p, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteActive records name as the active change.
func WriteActive(root, tool, name string) error {
	if err := os.MkdirAll(Dir(root, tool), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ActivePath(root, tool), []byte(name+"\n"), 0o644)
}
