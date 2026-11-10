package frontmatter

import (
	"reflect"
	"strings"
	"testing"
)

const sample = `---
schema: 1
id: S-0014                 # type prefix + sequence

# a comment on its own line
title: "Add: release workflow"
depends_on: [S-0011, S-0012]   # predecessors
labels: []
external:                  # written by mark-published only
  ado: 48213
  github: "org/repo#7"
quoted: 'it''s fine'
empty:
---

## Context
Body with --- inside.
---
`

func parse(t *testing.T, text string) *Document {
	t.Helper()
	d, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReadsEachKind(t *testing.T) {
	d := parse(t, sample)
	for key, want := range map[string]string{
		"schema": "1", "id": "S-0014", "title": "Add: release workflow", "quoted": "it's fine", "empty": "",
	} {
		if got := d.Get(key); got != want {
			t.Errorf("Get(%q) = %q, want %q", key, got, want)
		}
	}
	if got := d.List("depends_on"); !reflect.DeepEqual(got, []string{"S-0011", "S-0012"}) {
		t.Errorf("depends_on = %v", got)
	}
	if got := d.List("labels"); got == nil || len(got) != 0 {
		t.Errorf("labels = %#v", got)
	}
	if got := d.Map("external"); !reflect.DeepEqual(got, map[string]string{"ado": "48213", "github": "org/repo#7"}) {
		t.Errorf("external = %v", got)
	}
	if !strings.HasPrefix(d.Body, "\n## Context") || !strings.HasSuffix(d.Body, "---\n") {
		t.Errorf("body = %q", d.Body)
	}
	if d.Get("nope") != "" || d.List("nope") != nil || d.Map("nope") != nil || d.Has("nope") {
		t.Error("absent key should read empty")
	}
	if d.Get("depends_on") != "" || d.List("id") != nil || d.Map("id") != nil {
		t.Error("reading a key as the wrong kind should be empty")
	}
}

func TestUnchangedRoundTripIsByteStable(t *testing.T) {
	for name, text := range map[string]string{
		"sample": sample,
		"crlf":   strings.ReplaceAll(sample, "\n", "\r\n"),
		"empty":  "---\n---\nbody",
		"only":   "---\n# just a comment\n\n---\n",
	} {
		if got := string(parse(t, text).Bytes()); got != text {
			t.Errorf("%s changed:\n%q\nwant\n%q", name, got, text)
		}
	}
}

func TestEditingOneFieldLeavesTheRestAlone(t *testing.T) {
	d := parse(t, sample)
	if err := d.Set("id", "S-0015"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sample, "id: S-0014                 # type prefix + sequence", "id: S-0015", 1)
	if got := string(d.Bytes()); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSetSubAddsAndUpdatesKeepingOthers(t *testing.T) {
	d := parse(t, sample)
	if err := d.SetSub("external", "jira", "ABC-1"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSub("external", "ado", "1"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSub("origin", "path", "specs/x.md"); err != nil {
		t.Fatal(err)
	}
	again := parse(t, string(d.Bytes()))
	want := map[string]string{"ado": "1", "github": "org/repo#7", "jira": "ABC-1"}
	if got := again.Map("external"); !reflect.DeepEqual(got, want) {
		t.Fatalf("external = %v", got)
	}
	if got := again.Map("origin"); got["path"] != "specs/x.md" {
		t.Fatalf("origin = %v", got)
	}
}

func TestWrittenValuesReadBack(t *testing.T) {
	values := []string{
		"plain", "two words", "with: colon", "with # hash", `quo"te`, `back\slash`, "it's", "[not a list]",
		"{not a map}", "&anchor", "*alias", "| pipe", "> fold", "- dash", "---", "trailing ", " leading", "#start", "", "日本語",
	}
	for _, value := range values {
		d := New("body\n")
		if err := d.Set("k", value); err != nil {
			t.Fatal(err)
		}
		if err := d.SetList("l", []string{value + "x", "a, b", "c]"}); err != nil {
			t.Fatal(err)
		}
		if value != "" {
			if err := d.SetSub("m", "s", value); err != nil {
				t.Fatal(err)
			}
		}
		again, err := Parse(d.Bytes())
		if err != nil {
			t.Fatalf("%q: %v\n%s", value, err, d.Bytes())
		}
		if got := again.Get("k"); got != value {
			t.Errorf("scalar %q came back %q", value, got)
		}
		if got := again.List("l"); !reflect.DeepEqual(got, []string{value + "x", "a, b", "c]"}) {
			t.Errorf("list with %q came back %v", value, got)
		}
		if value != "" {
			if got := again.Map("m")["s"]; got != value {
				t.Errorf("sub %q came back %q", value, got)
			}
		}
		if again.Body != "body\n" {
			t.Errorf("body = %q", again.Body)
		}
	}
}

func TestSetRefusesValuesThatWouldInjectLines(t *testing.T) {
	d := New("")
	for _, value := range []string{"a\nstatus: published", "a\r\nb", "x\n---\nbody"} {
		if err := d.Set("title", value); err == nil {
			t.Errorf("Set(%q) should fail", value)
		}
		if err := d.SetList("l", []string{value}); err == nil {
			t.Errorf("SetList(%q) should fail", value)
		}
		if err := d.SetSub("m", "s", value); err == nil {
			t.Errorf("SetSub(%q) should fail", value)
		}
	}
	if d.Has("title") || d.Has("l") || d.Has("m") {
		t.Fatalf("a refused write left a key: %v", d.Keys())
	}
	if err := d.Set("bad key", "x"); err == nil {
		t.Error("invalid key should fail")
	}
	if err := d.SetSub("m", "s", ""); err == nil {
		t.Error("empty sub value would read as a deeper map")
	}
}

func TestDeleteAndKeys(t *testing.T) {
	d := parse(t, "---\na: 1\nb: 2\n---\n")
	d.Delete("a")
	d.Delete("missing")
	if !reflect.DeepEqual(d.Keys(), []string{"b"}) {
		t.Fatalf("keys = %v", d.Keys())
	}
	if got := string(d.Bytes()); got != "---\nb: 2\n---\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRejectsUnsupportedYAML(t *testing.T) {
	for name, c := range map[string]struct{ text, want string }{
		"no opening":    {"title: x\n---\n", "must start"},
		"no closing":    {"---\ntitle: x\n", "no closing"},
		"anchor":        {"---\na: &x 1\n---\n", "anchors"},
		"alias":         {"---\na: *x\n---\n", "anchors"},
		"literal":       {"---\na: |\n  text\n---\n", "multiline"},
		"folded":        {"---\na: >\n  text\n---\n", "multiline"},
		"flow map":      {"---\na: {b: 1}\n---\n", "inline maps"},
		"deep":          {"---\na:\n  b:\n    c: 1\n---\n", "one level"},
		"deep indent":   {"---\na:\n    c: 1\n---\n", "one level"},
		"block list":    {"---\n- a\n---\n", "block lists"},
		"block in map":  {"---\na:\n  - b\n---\n", "block lists"},
		"list in map":   {"---\na:\n  b: [1]\n---\n", "scalar"},
		"stray indent":  {"---\n  a: 1\n---\n", "indentation"},
		"no colon":      {"---\njust text\n---\n", "key: value"},
		"no space":      {"---\na:b\n---\n", "space after"},
		"bad key":       {"---\na b: 1\n---\n", "invalid key"},
		"duplicate":     {"---\na: 1\na: 2\n---\n", "duplicate"},
		"duplicate sub": {"---\na:\n  b: 1\n  b: 2\n---\n", "duplicate"},
		"open quote":    {"---\na: \"x\n---\n", "unterminated"},
		"after quote":   {"---\na: \"x\" y\n---\n", "after a quoted"},
		"open list":     {"---\na: [x\n---\n", "unterminated"},
		"after list":    {"---\na: [x] y\n---\n", "after a list"},
		"nested list":   {"---\na: [[x]]\n---\n", "scalars"},
		"trailing":      {"---\na: [x,]\n---\n", "trailing comma"},
		"empty item":    {"---\na: [x,,y]\n---\n", "empty list item"},
		"bad escape":    {"---\na: \"x\\ny\"\n---\n", "escapes"},
	} {
		_, err := Parse([]byte(c.text))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		}
	}
}

func TestErrorsNameTheLine(t *testing.T) {
	_, err := Parse([]byte("---\na: 1\nb: &x\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestFields(t *testing.T) {
	d := parse(t, sample)
	want := []Field{
		{Key: "schema", Kind: Scalar, Value: "1"},
		{Key: "id", Kind: Scalar, Value: "S-0014"},
		{Key: "title", Kind: Scalar, Value: "Add: release workflow"},
		{Key: "depends_on", Kind: List, Items: []string{"S-0011", "S-0012"}},
		{Key: "labels", Kind: List, Items: []string{}},
		{Key: "external", Kind: Map, Subs: map[string]string{"ado": "48213", "github": "org/repo#7"}},
		{Key: "quoted", Kind: Scalar, Value: "it's fine"},
		{Key: "empty", Kind: Scalar, Value: ""},
	}
	got := d.Fields()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields()\n got %#v\nwant %#v", got, want)
	}

	// The result is a copy: changing it leaves the document alone.
	before := string(d.Bytes())
	got[3].Items[0] = "X-9999"
	got[5].Subs["ado"] = "1"
	got[5].Subs["jira"] = "J-1"
	if string(d.Bytes()) != before {
		t.Fatal("changing Fields() changed the document")
	}
	if again := d.Fields(); !reflect.DeepEqual(again, want) {
		t.Fatalf("second Fields() = %#v", again)
	}

	if fields := New("body").Fields(); len(fields) != 0 {
		t.Fatalf("empty document has fields %#v", fields)
	}
}
