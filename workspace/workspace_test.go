package workspace

import (
	"path/filepath"
	"testing"
)

func TestPaths(t *testing.T) {
	if got, want := Dir("root", "zumen"), filepath.Join("root", ".zumen"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if got, want := ActivePath("root", "tomo"), filepath.Join("root", ".tomo", "active"); got != want {
		t.Errorf("ActivePath = %q, want %q", got, want)
	}
}

func TestActiveRoundTrip(t *testing.T) {
	root := t.TempDir()
	got, err := ReadActive(root, "zumen")
	if err != nil || got != "" {
		t.Fatalf("ReadActive before write = %q, %v", got, err)
	}
	if err := WriteActive(root, "zumen", "1-sample"); err != nil {
		t.Fatal(err)
	}
	got, err = ReadActive(root, "zumen")
	if err != nil || got != "1-sample" {
		t.Fatalf("ReadActive after write = %q, %v", got, err)
	}
}
