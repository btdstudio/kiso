package placeholder

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type findCase struct {
	name string
	in   string
	want []Match
}

func runCases(t *testing.T, cases []findCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Find(c.in)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Find(%q)\n got %#v\nwant %#v", c.in, got, c.want)
			}
		})
	}
}

func TestAcceptanceExamples(t *testing.T) {
	runCases(t, []findCase{
		{"ellipsis criterion", "- [ ] AC1: Given … When … Then …",
			[]Match{{1, "…"}, {1, "…"}, {1, "…"}}},
		{"angle criterion", "Given <context>, when <action>, then <observable result>",
			[]Match{{1, "<context>"}, {1, "<action>"}, {1, "<observable result>"}}},
		{"code, html and prose", strings.Join([]string{
			"Mentions `List<T>` in inline code.",
			"```",
			"x := f(...)",
			"```",
			"Line<br>break, <https://x.y>, </p>, <!-- note --> etc...",
		}, "\n"), nil},
	})
}

func TestEllipsisSlots(t *testing.T) {
	runCases(t, []findCase{
		{"alone", "...", []Match{{1, "..."}}},
		{"start", "... then more", []Match{{1, "..."}}},
		{"middle", "a … b", []Match{{1, "…"}}},
		{"end", "- D2. ...", []Match{{1, "..."}}},
		{"tab separated", "a\t...\tb", []Match{{1, "..."}}},
		{"etc", "and so on etc...", nil},
		{"wait", "wait… what", nil},
		{"four dots", "....", nil},
		{"parenthesised", "(...)", nil},
		{"two dots", "..", nil},
		{"next to code", "`x`... and ...`y`", nil},
		{"next to a slot", "<x>... and ...<y>", []Match{{1, "<x>"}, {1, "<y>"}}},
		{"inside a slot", "<x … z>", []Match{{1, "<x … z>"}}},
	})
}

func TestAngleSlots(t *testing.T) {
	runCases(t, []findCase{
		{"word", "<prefix>", []Match{{1, "<prefix>"}}},
		{"generic in prose", "a <T> here", []Match{{1, "<T>"}}},
		{"unlisted element", "<mark>", []Match{{1, "<mark>"}}},
		{"words", "<ISO date>", []Match{{1, "<ISO date>"}}},
		{"punctuation", "<file path | issue:N | url, one per line>",
			[]Match{{1, "<file path | issue:N | url, one per line>"}}},
		{"no letter", "<1> and < > and <>", nil},
		{"nested opens", "<x <y>", []Match{{1, "<y>"}}},
		{"unclosed", "a < b", nil},
		{"stray close", "a > b <c>", []Match{{1, "<c>"}}},
		{"closing tag", "</p>", nil},
		{"doctype", "<!doctype html>", nil},
		{"url autolink", "<https://x.y>", nil},
		{"mailto autolink", "<mailto:a@b>", nil},
		{"mailto upper", "<MAILTO:x>", nil},
		{"email autolink", "<a@b.c>", nil},
		{"element", "<br>", nil},
		{"element upper", "<BR>", nil},
		{"self-closing", "<br/>", nil},
		{"self-closing spaced", "<br />", nil},
		{"attributes", `<a href="x">`, nil},
		{"bare attribute", "<details open>", nil},
		{"every element", "<a><b><br><code><details><div><em><hr><i><img><kbd><li><ol><p>" +
			"<pre><span><strong><sub><summary><sup><table><td><th><tr><u><ul>", nil},
		{"element prefix is a word", "<prefix> <bold> <divider>",
			[]Match{{1, "<prefix>"}, {1, "<bold>"}, {1, "<divider>"}}},
		{"spans lines", "<what outcome —\nnot restated>", nil},
	})
}

func TestCodeAndComments(t *testing.T) {
	runCases(t, []findCase{
		{"backtick fence", "```\n<x>\n...\n```\n<y>", []Match{{5, "<y>"}}},
		{"tilde fence", "~~~go\n<x>\n~~~\n<y>", []Match{{4, "<y>"}}},
		{"longer close", "```\n<x>\n`````\n<y>", []Match{{4, "<y>"}}},
		{"shorter run doesn't close", "````\n```\n<x>\n````\n<y>", []Match{{5, "<y>"}}},
		{"other char doesn't close", "```\n~~~\n<x>\n```\n<y>", []Match{{5, "<y>"}}},
		{"close with trailing text doesn't close", "```\n``` x\n<x>\n```\n<y>", []Match{{5, "<y>"}}},
		{"indented fence in a list", "- item\n    ```\n    <x>\n    ```\n- <y>", []Match{{5, "<y>"}}},
		{"unclosed fence", "<w>\n```\n<x>\n...", []Match{{1, "<w>"}}},
		{"backtick in info string is not a fence", "``` a`b\n<x>", []Match{{2, "<x>"}}},
		{"two backticks are not a fence", "``\n<x>", []Match{{2, "<x>"}}},
		{"inline code", "`<x>` and `...` <y>", []Match{{1, "<y>"}}},
		{"double backticks", "``a ` <x>`` <y>", []Match{{1, "<y>"}}},
		{"unmatched backtick", "a ` <x>", []Match{{1, "<x>"}}},
		{"comment", "<!-- <x> ... --> <y>", []Match{{1, "<y>"}}},
		{"comment over lines", "<!--\n<x>\n... -->\n<y>", []Match{{4, "<y>"}}},
		{"fence inside comment", "<!--\n```\n-->\n<y>", []Match{{4, "<y>"}}},
		{"empty comment", "<!--> <x>\n<y>", []Match{{1, "<x>"}, {2, "<y>"}}},
		{"empty comment with dash", "<!---> <x>\n<y>", []Match{{1, "<x>"}, {2, "<y>"}}},
		{"comment of dashes", "<!----> <x>", []Match{{1, "<x>"}}},
		{"unclosed comment", "<w>\n<!-- <x>\n<y>", []Match{{1, "<w>"}}},
		{"comment opener in code", "`<!--` <y>", []Match{{1, "<y>"}}},
		{"backtick in comment", "<!-- ` --> <y> `", []Match{{1, "<y>"}}},
		{"slot with code inside", "<cite `file:line` here>", []Match{{1, "<cite `file:line` here>"}}},
		{"slot inside code", "`<x>`", nil},
	})
}

func TestLines(t *testing.T) {
	runCases(t, []findCase{
		{"empty", "", nil},
		{"none", "plain text\nmore", nil},
		{"one-based", "a\n\n<x>", []Match{{3, "<x>"}}},
		{"crlf", "a\r\n<x>\r\n...\r\n", []Match{{2, "<x>"}, {3, "..."}}},
		{"column order", "... <x> … <y>", []Match{{1, "..."}, {1, "<x>"}, {1, "…"}, {1, "<y>"}}},
	})
}

// TestDocumentTemplates runs Find on copies of real document templates and
// checks every placeholder it reports, with its line.
func TestDocumentTemplates(t *testing.T) {
	cases := map[string][]Match{
		"architecture.md": {
			{1, "<short name>"},
			{4, "<ISO date>"},
			{9, "<Three to five sentences: the shape of the solution and why it's the right one.>"},
			{13, "<Current codebase state relevant to this change; cite `file:line` where useful.>"},
			{17, "<decision>"},
			{17, "<rationale>"},
			{17, "<alternatives rejected in brief>"},
			{18, "..."},
			{22, "<Components, responsibilities, and how they connect. The implementer's map.>"},
			{26, "<Step-by-step behavior for the main flows, aligned to the acceptance criteria.>"},
			{30, "<Module boundaries, function/CLI/API shapes, config, env vars.>"},
			{34, "<what the design deliberately leaves out>"},
			{38, "<assumptions, endpoints to confirm, risks with their impact>"},
			{42, "<references, constraints, implementation hints.>"},
		},
		// The slot on lines 9-10 spans two lines, so it isn't reported.
		"functional-spec.md": {
			{1, "<short name of the change>"},
			{4, "<ISO date>"},
			{5, "<file path | issue:N | url, one per line>"},
			{16, "<what this change includes>"},
			{20, "<what it deliberately does not include>"},
			{26, "<capability, verifiable>"},
			{27, "..."},
			{31, "<constraint: performance, security, operability>"},
			{35, "<context>"},
			{35, "<action>"},
			{35, "<observable result>"},
			{36, "..."},
			{40, "<question>"},
			{40, "<resolved answer>"},
			{44, "<anything unconfirmed; resolve before design or mark as accepted risk>"},
			{48, "<assumptions, constraints, references. Keep light.>"},
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			got := Find(string(data))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Find(%s)\n got %d: %#v\nwant %d: %#v", name, len(got), got, len(want), want)
			}
		})
	}
}

// QA: inputs the coder's tables don't cover.
func TestQAEdges(t *testing.T) {
	runCases(t, []findCase{
		{"fence with info string", "```go\n<x>\n```\n<y>", []Match{{4, "<y>"}}},
		{"tab-indented fence", "\t~~~\n<x>\n\t~~~\n<y>", []Match{{4, "<y>"}}},
		{"close bracket inside code inside slot", "<x `>` y>", []Match{{1, "<x `>` y>"}}},
		{"two comments on a line", "<!-- a --> <x> <!-- b --> <y>", []Match{{1, "<x>"}, {1, "<y>"}}},
		{"checkbox criterion with slot", "- [ ] Given <context>, when <action>, then <result>",
			[]Match{{1, "<context>"}, {1, "<action>"}, {1, "<result>"}}},
		{"multibyte text around slots", "é <naïve slot> — …", []Match{{1, "<naïve slot>"}, {1, "…"}}},
		{"unicode space around ellipsis", "a\u00a0…\u00a0b", []Match{{1, "…"}}},
		{"unmatched double run stays literal", "a `` <x> ` <y>", []Match{{1, "<x>"}, {1, "<y>"}}},
		{"longer run does not close a span", "`x`` <y> `", nil},
	})
}

// FuzzFind checks that Find never panics, that line numbers are in range, and
// that each Text is found on its line.
func FuzzFind(f *testing.F) {
	for _, s := range []string{"", "<x> ...", "```\n<x>", "<!--\n<x>", "`` ` ``", "<<>>", "…\r\n<é>", "\xff<x>\xfe..."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		lines := strings.Split(s, "\n")
		for _, m := range Find(s) {
			if m.Line < 1 || m.Line > len(lines) {
				t.Fatalf("line %d out of range for %q", m.Line, s)
			}
			if m.Text == "" || !strings.Contains(lines[m.Line-1], m.Text) {
				t.Fatalf("Text %q not on line %d of %q", m.Text, m.Line, s)
			}
		}
	})
}
