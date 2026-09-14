package links

import (
	"strings"
	"unicode"

	"github.com/gamemaker1/markspace/internal/core"
)

// Entry kinds.
const (
	KindFolder = "folder"
	KindNote   = "note"
	KindFile   = "file"
)

// Entry is one item in a space snapshot used for resolution.
type Entry struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`            // "folder", "note" or "file"
	Path  string `json:"path"`            // entry path, notes end in .md
	Name  string `json:"name,omitempty"`  // base name, derived from Path when empty
	Title string `json:"title,omitempty"` // notes only
}

// Snapshot indexes the entries of one space. It is read-only after
// NewSnapshot and safe for concurrent use.
type Snapshot struct {
	entries     []Entry
	byID        map[string]int
	byPath      map[string]int
	bySuffix    map[string][]int // path after each "/", notes and files
	filesByName map[string][]int
	notesByStem map[string][]int // file name without .md
	notesByName map[string][]int // lowercase title, and lowercase folder name for index.md
}

// NewSnapshot builds a snapshot. It copies entries. When two entries share an
// id or a path, the first one wins.
func NewSnapshot(entries []Entry) *Snapshot {
	s := &Snapshot{
		entries:     make([]Entry, len(entries)),
		byID:        make(map[string]int, len(entries)),
		byPath:      make(map[string]int, len(entries)),
		bySuffix:    map[string][]int{},
		filesByName: map[string][]int{},
		notesByStem: map[string][]int{},
		notesByName: map[string][]int{},
	}
	copy(s.entries, entries)
	for i := range s.entries {
		e := &s.entries[i]
		if e.Name == "" {
			e.Name = baseName(e.Path)
		}
		if _, ok := s.byID[e.ID]; !ok {
			s.byID[e.ID] = i
		}
		if _, ok := s.byPath[e.Path]; ok {
			continue
		}
		s.byPath[e.Path] = i
		if e.Kind == KindFolder {
			continue
		}
		for j := 0; j < len(e.Path); j++ {
			if e.Path[j] == '/' {
				k := e.Path[j+1:]
				s.bySuffix[k] = append(s.bySuffix[k], i)
			}
		}
		switch e.Kind {
		case KindFile:
			s.filesByName[e.Name] = append(s.filesByName[e.Name], i)
		case KindNote:
			stem := strings.TrimSuffix(e.Name, ".md")
			s.notesByStem[stem] = append(s.notesByStem[stem], i)
			if e.Title != "" {
				k := lower(e.Title)
				s.notesByName[k] = append(s.notesByName[k], i)
			}
			if e.Name == "index.md" {
				if d := dirName(e.Path); d != "" {
					k := lower(baseName(d))
					if e.Title == "" || k != lower(e.Title) {
						s.notesByName[k] = append(s.notesByName[k], i)
					}
				}
			}
		}
	}
	return s
}

// Entries returns the entries of the snapshot. Callers must not modify them.
func (s *Snapshot) Entries() []Entry { return s.entries }

// ByID returns the entry with the id, or nil.
func (s *Snapshot) ByID(id string) *Entry {
	if i, ok := s.byID[id]; ok {
		return &s.entries[i]
	}
	return nil
}

// ByPath returns the entry at the entry path, or nil.
func (s *Snapshot) ByPath(p string) *Entry {
	if i, ok := s.byPath[p]; ok {
		return &s.entries[i]
	}
	return nil
}

// Resolve resolves one link from the note at entry path fromPath. It returns
// the matched entry or nil. External links resolve to nil.
func (s *Snapshot) Resolve(fromPath string, l Link) *Entry {
	if l.External {
		return nil
	}
	return s.ResolveTarget(fromPath, l.Target, l.Form)
}

// ResolveTarget resolves a decoded target without heading or label. Markdown
// forms resolve by path relative to the note's folder. For the two wikilink
// forms, a target containing "/" uses the path rule and any other target the
// name rule, whichever of the two forms is passed.
func (s *Snapshot) ResolveTarget(fromPath, target string, form Form) *Entry {
	if s == nil || target == "" {
		return nil
	}
	if !form.IsWikilink() {
		p, ok := cleanPath(dirName(fromPath), target)
		if !ok {
			return nil
		}
		return s.exact(p)
	}
	from := dirName(fromPath)
	if strings.IndexByte(target, '/') >= 0 {
		rooted := strings.HasPrefix(target, "/")
		p := strings.TrimPrefix(target, "/")
		if p == "" {
			return nil
		}
		if e := s.exact(p); e != nil || rooted {
			return e
		}
		c := append(append([]int(nil), s.bySuffix[p]...), s.bySuffix[p+".md"]...)
		return s.pick(from, c)
	}
	if c := s.filesByName[target]; len(c) > 0 {
		return s.pick(from, c)
	}
	if c := s.notesByStem[core.Slugify(target)]; len(c) > 0 {
		return s.pick(from, c)
	}
	return s.pick(from, s.notesByName[lower(target)])
}

// exact matches an entry path as written, then with ".md" appended.
func (s *Snapshot) exact(p string) *Entry {
	if i, ok := s.byPath[p]; ok {
		return &s.entries[i]
	}
	if i, ok := s.byPath[p+".md"]; ok {
		return &s.entries[i]
	}
	return nil
}

// pick applies the tie-break rules: nearest folder by tree distance, which
// puts the note's own folder first, then the shortest entry path in bytes,
// then the smallest entry path in byte order.
func (s *Snapshot) pick(from string, cands []int) *Entry {
	best, bestDist := -1, 0
	for _, i := range cands {
		p := s.entries[i].Path
		d := treeDistance(from, dirName(p))
		if best < 0 {
			best, bestDist = i, d
			continue
		}
		bp := s.entries[best].Path
		if d < bestDist || (d == bestDist && (len(p) < len(bp) || (len(p) == len(bp) && p < bp))) {
			best, bestDist = i, d
		}
	}
	if best < 0 {
		return nil
	}
	return &s.entries[best]
}

// treeDistance returns the number of folder steps between folders a and b.
func treeDistance(a, b string) int {
	as, bs := segments(a), segments(b)
	c := 0
	for c < len(as) && c < len(bs) && as[c] == bs[c] {
		c++
	}
	return len(as) + len(bs) - 2*c
}

// segments splits a folder path. The root has no segments.
func segments(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// cleanPath resolves a markdown path against folder dir. A leading "/" starts
// at the space root. Empty and "." segments are dropped. It returns false when
// ".." climbs above the root or the result is the root.
func cleanPath(dir, target string) (string, bool) {
	var out []string
	if !strings.HasPrefix(target, "/") {
		out = segments(dir)
	}
	for _, seg := range strings.Split(target, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) == 0 {
				return "", false
			}
			out = out[:len(out)-1]
		default:
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, "/"), true
}

// dirName returns the folder part of an entry path, "" for the root.
func dirName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// baseName returns the last segment of an entry path.
func baseName(p string) string {
	return p[strings.LastIndexByte(p, '/')+1:]
}

// lower maps every rune through unicode.ToLower. The browser port maps each
// code point the same way, so both compare titles alike.
func lower(s string) string {
	return strings.Map(unicode.ToLower, s)
}
