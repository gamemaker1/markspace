package links

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// parseSeeds are seed bodies for FuzzParse, added to the parse vectors.
var parseSeeds = []string{
	"",
	"[[",
	"]]",
	"![[x]]",
	"[a](b",
	"[a](<b)",
	"[[a|]] [[|b]] [[#h]] [[ ]]",
	"[![[x.png]]](y) [[[x]]](y)",
	"`a [x](b`c) d`",
	"<!-- [[a]] --> <!--> [[b]]",
	"> ```\n> [[a]]\n```\n[[b]]",
	"- a\n\n      [[code]]\n\n  [[para]]",
	"[a\nb](c\n\"t\")",
	"[x](a\\)b) [y](a(b)c) [z](a(b)",
	"\xff[[\xfe]] [\xed\xa0\x80](\xc3)",
	"[[a]]\r\n[b](c)\r\n",
	strings.Repeat("[", 2000) + strings.Repeat("`", 50) + strings.Repeat("<!--", 50),
}

// FuzzParse checks that Parse never panics, and that links are ordered, lie
// inside the body, cover their raw target text, and are either disjoint or an
// image or embed nested in the text of an enclosing markdown link.
func FuzzParse(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s)
	}
	for _, c := range loadVectors(f).Parse {
		f.Add(c.Body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		ls := Parse(body)
		checkLinks(t, body, ls)
		if again := Parse(body); !reflect.DeepEqual(ls, again) {
			t.Fatalf("Parse is not deterministic")
		}
	})
}

func checkLinks(t *testing.T, body string, ls []Link) {
	t.Helper()
	for i, l := range ls {
		if l.Start < 0 || l.End > len(body) || l.Start >= l.End {
			t.Fatalf("link %d offsets %d:%d outside body of %d bytes", i, l.Start, l.End, len(body))
		}
		if l.DestStart < l.Start || l.DestStart > l.TargetStart || l.TargetStart > l.TargetEnd || l.TargetEnd > l.DestEnd || l.DestEnd > l.End {
			t.Fatalf("link %d ranges out of order: %+v", i, l)
		}
		if got := body[l.TargetStart:l.TargetEnd]; got != l.Raw {
			t.Fatalf("link %d raw %q, body has %q", i, l.Raw, got)
		}
		if i > 0 && ls[i-1].Start >= l.Start {
			t.Fatalf("link %d starts at %d, not after %d", i, l.Start, ls[i-1].Start)
		}
		for j := 0; j < i; j++ {
			o := ls[j]
			if l.Start >= o.End {
				continue
			}
			nested := !o.Form.IsWikilink() && l.End <= o.DestStart && (l.Form == FormMarkdownImage || l.Embed)
			if !nested {
				t.Fatalf("link %d %+v overlaps link %d %+v", i, l, j, o)
			}
		}
	}
}

// nameParts are pieces of generated names. They include characters that
// need escaping in one form or another.
var nameParts = []string{
	"a", "b", "Ab", "é", "x y", "(1)", "#", "%", "'", "`", "[", "]", "|", "<", ">",
	":", "&", "?", "!", "日本", "-", "_", ".", "md", "index", "ab:c", "😀", "%20", "\\x"[1:],
}

type genSpace struct {
	r       *rand.Rand
	entries []Entry
	bodies  map[string]string
	next    int
}

func (g *genSpace) id() string {
	g.next++
	return fmt.Sprintf("e%03d", g.next)
}

func (g *genSpace) pickPart() string { return nameParts[g.r.IntN(len(nameParts))] }

// rawName returns a valid name body without an extension.
func (g *genSpace) rawName() string {
	var b strings.Builder
	for n := 1 + g.r.IntN(3); n > 0; n-- {
		b.WriteString(g.pickPart())
	}
	s := strings.Trim(b.String(), " ")
	if s == "" || strings.HasPrefix(s, ".") || s == ".." {
		s = "n" + s
	}
	return s
}

// taken reports whether a case-folded name exists in folder dir.
func taken(entries []Entry, dir, name string) bool {
	for _, e := range entries {
		if dirName(e.Path) == dir && lower(baseName(e.Path)) == lower(name) {
			return true
		}
	}
	return false
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func (g *genSpace) folders(entries []Entry) []string {
	out := []string{""}
	for _, e := range entries {
		if e.Kind == KindFolder {
			out = append(out, e.Path)
		}
	}
	return out
}

func (g *genSpace) noteName(dir string, allowIndex bool) string {
	if allowIndex && dir != "" && g.r.IntN(4) == 0 {
		return "index.md"
	}
	n := g.rawName()
	if g.r.IntN(2) == 0 {
		n = core.Slugify(n)
	}
	if n == "index" {
		n = "index-note"
	}
	return n + ".md"
}

func (g *genSpace) fileName() string {
	exts := []string{"", ".png", ".pdf", ".png", ".md"}
	return g.rawName() + exts[g.r.IntN(len(exts))]
}

func (g *genSpace) build() {
	nFolders, nNotes, nFiles := g.r.IntN(6), 1+g.r.IntN(7), g.r.IntN(7)
	add := func(kind string) {
		fs := g.folders(g.entries)
		dir := fs[g.r.IntN(len(fs))]
		for try := 0; try < 10; try++ {
			var name, title string
			switch kind {
			case KindFolder:
				name = g.rawName()
			case KindNote:
				name = g.noteName(dir, true)
				title = g.rawName()
				if g.r.IntN(3) == 0 && len(g.entries) > 0 {
					title = strings.TrimSuffix(g.entries[g.r.IntN(len(g.entries))].Name, ".md")
				}
			default:
				name = g.fileName()
			}
			if taken(g.entries, dir, name) {
				continue
			}
			g.entries = append(g.entries, Entry{ID: g.id(), Kind: kind, Path: joinPath(dir, name), Name: name, Title: title})
			return
		}
	}
	for i := 0; i < nFolders; i++ {
		add(KindFolder)
	}
	for i := 0; i < nNotes; i++ {
		add(KindNote)
	}
	for i := 0; i < nFiles; i++ {
		add(KindFile)
	}
	g.bodies = map[string]string{}
	for _, e := range g.entries {
		if e.Kind == KindNote {
			g.bodies[e.ID] = g.body(e)
		}
	}
}

var fillers = []string{"", " ", " ", "\n", "text ", "`c` ", "\n\n", "\n- ", "\n> ", "[", "]", "`", "\n    ", "(", "!", "\n```\n", "<!-- ", " -->", "| "}

// body returns a note body with links of every form to random entries.
func (g *genSpace) body(note Entry) string {
	var b strings.Builder
	for n := 1 + g.r.IntN(6); n > 0; n-- {
		b.WriteString(fillers[g.r.IntN(len(fillers))])
		t := g.entries[g.r.IntN(len(g.entries))]
		b.WriteString(g.link(note, t))
		b.WriteString(fillers[g.r.IntN(len(fillers))])
	}
	return b.String()
}

func (g *genSpace) link(note, t Entry) string {
	stem := strings.TrimSuffix(t.Name, ".md")
	heading := ""
	if g.r.IntN(4) == 0 {
		heading = "#h" + g.pickPart()
	}
	switch g.r.IntN(4) {
	case 0, 1:
		var target string
		switch g.r.IntN(6) {
		case 0:
			target = t.Name
		case 1:
			target = stem
		case 2:
			target = t.Title
		case 3:
			target = baseName(dirName(t.Path))
		case 4:
			target = strings.TrimSuffix(t.Path, ".md")
		default:
			target = "/" + t.Path
			if i := strings.IndexByte(t.Path, '/'); i >= 0 && g.r.IntN(2) == 0 {
				target = t.Path[i+1:]
			}
		}
		if target == "" {
			target = t.Name
		}
		label := ""
		if g.r.IntN(3) == 0 {
			label = "|" + g.pickPart()
		}
		embed := ""
		if g.r.IntN(4) == 0 {
			embed = "!"
		}
		return embed + "[[" + target + heading + label + "]]"
	default:
		p := t.Path
		if t.Kind == KindNote && g.r.IntN(3) == 0 {
			p = strings.TrimSuffix(p, ".md")
		}
		if g.r.IntN(4) == 0 {
			p = "/" + p
		} else {
			p = relPath(dirName(note.Path), p)
		}
		dest := encodeDest(p)
		if g.r.IntN(6) == 0 {
			dest = p
		}
		if heading != "" {
			dest += heading
		}
		if needsAngle(dest) || g.r.IntN(4) == 0 {
			dest = "<" + dest + ">"
		}
		title := ""
		if g.r.IntN(5) == 0 {
			title = ` "t"`
		}
		img := ""
		if g.r.IntN(4) == 0 {
			img = "!"
		}
		return img + "[" + g.pickPart() + "](" + dest + title + ")"
	}
}

// changes returns a random sequence of renames and moves and the entries
// after them.
func (g *genSpace) changes() []Change {
	cur := append([]Entry(nil), g.entries...)
	var out []Change
	for n := 1 + g.r.IntN(4); n > 0; n-- {
		e := cur[g.r.IntN(len(cur))]
		var newPath string
		if g.r.IntN(2) == 0 {
			if e.Name == "index.md" {
				continue
			}
			var name string
			switch e.Kind {
			case KindFolder:
				name = g.rawName()
			case KindNote:
				name = g.noteName("", false)
			default:
				name = g.fileName()
			}
			dir := dirName(e.Path)
			if taken(cur, dir, name) {
				continue
			}
			newPath = joinPath(dir, name)
		} else {
			fs := g.folders(cur)
			dir := fs[g.r.IntN(len(fs))]
			if dir == dirName(e.Path) || dir == e.Path || strings.HasPrefix(dir, e.Path+"/") || taken(cur, dir, e.Name) {
				continue
			}
			newPath = joinPath(dir, e.Name)
		}
		kind := "move"
		if dirName(newPath) == dirName(e.Path) {
			kind = "rename"
		}
		ch := Change{Kind: kind, ID: e.ID, OldPath: e.Path, NewPath: newPath}
		out = append(out, ch)
		cur = ApplyChanges(cur, []Change{ch})
	}
	return out
}

// checkRewriteStable runs one generated space through the stability check.
func checkRewriteStable(t *testing.T, seed uint64) {
	g := &genSpace{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
	g.build()
	changes := g.changes()
	before := NewSnapshot(g.entries)

	index := map[string][]string{}
	for _, e := range g.entries {
		if e.Kind != KindNote {
			continue
		}
		for _, l := range Parse(g.bodies[e.ID]) {
			for _, k := range LinkKeys(e.Path, l) {
				index[k] = append(index[k], e.ID)
			}
		}
	}
	affected := map[string]bool{}
	for _, id := range Affected(before, changes, func(k string) []string { return index[k] }) {
		affected[id] = true
	}

	rw := NewRewriter(before, changes)
	after := rw.After()
	for _, e := range g.entries {
		if e.Kind != KindNote {
			continue
		}
		body := g.bodies[e.ID]
		oldPath, newPath := e.Path, after.ByID(e.ID).Path
		fail := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("seed %d note %s %q -> %q\nentries %+v\nchanges %+v\nbody %q\n%s", seed, e.ID, oldPath, newPath, g.entries, changes, body, fmt.Sprintf(format, args...))
		}
		if got := Rewrite(before, nil, oldPath, oldPath, body); len(got) != 0 {
			fail("no changes gave edits %+v", got)
		}
		links0 := Parse(body)
		ids0 := make([]string, len(links0))
		for i, l := range links0 {
			if x := before.Resolve(oldPath, l); x != nil {
				ids0[i] = x.ID
			}
		}
		edits := rw.Rewrite(oldPath, newPath, body)
		for i, ed := range edits {
			if ed.Start < 0 || ed.End > len(body) || ed.Start > ed.End || (i > 0 && edits[i-1].End > ed.Start) {
				fail("bad edits %+v", edits)
			}
		}
		if len(edits) > 0 && !affected[e.ID] {
			fail("edits %+v but the note is not in Affected", edits)
		}
		nb := Apply(body, edits)
		links1 := Parse(nb)
		if len(links1) != len(links0) {
			fail("new body %q has %d links, want %d", nb, len(links1), len(links0))
		}
		for i, l := range links1 {
			if ids0[i] == "" {
				continue
			}
			got := after.Resolve(newPath, l)
			if got == nil || got.ID != ids0[i] {
				fail("new body %q link %d %+v resolves to %+v, want %s", nb, i, l, got, ids0[i])
			}
		}
		if got := NewRewriter(after, nil).Rewrite(newPath, newPath, nb); len(got) != 0 {
			fail("second rewrite of %q gave %+v", nb, got)
		}
		noop := make([]Change, len(changes))
		for i, c := range changes {
			p := after.ByID(c.ID).Path
			noop[i] = Change{Kind: c.Kind, ID: c.ID, OldPath: p, NewPath: p}
		}
		if got := Rewrite(after, noop, newPath, newPath, nb); len(got) != 0 {
			fail("no-op changes on %q gave %+v", nb, got)
		}
	}
}

// FuzzRewriteStable checks that rewriting every affected note after random
// renames and moves keeps every link resolving to the same entry id.
func FuzzRewriteStable(f *testing.F) {
	for i := uint64(0); i < 300; i++ {
		f.Add(i)
	}
	f.Fuzz(checkRewriteStable)
}
