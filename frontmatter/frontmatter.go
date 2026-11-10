// Package frontmatter reads and writes a controlled subset of YAML at the top of
// a markdown file: scalars, inline lists ([a, b]) and one level of nested map.
// Anchors, multiline scalars, block lists and deeper nesting are rejected with a
// clear error. Fields that aren't changed keep their exact bytes, comments
// included, so a tool can edit one field without disturbing a file.
package frontmatter

import (
	"fmt"
	"regexp"
	"strings"
)

const delimiter = "---"

type kind int

const (
	scalar kind = iota
	list
	nested
)

// field is one top-level key. raw holds its source lines until it's changed.
type field struct {
	key    string
	kind   kind
	value  string
	items  []string
	subs   []subfield
	raw    []string // nil once changed
	before []string // blank and comment lines kept ahead of this field
}

type subfield struct {
	key   string
	value string
}

// Document is a parsed file: its fields, in order, and its body.
type Document struct {
	fields   []*field
	trailing []string // blank and comment lines before the closing delimiter
	eol      string
	// Body is everything after the closing delimiter, byte for byte.
	Body string
}

// New returns an empty document with the given body.
func New(body string) *Document { return &Document{eol: "\n", Body: body} }

var keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// Parse splits frontmatter from the body. A file without an opening delimiter is
// an error, so a missing or mangled header can't pass for an empty one.
func Parse(data []byte) (*Document, error) {
	text := string(data)
	eol := "\n"
	if strings.HasPrefix(text, delimiter+"\r\n") {
		eol = "\r\n"
	} else if !strings.HasPrefix(text, delimiter+"\n") {
		return nil, fmt.Errorf("frontmatter: file must start with %q", delimiter)
	}
	rest := text[len(delimiter)+len(eol):]
	var lines []string
	for {
		i := strings.Index(rest, "\n")
		if i < 0 {
			if strings.TrimRight(rest, "\r") == delimiter {
				return parseLines(lines, "", eol)
			}
			return nil, fmt.Errorf("frontmatter: no closing %q", delimiter)
		}
		line := strings.TrimSuffix(rest[:i], "\r")
		rest = rest[i+1:]
		if line == delimiter {
			return parseLines(lines, rest, eol)
		}
		lines = append(lines, line)
	}
}

func isIgnorable(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

func parseLines(lines []string, body, eol string) (*Document, error) {
	d := &Document{eol: eol, Body: body}
	seen := map[string]bool{}
	var pending []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lineNo := i + 2 // the opening delimiter is line 1
		if isIgnorable(line) {
			pending = append(pending, line)
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			return nil, fmt.Errorf("frontmatter: line %d: unexpected indentation", lineNo)
		}
		if strings.HasPrefix(line, "- ") {
			return nil, fmt.Errorf("frontmatter: line %d: block lists aren't supported — use [a, b]", lineNo)
		}
		key, rawValue, err := splitKey(line, lineNo)
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, fmt.Errorf("frontmatter: line %d: duplicate key %q", lineNo, key)
		}
		seen[key] = true
		f := &field{key: key, raw: []string{line}, before: pending}
		pending = nil
		noValue := rawValue == "" || strings.HasPrefix(rawValue, "#")
		if noValue && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") {
			f.kind = nested
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") {
				i++
				sub, err := parseSub(lines[i], i+2)
				if err != nil {
					return nil, err
				}
				for _, existing := range f.subs {
					if existing.key == sub.key {
						return nil, fmt.Errorf("frontmatter: line %d: duplicate key %q", i+2, key+"."+sub.key)
					}
				}
				f.subs = append(f.subs, sub)
				f.raw = append(f.raw, lines[i])
			}
		} else if strings.HasPrefix(rawValue, "[") {
			f.kind = list
			f.items, err = parseList(rawValue, lineNo)
			if err != nil {
				return nil, err
			}
		} else {
			f.value, err = parseScalar(rawValue, lineNo)
			if err != nil {
				return nil, err
			}
		}
		d.fields = append(d.fields, f)
	}
	d.trailing = pending
	return d, nil
}

func splitKey(line string, lineNo int) (key, value string, err error) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", fmt.Errorf("frontmatter: line %d: expected \"key: value\"", lineNo)
	}
	key = line[:i]
	if !keyRe.MatchString(key) {
		return "", "", fmt.Errorf("frontmatter: line %d: invalid key %q", lineNo, key)
	}
	if rest := line[i+1:]; rest != "" && rest[0] != ' ' {
		return "", "", fmt.Errorf("frontmatter: line %d: expected a space after %q", lineNo, key+":")
	}
	return key, strings.TrimSpace(line[i+1:]), nil
}

func parseSub(line string, lineNo int) (subfield, error) {
	inner := line[2:]
	if strings.HasPrefix(inner, " ") || strings.HasPrefix(inner, "\t") {
		return subfield{}, fmt.Errorf("frontmatter: line %d: only one level of nesting is supported", lineNo)
	}
	if strings.HasPrefix(inner, "- ") {
		return subfield{}, fmt.Errorf("frontmatter: line %d: block lists aren't supported — use [a, b]", lineNo)
	}
	key, raw, err := splitKey(inner, lineNo)
	if err != nil {
		return subfield{}, err
	}
	if raw == "" {
		return subfield{}, fmt.Errorf("frontmatter: line %d: only one level of nesting is supported", lineNo)
	}
	if strings.HasPrefix(raw, "[") {
		return subfield{}, fmt.Errorf("frontmatter: line %d: a nested value must be a scalar", lineNo)
	}
	value, err := parseScalar(raw, lineNo)
	if err != nil {
		return subfield{}, err
	}
	return subfield{key, value}, nil
}

// parseScalar reads one value: double-quoted with \" and \\ escapes, single-quoted
// with ” for a quote, or plain up to a " #" comment.
func parseScalar(raw string, lineNo int) (string, error) {
	if raw == "" {
		return "", nil
	}
	switch raw[0] {
	case '"', '\'':
		value, rest, err := readQuoted(raw)
		if err != nil {
			return "", fmt.Errorf("frontmatter: line %d: %v", lineNo, err)
		}
		if rest = strings.TrimSpace(rest); rest != "" && !strings.HasPrefix(rest, "#") {
			return "", fmt.Errorf("frontmatter: line %d: unexpected text after a quoted value", lineNo)
		}
		return value, nil
	case '&', '*':
		return "", fmt.Errorf("frontmatter: line %d: anchors and aliases aren't supported", lineNo)
	case '|', '>':
		return "", fmt.Errorf("frontmatter: line %d: multiline values aren't supported", lineNo)
	case '{':
		return "", fmt.Errorf("frontmatter: line %d: inline maps aren't supported", lineNo)
	case '#':
		return "", nil
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw), nil
}

func readQuoted(raw string) (value, rest string, err error) {
	quote := raw[0]
	var b strings.Builder
	for i := 1; i < len(raw); i++ {
		c := raw[i]
		switch {
		case quote == '"' && c == '\\':
			i++
			if i >= len(raw) || (raw[i] != '"' && raw[i] != '\\') {
				return "", "", fmt.Errorf(`only \" and \\ escapes are supported`)
			}
			b.WriteByte(raw[i])
		case c == quote:
			if quote == '\'' && i+1 < len(raw) && raw[i+1] == '\'' {
				i++
				b.WriteByte('\'')
				continue
			}
			return b.String(), raw[i+1:], nil
		default:
			b.WriteByte(c)
		}
	}
	return "", "", fmt.Errorf("unterminated quote")
}

func parseList(raw string, lineNo int) ([]string, error) {
	items := []string{}
	s := raw[1:]
	for {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			return nil, fmt.Errorf("frontmatter: line %d: unterminated list", lineNo)
		}
		if s[0] == ']' {
			if len(items) > 0 {
				return nil, fmt.Errorf("frontmatter: line %d: trailing comma in list", lineNo)
			}
			return items, checkAfterList(s[1:], lineNo)
		}
		var item string
		if s[0] == '"' || s[0] == '\'' {
			value, rest, err := readQuoted(s)
			if err != nil {
				return nil, fmt.Errorf("frontmatter: line %d: %v", lineNo, err)
			}
			item, s = value, strings.TrimLeft(rest, " ")
		} else {
			end := strings.IndexAny(s, ",]")
			if end < 0 {
				return nil, fmt.Errorf("frontmatter: line %d: unterminated list", lineNo)
			}
			item = strings.TrimSpace(s[:end])
			if item == "" {
				return nil, fmt.Errorf("frontmatter: line %d: empty list item", lineNo)
			}
			if strings.ContainsAny(item[:1], "[{&*|>") {
				return nil, fmt.Errorf("frontmatter: line %d: list items must be scalars", lineNo)
			}
			s = s[end:]
		}
		items = append(items, item)
		if s == "" {
			return nil, fmt.Errorf("frontmatter: line %d: unterminated list", lineNo)
		}
		switch s[0] {
		case ',':
			s = s[1:]
			if strings.TrimLeft(s, " ") != "" && strings.TrimLeft(s, " ")[0] == ']' {
				return nil, fmt.Errorf("frontmatter: line %d: trailing comma in list", lineNo)
			}
		case ']':
			return items, checkAfterList(s[1:], lineNo)
		default:
			return nil, fmt.Errorf("frontmatter: line %d: expected ',' or ']' in list", lineNo)
		}
	}
}

func checkAfterList(rest string, lineNo int) error {
	if rest = strings.TrimSpace(rest); rest != "" && !strings.HasPrefix(rest, "#") {
		return fmt.Errorf("frontmatter: line %d: unexpected text after a list", lineNo)
	}
	return nil
}

func (d *Document) find(key string) *field {
	for _, f := range d.fields {
		if f.key == key {
			return f
		}
	}
	return nil
}

// Has reports whether a key is present.
func (d *Document) Has(key string) bool { return d.find(key) != nil }

// Keys lists the top-level keys in order.
func (d *Document) Keys() []string {
	keys := make([]string, 0, len(d.fields))
	for _, f := range d.fields {
		keys = append(keys, f.key)
	}
	return keys
}

// Get returns a scalar's value ("" when absent or not a scalar).
func (d *Document) Get(key string) string {
	if f := d.find(key); f != nil && f.kind == scalar {
		return f.value
	}
	return ""
}

// List returns a list's items (nil when absent or not a list).
func (d *Document) List(key string) []string {
	if f := d.find(key); f != nil && f.kind == list {
		return append([]string{}, f.items...)
	}
	return nil
}

// Map returns a nested map's values (nil when absent or not a map).
func (d *Document) Map(key string) map[string]string {
	f := d.find(key)
	if f == nil || f.kind != nested {
		return nil
	}
	out := make(map[string]string, len(f.subs))
	for _, s := range f.subs {
		out[s.key] = s.value
	}
	return out
}

// Kind is the shape of a top-level field's value.
type Kind int

const (
	// Scalar is a single value: key: value.
	Scalar Kind = iota
	// List is an inline list: key: [a, b].
	List
	// Map is one level of nested keys, indented under the key.
	Map
)

// Field is a read-only view of one top-level field. Quotes, escapes and
// comments are already removed.
type Field struct {
	Key   string
	Kind  Kind
	Value string            // Scalar
	Items []string          // List; a copy
	Subs  map[string]string // Map; a copy
}

// Fields returns the top-level fields in file order. Changing the result
// doesn't change the document.
func (d *Document) Fields() []Field {
	out := make([]Field, 0, len(d.fields))
	for _, f := range d.fields {
		field := Field{Key: f.key}
		switch f.kind {
		case scalar:
			field.Kind, field.Value = Scalar, f.value
		case list:
			field.Kind, field.Items = List, append([]string{}, f.items...)
		case nested:
			field.Kind, field.Subs = Map, make(map[string]string, len(f.subs))
			for _, s := range f.subs {
				field.Subs[s.key] = s.value
			}
		}
		out = append(out, field)
	}
	return out
}

// slot returns the field for a key, ready to overwrite, adding it at the end if
// it's new.
func (d *Document) slot(key string) (*field, error) {
	if !keyRe.MatchString(key) {
		return nil, fmt.Errorf("frontmatter: invalid key %q", key)
	}
	f := d.find(key)
	if f == nil {
		f = &field{key: key}
		d.fields = append(d.fields, f)
	}
	f.raw = nil
	f.value, f.items, f.subs = "", nil, nil
	return f, nil
}

func checkValue(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("frontmatter: values can't span lines: %q", value)
	}
	return nil
}

// Set writes a scalar, replacing any existing value.
func (d *Document) Set(key, value string) error {
	if err := checkValue(value); err != nil {
		return err
	}
	f, err := d.slot(key)
	if err != nil {
		return err
	}
	f.kind, f.value = scalar, value
	return nil
}

// SetList writes an inline list, replacing any existing value.
func (d *Document) SetList(key string, items []string) error {
	for _, item := range items {
		if err := checkValue(item); err != nil {
			return err
		}
	}
	f, err := d.slot(key)
	if err != nil {
		return err
	}
	f.kind, f.items = list, append([]string{}, items...)
	return nil
}

// SetSub writes one key of a nested map, creating the map if needed and keeping
// its other keys.
func (d *Document) SetSub(key, sub, value string) error {
	if !keyRe.MatchString(sub) {
		return fmt.Errorf("frontmatter: invalid key %q", sub)
	}
	if value == "" {
		return fmt.Errorf("frontmatter: %s.%s can't be empty", key, sub)
	}
	if err := checkValue(value); err != nil {
		return err
	}
	var subs []subfield
	if existing := d.find(key); existing != nil && existing.kind == nested {
		subs = existing.subs
	}
	f, err := d.slot(key)
	if err != nil {
		return err
	}
	f.kind = nested
	f.subs = subs
	for i := range f.subs {
		if f.subs[i].key == sub {
			f.subs[i].value = value
			return nil
		}
	}
	f.subs = append(f.subs, subfield{sub, value})
	return nil
}

// Delete removes a key.
func (d *Document) Delete(key string) {
	for i, f := range d.fields {
		if f.key == key {
			d.fields = append(d.fields[:i], d.fields[i+1:]...)
			return
		}
	}
}

var plainRe = regexp.MustCompile(`^[A-Za-z0-9_./@+-][A-Za-z0-9_./@+ -]*$`)

func quote(value string) string {
	if plainRe.MatchString(value) && !strings.HasSuffix(value, " ") {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func (f *field) lines() []string {
	if f.raw != nil {
		return f.raw
	}
	switch f.kind {
	case list:
		items := make([]string, len(f.items))
		for i, item := range f.items {
			items[i] = quote(item)
		}
		return []string{f.key + ": [" + strings.Join(items, ", ") + "]"}
	case nested:
		lines := []string{f.key + ":"}
		for _, s := range f.subs {
			lines = append(lines, "  "+s.key+": "+quote(s.value))
		}
		return lines
	}
	if f.value == "" {
		return []string{f.key + ":"}
	}
	return []string{f.key + ": " + quote(f.value)}
}

// Bytes renders the document. A parsed document with no changes comes back
// identical, apart from a file with no newline after its closing delimiter, which
// gains one.
func (d *Document) Bytes() []byte {
	var b strings.Builder
	eol := d.eol
	if eol == "" {
		eol = "\n"
	}
	write := func(line string) {
		b.WriteString(line)
		b.WriteString(eol)
	}
	write(delimiter)
	for _, f := range d.fields {
		for _, line := range f.before {
			write(line)
		}
		for _, line := range f.lines() {
			write(line)
		}
	}
	for _, line := range d.trailing {
		write(line)
	}
	write(delimiter)
	b.WriteString(d.Body)
	return []byte(b.String())
}
