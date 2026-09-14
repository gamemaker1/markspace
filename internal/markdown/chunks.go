package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Chunking limits.
const (
	// defaultChunkRunes is used when Chunks is called with a non-positive size.
	defaultChunkRunes = 1200
	// maxChunks caps how many chunks one body produces.
	maxChunks = 200
	// minChunkRunes is the least non-whitespace a chunk must hold to be kept.
	minChunkRunes = 20
	// overlapPercent of the target is carried from one chunk to the next.
	overlapPercent = 15
)

// mdBlock is one markdown block: either a heading or a run of lines between
// blank lines. A fenced code block is always one block.
type mdBlock struct {
	text    string
	heading bool
	level   int
	title   string
}

// heading is one step of the enclosing heading path.
type heading struct {
	level int
	text  string
}

// Chunks splits a body into pieces of about targetRunes runes each, for
// embedding. A non-positive targetRunes means 1200. Splits happen at headings
// first and at blank lines otherwise, never inside a fenced code block. Each
// chunk is prefixed with its enclosing heading path, as
// "# Title > ## Section", so the embedded text carries its context. Chunks
// with less than 20 runes of non-whitespace are dropped and at most 200 chunks
// are returned.
func (r *Renderer) Chunks(md string, targetRunes int) []string {
	if targetRunes <= 0 {
		targetRunes = defaultChunkRunes
	}
	overlap := targetRunes * overlapPercent / 100

	var (
		out      []string
		path     []heading
		current  []string
		atStart  []heading
		curRunes int
		carry    string
	)

	flush := func(sizeDriven bool) {
		if len(current) == 0 {
			carry = ""
			return
		}
		body := strings.Join(current, "\n\n")
		if nonSpaceRunes(body) >= minChunkRunes {
			out = append(out, withHeadingPath(atStart, body))
		}
		if sizeDriven {
			carry = tailRunes(body, overlap)
		} else {
			carry = ""
		}
		current = nil
		curRunes = 0
	}

	for _, blk := range splitBlocks(stripFrontmatter(md)) {
		if len(out) >= maxChunks {
			return out[:maxChunks]
		}
		if blk.heading {
			flush(false)
			path = pushHeading(path, blk.level, blk.title)
			continue
		}
		runes := utf8.RuneCountInString(blk.text)
		if curRunes > 0 && curRunes+runes > targetRunes {
			flush(true)
		}
		if len(current) == 0 {
			atStart = append([]heading(nil), path...)
			if carry != "" {
				current = append(current, carry)
				curRunes += utf8.RuneCountInString(carry)
				carry = ""
			}
		}
		current = append(current, blk.text)
		curRunes += runes
	}
	flush(false)
	if len(out) > maxChunks {
		out = out[:maxChunks]
	}
	return out
}

// splitBlocks breaks a body into blocks. Blank lines separate blocks, headings
// form their own block, and a fenced code block is never split.
func splitBlocks(body string) []mdBlock {
	var (
		blocks []mdBlock
		buf    []string
		fence  string
	)
	emit := func() {
		if len(buf) == 0 {
			return
		}
		text := strings.TrimRight(strings.Join(buf, "\n"), "\n")
		buf = nil
		if strings.TrimSpace(text) != "" {
			blocks = append(blocks, mdBlock{text: text})
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if fence != "" {
			buf = append(buf, line)
			if closesFence(line, fence) {
				fence = ""
				emit()
			}
			continue
		}
		if marker := opensFence(line); marker != "" {
			emit()
			fence = marker
			buf = append(buf, line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			emit()
			continue
		}
		if level, title, ok := atxHeading(line); ok {
			emit()
			blocks = append(blocks, mdBlock{heading: true, level: level, title: title})
			continue
		}
		buf = append(buf, line)
	}
	emit()
	return blocks
}

// opensFence returns the fence marker a line opens, or the empty string.
func opensFence(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return ""
	}
	for _, ch := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, ch) {
			n := 0
			for n < len(trimmed) && trimmed[n] == ch[0] {
				n++
			}
			return strings.Repeat(ch[:1], n)
		}
	}
	return ""
}

// closesFence reports whether a line closes the given fence.
func closesFence(line, fence string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, fence) {
		return false
	}
	return strings.Trim(trimmed, fence[:1]) == ""
}

// atxHeading parses an ATX heading line.
func atxHeading(line string) (level int, title string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == '#' {
		n++
	}
	if n > 6 || n >= len(trimmed) || (trimmed[n] != ' ' && trimmed[n] != '\t') {
		return 0, "", false
	}
	title = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(trimmed[n:]), "#"))
	return n, strings.TrimSpace(title), true
}

// pushHeading replaces the tail of a heading path with a new heading.
func pushHeading(path []heading, level int, title string) []heading {
	kept := path[:0:0]
	for _, h := range path {
		if h.level < level {
			kept = append(kept, h)
		}
	}
	return append(kept, heading{level: level, text: title})
}

// withHeadingPath prefixes a chunk body with its heading path.
func withHeadingPath(path []heading, body string) string {
	if len(path) == 0 {
		return body
	}
	parts := make([]string, 0, len(path))
	for _, h := range path {
		parts = append(parts, strings.Repeat("#", h.level)+" "+h.text)
	}
	return strings.Join(parts, " > ") + "\n\n" + body
}

// tailRunes returns the last n runes of s, moved forward to the next word
// boundary so the overlap does not start inside a word.
func tailRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return strings.TrimSpace(s)
	}
	tail := string(runes[len(runes)-n:])
	if i := strings.IndexAny(tail, " \n\t"); i >= 0 {
		tail = tail[i+1:]
	}
	if strings.Contains(tail, "```") || strings.Contains(tail, "~~~") {
		// Carrying part of a code fence would produce an unbalanced chunk.
		return ""
	}
	return strings.TrimSpace(tail)
}

// nonSpaceRunes counts the runes of s that are not whitespace.
func nonSpaceRunes(s string) int {
	count := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return count
}
