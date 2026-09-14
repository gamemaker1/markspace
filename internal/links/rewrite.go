package links

import (
	"sort"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// Edit is a byte-range replacement in a body.
type Edit struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// Change describes one tree change. A folder change moves its descendants.
type Change struct {
	Kind    string `json:"kind"` // "rename" or "move"
	ID      string `json:"id"`
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

// ApplyChanges returns a copy of entries with the changes applied in order.
// A change finds its entry by ID, or by OldPath when no entry has the ID.
// Descendants of a changed folder get the new path prefix.
func ApplyChanges(entries []Entry, changes []Change) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	byID := make(map[string]int, len(out))
	for i, e := range out {
		if _, ok := byID[e.ID]; !ok {
			byID[e.ID] = i
		}
	}
	for _, ch := range changes {
		i, ok := byID[ch.ID]
		if !ok || ch.ID == "" {
			i = -1
			for j := range out {
				if out[j].Path == ch.OldPath {
					i = j
					break
				}
			}
			if i < 0 {
				continue
			}
		}
		old := out[i].Path
		if old == ch.NewPath || ch.NewPath == "" {
			continue
		}
		for j := range out {
			p := out[j].Path
			if p == old || strings.HasPrefix(p, old+"/") {
				out[j].Path = ch.NewPath + p[len(old):]
				out[j].Name = baseName(out[j].Path)
			}
		}
	}
	return out
}

// Rewriter computes rewrites for one set of changes. Build it once and call
// Rewrite for each affected note.
type Rewriter struct {
	before, after *Snapshot
}

// NewRewriter builds the snapshot after the changes.
func NewRewriter(before *Snapshot, changes []Change) *Rewriter {
	return &Rewriter{before: before, after: NewSnapshot(ApplyChanges(before.entries, changes))}
}

// After returns the snapshot after the changes.
func (r *Rewriter) After() *Snapshot { return r.after }

// Rewrite computes the edits for one note body. See the package function.
func Rewrite(before *Snapshot, changes []Change, noteOldPath, noteNewPath, body string) []Edit {
	return NewRewriter(before, changes).Rewrite(noteOldPath, noteNewPath, body)
}

// Rewrite returns the edits for one note body, sorted by Start and not
// overlapping. A link changes only when it resolved before the changes and
// the text as written no longer resolves to the same entry afterwards.
func (r *Rewriter) Rewrite(noteOldPath, noteNewPath, body string) []Edit {
	var edits []Edit
	for _, l := range Parse(body) {
		if l.External {
			continue
		}
		e0 := r.before.Resolve(noteOldPath, l)
		if e0 == nil {
			continue
		}
		if e1 := r.after.Resolve(noteNewPath, l); e1 != nil && e1.ID == e0.ID {
			continue
		}
		target := r.after.ByID(e0.ID)
		if target == nil {
			continue
		}
		if l.Form.IsWikilink() {
			edits = append(edits, r.wikiEdit(noteNewPath, l, target))
		} else {
			edits = append(edits, r.markdownEdit(noteNewPath, l, target, body))
		}
	}
	sort.SliceStable(edits, func(a, b int) bool { return edits[a].Start < edits[b].Start })
	return edits
}

// markdownEdit rewrites a markdown destination. A rooted link stays rooted
// and a relative link stays relative. ".md" is dropped only when the link was
// written without it and the shorter path still resolves to the target.
func (r *Rewriter) markdownEdit(notePath string, l Link, target *Entry, body string) Edit {
	rooted := strings.HasPrefix(l.Target, "/")
	mk := func(p string) string {
		if rooted {
			return "/" + p
		}
		return relPath(dirName(notePath), p)
	}
	cand := mk(target.Path)
	if !strings.HasSuffix(l.Target, ".md") && target.Kind == KindNote && strings.HasSuffix(target.Path, ".md") {
		short := mk(strings.TrimSuffix(target.Path, ".md"))
		if e := r.after.ResolveTarget(notePath, short, l.Form); e != nil && e.ID == target.ID {
			cand = short
		}
	}
	enc := encodeDest(cand)
	if l.Angle || !needsAngle(cand) {
		return Edit{Start: l.TargetStart, End: l.TargetEnd, Text: enc}
	}
	suffix := body[l.TargetEnd:l.DestEnd]
	suffix = strings.ReplaceAll(strings.ReplaceAll(suffix, "<", "%3C"), ">", "%3E")
	return Edit{Start: l.DestStart, End: l.DestEnd, Text: "<" + enc + suffix + ">"}
}

// wikiEdit rewrites a wikilink target. A name wikilink tries the new name
// first, then a path. A path wikilink stays a path from the root and keeps
// or drops ".md" as written. When no wikilink text resolves to the target,
// the link becomes a markdown link to the relative path.
func (r *Rewriter) wikiEdit(notePath string, l Link, target *Entry) Edit {
	var cands []string
	if l.Form == FormWikilink {
		switch target.Kind {
		case KindFile:
			cands = append(cands, target.Name)
		case KindNote:
			if target.Name == "index.md" {
				if d := dirName(target.Path); d != "" {
					cands = append(cands, baseName(d))
				}
			} else {
				cands = append(cands, strings.TrimSuffix(target.Name, ".md"))
			}
			if target.Title != "" {
				cands = append(cands, target.Title)
			}
		}
	}
	hasMD := l.Form == FormPathWikilink && strings.HasSuffix(l.Target, ".md")
	rooted := l.Form == FormPathWikilink && strings.HasPrefix(l.Target, "/")
	bases := []string{target.Path}
	if !hasMD && target.Kind == KindNote && strings.HasSuffix(target.Path, ".md") {
		bases = []string{strings.TrimSuffix(target.Path, ".md"), target.Path}
	}
	if !rooted {
		for _, b := range bases {
			if strings.IndexByte(b, '/') >= 0 {
				cands = append(cands, b)
			}
		}
	}
	for _, b := range bases {
		cands = append(cands, "/"+b)
	}
	for _, c := range cands {
		if !wikiTargetOK(c) {
			continue
		}
		if e := r.after.ResolveTarget(notePath, c, FormWikilink); e != nil && e.ID == target.ID {
			return Edit{Start: l.TargetStart, End: l.TargetEnd, Text: c}
		}
	}
	dest := encodeDest(relPath(dirName(notePath), target.Path))
	if l.Heading != "" {
		dest += "#" + encodeDest(l.Heading)
	}
	label := l.Label
	if label == "" && !l.Embed {
		label = l.Raw
		if l.Heading != "" {
			label += "#" + l.Heading
		}
	}
	text := "[" + escapeLabel(label) + "](<" + dest + ">)"
	if l.Embed {
		text = "!" + text
	}
	return Edit{Start: l.Start, End: l.End, Text: text}
}

// wikiTargetOK reports whether c can be written as a wikilink target that
// parses back to c without changing the text around it.
func wikiTargetOK(c string) bool {
	if c == "" || c[0] == ' ' || c[len(c)-1] == ' ' {
		return false
	}
	for i := 0; i < len(c); i++ {
		switch b := c[i]; {
		case b < 0x20, b == 0x7f:
			return false
		case strings.IndexByte("|#[]`<>\\", b) >= 0:
			return false
		}
	}
	return true
}

// relPath returns the path from folder dir to entry path p. A path whose
// first segment looks like a URI scheme gets a "./" prefix.
func relPath(dir, p string) string {
	ds, ps := segments(dir), segments(p)
	c := 0
	for c < len(ds) && c < len(ps) && ds[c] == ps[c] {
		c++
	}
	parts := make([]string, 0, len(ds)-c+len(ps)-c)
	for range ds[c:] {
		parts = append(parts, "..")
	}
	parts = append(parts, ps[c:]...)
	out := strings.Join(parts, "/")
	if out == "" {
		return "."
	}
	if schemeLen(out) > 0 {
		return "./" + out
	}
	return out
}

// needsAngle reports whether a destination must be written as <...>.
func needsAngle(p string) bool { return strings.ContainsAny(p, " ()") }

// encodeDest percent-encodes a markdown destination. It encodes control
// bytes, "%" and "\" so decoding restores the path, "#" and "?" so the
// heading split is kept, "<" and ">" for angle destinations, "[", "]" and "`"
// so no earlier bracket or code span can close inside the link, and "|" for
// table cells. Other bytes, including non-ASCII, stay as written.
func encodeDest(p string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == 0x7f || strings.IndexByte("%\\#?<>[]`|", c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// escapeLabel escapes link text for the markdown form of a wikilink.
// Backticks become "&#96;" because a backslash does not stop a backtick from
// closing an earlier code span.
func escapeLabel(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '`':
			b.WriteString("&#96;")
		case strings.IndexByte("\\[]<>|", c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Apply returns body with the edits applied. Edits must be sorted by Start
// and must not overlap, as Rewrite returns them.
func Apply(body string, edits []Edit) string {
	if len(edits) == 0 {
		return body
	}
	var b strings.Builder
	last := 0
	for _, e := range edits {
		b.WriteString(body[last:e.Start])
		b.WriteString(e.Text)
		last = e.End
	}
	b.WriteString(body[last:])
	return b.String()
}

// EntryKeys returns the lookup keys under which links to e are indexed: the
// lowercase name with and without ".md", the lowercase title of a note, and
// the lowercase folder name of a folder note.
func EntryKeys(e Entry) []string {
	name := e.Name
	if name == "" {
		name = baseName(e.Path)
	}
	keys := []string{lower(name), lower(strings.TrimSuffix(name, ".md"))}
	if e.Kind == KindNote {
		if e.Title != "" {
			keys = append(keys, lower(e.Title))
		}
		if name == "index.md" {
			if d := dirName(e.Path); d != "" {
				keys = append(keys, lower(baseName(d)))
			}
		}
	}
	return uniq(keys)
}

// LinkKeys returns the keys of one link in the note at fromPath, for the
// outbound-link index that Affected reads. A wikilink gives its last path
// segment lowercased, with and without ".md", and the slug of that segment.
// A markdown link gives the last segment of its cleaned path. External links
// and paths above the root give no keys.
func LinkKeys(fromPath string, l Link) []string {
	if l.External {
		return nil
	}
	var base string
	if l.Form.IsWikilink() {
		base = baseName(l.Target)
		if base == "" {
			return nil
		}
		return uniq([]string{lower(base), lower(strings.TrimSuffix(base, ".md")), core.Slugify(base)})
	}
	p, ok := cleanPath(dirName(fromPath), l.Target)
	if !ok {
		return nil
	}
	base = baseName(p)
	return uniq([]string{lower(base), lower(strings.TrimSuffix(base, ".md"))})
}

// Affected returns the sorted ids of the notes whose bodies may need Rewrite
// for the changes. notesWithKey returns the ids of the notes that have a link
// with a given LinkKeys key. Callers maintain that index from LinkKeys over
// every note, so the app does not parse every note in the space. The result
// also holds every note whose own path changes.
//
// The keys cover every entry whose path changes, under its names before and
// after the change. A link can only resolve differently when its old target
// or its new target is such an entry, or when the note itself moved.
func Affected(before *Snapshot, changes []Change, notesWithKey func(key string) []string) []string {
	after := ApplyChanges(before.entries, changes)
	ids := map[string]bool{}
	keys := map[string]bool{}
	for i, a := range after {
		b := before.entries[i]
		if a.Path == b.Path {
			continue
		}
		if a.Kind == KindNote {
			ids[a.ID] = true
		}
		for _, k := range EntryKeys(b) {
			keys[k] = true
		}
		for _, k := range EntryKeys(a) {
			keys[k] = true
		}
	}
	if notesWithKey != nil {
		for k := range keys {
			for _, id := range notesWithKey(k) {
				ids[id] = true
			}
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// uniq returns s without repeated values, keeping the first of each.
func uniq(s []string) []string {
	out := make([]string, 0, len(s))
	seen := make(map[string]bool, len(s))
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
