// Package changelog appends timestamped lines to a log file. Each entry is one
// self-contained line, so logs merge cleanly with git's merge=union driver.
package changelog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Append adds one "- <UTC timestamp> <line>" entry to the file at path, creating
// the file and its parent directories when absent.
func Append(path string, at time.Time, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "- %s %s\n", at.UTC().Format("2006-01-02T15:04:05.000Z"), line)
	return err
}
