// Package placeholder finds template placeholders in markdown text. It knows
// nothing about any particular template: it recognises two slot styles by
// pattern, outside code.
//
// Ellipsis slots are a "…" or "..." that stands alone, with whitespace or the
// line's start or end on both sides. "etc..." and "wait…" are prose, not slots.
//
// Angle-bracket slots are "<…>" on one line, containing at least one letter
// and no "<" or ">" inside. These are not slots:
//   - closing tags ("</…>"), comments and doctypes ("<!…>")
//   - autolinks: anything containing "://" or "@", or starting with "mailto:"
//   - common HTML elements ("<br>", "<details open>", "<a href=…>"), matched by
//     name, case-insensitively, from a fixed list
//
// Nothing inside fenced code blocks, inline code spans or HTML comments is
// reported. Fences and comments may be left unclosed; they then run to the end
// of the document, as markdown renders them.
//
// Known false positives: a bare generic such as <T>, or an HTML element that is
// not on the list, in prose outside code. Write it in backticks. An angle
// bracket slot that spans lines is not reported.
package placeholder

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Match is one placeholder. Line is 1-based; Text is the placeholder as written.
type Match struct {
	Line int
	Text string
}

// Find returns the template placeholders in a markdown document, in order.
func Find(markdown string) []Match {
	var out []Match
	var (
		inFence   bool
		fenceChar byte
		fenceLen  int
		inComment bool
	)
	for i, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if inFence {
			if fenceClose(line, fenceChar, fenceLen) {
				inFence = false
			}
			continue
		}
		if !inComment {
			if c, n, ok := fenceOpen(line); ok {
				inFence, fenceChar, fenceLen = true, c, n
				continue
			}
		}
		var masked []byte
		masked, inComment = mask(line, inComment)
		for _, r := range slots(line, masked) {
			out = append(out, Match{Line: i + 1, Text: line[r[0]:r[1]]})
		}
	}
	return out
}

// fenceOpen reports whether line opens a fenced code block: three or more
// backticks or tildes after any leading whitespace. A backtick fence's info
// string may not contain a backtick.
func fenceOpen(line string) (char byte, n int, ok bool) {
	s := strings.TrimLeft(line, " \t")
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return 0, 0, false
	}
	char = s[0]
	n = run(s, 0, char)
	if n < 3 || (char == '`' && strings.IndexByte(s[n:], '`') >= 0) {
		return 0, 0, false
	}
	return char, n, true
}

// fenceClose reports whether line closes a fence opened with n of char: at
// least n of the same character, then only whitespace.
func fenceClose(line string, char byte, n int) bool {
	s := strings.TrimLeft(line, " \t")
	m := run(s, 0, char)
	return m >= n && strings.TrimSpace(s[m:]) == ""
}

// run counts the bytes equal to c in s from i.
func run(s string, i int, c byte) int {
	j := i
	for j < len(s) && s[j] == c {
		j++
	}
	return j - i
}

// mask returns a copy of line with inline code spans and HTML comments
// replaced by spaces, byte for byte, so offsets still index line. inComment
// says whether the line starts inside a comment; the result says whether it
// ends inside one. Scanning is left to right, and whichever construct starts
// first wins.
func mask(line string, inComment bool) ([]byte, bool) {
	b := []byte(line)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			b[k] = ' '
		}
	}
	i := 0
	for i < len(line) {
		if inComment {
			j := strings.Index(line[i:], "-->")
			if j < 0 {
				blank(i, len(line))
				return b, true
			}
			blank(i, i+j+3)
			i += j + 3
			inComment = false
			continue
		}
		switch {
		case line[i] == '`':
			n := run(line, i, '`')
			if end := closingTicks(line, i+n, n); end >= 0 {
				blank(i, end)
				i = end
			} else {
				i += n // an unmatched run is literal
			}
		case strings.HasPrefix(line[i:], "<!-->"), strings.HasPrefix(line[i:], "<!--->"):
			// Empty comments: the closing "-->" overlaps the opener.
			n := strings.IndexByte(line[i:], '>') + 1
			blank(i, i+n)
			i += n
		case strings.HasPrefix(line[i:], "<!--"):
			blank(i, i+4)
			i += 4
			inComment = true
		default:
			i++
		}
	}
	return b, inComment
}

// closingTicks finds the next run of exactly n backticks in line from i and
// returns the offset just past it, or -1.
func closingTicks(line string, i, n int) int {
	for i < len(line) {
		if line[i] != '`' {
			i++
			continue
		}
		m := run(line, i, '`')
		if m == n {
			return i + m
		}
		i += m
	}
	return -1
}

// slots returns the byte ranges of the placeholders in line, in column order.
// masked is line with code and comments blanked.
func slots(line string, masked []byte) [][2]int {
	angles := angleSlots(masked)
	for _, r := range angles {
		for k := r[0]; k < r[1]; k++ {
			masked[k] = ' ' // an ellipsis inside a slot isn't reported twice
		}
	}
	dots := ellipses(line, masked)
	out := make([][2]int, 0, len(angles)+len(dots))
	for len(angles) > 0 || len(dots) > 0 {
		if len(dots) == 0 || (len(angles) > 0 && angles[0][0] < dots[0][0]) {
			out, angles = append(out, angles[0]), angles[1:]
		} else {
			out, dots = append(out, dots[0]), dots[1:]
		}
	}
	return out
}

// angleSlots returns the ranges of the "<…>" slots in masked.
func angleSlots(masked []byte) [][2]int {
	var out [][2]int
	i := 0
	for i < len(masked) {
		if masked[i] != '<' {
			i++
			continue
		}
		j := i + 1
		for j < len(masked) && masked[j] != '>' && masked[j] != '<' {
			j++
		}
		if j == len(masked) {
			break
		}
		if masked[j] == '<' {
			i = j
			continue
		}
		if isSlot(string(masked[i+1 : j])) {
			out = append(out, [2]int{i, j + 1})
		}
		i = j + 1
	}
	return out
}

// htmlElements are the element names that aren't slots.
var htmlElements = map[string]bool{
	"a": true, "b": true, "br": true, "code": true, "details": true, "div": true,
	"em": true, "hr": true, "i": true, "img": true, "kbd": true, "li": true,
	"ol": true, "p": true, "pre": true, "span": true, "strong": true, "sub": true,
	"summary": true, "sup": true, "table": true, "td": true, "th": true, "tr": true,
	"u": true, "ul": true,
}

// isSlot reports whether the text between "<" and ">" is a placeholder.
func isSlot(inner string) bool {
	if !strings.ContainsFunc(inner, unicode.IsLetter) {
		return false
	}
	if inner[0] == '/' || inner[0] == '!' {
		return false
	}
	lower := strings.ToLower(inner)
	if strings.Contains(inner, "://") || strings.Contains(inner, "@") || strings.HasPrefix(lower, "mailto:") {
		return false
	}
	k := 0
	for k < len(lower) && (lower[k] >= 'a' && lower[k] <= 'z' || lower[k] >= '0' && lower[k] <= '9') {
		k++
	}
	if k > 0 && htmlElements[lower[:k]] {
		rest := inner[k:]
		if rest == "" || rest[0] == '/' || rest[0] == ' ' || rest[0] == '\t' {
			return false
		}
	}
	return true
}

// ellipses returns the ranges of standalone "..." and "…" tokens. Tokens are
// found in masked, but standing alone is judged against the original line, so
// "`x`..." stays prose.
func ellipses(line string, masked []byte) [][2]int {
	var out [][2]int
	m := string(masked)
	i := 0
	for i < len(m) {
		r, size := utf8.DecodeRuneInString(m[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		j := i
		for j < len(m) {
			r, size := utf8.DecodeRuneInString(m[j:])
			if unicode.IsSpace(r) {
				break
			}
			j += size
		}
		if tok := m[i:j]; (tok == "..." || tok == "…") && spaceBefore(line, i) && spaceAfter(line, j) {
			out = append(out, [2]int{i, j})
		}
		i = j
	}
	return out
}

func spaceBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsSpace(r)
}

func spaceAfter(s string, j int) bool {
	if j == len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[j:])
	return unicode.IsSpace(r)
}
