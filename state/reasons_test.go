package state

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSetGateStoresSkipReason(t *testing.T) {
	c := NewChange("1-x")
	if err := machine.SetGate(&c, "explore", Skipped, "no research"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Reasons, map[string]string{"explore": "no research"}) {
		t.Fatalf("after skip, Reasons = %#v", c.Reasons)
	}

	// Skipping again replaces the reason, verbatim.
	odd := "  no repo: see #12 — \"later\"\nsecond line "
	if err := machine.SetGate(&c, "explore", Skipped, odd); err != nil {
		t.Fatal(err)
	}
	if got := c.Reasons["explore"]; got != odd {
		t.Fatalf("Reasons[explore] = %q, want %q", got, odd)
	}

	// Any other status removes it, and an empty result is nil.
	if err := machine.SetGate(&c, "explore", Draft, ""); err != nil {
		t.Fatal(err)
	}
	if c.Reasons != nil {
		t.Fatalf("after draft, Reasons = %#v, want nil", c.Reasons)
	}
	if err := machine.SetGate(&c, "explore", Skipped, "r"); err != nil {
		t.Fatal(err)
	}
	if err := machine.SetGate(&c, "explore", "", ""); err != nil {
		t.Fatal(err)
	}
	if c.Reasons != nil {
		t.Fatalf("after clearing, Reasons = %#v, want nil", c.Reasons)
	}

	// A reason given with another status isn't stored.
	if err := machine.SetGate(&c, "intake", Approved, "looks good"); err != nil {
		t.Fatal(err)
	}
	if c.Reasons != nil {
		t.Fatalf("after approve with a reason, Reasons = %#v, want nil", c.Reasons)
	}

	// Removing one reason leaves the others.
	two := Machine{Tool: "sample", Phases: []Phase{
		{Name: "a", Kind: Gated, Skippable: true},
		{Name: "b", Kind: Gated, Skippable: true},
	}}
	c = NewChange("1-x")
	for _, p := range []string{"a", "b"} {
		if err := two.SetGate(&c, p, Skipped, "r"+p); err != nil {
			t.Fatal(err)
		}
	}
	if err := two.SetGate(&c, "a", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Reasons, map[string]string{"b": "rb"}) {
		t.Fatalf("after approving a, Reasons = %#v", c.Reasons)
	}
}

func TestReopenKeepsReasonsOfPhasesStillSkipped(t *testing.T) {
	m := Machine{Tool: "sample", Phases: []Phase{
		{Name: "a", Kind: Gated},
		{Name: "b", Kind: Gated, Skippable: true},
		{Name: "c", Kind: Gated},
		{Name: "d", Kind: Gated, Skippable: true},
		{Name: "e", Kind: Run},
	}}
	start := NewChange("1-x")
	for _, step := range [][3]string{
		{"a", Approved, ""}, {"b", Skipped, "rb"}, {"c", Approved, ""}, {"d", Skipped, "rd"}, {"e", InProgress, ""},
	} {
		if err := m.SetGate(&start, step[0], step[1], step[2]); err != nil {
			t.Fatal(err)
		}
	}

	// Reopening a skipped phase drops its reason; a later skip keeps its own.
	c := start
	if err := m.Reopen(&c, "b", "there is research after all"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Reasons, map[string]string{"d": "rd"}) {
		t.Fatalf("reopen at b: Reasons = %#v", c.Reasons)
	}

	// Reopening an earlier phase keeps every skip and its reason.
	c = start
	if err := m.Reopen(&c, "a", "intake changed"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Reasons, map[string]string{"b": "rb", "d": "rd"}) {
		t.Fatalf("reopen at a: Reasons = %#v", c.Reasons)
	}

	// Reopening the only skipped phase leaves no reasons at all.
	c = start
	if err := m.Reopen(&c, "d", "r"); err != nil {
		t.Fatal(err)
	}
	if err := m.Reopen(&c, "b", "r"); err != nil {
		t.Fatal(err)
	}
	if c.Reasons != nil {
		t.Fatalf("after reopening both skips, Reasons = %#v, want nil", c.Reasons)
	}
}

func TestReasonsCopyIsIndependent(t *testing.T) {
	orig := NewChange("1-x")
	if err := machine.SetGate(&orig, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if err := machine.SetGate(&orig, "explore", Skipped, "r"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"explore": "r"}

	probe := orig
	if err := machine.SetGate(&probe, "explore", Skipped, "changed"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.Reasons, want) {
		t.Fatalf("SetGate on a copy changed the original: %#v", orig.Reasons)
	}
	probe = orig
	if err := machine.SetGate(&probe, "explore", Draft, ""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.Reasons, want) {
		t.Fatalf("clearing on a copy changed the original: %#v", orig.Reasons)
	}
	probe = orig
	if err := machine.Reopen(&probe, "explore", "r"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.Reasons, want) {
		t.Fatalf("Reopen on a copy changed the original: %#v", orig.Reasons)
	}
}

func TestRefusedTransitionsKeepReasons(t *testing.T) {
	c := NewChange("1-x")
	if err := machine.SetGate(&c, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if err := machine.SetGate(&c, "explore", Skipped, "r"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"explore": "r"}
	check := func(what string) {
		t.Helper()
		if !reflect.DeepEqual(c.Reasons, want) {
			t.Fatalf("%s: Reasons = %#v", what, c.Reasons)
		}
	}

	for _, r := range []struct{ phase, status, reason string }{
		{"bogus", Skipped, "r"},
		{"explore", "bogus", "r"},
		{"explore", Draft + "x", ""},
		{"explore", Skipped, ""},
		{"intake", Skipped, "r"},
	} {
		if err := machine.SetGate(&c, r.phase, r.status, r.reason); err == nil {
			t.Fatalf("SetGate(%q, %q, %q) succeeded", r.phase, r.status, r.reason)
		}
		check("refused SetGate " + r.phase + " " + r.status)
	}
	for _, r := range []struct{ phase, reason string }{
		{"bogus", "r"},
		{"explore", ""},
		{"review", "r"},
	} {
		if err := machine.Reopen(&c, r.phase, r.reason); err == nil {
			t.Fatalf("Reopen(%q, %q) succeeded", r.phase, r.reason)
		}
		check("refused Reopen " + r.phase)
	}

	if err := machine.Close(&c, "done"); err != nil {
		t.Fatal(err)
	}
	check("Close")
	if err := machine.SetGate(&c, "explore", Draft, ""); err == nil {
		t.Fatal("SetGate on a closed change succeeded")
	}
	check("SetGate on a closed change")
	if err := machine.Reopen(&c, "explore", "r"); err == nil {
		t.Fatal("Reopen on a closed change succeeded")
	}
	check("Reopen on a closed change")
}

// rawChange reads one change's record from STATE.json as plain JSON.
func rawChange(t *testing.T, s Store, root, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(s.StatePath(root))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Changes map[string]map[string]any `json:"changes"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file.Changes[name]
}

func TestReasonsInStateJSON(t *testing.T) {
	root, s := t.TempDir(), store()
	if _, err := s.Seed(root, "1-x", SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawChange(t, s, root, "1-x")["reasons"]; ok {
		t.Fatal("a seeded change has a reasons key")
	}
	if _, err := s.Advance(root, "intake", Approved, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawChange(t, s, root, "1-x")["reasons"]; ok {
		t.Fatal("a change with no skips has a reasons key")
	}
	if _, err := s.Advance(root, "explore", Skipped, "no repo yet"); err != nil {
		t.Fatal(err)
	}
	got := rawChange(t, s, root, "1-x")["reasons"]
	if !reflect.DeepEqual(got, map[string]any{"explore": "no repo yet"}) {
		t.Fatalf("reasons in STATE.json = %#v", got)
	}
	loaded, err := s.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Reasons, map[string]string{"explore": "no repo yet"}) {
		t.Fatalf("loaded Reasons = %#v", loaded.Reasons)
	}
	if _, err := s.Advance(root, "explore", Draft, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawChange(t, s, root, "1-x")["reasons"]; ok {
		t.Fatal("reasons key kept after the skip was undone")
	}

	// A file from v0.9.0, with a skip but no reasons key, loads with no reasons
	// and gains no key on the next save.
	old := `{
  "schema": 1,
  "changes": {
    "1-x": {
      "phase": "explore",
      "status": "skipped",
      "gates": {
        "explore": "skipped",
        "intake": "approved"
      },
      "sources": [],
      "updatedAt": "2026-11-01T00:00:00.000Z"
    }
  }
}
`
	if err := os.WriteFile(s.StatePath(root), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Reasons != nil || loaded.Gates["explore"] != Skipped {
		t.Fatalf("v0.9.0 file loaded as %+v", loaded)
	}
	if _, err := s.Advance(root, "build", InProgress, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawChange(t, s, root, "1-x")["reasons"]; ok {
		t.Fatal("saving a v0.9.0 change added a reasons key")
	}
}
