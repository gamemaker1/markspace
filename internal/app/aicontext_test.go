package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestFindSection(t *testing.T) {
	body := strings.Join([]string{
		"# Plan",
		"intro",
		"## Rollout **plan** ##",
		"step one",
		"```",
		"## Not a heading",
		"```",
		"### Details",
		"deep detail",
		"## Rollback",
		"undo",
		"Setext title",
		"------------",
		"setext body",
		"- list item",
		"---",
		"after rule",
	}, "\n")
	tests := []struct {
		heading, wantTitle string
		wantText           string
		found              bool
	}{
		{"rollout plan", "Rollout plan", "## Rollout **plan** ##\nstep one\n```\n## Not a heading\n```\n### Details\ndeep detail", true},
		{"## ROLLOUT PLAN", "Rollout plan", "## Rollout **plan** ##\nstep one\n```\n## Not a heading\n```\n### Details\ndeep detail", true},
		{"Details", "Details", "### Details\ndeep detail", true},
		{"rollback", "Rollback", "## Rollback\nundo", true},
		{"Setext title", "Setext title", "Setext title\n------------\nsetext body\n- list item\n---\nafter rule", true},
		{"Not a heading", "", "", false},
		{"missing", "", "", false},
		{"  ## ", "", "", false},
	}
	for _, tc := range tests {
		text, title, found := findSection(body, tc.heading)
		if found != tc.found || text != tc.wantText || title != tc.wantTitle {
			t.Errorf("findSection(%q) = %q, %q, %v\nwant %q, %q, %v", tc.heading, text, title, found, tc.wantText, tc.wantTitle, tc.found)
		}
	}
	if _, _, found := findSection("# Plan\nintro", "Plan"); !found {
		t.Error("a level 1 heading at the start was not found")
	}
}

// aiRun calls RunAI and fails the test on an error.
func aiRun(t *testing.T, h *rharness, actor *core.Actor, req core.AIRequest) *core.AIResult {
	t.Helper()
	res, err := h.app.RunAI(h.ctx, actor, req)
	if err != nil {
		t.Fatalf("RunAI: %v", err)
	}
	return res
}

// mustNotMention fails when the JSON form of v contains any needle.
func mustNotMention(t *testing.T, v any, needles ...string) {
	t.Helper()
	raw, _ := json.Marshal(v)
	for _, needle := range needles {
		if strings.Contains(string(raw), needle) {
			t.Fatalf("%s leaked into %s", needle, raw)
		}
	}
}

func TestAIContextLinkedAndBacklinksHideInvisibleNotes(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	reader := h.user("Reader", false)
	sp := h.space(owner, "Team")
	plan := h.note(owner, sp, "", "Plan", "See [[Deploy]] and [[Secret]] and [[Nowhere]].")
	deploy := h.note(owner, sp, "", "Deploy", "deploy steps alpha, back to [[Plan]]")
	secret := h.note(owner, sp, "", "Secret", "walrus salaries, see [[Plan]]")
	h.share(plan.ID, reader, core.AccessView, false)
	h.share(deploy.ID, reader, core.AccessView, false)

	linked := aiRun(t, h, reader, core.AIRequest{Task: core.TaskSummarize, Text: "summary",
		Context: []core.AIContextRef{{Kind: core.ContextLinked, NoteID: plan.ID}}})
	if len(linked.Sources) != 1 || linked.Sources[0].NoteID != deploy.ID || linked.Sources[0].Kind != core.ContextLinked ||
		linked.Sources[0].Title != "Deploy" || linked.Sources[0].Skipped != "" {
		t.Fatalf("linked sources = %+v", linked.Sources)
	}
	if !strings.Contains(linked.Text, "deploy steps alpha") {
		t.Fatalf("prompt lacks the linked note: %q", linked.Text)
	}
	mustNotMention(t, linked, secret.ID, "Secret", "walrus")

	back := aiRun(t, h, reader, core.AIRequest{Task: core.TaskSummarize, Text: "summary",
		Context: []core.AIContextRef{{Kind: core.ContextBacklinks, NoteID: plan.ID}}})
	if len(back.Sources) != 1 || back.Sources[0].NoteID != deploy.ID || back.Sources[0].Kind != core.ContextBacklinks {
		t.Fatalf("backlink sources = %+v", back.Sources)
	}
	mustNotMention(t, back, secret.ID, "Secret", "walrus")

	direct := aiRun(t, h, reader, core.AIRequest{Task: core.TaskSummarize, Text: "summary",
		Context: []core.AIContextRef{{Kind: core.ContextNote, NoteID: secret.ID}, {Kind: core.ContextLinked, NoteID: secret.ID}}})
	for _, s := range direct.Sources {
		if s.Skipped != skipNotFound || s.Title != "" {
			t.Fatalf("hidden note reference = %+v", s)
		}
	}
	mustNotMention(t, direct, "Secret", "walrus")

	ownerView := aiRun(t, h, owner, core.AIRequest{Task: core.TaskSummarize, Text: "summary",
		Context: []core.AIContextRef{{Kind: core.ContextLinked, NoteID: deploy.ID}}})
	if len(ownerView.Sources) != 1 || ownerView.Sources[0].NoteID != plan.ID {
		t.Fatalf("owner linked sources = %+v", ownerView.Sources)
	}
	none := aiRun(t, h, owner, core.AIRequest{Task: core.TaskSummarize, Text: "summary",
		Context: []core.AIContextRef{{Kind: core.ContextLinked, NoteID: h.note(owner, sp, "", "Lonely", "no links").ID}}})
	if len(none.Sources) != 1 || none.Sources[0].Skipped != skipNoLinks {
		t.Fatalf("note without links = %+v", none.Sources)
	}
}

func TestAIContextSectionAndSelection(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Team")
	n := h.note(owner, sp, "", "Release", "# Release\nintro text\n## Rollout plan\nroll it out\n## Rollback\nundo it\n")

	res := aiRun(t, h, owner, core.AIRequest{Task: core.TaskImprove, NoteID: n.ID, Context: []core.AIContextRef{
		{Kind: core.ContextSelection, Text: "the chosen words"},
		{Kind: core.ContextSection, NoteID: n.ID, Heading: "rollout PLAN"},
		{Kind: core.ContextSection, NoteID: n.ID, Heading: "Missing"},
	}})
	if len(res.Sources) != 3 {
		t.Fatalf("sources = %+v", res.Sources)
	}
	sel, sec, missing := res.Sources[0], res.Sources[1], res.Sources[2]
	if sel.Kind != core.ContextSelection || sel.Title != "Selection" || sel.Excerpt != "the chosen words" {
		t.Fatalf("selection source = %+v", sel)
	}
	if sec.Title != "Release > Rollout plan" || sec.Excerpt != "## Rollout plan\nroll it out" || sec.Path != n.Path {
		t.Fatalf("section source = %+v", sec)
	}
	if missing.Skipped != skipHeading || missing.NoteID != n.ID {
		t.Fatalf("missing heading source = %+v", missing)
	}
	// The selection is the task text, so the note body is not.
	if strings.Contains(res.Text, "intro text") || strings.Contains(res.Text, "undo it") {
		t.Fatalf("prompt holds more than the section: %q", res.Text)
	}
}

func TestAIContextBudget(t *testing.T) {
	h := newRealHarness(t)
	h.cfg.AI.ContextChars = 200
	owner := h.user("Owner", false)
	sp := h.space(owner, "Team")
	long := h.note(owner, sp, "", "Long", strings.Repeat("l", 500))
	later := h.note(owner, sp, "", "Later", "later body")

	res := aiRun(t, h, owner, core.AIRequest{Task: core.TaskSummarize, Text: "short", Context: []core.AIContextRef{
		{Kind: core.ContextSelection, Text: strings.Repeat("s", 100)},
		{Kind: core.ContextNote, NoteID: long.ID},
		{Kind: core.ContextNote, NoteID: later.ID},
		{Kind: core.ContextURL, URL: "https://example.com"},
	}})
	if len(res.Sources) != 4 {
		t.Fatalf("sources = %+v", res.Sources)
	}
	if s := res.Sources[1]; !s.Truncated || s.Skipped != "" {
		t.Fatalf("cut note = %+v", s)
	}
	if !strings.Contains(res.Text, strings.Repeat("l", 95)) || strings.Contains(res.Text, strings.Repeat("l", 96)) {
		t.Fatal("the cut note does not hold exactly the 95 runes left")
	}
	for _, s := range res.Sources[2:] {
		if s.Skipped != skipBudget {
			t.Fatalf("late reference = %+v", s)
		}
	}
	if strings.Contains(res.Text, "later body") {
		t.Fatal("a skipped note reached the prompt")
	}
}

// pngBytes encodes an opaque PNG of the given size.
func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// mxBuildPDF writes a minimal one-page PDF with one Helvetica text line.
func mxBuildPDF(text string) []byte {
	content := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [4 0 R] /Count 1 >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func TestAIContextFiles(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	reader := h.user("Reader", false)
	sp := h.space(owner, "Team")
	gallery := h.folder(owner, sp, "", "Gallery")
	hidden := h.folder(owner, sp, "", "Hidden")
	h.share(gallery.ID, reader, core.AccessView, true)

	upload := func(parentID, name string, data []byte) *core.File {
		f, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{SpaceID: sp.ID, ParentID: parentID, Name: name}, bytes.NewReader(data))
		if err != nil {
			t.Fatalf("upload %s: %v", name, err)
		}
		return f
	}
	chart := upload(gallery.ID, "chart.png", pngBytes(t, 2000, 1000))
	notes := upload(gallery.ID, "notes.txt", []byte("file text body"))
	report := upload(gallery.ID, "report.pdf", mxBuildPDF("quarterly walrus figures"))
	blob := upload(gallery.ID, "blob.bin", []byte{0, 1, 2, 3, 0xff})
	secret := upload(hidden.ID, "secret.txt", []byte("hidden text"))
	old := upload(gallery.ID, "old.txt", []byte("trashed text"))
	if err := h.app.TrashItem(h.ctx, owner, old.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}

	refs := []core.AIContextRef{
		{Kind: core.ContextFile, FileID: chart.ID},
		{Kind: core.ContextFile, FileID: notes.ID},
		{Kind: core.ContextFile, FileID: report.ID},
		{Kind: core.ContextFile, FileID: blob.ID},
		{Kind: core.ContextFile, FileID: secret.ID},
		{Kind: core.ContextFile, FileID: old.ID},
		{Kind: core.ContextFile},
	}
	res := aiRun(t, h, reader, core.AIRequest{Task: core.TaskSummarize, Text: "x", Context: refs})
	want := []struct{ title, skipped string }{
		{"chart.png", skipNoVision},
		{"notes.txt", ""},
		{"report.pdf", ""},
		{"blob.bin", skipUnsupported},
		{"", skipNotFound},
		{"", skipNotFound},
		{"", skipIncomplete},
	}
	if len(res.Sources) != len(want) {
		t.Fatalf("sources = %+v", res.Sources)
	}
	for i, w := range want {
		if s := res.Sources[i]; s.Title != w.title || s.Skipped != w.skipped || s.FileID != refs[i].FileID || s.Kind != core.ContextFile {
			t.Errorf("source %d = %+v, want title %q skipped %q", i, s, w.title, w.skipped)
		}
	}
	if !strings.Contains(res.Text, "file text body") {
		t.Fatalf("prompt lacks the text file: %q", res.Text)
	}
	if !strings.Contains(res.Text, "quarterly walrus figures") {
		t.Fatalf("prompt lacks the PDF text: %q", res.Text)
	}
	mustNotMention(t, res, "secret.txt", "hidden text", "old.txt", "trashed text")

	_, err := h.app.RunAI(h.ctx, reader, core.AIRequest{Task: core.TaskDescribe, Context: refs[:1]})
	if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), "vision") {
		t.Fatalf("describe without vision: err = %v", err)
	}

	h.cfg.AI.Providers[0].Vision = true
	res = aiRun(t, h, reader, core.AIRequest{Task: core.TaskDescribe, Context: refs[:1]})
	if len(res.Sources) != 1 || res.Sources[0].Skipped != "" || res.Sources[0].Path != chart.URL {
		t.Fatalf("vision sources = %+v", res.Sources)
	}
}

func TestAIContextImagesAreNormalizedAndCapped(t *testing.T) {
	h := newRealHarness(t)
	h.cfg.AI.Providers[0].Vision = true
	owner := h.user("Owner", false)
	sp := h.space(owner, "Team")
	var refs []core.AIContextRef
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("img%d.png", i)
		f, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{SpaceID: sp.ID, Name: name}, bytes.NewReader(pngBytes(t, 2000, 100)))
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		refs = append(refs, core.AIContextRef{Kind: core.ContextFile, FileID: f.ID})
	}
	call, err := h.app.prepareAI(h.ctx, owner, core.AIRequest{Task: core.TaskDescribe, Context: refs})
	if err != nil {
		t.Fatalf("prepareAI: %v", err)
	}
	images := call.msgs[len(call.msgs)-1].Images
	if len(images) != maxContextImages {
		t.Fatalf("%d images on the user message", len(images))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(images[0].Data))
	if err != nil || format != "jpeg" || cfg.Width != 1568 || images[0].MediaType != "image/jpeg" {
		t.Fatalf("image %s %dx%d %s: %v", format, cfg.Width, cfg.Height, images[0].MediaType, err)
	}
	if last := call.sources[5]; last.Skipped != skipImageLimit {
		t.Fatalf("sixth image = %+v", last)
	}
}

func TestAIContextURL(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<title>Runbook</title><script>SCRIPTBODY</script><p>Readable runbook text.</p>`)
	}))
	defer page.Close()
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	refs := []core.AIContextRef{{Kind: core.ContextURL, URL: page.URL}}

	res := aiRun(t, h, owner, core.AIRequest{Task: core.TaskSummarize, Text: "x", Context: refs})
	if len(res.Sources) != 1 || res.Sources[0].Skipped != skipPrivateURL || res.Sources[0].URL != page.URL {
		t.Fatalf("loopback URL with private addresses off = %+v", res.Sources)
	}

	h.cfg.AI.AllowPrivateURLs = true
	res = aiRun(t, h, owner, core.AIRequest{Task: core.TaskSummarize, Text: "x", Context: refs})
	if len(res.Sources) != 1 || res.Sources[0].Title != "Runbook" || res.Sources[0].Skipped != "" {
		t.Fatalf("URL sources = %+v", res.Sources)
	}
	if !strings.Contains(res.Text, "Readable runbook text.") || strings.Contains(res.Text, "SCRIPTBODY") {
		t.Fatalf("prompt = %q", res.Text)
	}
}

func TestAIContextSearchAndUseContext(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Team")
	h.note(owner, sp, "", "Rollback", "the rollback procedure uses the previous tag")
	h.note(owner, sp, "", "Other", "unrelated gardening")

	res := aiRun(t, h, owner, core.AIRequest{Task: core.TaskAsk, SpaceID: sp.ID, Question: "rollback", UseContext: true})
	if len(res.Sources) == 0 || res.Sources[0].Kind != core.ContextSearch || res.Sources[0].Title != "Rollback" {
		t.Fatalf("useContext sources = %+v", res.Sources)
	}
	if !strings.Contains(res.Text, "previous tag") {
		t.Fatalf("prompt lacks the search hit: %q", res.Text)
	}

	res = aiRun(t, h, owner, core.AIRequest{Task: core.TaskAsk, Question: "q", Context: []core.AIContextRef{
		{Kind: core.ContextSearch, Query: "rollback"},
		{Kind: "mystery"},
	}})
	if len(res.Sources) != 2 || res.Sources[0].Skipped != skipNoSpace || res.Sources[1].Skipped != skipUnknownKind {
		t.Fatalf("sources without a space = %+v", res.Sources)
	}
}

func TestAIStreamPutsSourcesOnDone(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	ch, err := h.app.StreamAI(h.ctx, owner, core.AIRequest{Task: core.TaskExplain, Text: "x",
		Context: []core.AIContextRef{{Kind: core.ContextSelection, Text: "a term"}}})
	if err != nil {
		t.Fatalf("StreamAI: %v", err)
	}
	var done *core.AIChunk
	for chunk := range ch {
		if chunk.Done {
			c := chunk
			done = &c
		}
	}
	if done == nil || len(done.Sources) != 1 || done.Sources[0].Excerpt != "a term" {
		t.Fatalf("done chunk = %+v", done)
	}
}

func TestPublicSourcesCutExcerpts(t *testing.T) {
	in := []core.AISource{{Title: "t", Excerpt: strings.Repeat("é", 1000)}}
	out := publicSources(in)
	if n := len([]rune(out[0].Excerpt)); n != sourceExcerptRunes || len([]rune(in[0].Excerpt)) != 1000 {
		t.Fatalf("excerpt kept %d runes", n)
	}
	if publicSources(nil) != nil {
		t.Fatal("no sources should stay nil")
	}
}
