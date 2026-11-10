// Package ticket is the contract between planning and execution: one markdown
// file per ticket, with a schema both tools validate identically. Local until
// published; then the PM tool owns it, and the file is a frozen snapshot. The
// freeze covers the body and the frontmatter (see ContentHash).
package ticket

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/btdstudio/kiso/frontmatter"
)

// Schema is the ticket format version.
const Schema = 1

// Types, from the highest level down.
const (
	Epic    = "epic"
	Feature = "feature"
	Story   = "story"
	Task    = "task"
)

// Statuses.
const (
	StatusDraft     = "draft"
	StatusReady     = "ready"
	StatusPublished = "published"
	StatusClosed    = "closed" // native tomo tickets only
)

// NativePrefix marks a ticket created in a tomo workspace rather than planned in
// zumen, so its id can't collide with an imported one.
const NativePrefix = "N"

// Prefixes maps a type to its planned-ticket id prefix.
var Prefixes = map[string]string{Epic: "E", Feature: "F", Story: "S", Task: "T"}

var rank = map[string]int{Epic: 4, Feature: 3, Story: 2, Task: 1}

// Origin says where an imported ticket came from.
type Origin struct {
	Workspace string
	Path      string
}

// Ticket is one parsed ticket. Change it through its methods, so Bytes keeps every
// field that didn't change exactly as it was.
type Ticket struct {
	Schema        int
	ID            string
	Type          string
	Title         string
	Parent        string
	Repo          string
	DependsOn     []string
	Supersedes    string
	Change        string
	Spec          string
	ADRs          []string
	Labels        []string
	Estimate      string
	Status        string
	External      map[string]string
	PublishedHash string
	Origin        Origin
	Body          string
	Path          string // where it was loaded from, if anywhere

	doc *frontmatter.Document
}

// Parse reads a ticket. It checks only that the file is readable; Validate checks
// the schema.
func Parse(data []byte) (*Ticket, error) {
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return nil, err
	}
	t := &Ticket{
		ID:            doc.Get("id"),
		Type:          doc.Get("type"),
		Title:         doc.Get("title"),
		Parent:        doc.Get("parent"),
		Repo:          doc.Get("repo"),
		DependsOn:     doc.List("depends_on"),
		Supersedes:    doc.Get("supersedes"),
		Change:        doc.Get("change"),
		Spec:          doc.Get("spec"),
		ADRs:          doc.List("adrs"),
		Labels:        doc.List("labels"),
		Estimate:      doc.Get("estimate"),
		Status:        doc.Get("status"),
		External:      doc.Map("external"),
		PublishedHash: doc.Get("published_hash"),
		Body:          doc.Body,
		doc:           doc,
	}
	if raw := doc.Get("schema"); raw != "" {
		if t.Schema, err = strconv.Atoi(raw); err != nil {
			return nil, fmt.Errorf("ticket: invalid schema %q", raw)
		}
	}
	if origin := doc.Map("origin"); origin != nil {
		t.Origin = Origin{Workspace: origin["workspace"], Path: origin["path"]}
	}
	return t, nil
}

// New builds a draft ticket from the fields set on t, in schema order.
func New(t Ticket) (*Ticket, error) {
	doc := frontmatter.New(t.Body)
	scalars := []struct{ key, value string }{
		{"schema", strconv.Itoa(Schema)}, {"id", t.ID}, {"type", t.Type}, {"title", t.Title},
		{"parent", t.Parent}, {"repo", t.Repo},
	}
	for _, s := range scalars {
		if s.value != "" {
			if err := doc.Set(s.key, s.value); err != nil {
				return nil, err
			}
		}
	}
	if err := doc.SetList("depends_on", t.DependsOn); err != nil {
		return nil, err
	}
	if t.Supersedes != "" {
		if err := doc.Set("supersedes", t.Supersedes); err != nil {
			return nil, err
		}
	}
	for _, s := range []struct{ key, value string }{{"change", t.Change}, {"spec", t.Spec}} {
		if s.value != "" {
			if err := doc.Set(s.key, s.value); err != nil {
				return nil, err
			}
		}
	}
	if err := doc.SetList("adrs", t.ADRs); err != nil {
		return nil, err
	}
	if err := doc.SetList("labels", t.Labels); err != nil {
		return nil, err
	}
	if t.Estimate != "" {
		if err := doc.Set("estimate", t.Estimate); err != nil {
			return nil, err
		}
	}
	status := t.Status
	if status == "" {
		status = StatusDraft
	}
	if err := doc.Set("status", status); err != nil {
		return nil, err
	}
	return Parse(doc.Bytes())
}

// Bytes renders the ticket.
func (t *Ticket) Bytes() []byte { return t.doc.Bytes() }

// Hash is the sha256 of a ticket body, with line endings normalized to LF and
// exactly one trailing newline, so an editor or OS can't cause false drift. A
// published_hash without the "v2:" prefix is this legacy body-only hash.
func Hash(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	normalized = strings.TrimRight(normalized, "\n") + "\n"
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// v2Prefix marks a ContentHash value.
const v2Prefix = "v2:"

// excludedKeys are the frontmatter keys written after a freeze, so they don't
// count toward it. Every other key does, including keys the schema doesn't know.
var excludedKeys = map[string]bool{
	"external": true, "published_hash": true, "ready_hash": true, "status": true, "origin": true,
}

// ContentHash is the canonical hash of a ticket: "v2:<body>.<frontmatter>", each
// half the first 32 hex characters of a sha256. The body is normalized as Hash
// does. The frontmatter is every key except excludedKeys, sorted, one
// key=<JSON value> per line, with list items sorted and empty values left out,
// so formatting, order and absent optional fields don't change it.
func ContentHash(t *Ticket) string {
	sum := sha256.Sum256([]byte(canonicalFrontmatter(t.doc)))
	return v2Prefix + Hash(t.Body)[:32] + "." + hex.EncodeToString(sum[:])[:32]
}

func canonicalFrontmatter(doc *frontmatter.Document) string {
	fields := doc.Fields()
	// Sort by key, not by rendered line: "a-b=" sorts before "a=", but "a"
	// sorts before "a-b".
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	var b strings.Builder
	for _, f := range fields {
		if excludedKeys[f.Key] {
			continue
		}
		var value any
		switch f.Kind {
		case frontmatter.Scalar:
			if f.Value == "" {
				continue
			}
			value = f.Value
		case frontmatter.List:
			if len(f.Items) == 0 {
				continue
			}
			sort.Strings(f.Items)
			value = f.Items
		case frontmatter.Map:
			for k, v := range f.Subs {
				if v == "" {
					delete(f.Subs, k)
				}
			}
			if len(f.Subs) == 0 {
				continue
			}
			value = f.Subs
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			panic(err) // strings, string slices and string maps always encode
		}
		b.WriteString(f.Key + "=" + string(encoded) + "\n")
	}
	return b.String()
}

// invalidUTF8Fields lists the frontmatter fields whose values aren't valid
// UTF-8. ContentHash can't tell such values apart (JSON replaces each invalid
// byte with U+FFFD), so ValidateFile refuses them.
func invalidUTF8Fields(doc *frontmatter.Document) []string {
	var keys []string
	for _, f := range doc.Fields() {
		valid := utf8.ValidString(f.Value)
		for _, item := range f.Items {
			valid = valid && utf8.ValidString(item)
		}
		for _, v := range f.Subs { // keys are ASCII: the parser checks them
			valid = valid && utf8.ValidString(v)
		}
		if !valid {
			keys = append(keys, f.Key)
		}
	}
	return keys
}

// parseV2 splits a "v2:<32 hex>.<32 hex>" value into its halves.
func parseV2(v string) (body, fm string, ok bool) {
	rest, found := strings.CutPrefix(v, v2Prefix)
	if !found {
		return "", "", false
	}
	body, fm, found = strings.Cut(rest, ".")
	if !found || !isHex32(body) || !isHex32(fm) {
		return "", "", false
	}
	return body, fm, true
}

func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

// Frozen reports whether the ticket is frozen: published and not unfrozen.
func (t *Ticket) Frozen() bool { return t.PublishedHash != "" }

// Drift reports which parts of a frozen ticket changed since it was published.
// A legacy published_hash (no "v2:" prefix) covers the body only, so its
// frontmatter never drifts. A malformed "v2:" value drifts on both sides,
// because neither can be verified.
func (t *Ticket) Drift() (body, frontmatter bool) {
	if !t.Frozen() {
		return false, false
	}
	if !strings.HasPrefix(t.PublishedHash, v2Prefix) {
		return Hash(t.Body) != t.PublishedHash, false
	}
	b, f, ok := parseV2(t.PublishedHash)
	if !ok {
		return true, true
	}
	nb, nf, _ := parseV2(ContentHash(t))
	return nb != b, nf != f
}

// Drifted reports whether a frozen ticket no longer matches what was published,
// in its body or its frontmatter.
func (t *Ticket) Drifted() bool {
	body, fm := t.Drift()
	return body || fm
}

// malformedFreeze reports a "v2:" published_hash that isn't a valid v2 hash.
func (t *Ticket) malformedFreeze() bool {
	if !strings.HasPrefix(t.PublishedHash, v2Prefix) {
		return false
	}
	_, _, ok := parseV2(t.PublishedHash)
	return !ok
}

// driftMessage says what drifted: "body changed", "frontmatter changed" or both.
func driftMessage(body, fm bool) string {
	switch {
	case body && fm:
		return "body and frontmatter changed"
	case fm:
		return "frontmatter changed"
	default:
		return "body changed"
	}
}

// MarkPublished records a PM tool's item id, freezes the ticket with ContentHash
// and sets the status. It refuses a drifted ticket, and a different id for a
// tool on a frozen ticket. A legacy freeze is upgraded to "v2:".
func (t *Ticket) MarkPublished(tool, itemID string) error {
	if tool == "" || itemID == "" {
		return fmt.Errorf("ticket %s: a tool and an item id are required", t.ID)
	}
	if t.malformedFreeze() {
		return fmt.Errorf("ticket %s: published_hash %q isn't a valid v2 hash — unfreeze it to re-freeze", t.ID, t.PublishedHash)
	}
	if body, fm := t.Drift(); body || fm {
		return fmt.Errorf("ticket %s: %s since it was published — unfreeze it or write a superseding ticket", t.ID, driftMessage(body, fm))
	}
	if existing := t.External[tool]; t.Frozen() && existing != "" && existing != itemID {
		return fmt.Errorf("ticket %s: frozen with %s id %s, can't change it to %s", t.ID, tool, existing, itemID)
	}
	if err := t.doc.SetSub("external", tool, itemID); err != nil {
		return err
	}
	hash := ContentHash(t)
	if err := t.doc.Set("published_hash", hash); err != nil {
		return err
	}
	if err := t.doc.Set("status", StatusPublished); err != nil {
		return err
	}
	t.External = t.doc.Map("external")
	t.PublishedHash = hash
	t.Status = StatusPublished
	return nil
}

// Unfreeze clears the freeze but keeps the external ids, so the next sync updates
// the existing item instead of creating another.
func (t *Ticket) Unfreeze() error {
	if !t.Frozen() {
		return fmt.Errorf("ticket %s: isn't frozen", t.ID)
	}
	t.doc.Delete("published_hash")
	t.PublishedHash = ""
	return nil
}

// SetOrigin records where an imported ticket came from.
func (t *Ticket) SetOrigin(o Origin) error {
	if err := t.doc.SetSub("origin", "workspace", o.Workspace); err != nil {
		return err
	}
	if err := t.doc.SetSub("origin", "path", o.Path); err != nil {
		return err
	}
	t.Origin = o
	return nil
}

// Problem is one validation failure.
type Problem struct {
	ID      string // the ticket, or its path if the id isn't usable
	Message string
}

func (p Problem) String() string { return p.ID + ": " + p.Message }

var idRe = regexp.MustCompile(`^([EFSTN])-(\d{4})$`)

var acceptanceRe = regexp.MustCompile(`(?im)^##\s+Acceptance criteria\s*$`)
var checkboxRe = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+\S`)
var headingRe = regexp.MustCompile(`(?m)^##\s`)

// HasAcceptanceCriteria reports whether the body lists at least one criterion
// under its "Acceptance criteria" heading.
func HasAcceptanceCriteria(body string) bool {
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

func sequence(id string) (int, bool) {
	m := idRe.FindStringSubmatch(id)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[2])
	return n, true
}

// ValidateFile checks one ticket on its own. References to other tickets aren't
// resolved, because a ticket copied into a tomo workspace no longer has its
// neighbors.
func ValidateFile(t *Ticket) []Problem {
	var problems []Problem
	label := t.ID
	if label == "" {
		label = t.Path
	}
	add := func(format string, args ...any) {
		problems = append(problems, Problem{label, fmt.Sprintf(format, args...)})
	}
	if t.Schema != Schema {
		add("schema must be %d, got %d", Schema, t.Schema)
	}
	if t.Title == "" {
		add("title is required")
	}
	if _, known := rank[t.Type]; !known {
		add("unknown type %q", t.Type)
	}
	m := idRe.FindStringSubmatch(t.ID)
	if m == nil {
		add("id %q must look like S-0001", t.ID)
	} else if m[1] == NativePrefix {
		if t.Type != Task {
			add("a native %s- ticket must be a task", NativePrefix)
		}
		if t.Origin != (Origin{}) {
			add("a native %s- ticket can't have an origin", NativePrefix)
		}
	} else if want, known := Prefixes[t.Type]; known && m[1] != want {
		add("id prefix %s doesn't match type %s (expected %s)", m[1], t.Type, want)
	}
	if t.Path != "" && t.ID != "" && strings.TrimSuffix(filepath.Base(t.Path), ".md") != t.ID {
		add("file name %s doesn't match id", filepath.Base(t.Path))
	}
	if (t.Type == Story || t.Type == Task) && t.Repo == "" {
		add("repo is required for a %s", t.Type)
	}
	switch t.Status {
	case StatusDraft:
	case StatusReady:
		if !HasAcceptanceCriteria(t.Body) {
			add("status ready requires at least one acceptance criterion")
		}
	case StatusPublished:
		if len(t.External) == 0 {
			add("status published requires an external id")
		}
	case StatusClosed:
		if m != nil && m[1] != NativePrefix {
			add("only a native %s- ticket can be closed; the PM tool owns a published ticket's status", NativePrefix)
		}
	default:
		add("unknown status %q", t.Status)
	}
	switch t.Estimate {
	case "", "S", "M", "L":
	default:
		add("estimate must be S, M or L, got %q", t.Estimate)
	}
	if t.PublishedHash != "" && len(t.External) == 0 {
		add("published_hash requires an external id")
	}
	for _, key := range invalidUTF8Fields(t.doc) {
		add("frontmatter field %s isn't valid UTF-8", key)
	}
	if t.malformedFreeze() {
		add("published_hash %q isn't a valid v2 hash", t.PublishedHash)
	} else if body, fm := t.Drift(); body || fm {
		add("drifted: %s since it was published", driftMessage(body, fm))
	}
	return problems
}

// ValidateWorkspace runs ValidateFile on every ticket, then checks references
// between them. A repo must be in repos.
func ValidateWorkspace(tickets []*Ticket, repos []string) []Problem {
	var problems []Problem
	byID := map[string]*Ticket{}
	registered := map[string]bool{}
	for _, r := range repos {
		registered[r] = true
	}
	for _, t := range sorted(tickets) {
		problems = append(problems, ValidateFile(t)...)
		if _, dup := byID[t.ID]; dup && t.ID != "" {
			problems = append(problems, Problem{t.ID, "duplicate id"})
		}
		byID[t.ID] = t
	}
	for _, t := range sorted(tickets) {
		add := func(format string, args ...any) {
			problems = append(problems, Problem{t.ID, fmt.Sprintf(format, args...)})
		}
		if t.Parent != "" {
			parent, ok := byID[t.Parent]
			if !ok {
				add("parent %s doesn't exist", t.Parent)
			} else if rank[parent.Type] <= rank[t.Type] {
				add("parent %s is a %s, which isn't higher than a %s", t.Parent, parent.Type, t.Type)
			}
		}
		for _, dep := range t.DependsOn {
			if _, ok := byID[dep]; !ok {
				add("depends_on %s doesn't exist", dep)
			}
		}
		if t.Supersedes != "" {
			if _, ok := byID[t.Supersedes]; !ok {
				add("supersedes %s doesn't exist", t.Supersedes)
			}
		}
		if t.Repo != "" && !registered[t.Repo] {
			add("repo %q isn't registered", t.Repo)
		}
	}
	if _, err := Order(tickets); err != nil {
		problems = append(problems, Problem{"tickets", err.Error()})
	}
	return problems
}

func sorted(tickets []*Ticket) []*Ticket {
	out := append([]*Ticket{}, tickets...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Order returns the tickets in publish order: parents, predecessors and superseded
// tickets before the tickets that refer to them, because a link needs the other
// item's external id. Ties sort by id. A cycle is an error naming its tickets.
// References to tickets that aren't in the list are ignored; ValidateWorkspace
// reports those.
func Order(tickets []*Ticket) ([]*Ticket, error) {
	list := sorted(tickets)
	byID := map[string]*Ticket{}
	for _, t := range list {
		byID[t.ID] = t
	}
	waiting := map[string]int{}
	dependents := map[string][]string{}
	for _, t := range list {
		refs := append([]string{}, t.DependsOn...)
		refs = append(refs, t.Parent, t.Supersedes)
		seen := map[string]bool{}
		for _, ref := range refs {
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			if _, ok := byID[ref]; ok {
				waiting[t.ID]++
				dependents[ref] = append(dependents[ref], t.ID)
			}
		}
	}
	var ready []string
	for _, t := range list {
		if waiting[t.ID] == 0 {
			ready = append(ready, t.ID)
		}
	}
	placed := map[string]bool{}
	var out []*Ticket
	for len(ready) > 0 {
		sort.Strings(ready)
		id := ready[0]
		ready = ready[1:]
		if placed[id] {
			continue
		}
		placed[id] = true
		out = append(out, byID[id])
		for _, next := range dependents[id] {
			waiting[next]--
			if waiting[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	if len(placed) < len(byID) {
		var stuck []string
		for id := range byID {
			if !placed[id] {
				stuck = append(stuck, id)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("dependency cycle among %s", strings.Join(stuck, ", "))
	}
	return out, nil
}

// NextID returns the next id for a type. Ids share one workspace-wide sequence
// across all types.
func NextID(tickets []*Ticket, typ string) (string, error) {
	prefix, ok := Prefixes[typ]
	if !ok {
		return "", fmt.Errorf("ticket: unknown type %q", typ)
	}
	return fmt.Sprintf("%s-%04d", prefix, nextSequence(tickets)), nil
}

// NextNativeID returns the next id for a native tomo ticket.
func NextNativeID(tickets []*Ticket) string {
	return fmt.Sprintf("%s-%04d", NativePrefix, nextSequence(tickets))
}

func nextSequence(tickets []*Ticket) int {
	max := 0
	for _, t := range tickets {
		if n, ok := sequence(t.ID); ok && n > max {
			max = n
		}
	}
	return max + 1
}

// LoadDir reads every .md file in a directory as a ticket. A file that can't be
// parsed is reported rather than skipped, because a ticket left out would make
// references to it look broken. A missing directory has no tickets.
func LoadDir(dir string) ([]*Ticket, []Problem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var tickets []*Ticket
	var problems []Problem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		t, err := Parse(data)
		if err != nil {
			problems = append(problems, Problem{path, err.Error()})
			continue
		}
		t.Path = path
		tickets = append(tickets, t)
	}
	return tickets, problems, nil
}
