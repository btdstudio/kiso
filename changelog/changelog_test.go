package changelog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendCreatesAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "LOG.md")
	zone := time.FixedZone("UTC+2", 2*60*60)
	if err := Append(path, time.Date(2026, 11, 7, 2, 0, 0, 5_000_000, zone), "first"); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC), "second"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "- 2026-11-07T00:00:00.005Z first\n- 2026-11-07T01:00:00.000Z second\n"
	if string(got) != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}
