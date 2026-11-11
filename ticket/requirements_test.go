package ticket

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestRequirementsRoundTrip(t *testing.T) {
	with := strings.Replace(example, "adrs: [ADR-0004]\n", "adrs: [ADR-0004]\nrequirements: [R3, N1]\n", 1)
	if with == example {
		t.Fatal("the fixture has no adrs line")
	}
	tk, err := Parse([]byte(with))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tk.Requirements, []string{"R3", "N1"}) {
		t.Fatalf("Requirements = %#v", tk.Requirements)
	}
	if got := string(tk.Bytes()); got != with {
		t.Fatalf("round trip changed the file:\n%s", got)
	}

	for name, c := range map[string]struct {
		line string
		want []string
	}{
		"duplicates and any id": {"requirements: [R2, R2, whatever]\n", []string{"R2", "R2", "whatever"}},
		"scalar":                {"requirements: R1\n", nil},
		"absent":                {"", nil},
	} {
		text := strings.Replace(example, "adrs: [ADR-0004]\n", "adrs: [ADR-0004]\n"+c.line, 1)
		tk, err := Parse([]byte(text))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(tk.Requirements, c.want) {
			t.Errorf("%s: Requirements = %#v, want %#v", name, tk.Requirements, c.want)
		}
		if got := string(tk.Bytes()); got != text {
			t.Errorf("%s: round trip changed the file:\n%s", name, got)
		}
	}
}

func TestNewWritesRequirementsAfterADRs(t *testing.T) {
	base := Ticket{ID: "S-0001", ADRs: []string{"ADR-0001"}, Labels: []string{"ci"}}
	with := base
	with.Requirements = []string{"R3", "N1"}
	tk := mk(t, with)
	text := string(tk.Bytes())
	adrs := strings.Index(text, "\nadrs: [ADR-0001]\n")
	reqs := strings.Index(text, "\nrequirements: [R3, N1]\n")
	labels := strings.Index(text, "\nlabels: [ci]\n")
	if adrs < 0 || reqs < 0 || labels < 0 || !(adrs < reqs && reqs < labels) {
		t.Fatalf("want adrs, requirements, labels in that order:\n%s", text)
	}
	again, err := Parse(tk.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Requirements, []string{"R3", "N1"}) {
		t.Fatalf("parsed back: %#v", again.Requirements)
	}

	// Nil or empty: no key, and the same bytes as before the field existed.
	want := mk(t, base).Bytes()
	for name, reqs := range map[string][]string{"nil": nil, "empty": {}} {
		f := base
		f.Requirements = reqs
		got := mk(t, f).Bytes()
		if bytes.Contains(got, []byte("requirements")) {
			t.Errorf("%s: wrote a requirements key:\n%s", name, got)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: bytes differ from a ticket without the field:\n%s", name, got)
		}
	}
}

func TestRequirementsFreeze(t *testing.T) {
	const fmDrift = "drifted: frontmatter changed since it was published"
	checkDrift(t, frozenText(t, freezeFixture), []driftCase{
		{"empty list added", replace("x: keep\n", "x: keep\nrequirements: []\n"), false, false, ""},
		{"requirement added", replace("x: keep\n", "x: keep\nrequirements: [R1]\n"), false, true, fmDrift},
	})

	cited := strings.Replace(freezeFixture, "labels: [x, y]\n", "labels: [x, y]\nrequirements: [R1, R2]\n", 1)
	checkDrift(t, frozenText(t, cited), []driftCase{
		{"reordered", replace("[R1, R2]", "[R2, R1]"), false, false, ""},
		{"changed", replace("[R1, R2]", "[R1, R3]"), false, true, fmDrift},
		{"one removed", replace("[R1, R2]", "[R1]"), false, true, fmDrift},
		{"removed", replace("requirements: [R1, R2]\n", ""), false, true, fmDrift},
		{"emptied", replace("[R1, R2]", "[]"), false, true, fmDrift},
	})
}

func TestRequirementsAreNotValidated(t *testing.T) {
	for _, status := range []string{StatusDraft, StatusReady} {
		plain := mk(t, Ticket{ID: "S-0001", Status: status, Body: readyBody})
		cited := mk(t, Ticket{ID: "S-0001", Status: status, Body: readyBody, Requirements: []string{"anything", "R1", "R1"}})
		if got, want := messages(ValidateFile(cited)), messages(ValidateFile(plain)); got != want {
			t.Errorf("%s: problems with requirements = %q, without = %q", status, got, want)
		}
		if got := messages(ValidateFile(cited)); got != "" {
			t.Errorf("%s: problems = %q, want none", status, got)
		}
	}
}

func TestNewRejectsUnwritableRequirement(t *testing.T) {
	_, err := New(Ticket{ID: "S-0001", Type: Story, Title: "A ticket", Repo: "app", Requirements: []string{"R1\nstatus: published"}})
	if err == nil {
		t.Fatal("a newline in a requirement must not inject a field")
	}
}
