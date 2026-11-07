package ticket

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The example from the design doc, comments included.
const example = `---
schema: 1
id: S-0014                 # type prefix + workspace-global sequence: E, F, S, T
type: story                # epic | feature | story | task
title: Add release workflow to shared actions repo
parent: F-0003             # optional
repo: platform-actions     # logical name from REPOS.json; required for story/task
depends_on: [S-0011, S-0012]
supersedes: S-0009         # optional; a published ticket this one replaces
change: 3-release-automation
spec: specs/3-release-automation/functional-spec.md
adrs: [ADR-0004]
labels: [ci, release]
estimate: M                # S | M | L, optional
status: draft              # draft | ready | published
origin:                    # set by tomo intake only
  workspace: git@github.com:org/platform-planning.git
  path: specs/3-release-automation/tickets/S-0014.md
---

## Context
Why this exists; links to spec sections and ADRs.

## Acceptance criteria
- [ ] AC1: Given a repo When a tag lands Then a release is cut

## Notes
Constraints, risks, out of scope.
`

const readyBody = "\n## Acceptance criteria\n- [ ] AC1: Given x When y Then z\n"

func mk(t *testing.T, f Ticket) *Ticket {
	t.Helper()
	if f.Title == "" {
		f.Title = "A ticket"
	}
	if f.Type == "" {
		f.Type = Story
	}
	if f.Repo == "" && (f.Type == Story || f.Type == Task) {
		f.Repo = "app"
	}
	tk, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func messages(problems []Problem) string {
	var lines []string
	for _, p := range problems {
		lines = append(lines, p.String())
	}
	return strings.Join(lines, "\n")
}

func TestParsesTheDesignExample(t *testing.T) {
	tk, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if tk.Schema != 1 || tk.ID != "S-0014" || tk.Type != Story || tk.Repo != "platform-actions" || tk.Estimate != "M" || tk.Status != StatusDraft {
		t.Fatalf("ticket = %+v", tk)
	}
	if !reflect.DeepEqual(tk.DependsOn, []string{"S-0011", "S-0012"}) || tk.Supersedes != "S-0009" || !reflect.DeepEqual(tk.ADRs, []string{"ADR-0004"}) {
		t.Fatalf("ticket = %+v", tk)
	}
	if tk.Origin.Workspace != "git@github.com:org/platform-planning.git" {
		t.Fatalf("origin = %+v", tk.Origin)
	}
	if problems := ValidateFile(tk); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
	if got := string(tk.Bytes()); got != example {
		t.Fatalf("not byte stable:\n%s", got)
	}
}

func TestParseRejectsMalformedFiles(t *testing.T) {
	for _, text := range []string{"no frontmatter", "---\nschema: x\n---\n", "---\nid: &x\n---\n"} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("Parse(%q) should fail", text)
		}
	}
}

func TestNewRoundTrips(t *testing.T) {
	tk := mk(t, Ticket{ID: "S-0001", Title: "Fix: a thing", Parent: "F-0002", DependsOn: []string{"S-0003"}, Labels: []string{"ci"}, Estimate: "S", Body: readyBody})
	again, err := Parse(tk.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if again.Title != "Fix: a thing" || again.Status != StatusDraft || again.Schema != Schema || again.Parent != "F-0002" || again.Body != readyBody {
		t.Fatalf("again = %+v", again)
	}
	if _, err := New(Ticket{ID: "S-0001", Title: "a\nstatus: published"}); err == nil {
		t.Fatal("a newline in a title must not inject a field")
	}
}

func TestValidateFile(t *testing.T) {
	cases := []struct {
		name string
		f    Ticket
		want string // "" = valid
	}{
		{"valid story", Ticket{ID: "S-0001"}, ""},
		{"valid epic without repo", Ticket{ID: "E-0001", Type: Epic}, ""},
		{"bad id", Ticket{ID: "S-1"}, "must look like"},
		{"wrong prefix", Ticket{ID: "F-0001", Type: Story}, "doesn't match type"},
		{"unknown type", Ticket{ID: "S-0001", Type: "bug"}, "unknown type"},
		{"unknown status", Ticket{ID: "S-0001", Status: "doing"}, "unknown status"},
		{"bad estimate", Ticket{ID: "S-0001", Estimate: "XL"}, "estimate"},
		{"ready without criteria", Ticket{ID: "S-0001", Status: StatusReady, Body: "\n## Context\n- [ ] not here\n"}, "acceptance criterion"},
		{"ready with criteria", Ticket{ID: "S-0001", Status: StatusReady, Body: readyBody}, ""},
		{"published without external", Ticket{ID: "S-0001", Status: StatusPublished}, "external id"},
		{"planned closed", Ticket{ID: "S-0001", Status: StatusClosed}, "native"},
		{"native task", Ticket{ID: "N-0001", Type: Task}, ""},
		{"native closed", Ticket{ID: "N-0001", Type: Task, Status: StatusClosed}, ""},
		{"native story", Ticket{ID: "N-0001", Type: Story}, "must be a task"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := ValidateFile(mk(t, c.f))
			if c.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems:\n%s", messages(problems))
				}
				return
			}
			if !strings.Contains(messages(problems), c.want) {
				t.Fatalf("want %q in:\n%s", c.want, messages(problems))
			}
		})
	}
}

func TestValidateFileRequiredFields(t *testing.T) {
	tk, err := Parse([]byte("---\nid: S-0001\ntype: story\nstatus: draft\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := messages(ValidateFile(tk))
	for _, want := range []string{"schema must be 1", "title is required", "repo is required"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
	native, err := Parse([]byte("---\nschema: 1\nid: N-0001\ntype: task\ntitle: x\nrepo: a\nstatus: draft\norigin:\n  path: x\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(messages(ValidateFile(native)), "can't have an origin") {
		t.Errorf("got:\n%s", messages(ValidateFile(native)))
	}
	hashOnly, err := Parse([]byte("---\nschema: 1\nid: S-0001\ntype: story\ntitle: x\nrepo: a\nstatus: draft\npublished_hash: abc\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(messages(ValidateFile(hashOnly)), "published_hash requires") {
		t.Errorf("got:\n%s", messages(ValidateFile(hashOnly)))
	}
}

// A snapshot in a tomo workspace passes on its own, though its neighbors are gone.
func TestSnapshotPassesValidateFileWithoutNeighbors(t *testing.T) {
	tk := mk(t, Ticket{ID: "S-0014", Parent: "F-0003", DependsOn: []string{"S-0011"}, Supersedes: "S-0009", ADRs: []string{"ADR-1"}, Spec: "specs/x.md", Status: StatusReady, Body: readyBody})
	if err := tk.SetOrigin(Origin{Workspace: "git@github.com:o/p.git", Path: "specs/x/S-0014.md"}); err != nil {
		t.Fatal(err)
	}
	again, err := Parse(tk.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if problems := ValidateFile(again); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
	if again.Origin.Path != "specs/x/S-0014.md" {
		t.Fatalf("origin = %+v", again.Origin)
	}
	if problems := ValidateWorkspace([]*Ticket{again}, []string{"app"}); len(problems) == 0 {
		t.Fatal("the same ticket should fail whole-workspace checks")
	}
}

func TestValidateWorkspace(t *testing.T) {
	good := []*Ticket{
		mk(t, Ticket{ID: "E-0001", Type: Epic}),
		mk(t, Ticket{ID: "F-0002", Type: Feature, Parent: "E-0001"}),
		mk(t, Ticket{ID: "S-0003", Parent: "F-0002"}),
		mk(t, Ticket{ID: "S-0004", Parent: "F-0002", DependsOn: []string{"S-0003"}}),
		mk(t, Ticket{ID: "T-0005", Type: Task, Parent: "S-0004"}),
		mk(t, Ticket{ID: "S-0006", Supersedes: "S-0003"}),
	}
	if problems := ValidateWorkspace(good, []string{"app"}); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
	for name, c := range map[string]struct {
		tickets []*Ticket
		want    string
	}{
		"duplicate":      {[]*Ticket{mk(t, Ticket{ID: "S-0001"}), mk(t, Ticket{ID: "S-0001"})}, "duplicate id"},
		"missing parent": {[]*Ticket{mk(t, Ticket{ID: "S-0001", Parent: "F-0009"})}, "parent F-0009 doesn't exist"},
		"same level":     {[]*Ticket{mk(t, Ticket{ID: "S-0001"}), mk(t, Ticket{ID: "S-0002", Parent: "S-0001"})}, "isn't higher"},
		"inverted":       {[]*Ticket{mk(t, Ticket{ID: "E-0001", Type: Epic, Parent: "S-0002"}), mk(t, Ticket{ID: "S-0002"})}, "isn't higher"},
		"missing dep":    {[]*Ticket{mk(t, Ticket{ID: "S-0001", DependsOn: []string{"S-0009"}})}, "depends_on S-0009"},
		"missing super":  {[]*Ticket{mk(t, Ticket{ID: "S-0001", Supersedes: "S-0009"})}, "supersedes S-0009"},
		"unregistered":   {[]*Ticket{mk(t, Ticket{ID: "S-0001", Repo: "other"})}, `repo "other"`},
		"self cycle":     {[]*Ticket{mk(t, Ticket{ID: "S-0001", DependsOn: []string{"S-0001"}})}, "cycle among S-0001"},
		"cycle": {[]*Ticket{
			mk(t, Ticket{ID: "S-0001", DependsOn: []string{"S-0003"}}),
			mk(t, Ticket{ID: "S-0002", DependsOn: []string{"S-0001"}}),
			mk(t, Ticket{ID: "S-0003", DependsOn: []string{"S-0002"}}),
		}, "cycle among S-0001, S-0002, S-0003"},
		"parent cycle": {[]*Ticket{
			mk(t, Ticket{ID: "F-0001", Type: Feature, DependsOn: []string{"S-0002"}}),
			mk(t, Ticket{ID: "S-0002", Parent: "F-0001"}),
		}, "cycle among"},
	} {
		got := messages(ValidateWorkspace(c.tickets, []string{"app"}))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q in:\n%s", name, c.want, got)
		}
	}
}

func TestOrderPutsReferencesFirst(t *testing.T) {
	tickets := []*Ticket{
		mk(t, Ticket{ID: "S-0005", Parent: "F-0002", DependsOn: []string{"S-0006"}}),
		mk(t, Ticket{ID: "S-0006", Parent: "F-0002"}),
		mk(t, Ticket{ID: "F-0002", Type: Feature, Parent: "E-0001"}),
		mk(t, Ticket{ID: "E-0001", Type: Epic}),
		mk(t, Ticket{ID: "S-0007", Supersedes: "S-0005", DependsOn: []string{"S-0099"}}),
	}
	ordered, err := Order(tickets)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tk := range ordered {
		ids = append(ids, tk.ID)
	}
	want := []string{"E-0001", "F-0002", "S-0006", "S-0005", "S-0007"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
}

func TestHashIgnoresLineEndingsAndTrailingNewlines(t *testing.T) {
	want := Hash("a\nb\n")
	for _, body := range []string{"a\nb", "a\nb\n\n\n", "a\r\nb\r\n"} {
		if got := Hash(body); got != want {
			t.Errorf("Hash(%q) differs", body)
		}
	}
	if Hash("a\nc\n") == want {
		t.Error("different content hashed the same")
	}
}

func TestPublishFreezeDriftUnfreeze(t *testing.T) {
	tk := mk(t, Ticket{ID: "S-0001", Status: StatusReady, Body: readyBody})
	if tk.Frozen() {
		t.Fatal("new ticket is frozen")
	}
	if err := tk.Unfreeze(); err == nil {
		t.Fatal("unfreezing an unfrozen ticket should fail")
	}
	if err := tk.MarkPublished("", "1"); err == nil {
		t.Fatal("missing tool should fail")
	}
	if err := tk.MarkPublished("ado", "48213"); err != nil {
		t.Fatal(err)
	}
	if !tk.Frozen() || tk.Status != StatusPublished || tk.External["ado"] != "48213" {
		t.Fatalf("ticket = %+v", tk)
	}
	frozen, err := Parse(tk.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if problems := ValidateFile(frozen); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}

	// A second tool can be added, and the same id is a no-op. A different id can't.
	if err := frozen.MarkPublished("github", "org/repo#7"); err != nil {
		t.Fatal(err)
	}
	if err := frozen.MarkPublished("ado", "48213"); err != nil {
		t.Fatal(err)
	}
	if err := frozen.MarkPublished("ado", "999"); err == nil || !strings.Contains(err.Error(), "can't change") {
		t.Fatalf("err = %v", err)
	}

	// Editing the body is drift, flagged and refused.
	drifted, err := Parse([]byte(strings.Replace(string(frozen.Bytes()), "AC1", "AC9", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !drifted.Drifted() || !strings.Contains(messages(ValidateFile(drifted)), "drifted") {
		t.Fatalf("drift not flagged: %s", messages(ValidateFile(drifted)))
	}
	if err := drifted.MarkPublished("jira", "X-1"); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("err = %v", err)
	}

	// Frontmatter edits aren't drift: the PM tool owns status after publish.
	retagged, err := Parse([]byte(strings.Replace(string(frozen.Bytes()), "labels: []", "labels: [late]", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if retagged.Drifted() {
		t.Fatal("a frontmatter edit counted as drift")
	}

	// Unfreeze keeps the ids, passes validation, and can be republished.
	if err := drifted.Unfreeze(); err != nil {
		t.Fatal(err)
	}
	after, err := Parse(drifted.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if after.Frozen() || after.External["ado"] != "48213" || after.Status != StatusPublished {
		t.Fatalf("after = %+v", after)
	}
	if problems := ValidateFile(after); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
	if err := after.MarkPublished("ado", "48213"); err != nil {
		t.Fatal(err)
	}
	if !after.Frozen() || after.Drifted() {
		t.Fatalf("not refrozen: %+v", after)
	}
}

func TestMarkPublishedKeepsOtherFieldsByteStable(t *testing.T) {
	tk, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if err := tk.MarkPublished("ado", "1"); err != nil {
		t.Fatal(err)
	}
	got := string(tk.Bytes())
	for _, line := range []string{
		"id: S-0014                 # type prefix + workspace-global sequence: E, F, S, T",
		"repo: platform-actions     # logical name from REPOS.json; required for story/task",
		"Constraints, risks, out of scope.",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("lost %q:\n%s", line, got)
		}
	}
}

func TestNextID(t *testing.T) {
	tickets := []*Ticket{mk(t, Ticket{ID: "S-0014"}), mk(t, Ticket{ID: "F-0003", Type: Feature})}
	if got, err := NextID(tickets, Task); err != nil || got != "T-0015" {
		t.Fatalf("NextID = %q, %v", got, err)
	}
	if got, err := NextID(nil, Epic); err != nil || got != "E-0001" {
		t.Fatalf("NextID = %q, %v", got, err)
	}
	if _, err := NextID(nil, "bug"); err == nil {
		t.Fatal("unknown type should fail")
	}
	if got := NextNativeID(tickets); got != "N-0015" {
		t.Fatalf("NextNativeID = %q", got)
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("S-0001.md", string(mk(t, Ticket{ID: "S-0001"}).Bytes()))
	write("S-0002.md", string(mk(t, Ticket{ID: "S-0009"}).Bytes()))
	write("bad.md", "not a ticket")
	write("notes.txt", "ignored")
	tickets, problems, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 || len(problems) != 1 || !strings.Contains(problems[0].ID, "bad.md") {
		t.Fatalf("tickets = %d, problems = %v", len(tickets), problems)
	}
	if got := messages(ValidateFile(tickets[1])); !strings.Contains(got, "file name S-0002.md doesn't match") {
		t.Fatalf("got: %s", got)
	}
	if tickets, problems, err := LoadDir(filepath.Join(dir, "missing")); err != nil || tickets != nil || problems != nil {
		t.Fatalf("missing dir = %v, %v, %v", tickets, problems, err)
	}
}
