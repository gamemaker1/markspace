package markdown

import (
	"fmt"
	"html"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// diffContextLines is how many unchanged lines stay visible on each side of a
// collapsed run.
const diffContextLines = 3

// diffCollapseThreshold is the length a run of unchanged lines must exceed
// before the middle of the run is collapsed.
const diffCollapseThreshold = 2 * diffContextLines

// CSS classes emitted by DiffHTML.
const (
	classDiff     = "diff"
	classDiffLine = "diff-line"
	classDiffAdd  = "diff-add"
	classDiffDel  = "diff-del"
	classDiffEq   = "diff-eq"
	classDiffMark = "diff-mark"
	classDiffSkip = "diff-skip"
)

// DiffHTML renders a line diff between two bodies as HTML. Every line is
// escaped, so the output is safe to insert into a page. Long runs of unchanged
// lines are collapsed to a note of how many lines are hidden.
func (r *Renderer) DiffHTML(oldText, newText string) string {
	dmp := diffmatchpatch.New()
	oldChars, newChars, lines := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(oldChars, newChars, false), lines)

	var b strings.Builder
	b.WriteString(`<div class="` + classDiff + `">`)
	for _, d := range diffs {
		if d.Text == "" {
			continue
		}
		content := strings.Split(strings.TrimSuffix(d.Text, "\n"), "\n")
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			writeDiffLines(&b, content, classDiffAdd, "+")
		case diffmatchpatch.DiffDelete:
			writeDiffLines(&b, content, classDiffDel, "-")
		default:
			writeEqualLines(&b, content)
		}
	}
	b.WriteString("</div>")
	return b.String()
}

// writeDiffLines writes one line per entry with the given class and mark.
func writeDiffLines(b *strings.Builder, lines []string, class, mark string) {
	for _, line := range lines {
		b.WriteString(`<div class="` + classDiffLine + " " + class + `"><span class="` + classDiffMark + `">`)
		b.WriteString(html.EscapeString(mark))
		b.WriteString("</span>")
		b.WriteString(html.EscapeString(line))
		b.WriteString("</div>")
	}
}

// writeEqualLines writes unchanged lines, collapsing the middle of a long run.
func writeEqualLines(b *strings.Builder, lines []string) {
	if len(lines) <= diffCollapseThreshold {
		writeDiffLines(b, lines, classDiffEq, " ")
		return
	}
	hidden := len(lines) - diffCollapseThreshold
	writeDiffLines(b, lines[:diffContextLines], classDiffEq, " ")
	fmt.Fprintf(b, `<div class="%s">… %d unchanged lines …</div>`, classDiffSkip, hidden)
	writeDiffLines(b, lines[len(lines)-diffContextLines:], classDiffEq, " ")
}
