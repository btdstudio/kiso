package version

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.2.0", "v0.2.0"},
		{"(devel)", "dev"},
		{"", "dev"},
	}
	for _, c := range cases {
		if got := fromBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: c.in}}); got != c.want {
			t.Errorf("fromBuildInfo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := fromBuildInfo(nil); got != "dev" {
		t.Errorf("fromBuildInfo(nil) = %q, want dev", got)
	}
}

func TestStringNonEmpty(t *testing.T) {
	if s := String(); s == "" {
		t.Fatal("String() returned empty")
	}
}
