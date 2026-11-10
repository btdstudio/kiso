package ticket

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	if f.Status == StatusReady {
		// A ready ticket carries a readiness mark, as one readied through the CLI does.
		if err := tk.MarkReady(); err != nil {
			t.Fatal(err)
		}
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
	if !strings.Contains(messages(ValidateFile(drifted)), "drifted: body changed since") {
		t.Fatalf("drift message: %s", messages(ValidateFile(drifted)))
	}
	if err := drifted.MarkPublished("jira", "X-1"); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("err = %v", err)
	}

	// Frontmatter edits are drift too: the freeze covers the frontmatter.
	retagged, err := Parse([]byte(strings.Replace(string(frozen.Bytes()), "labels: []", "labels: [late]", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !retagged.Drifted() {
		t.Fatal("a labels edit didn't count as drift")
	}
	if !strings.Contains(messages(ValidateFile(retagged)), "drifted: frontmatter changed since") {
		t.Fatalf("drift message: %s", messages(ValidateFile(retagged)))
	}
	if err := retagged.MarkPublished("jira", "X-1"); err == nil || !strings.Contains(err.Error(), "frontmatter changed since") {
		t.Fatalf("err = %v", err)
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

// freezeFixture is a ready ticket with every kind of field, including an
// unknown key and a map that the freeze covers.
const freezeFixture = "---\nschema: 1\nid: S-0001\ntype: story\ntitle: A ticket\nparent: F-0001\nrepo: a\n" +
	"depends_on: [S-0002, S-0003]\nlabels: [x, y]\nx: keep\nmeta:\n  k: v\nstatus: ready\n---\n" + readyBody

// frozenText publishes text and returns the frozen file.
func frozenText(t *testing.T, text string) string {
	t.Helper()
	tk, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if err := tk.MarkPublished("ado", "1"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.PublishedHash, "v2:") {
		t.Fatalf("published_hash = %q", tk.PublishedHash)
	}
	return string(tk.Bytes())
}

// setLine replaces the line starting with prefix.
func setLine(t *testing.T, text, prefix, line string) string {
	t.Helper()
	i := strings.Index(text, "\n"+prefix)
	if i < 0 {
		t.Fatalf("no line %q in:\n%s", prefix, text)
	}
	end := strings.Index(text[i+1:], "\n") + i + 1
	return text[:i+1] + line + text[end:]
}

type driftCase struct {
	name   string
	edit   func(string) string
	body   bool
	fm     bool
	expect string // a substring of the ValidateFile messages; "" means none
}

func checkDrift(t *testing.T, frozen string, cases []driftCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			edited := c.edit(frozen)
			if edited == frozen {
				t.Fatal("the edit changed nothing")
			}
			tk, err := Parse([]byte(edited))
			if err != nil {
				t.Fatal(err)
			}
			body, fm := tk.Drift()
			if body != c.body || fm != c.fm {
				t.Fatalf("Drift() = (%v, %v), want (%v, %v)\n%s", body, fm, c.body, c.fm, edited)
			}
			if tk.Drifted() != (c.body || c.fm) {
				t.Fatalf("Drifted() = %v", tk.Drifted())
			}
			got := messages(ValidateFile(tk))
			if c.expect == "" && strings.Contains(got, "drifted") {
				t.Fatalf("unexpected drift: %s", got)
			}
			if c.expect != "" && !strings.Contains(got, c.expect) {
				t.Fatalf("messages = %q, want %q", got, c.expect)
			}
		})
	}
}

func replace(old, new string) func(string) string {
	return func(s string) string { return strings.Replace(s, old, new, 1) }
}

func TestFreezeCoversFrontmatter(t *testing.T) {
	const fmDrift = "drifted: frontmatter changed since it was published"
	checkDrift(t, frozenText(t, freezeFixture), []driftCase{
		{"title", replace("title: A ticket", "title: B ticket"), false, true, fmDrift},
		{"parent", replace("parent: F-0001", "parent: F-0009"), false, true, fmDrift},
		{"depends_on", replace("[S-0002, S-0003]", "[S-0002]"), false, true, fmDrift},
		{"repo", replace("repo: a\n", "repo: b\n"), false, true, fmDrift},
		{"labels", replace("labels: [x, y]", "labels: [x, z]"), false, true, fmDrift},
		{"unknown key", replace("x: keep", "x: changed"), false, true, fmDrift},
		{"new key", replace("x: keep\n", "x: keep\nestimate: S\n"), false, true, fmDrift},
		{"map value", replace("  k: v\n", "  k: w\n"), false, true, fmDrift},
		{"body and frontmatter", func(s string) string {
			return strings.Replace(strings.Replace(s, "title: A ticket", "title: B", 1), "AC1", "AC9", 1)
		}, true, true, "drifted: body and frontmatter changed since it was published"},
	})
}

func TestFreezeIgnoresFieldsWrittenAfterIt(t *testing.T) {
	withMarks := strings.Replace(freezeFixture, "id: S-0001\n", "id: S-0001\nready_hash: v2:old\norigin:\n  workspace: w\n  path: p\n", 1)
	checkDrift(t, frozenText(t, freezeFixture), []driftCase{
		{"status changed", replace("status: published", "status: ready"), false, false, ""},
		{"status removed", replace("status: published\n", ""), false, false, ""},
		{"second external id", replace("  ado: 1\n", "  ado: 1\n  github: org/repo#7\n"), false, false, ""},
		{"origin added", replace("id: S-0001\n", "id: S-0001\norigin:\n  workspace: w\n  path: p\n"), false, false, ""},
		{"ready_hash added", replace("id: S-0001\n", "id: S-0001\nready_hash: v2:any\n"), false, false, ""},
	})
	// MarkPublished removes ready_hash, so put one back by hand: a stray mark on
	// a published ticket still mustn't count as drift.
	frozenWithMarks := strings.Replace(frozenText(t, withMarks), "id: S-0001\n", "id: S-0001\nready_hash: v2:old\n", 1)
	checkDrift(t, frozenWithMarks, []driftCase{
		{"ready_hash changed", replace("ready_hash: v2:old", "ready_hash: v2:new"), false, false, ""},
		{"ready_hash removed", replace("ready_hash: v2:old\n", ""), false, false, ""},
		{"origin changed", replace("  workspace: w\n", "  workspace: other\n"), false, false, ""},
		{"origin removed", replace("origin:\n  workspace: w\n  path: p\n", ""), false, false, ""},
	})
}

func TestFreezeIgnoresFormatting(t *testing.T) {
	frozen := frozenText(t, freezeFixture)
	checkDrift(t, frozen, []driftCase{
		{"reordered", func(s string) string {
			s = strings.Replace(s, "title: A ticket\n", "", 1)
			return strings.Replace(s, "x: keep\n", "x: keep\ntitle: A ticket\n", 1)
		}, false, false, ""},
		{"double quoted", replace("title: A ticket", `title: "A ticket"`), false, false, ""},
		{"single quoted", replace("repo: a\n", "repo: 'a'\n"), false, false, ""},
		{"list spacing", replace("[S-0002, S-0003]", "[ S-0002 ,S-0003 ]"), false, false, ""},
		{"list order", replace("[S-0002, S-0003]", "[S-0003, S-0002]"), false, false, ""},
		{"inline comment", replace("x: keep", "x: keep   # a note"), false, false, ""},
		{"comment line", replace("schema: 1\n", "schema: 1\n# a comment\n"), false, false, ""},
		{"quoted schema", replace("schema: 1\n", "schema: \"1\"\n"), false, false, ""},
		{"crlf", func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }, false, false, ""},
		{"trailing newlines", func(s string) string { return s + "\n\n\n" }, false, false, ""},
	})
}

func TestFreezeSkipsAbsentFields(t *testing.T) {
	checkDrift(t, frozenText(t, freezeFixture), []driftCase{
		{"empty list", replace("x: keep\n", "x: keep\nrequirements: []\n"), false, false, ""},
		{"empty scalar", replace("x: keep\n", "x: keep\nestimate: \"\"\n"), false, false, ""},
		{"key without a value", replace("x: keep\n", "x: keep\nsupersedes:\n"), false, false, ""},
		{"empty map entry", replace("  k: v\n", "  k: v\n  blank: \"\"\n"), false, false, ""},
		{"map of empty values", replace("x: keep\n", "x: keep\nextra:\n  a: \"\"\n"), false, false, ""},
	})
}

func TestMalformedV2FreezeIsReported(t *testing.T) {
	frozen := frozenText(t, freezeFixture)
	parsed, err := Parse([]byte(frozen))
	if err != nil {
		t.Fatal(err)
	}
	good := parsed.PublishedHash
	for name, value := range map[string]string{
		"short":          "v2:abc",
		"uppercase hex":  "v2:" + strings.ToUpper(good[3:]),
		"missing a half": good[:3+32],
		"extra half":     good + ".00",
	} {
		t.Run(name, func(t *testing.T) {
			tk, err := Parse([]byte(setLine(t, frozen, "published_hash:", "published_hash: \""+value+"\"")))
			if err != nil {
				t.Fatal(err)
			}
			if !tk.Drifted() {
				t.Fatal("a malformed freeze didn't count as drift")
			}
			got := messages(ValidateFile(tk))
			if !strings.Contains(got, "isn't a valid v2 hash") || strings.Contains(got, "drifted") {
				t.Fatalf("messages = %q", got)
			}
			err = tk.MarkPublished("ado", "1")
			if err == nil {
				t.Fatal("MarkPublished accepted a malformed freeze")
			}
			if !strings.Contains(err.Error(), "isn't a valid v2 hash") || strings.Contains(err.Error(), "changed since") {
				t.Fatalf("MarkPublished error = %v", err)
			}
			// Unfreezing clears it, and the ticket can be re-frozen.
			if err := tk.Unfreeze(); err != nil {
				t.Fatal(err)
			}
			if err := tk.MarkPublished("ado", "1"); err != nil || tk.PublishedHash != ContentHash(tk) {
				t.Fatalf("re-freeze: err = %v, published_hash = %q", err, tk.PublishedHash)
			}
		})
	}
}

func TestLegacyBodyOnlyFreeze(t *testing.T) {
	body := readyBody
	legacy := "---\nschema: 1\nid: S-0001\ntype: story\ntitle: A ticket\nrepo: a\nlabels: [x]\nstatus: published\n" +
		"external:\n  ado: 1\npublished_hash: " + Hash(body) + "\n---\n" + body
	tk, err := Parse([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if problems := ValidateFile(tk); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
	if b, f := tk.Drift(); b || f {
		t.Fatalf("Drift() = (%v, %v)", b, f)
	}
	checkDrift(t, legacy, []driftCase{
		{"body edit", replace("AC1", "AC9"), true, false, "drifted: body changed since it was published"},
		{"title edit", replace("title: A ticket", "title: B ticket"), false, false, ""},
		{"labels edit", replace("labels: [x]", "labels: [x, late]"), false, false, ""},
	})

	// Unfreeze and republish writes the v2 freeze.
	if err := tk.Unfreeze(); err != nil {
		t.Fatal(err)
	}
	if err := tk.MarkPublished("ado", "1"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.PublishedHash, "v2:") || tk.PublishedHash != ContentHash(tk) {
		t.Fatalf("published_hash = %q, ContentHash = %q", tk.PublishedHash, ContentHash(tk))
	}

	// Publishing an undrifted legacy ticket to a second tool upgrades it too.
	second, err := Parse([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if err := second.MarkPublished("github", "org/repo#7"); err != nil {
		t.Fatal(err)
	}
	if second.PublishedHash != ContentHash(second) {
		t.Fatalf("published_hash = %q", second.PublishedHash)
	}

	// Publishing a v2 ticket again leaves its freeze as it was.
	again, err := Parse(second.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := again.MarkPublished("ado", "1"); err != nil {
		t.Fatal(err)
	}
	if again.PublishedHash != second.PublishedHash {
		t.Fatalf("republish changed the freeze: %q -> %q", second.PublishedHash, again.PublishedHash)
	}
}

// TestContentHashIsPinned fixes the canonical form. If this fails, the hash of
// every v2 freeze changed: that needs a new prefix, not a new literal.
func TestContentHashIsPinned(t *testing.T) {
	const text = "---\nschema: 1\nid: S-0042\ntype: story\ntitle: \"Say \\\"hi\\\" <now> & then\"\nrepo: kiso\n" +
		"depends_on: [S-0002, S-0001]\nlabels: []\nzeta: unknown\nmeta:\n  b: 2\n  a: 1\nstatus: published\n" +
		"external:\n  ado: 7\norigin:\n  workspace: w\n  path: p\nready_hash: v2:x\npublished_hash: \"v2:y\"\n---\nbody text\n"
	tk, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	// Checked by hand: the sha256 of "body text\n" and of this text, each cut
	// to 32 hex characters (shasum -a 256).
	const canonical = `depends_on=["S-0001","S-0002"]
id="S-0042"
meta={"a":"1","b":"2"}
repo="kiso"
schema="1"
title="Say \"hi\" \u003cnow\u003e \u0026 then"
type="story"
zeta="unknown"
`
	if got := canonicalFrontmatter(tk.doc); got != canonical {
		t.Fatalf("canonical frontmatter:\n%s\nwant:\n%s", got, canonical)
	}
	const want = "v2:044bbaad51900e452b203079800e84aa.14f77507924d080a4363e45918b15b41"
	if got := ContentHash(tk); got != want {
		t.Fatalf("ContentHash = %s, want %s", got, want)
	}
}

func TestKeysSortByNameNotByLine(t *testing.T) {
	tk, err := Parse([]byte("---\na: 1\na-b: 2\na1: 3\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := "a=\"1\"\na-b=\"2\"\na1=\"3\"\n"
	if got := canonicalFrontmatter(tk.doc); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHashIsUnchanged(t *testing.T) {
	// The legacy hash of v0.8.0, so old freezes keep validating.
	if got := Hash("a\r\nb"); got != "911169ddaaf146aff539f58c26c489af3b892dff0fe283c1c264c65ae5aa59a2" {
		t.Fatalf("Hash = %s", got)
	}
}

// QA: cases the implement tests don't cover.

func TestQAHashLeavesTheDocumentAlone(t *testing.T) {
	tk, err := Parse([]byte(freezeFixture))
	if err != nil {
		t.Fatal(err)
	}
	tk2, err := Parse([]byte(strings.Replace(freezeFixture, "[S-0002, S-0003]", "[S-0003, S-0002]", 1)))
	if err != nil {
		t.Fatal(err)
	}
	before := string(tk2.Bytes())
	if ContentHash(tk) != ContentHash(tk2) {
		t.Fatal("list order changed the hash")
	}
	if string(tk2.Bytes()) != before || strings.Join(tk2.doc.List("depends_on"), ",") != "S-0003,S-0002" {
		t.Fatal("ContentHash changed the document")
	}
}

func TestQAFreezeSurvivesRoundTrips(t *testing.T) {
	frozen := frozenText(t, freezeFixture)
	tk, err := Parse([]byte(frozen))
	if err != nil {
		t.Fatal(err)
	}
	// Parse, render and parse again: still the same freeze, no drift.
	again, err := Parse(tk.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if again.Drifted() || ContentHash(again) != tk.PublishedHash {
		t.Fatalf("round trip drifted: %q vs %q", ContentHash(again), tk.PublishedHash)
	}
	// A ticket built with New and published validates cleanly.
	built := mk(t, Ticket{ID: "S-0009", Status: StatusReady, Body: readyBody, DependsOn: []string{"S-0002", "S-0001"}, Labels: []string{"b", "a"}})
	if err := built.MarkPublished("ado", "9"); err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(built.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if problems := ValidateFile(reparsed); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
}

func TestQAMalformedHalves(t *testing.T) {
	frozen := frozenText(t, freezeFixture)
	parsed, err := Parse([]byte(frozen))
	if err != nil {
		t.Fatal(err)
	}
	good := parsed.PublishedHash
	b, f, _ := strings.Cut(good[3:], ".")
	for name, value := range map[string]string{
		"33-character half": "v2:" + b + "." + f + "0",
		"31-character half": "v2:" + b[:31] + "." + f,
		"non-hex character": "v2:" + b[:31] + "g." + f,
		"no separator":      "v2:" + b + f,
		"prefix only":       "v2:",
		"uppercase prefix":  "V2:" + b + "." + f,
	} {
		t.Run(name, func(t *testing.T) {
			tk, err := Parse([]byte(setLine(t, frozen, "published_hash:", "published_hash: \""+value+"\"")))
			if err != nil {
				t.Fatal(err)
			}
			if !tk.Drifted() {
				t.Fatal("not drifted")
			}
			got := messages(ValidateFile(tk))
			// "V2:" isn't the v2 prefix, so it's a legacy hash that doesn't match.
			want := "isn't a valid v2 hash"
			if strings.HasPrefix(value, "V2:") {
				want = "drifted: body changed"
			}
			if !strings.Contains(got, want) {
				t.Fatalf("messages = %q, want %q", got, want)
			}
		})
	}
}

// Security #1: JSON can't tell invalid UTF-8 bytes apart, so two values that
// differ only in them would hash the same. ValidateFile refuses them instead.
func TestInvalidUTF8FrontmatterIsReported(t *testing.T) {
	head := "---\nschema: 1\nid: S-0001\ntype: story\nrepo: a\nstatus: draft\n"
	for name, c := range map[string]struct{ fm, key string }{
		"scalar":    {"title: x\xffy\n", "title"},
		"list item": {"title: x\nlabels: [ok, b\xfe]\n", "labels"},
		"map value": {"title: x\nmeta:\n  k: v\xff\n", "meta"},
		"map key":   {"title: x\nmeta:\n  k\xff: v\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			tk, err := Parse([]byte(head + c.fm + "---\nbody\n"))
			if c.key == "" {
				// Keys must match the parser's key pattern, so an invalid byte
				// in one is rejected before it reaches the hash.
				if err == nil {
					t.Fatal("parser accepted a key with an invalid byte")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "frontmatter field " + c.key + " isn't valid UTF-8"
			if got := messages(ValidateFile(tk)); !strings.Contains(got, want) {
				t.Fatalf("messages = %q, want %q", got, want)
			}
		})
	}

	// Valid non-ASCII text is fine.
	ok, err := Parse([]byte(head + "title: café — naïve\nlabels: [日本]\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := messages(ValidateFile(ok)); strings.Contains(got, "UTF-8") {
		t.Fatalf("valid UTF-8 reported: %s", got)
	}

	// The case the check exists for: the two values hash the same.
	a, _ := Parse([]byte(head + "title: x\xffy\n---\nb\n"))
	b, _ := Parse([]byte(head + "title: x\xfey\n---\nb\n"))
	if ContentHash(a) != ContentHash(b) {
		t.Fatal("the collision this check guards against is gone; revisit the check")
	}
}

// draftText is a draft story with the given acceptance criteria lines.
func draftText(criteria string) string {
	return "---\nschema: 1\nid: S-0001\ntype: story\ntitle: A ticket\nrepo: a\nlabels: [x]\nstatus: draft\n---\n" +
		"\n## Context\nWhy.\n\n## Acceptance criteria\n" + criteria + "\n## Notes\nNone.\n"
}

func parseText(t *testing.T, text string) *Ticket {
	t.Helper()
	tk, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func noToolNames(t *testing.T, s string) {
	t.Helper()
	if lower := strings.ToLower(s); strings.Contains(lower, "zumen") || strings.Contains(lower, "tomo") {
		t.Fatalf("message names a tool: %s", s)
	}
}

func TestMarkReady(t *testing.T) {
	draft := draftText("- [ ] AC1: Given a repo When a tag lands Then a release is cut\n")
	tk := parseText(t, draft)
	if err := tk.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if tk.Status != StatusReady || tk.ReadyHash == "" || tk.ReadyHash != ContentHash(tk) {
		t.Fatalf("status = %q, ready_hash = %q, ContentHash = %q", tk.Status, tk.ReadyHash, ContentHash(tk))
	}
	if problems := ValidateFile(tk); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
	// Byte-stable: only the status line changes and ready_hash is added.
	got := string(tk.Bytes())
	want := strings.Replace(draft, "status: draft\n", "status: ready\nready_hash: "+strconvQuote(tk.ReadyHash)+"\n", 1)
	if got != want {
		t.Fatalf("bytes:\n%s\nwant:\n%s", got, want)
	}
	// It survives a round trip.
	again := parseText(t, got)
	if again.ReadyHash != tk.ReadyHash {
		t.Fatalf("round trip: %q", again.ReadyHash)
	}
	if problems := ValidateFile(again); len(problems) != 0 {
		t.Fatalf("problems after round trip:\n%s", messages(problems))
	}
}

// strconvQuote renders a value the way the frontmatter writer does: v2 values
// contain ":", so they're double-quoted.
func strconvQuote(s string) string { return `"` + s + `"` }

func TestReadinessIsValidated(t *testing.T) {
	ready := parseText(t, draftText("- [ ] AC1: Given x When y Then z\n"))
	if err := ready.MarkReady(); err != nil {
		t.Fatal(err)
	}
	readyText := string(ready.Bytes())

	cases := []struct {
		name string
		text string
		want string // "" means no readiness problem
	}{
		{"set to ready by hand", strings.Replace(draftText("- [ ] AC1: Given x When y Then z\n"), "status: draft", "status: ready", 1),
			"not readied through the CLI"},
		{"body edited", strings.Replace(readyText, "Why.", "Why not.", 1), "body changed since readied"},
		{"title edited", strings.Replace(readyText, "title: A ticket", "title: Another", 1), "frontmatter changed since readied"},
		{"labels edited", strings.Replace(readyText, "labels: [x]", "labels: [x, y]", 1), "frontmatter changed since readied"},
		{"both edited", strings.Replace(strings.Replace(readyText, "Why.", "Why not.", 1), "title: A ticket", "title: B", 1),
			"body and frontmatter changed since readied"},
		{"malformed mark", setLine(t, readyText, "ready_hash:", `ready_hash: "v2:abc"`), `ready_hash "v2:abc" isn't a valid v2 hash`},
		{"external added", strings.Replace(readyText, "labels: [x]\n", "labels: [x]\nexternal:\n  ado: 1\n", 1), ""},
		{"origin added", strings.Replace(readyText, "labels: [x]\n", "labels: [x]\norigin:\n  workspace: w\n  path: p\n", 1), ""},
		{"untouched", readyText, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := messages(ValidateFile(parseText(t, c.text)))
			noToolNames(t, got)
			if c.want == "" {
				if strings.Contains(got, "readied") || strings.Contains(got, "ready_hash") {
					t.Fatalf("unexpected readiness problem: %s", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("messages = %q, want %q", got, c.want)
			}
		})
	}

	// Readying again after an edit refreshes the mark.
	edited := parseText(t, strings.Replace(readyText, "Why.", "Why not.", 1))
	if err := edited.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if problems := ValidateFile(parseText(t, string(edited.Bytes()))); len(problems) != 0 {
		t.Fatalf("problems after re-readying:\n%s", messages(problems))
	}

	// A stale mark on a draft isn't an error.
	stale := strings.Replace(strings.Replace(readyText, "status: ready", "status: draft", 1), "Why.", "Why not.", 1)
	if problems := ValidateFile(parseText(t, stale)); len(problems) != 0 {
		t.Fatalf("draft with a stale mark:\n%s", messages(problems))
	}
}

func TestMarkReadyRefusesOtherStatuses(t *testing.T) {
	published := parseText(t, draftText("- [ ] AC1: Given x When y Then z\n"))
	if err := published.MarkPublished("ado", "1"); err != nil {
		t.Fatal(err)
	}
	closed := mk(t, Ticket{ID: "N-0001", Type: Task, Status: StatusClosed})
	for name, tk := range map[string]*Ticket{"published": published, "closed": closed} {
		t.Run(name, func(t *testing.T) {
			before := string(tk.Bytes())
			err := tk.MarkReady()
			if err == nil || !strings.Contains(err.Error(), "only a draft or ready ticket can be readied") {
				t.Fatalf("err = %v", err)
			}
			if string(tk.Bytes()) != before {
				t.Fatal("a refused MarkReady changed the ticket")
			}
		})
	}
}

func TestMarkPublishedRemovesTheReadinessMark(t *testing.T) {
	tk := parseText(t, draftText("- [ ] AC1: Given x When y Then z\n"))
	if err := tk.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if err := tk.MarkPublished("ado", "1"); err != nil {
		t.Fatal(err)
	}
	if tk.ReadyHash != "" || strings.Contains(string(tk.Bytes()), "ready_hash") {
		t.Fatalf("ready_hash kept:\n%s", tk.Bytes())
	}
	again := parseText(t, string(tk.Bytes()))
	if problems := ValidateFile(again); len(problems) != 0 {
		t.Fatalf("problems:\n%s", messages(problems))
	}
	if again.PublishedHash != ContentHash(again) {
		t.Fatalf("freeze = %q, ContentHash = %q", again.PublishedHash, ContentHash(again))
	}
}

func TestPlaceholderCriteria(t *testing.T) {
	cases := []struct {
		name     string
		criteria string
		want     []string // substrings of the messages, in any order
		notWant  []string
	}{
		{"ellipsis template only", "- [ ] AC1: Given … When … Then …\n",
			[]string{
				"status ready requires at least one acceptance criterion without a placeholder",
				`acceptance criterion "- [ ] AC1: Given … When … Then …" has a placeholder "…"`,
			}, nil},
		{"angle template only", "- [ ] Given <context>, when <action>, then <result>\n",
			[]string{
				"requires at least one acceptance criterion without a placeholder",
				`acceptance criterion "- [ ] Given <context>, when <action>, then <result>" has a placeholder "<context>"`,
			}, nil},
		{"one real, one placeholder", "- [ ] AC1: Given x When y Then z\n- [ ] AC2: Given … When … Then …\n",
			[]string{`acceptance criterion "- [ ] AC2: Given … When … Then …" has a placeholder "…"`},
			[]string{"requires at least one", "AC1"}},
		{"inline code isn't a placeholder", "- [ ] AC1: Given `List<T>` When `...` Then z\n",
			nil, []string{"placeholder", "requires at least one"}},
		{"fenced code in the section", "- [ ] AC1: Given x When y Then z\n```\n- [ ] ... <x>\n```\n",
			nil, []string{"placeholder", "requires at least one"}},
		{"no checkbox at all", "Nothing here.\n",
			[]string{"status ready requires at least one acceptance criterion"},
			[]string{"without a placeholder"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, eol := range []string{"\n", "\r\n"} {
				text := strings.ReplaceAll(draftText(c.criteria), "\n", eol)
				tk := parseText(t, text)
				if err := tk.MarkReady(); err != nil {
					t.Fatal(err)
				}
				got := messages(ValidateFile(tk))
				noToolNames(t, got)
				for _, w := range c.want {
					if !strings.Contains(got, w) {
						t.Fatalf("eol %q: messages =\n%s\nwant %q", eol, got, w)
					}
				}
				for _, w := range c.notWant {
					if strings.Contains(got, w) {
						t.Fatalf("eol %q: messages =\n%s\ndon't want %q", eol, got, w)
					}
				}
				if strings.Contains(got, "readied") {
					t.Fatalf("eol %q: readiness problem: %s", eol, got)
				}
			}
		})
	}
}

func TestHasAcceptanceCriteriaIgnoresPlaceholders(t *testing.T) {
	cases := map[string]bool{
		"\n## Acceptance criteria\n- [ ] AC1: Given … When … Then …\n":                   false,
		"\n## Acceptance criteria\n- [ ] AC1: Given x When y Then z\n":                   true,
		"\n## Acceptance criteria\n- [ ] Given <context>\n- [x] AC2: done\n":             true,
		"\n## Context\n- [ ] AC1: Given x When y Then z\n## Acceptance criteria\nnone\n": false,
		"\n## Context\nGiven … <context>\n## Acceptance criteria\n- [ ] AC1: real\n":     true,
		"\n## Acceptance criteria\n- [ ] AC1: real\n## Notes\n- [ ] … later\n":           true,
		"\n## Acceptance criteria\n\n\n* [ ] AC1: star bullet\n":                         true,
		"no heading\n- [ ] AC1: real\n":                                                  false,
	}
	for body, want := range cases {
		if got := HasAcceptanceCriteria(body); got != want {
			t.Errorf("HasAcceptanceCriteria(%q) = %v, want %v", body, got, want)
		}
	}
}

// QA: the criteria section ends at the next "## " heading.
func TestQACriteriaSectionBounds(t *testing.T) {
	cases := map[string]bool{
		"\n## Acceptance criteria\nNone yet.\n## Notes\n- [ ] AC1: Given x When y Then z\n": false,
		"\n## Acceptance criteria\n### Sub\n- [ ] AC1: real\n":                              true, // "###" isn't a section break
		"\n## acceptance criteria\n- [ ] AC1: real\n":                                       true, // heading is case-insensitive
		"\n### Acceptance criteria\n- [ ] AC1: real\n":                                      false,
		"\n## Acceptance criteria  \n- [ ] AC1: real\n":                                     true, // trailing spaces
	}
	for body, want := range cases {
		if got := HasAcceptanceCriteria(body); got != want {
			t.Errorf("HasAcceptanceCriteria(%q) = %v, want %v", body, got, want)
		}
	}

	// A placeholder checkbox after the section isn't reported.
	text := strings.Replace(draftText("- [ ] AC1: Given x When y Then z\n"), "## Notes\nNone.\n", "## Notes\n- [ ] Given … later\n", 1)
	tk := parseText(t, text)
	if err := tk.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if got := messages(ValidateFile(tk)); strings.Contains(got, "placeholder") {
		t.Fatalf("a checkbox outside the section was reported: %s", got)
	}
}

// QA: for bodies without placeholders, HasAcceptanceCriteria agrees with the
// v0.8.0 implementation, so the rewrite changes nothing but placeholders.
func TestQAHasAcceptanceCriteriaMatchesV080WithoutPlaceholders(t *testing.T) {
	v080 := func(body string) bool {
		acceptanceRe := regexp.MustCompile(`(?im)^##\s+Acceptance criteria\s*$`)
		checkboxRe := regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+\S`)
		headingRe := regexp.MustCompile(`(?m)^##\s`)
		loc := acceptanceRe.FindStringIndex(body)
		if loc == nil {
			return false
		}
		section := body[loc[1]:]
		if next := headingRe.FindStringIndex(section); next != nil {
			section = section[:next[0]]
		}
		return checkboxRe.MatchString(section)
	}
	heads := []string{"## Acceptance criteria", "## acceptance criteria", "##  Acceptance criteria  ", "### Acceptance criteria", "## Acceptance"}
	middles := []string{"", "\n", "\n\n", "\ntext\n", "\n## Next\n"}
	items := []string{"- [ ] AC1: x", "* [x] AC1: x", "  - [X] AC1: x", "- [] AC1: x", "-[ ] AC1: x", "- [ ]", "1. [ ] AC1: x", "\t- [ ] AC1: x"}
	tails := []string{"", "\n", "\n## Notes\n- [ ] AC9: y\n", "\n### Sub\n"}
	n := 0
	for _, eol := range []string{"\n", "\r\n"} {
		for _, h := range heads {
			for _, m := range middles {
				for _, it := range items {
					for _, tl := range tails {
						body := strings.ReplaceAll("\n## Context\nwhy\n"+h+m+it+tl, "\n", eol)
						n++
						if it == "- [ ]" {
							// v0.8.0's \s+\S could run past the end of the line, so
							// an empty checkbox counted whenever text followed it.
							// TestQAEmptyCheckboxIsNotACriterion pins the fix.
							continue
						}
						if got, want := HasAcceptanceCriteria(body), v080(body); got != want {
							t.Errorf("HasAcceptanceCriteria(%q) = %v, v0.8.0 said %v", body, got, want)
						}
					}
				}
			}
		}
	}
	if n < 1000 {
		t.Fatalf("only %d bodies checked", n)
	}
}

// QA: an empty checkbox isn't a criterion, even with text on a later line. In
// v0.8.0 it counted, because the checkbox pattern could cross the line end.
func TestQAEmptyCheckboxIsNotACriterion(t *testing.T) {
	for _, body := range []string{
		"\n## Acceptance criteria\n- [ ]\n### Sub\n",
		"\n## Acceptance criteria\n- [ ]\nsome text\n",
		"\n## Acceptance criteria\n- [ ]   \r\nmore\r\n",
	} {
		if HasAcceptanceCriteria(body) {
			t.Errorf("HasAcceptanceCriteria(%q) = true", body)
		}
	}
}
