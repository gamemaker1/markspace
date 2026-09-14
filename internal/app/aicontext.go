package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/extract"
	"github.com/gamemaker1/markspace/internal/fetch"
)

const (
	// maxContextRefs caps the references resolved for one request.
	maxContextRefs = 20
	// maxURLRefs caps the web pages fetched for one request.
	maxURLRefs = 5
	// maxRelatedNotes caps the notes one linked or backlinks reference adds.
	maxRelatedNotes = 10
	// perRefChars caps one linked note, backlink, web page or text file.
	perRefChars = 8000
	// maxContextImages caps the images one request carries.
	maxContextImages = 5
	// minContextChars is the smallest cut worth adding to the budget. The
	// prompt builder applies the same floor.
	minContextChars = 64
	// maxFileBytes caps how much of an image or PDF file is read.
	maxFileBytes = 32 << 20
	// maxFileTextBytes caps a text file read and the text
	// extracted from a PDF.
	maxFileTextBytes = 1 << 20
	// sniffBytes is how much of an file is read to detect its format.
	sniffBytes = 1024
	// pdfTimeout bounds text extraction from one PDF.
	pdfTimeout = 20 * time.Second
	// sourceExcerptRunes caps the excerpt a source carries back to the client.
	sourceExcerptRunes = 300
	// sectionTitleSeparator joins the note title and the heading of a section.
	sectionTitleSeparator = " > "
	// selectionTitle is the title of a selection source.
	selectionTitle = "Selection"
)

// Reasons a context reference is skipped. Clients show them as they are.
const (
	skipBudget        = "context budget reached"
	skipNoVision      = "model does not accept images"
	skipNotFound      = "not found"
	skipHeading       = "heading not found"
	skipEmpty         = "empty"
	skipUnreadable    = "could not be read"
	skipTooManyRefs   = "too many context references"
	skipTooManyURLs   = "too many web pages"
	skipImageLimit    = "image limit reached"
	skipImageTooLarge = "image too large"
	skipFileTooLarge  = "file too large"
	skipUnsupported   = "unsupported file type"
	skipNotUTF8       = "not UTF-8 text"
	skipNoLinks       = "no linked notes"
	skipNoBacklinks   = "no backlinks"
	skipNoMatches     = "no matches"
	skipNoSpace       = "no space to search"
	skipIncomplete    = "reference is incomplete"
	skipUnknownKind   = "unknown context kind"
	skipPrivateURL    = "private address not allowed"
	skipURLScheme     = "only http and https URLs are allowed"
	skipRedirects     = "too many redirects"
	skipContentType   = "unsupported content type"
	skipTimeout       = "timed out"
	skipUnreachable   = "could not be fetched"
)

// contextBudget returns the character budget of one prompt.
func (a *App) contextBudget() int {
	if a.cfg.AI.ContextChars > 0 {
		return a.cfg.AI.ContextChars
	}
	if a.ai != nil && a.ai.MaxContextRunes() > 0 {
		return a.ai.MaxContextRunes()
	}
	return 48000
}

// taskTextBudget mirrors ai.TaskTextBudget: an ask keeps at most half of the
// budget for its note text, and every other task may use all of it.
func taskTextBudget(task core.AITask, budget int) int {
	if task == core.TaskAsk {
		return budget / 2
	}
	return budget
}

// firstSelection returns the text of the first non-empty selection reference.
func firstSelection(refs []core.AIContextRef) string {
	for _, ref := range refs {
		if ref.Kind == core.ContextSelection && strings.TrimSpace(ref.Text) != "" {
			return ref.Text
		}
	}
	return ""
}

// contextRefs returns the references of a request. useContext on a request
// without references acts as one search built from the question.
func contextRefs(req core.AIRequest, spaceID string) []core.AIContextRef {
	if len(req.Context) > 0 || !req.UseContext || spaceID == "" {
		return req.Context
	}
	query := req.Question
	if strings.TrimSpace(query) == "" {
		query = req.Text
	}
	return []core.AIContextRef{{Kind: core.ContextSearch, Query: query}}
}

// contextResolver resolves the references of one request in order, under the
// actor's access and one character budget.
type contextResolver struct {
	a        *App
	actor    *core.Actor
	cache    *grantCache
	vision   bool
	taskText string
	spaceID  string
	budget   int
	used     int
	spent    bool
	urls     int
	spaces   map[string]*core.Space
	fetcher  *fetch.Fetcher

	// sources carry the full resolved text, which only the prompt sees.
	sources []core.AISource
	images  []core.AIImage
}

// resolveContext resolves every context reference of req. The task text is
// charged to the budget first. vision decides whether image files are
// sent, and is false when no model of the route accepts images.
func (a *App) resolveContext(ctx context.Context, actor *core.Actor, req core.AIRequest, spaceID string, vision bool, budget int) *contextResolver {
	r := &contextResolver{
		a:        a,
		actor:    actor,
		cache:    a.grants(actor),
		vision:   vision,
		taskText: strings.TrimSpace(req.Text),
		spaceID:  spaceID,
		budget:   budget,
		spaces:   map[string]*core.Space{},
	}
	r.used = min(utf8.RuneCountInString(req.Text), taskTextBudget(req.Task, budget))
	r.spent = r.used >= budget
	refs := contextRefs(req, spaceID)
	for i, ref := range refs {
		if i >= maxContextRefs {
			r.skip(refSource(ref), skipTooManyRefs)
			continue
		}
		r.resolve(ctx, ref)
	}
	if len(refs) > 0 {
		skipped := 0
		for _, s := range r.sources {
			if s.Skipped != "" {
				skipped++
			}
		}
		a.log.Info("ai context resolved", "task", req.Task, "refs", len(refs), "sources", len(r.sources),
			"skipped", skipped, "images", len(r.images), "chars", r.used, "budget", budget, "userId", actor.UserID())
	}
	return r
}

// refSource starts a source with the identifiers the client sent, and no
// title, so a skipped reference names nothing the client did not already know.
func refSource(ref core.AIContextRef) core.AISource {
	return core.AISource{Kind: ref.Kind, NoteID: ref.NoteID, FileID: ref.FileID, URL: ref.URL}
}

// resolve dispatches one reference by kind.
func (r *contextResolver) resolve(ctx context.Context, ref core.AIContextRef) {
	if ref.Kind == core.ContextSelection {
		r.selection(ref)
		return
	}
	src := refSource(ref)
	if r.spent {
		r.skip(src, skipBudget)
		return
	}
	switch ref.Kind {
	case core.ContextNote:
		r.note(ctx, ref)
	case core.ContextSection:
		r.section(ctx, ref)
	case core.ContextLinked:
		r.linked(ctx, ref)
	case core.ContextBacklinks:
		r.backlinks(ctx, ref)
	case core.ContextFile:
		r.file(ctx, ref)
	case core.ContextURL:
		r.url(ctx, ref)
	case core.ContextSearch:
		r.search(ctx, ref)
	default:
		r.skip(src, skipUnknownKind)
	}
}

// skip lists a reference that adds nothing to the prompt.
func (r *contextResolver) skip(src core.AISource, reason string) {
	src.Skipped = reason
	src.Excerpt = ""
	r.sources = append(r.sources, src)
}

// addText charges text to the budget and lists the source. With limit above
// 0 the text is first cut to limit runes. A text that no longer fits is cut
// and marked truncated, unless fewer than minContextChars runes are left, in
// which case the budget counts as spent and the source is skipped.
func (r *contextResolver) addText(src core.AISource, text string, limit int, truncated bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		r.skip(src, skipEmpty)
		return
	}
	if r.spent {
		r.skip(src, skipBudget)
		return
	}
	n := utf8.RuneCountInString(text)
	if limit > 0 && n > limit {
		text, n, truncated = truncateRunes(text, limit), limit, true
	}
	if remaining := r.budget - r.used; n > remaining {
		if remaining < minContextChars {
			r.spent = true
			r.skip(src, skipBudget)
			return
		}
		text, n, truncated = truncateRunes(text, remaining), remaining, true
	}
	r.used += n
	r.spent = r.used >= r.budget
	src.Excerpt = text
	src.Truncated = truncated
	r.sources = append(r.sources, src)
}

// selection resolves text the client sent. A selection that is the task text
// is already in the user message and costs nothing more.
func (r *contextResolver) selection(ref core.AIContextRef) {
	src := refSource(ref)
	src.Title = selectionTitle
	text := strings.TrimSpace(ref.Text)
	if text == "" {
		r.skip(src, skipEmpty)
		return
	}
	if text == r.taskText {
		src.Excerpt = text
		r.sources = append(r.sources, src)
		return
	}
	r.addText(src, text, 0, false)
}

// isAccessErr reports whether err is how a grant check hides a resource.
func isAccessErr(err error) bool {
	return errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrForbidden) || errors.Is(err, core.ErrUnauthorized)
}

// viewNote loads a note the client named and checks view access. On failure
// it lists the reference as not found, without a title.
func (r *contextResolver) viewNote(ctx context.Context, src core.AISource, id string) (*core.Note, bool) {
	if id == "" {
		r.skip(src, skipIncomplete)
		return nil, false
	}
	note, _, err := r.a.requireNoteAny(ctx, r.actor, id, core.AccessView)
	if err != nil {
		if isAccessErr(err) {
			r.skip(src, skipNotFound)
		} else {
			r.a.log.Warn("ai context note lookup failed", "noteId", id, "err", err)
			r.skip(src, skipUnreadable)
		}
		return nil, false
	}
	return note, true
}

// body reads the markdown body of a note the actor may view.
func (r *contextResolver) body(ctx context.Context, note *core.Note) (string, error) {
	space, ok := r.spaces[note.SpaceID]
	if !ok {
		var err error
		space, err = r.a.space(ctx, note.SpaceID)
		if err != nil {
			return "", err
		}
		r.spaces[note.SpaceID] = space
	}
	if err := r.a.noteBody(ctx, space, note); err != nil {
		return "", err
	}
	return note.Content, nil
}

// noteSource describes a visible note.
func noteSource(kind core.AIContextKind, note *core.Note) core.AISource {
	return core.AISource{Kind: kind, NoteID: note.ID, Title: note.Title, Path: note.Path}
}

// note resolves a whole note body.
func (r *contextResolver) note(ctx context.Context, ref core.AIContextRef) {
	note, ok := r.viewNote(ctx, refSource(ref), ref.NoteID)
	if !ok {
		return
	}
	src := noteSource(core.ContextNote, note)
	body, err := r.body(ctx, note)
	if err != nil {
		r.a.log.Warn("ai context note body unreadable", "noteId", note.ID, "err", err)
		r.skip(src, skipUnreadable)
		return
	}
	r.addText(src, body, 0, false)
}

// section resolves the part of a note under one heading.
func (r *contextResolver) section(ctx context.Context, ref core.AIContextRef) {
	note, ok := r.viewNote(ctx, refSource(ref), ref.NoteID)
	if !ok {
		return
	}
	src := noteSource(core.ContextSection, note)
	src.Title = note.Title + sectionTitleSeparator + headingDisplay(ref.Heading)
	if normalizeHeading(ref.Heading) == "" {
		r.skip(src, skipIncomplete)
		return
	}
	body, err := r.body(ctx, note)
	if err != nil {
		r.a.log.Warn("ai context note body unreadable", "noteId", note.ID, "err", err)
		r.skip(src, skipUnreadable)
		return
	}
	text, heading, found := findSection(body, ref.Heading)
	if !found {
		r.skip(src, skipHeading)
		return
	}
	src.Title = note.Title + sectionTitleSeparator + heading
	r.addText(src, text, 0, false)
}

// related adds one visible linked note or backlink, charged at most
// perRefChars. It reports false once the budget is spent.
func (r *contextResolver) related(ctx context.Context, kind core.AIContextKind, note *core.Note) bool {
	src := noteSource(kind, note)
	if r.spent {
		r.skip(src, skipBudget)
		return false
	}
	body, err := r.body(ctx, note)
	if err != nil {
		r.a.log.Warn("ai context related note unreadable", "noteId", note.ID, "err", err)
		r.skip(src, skipUnreadable)
		return true
	}
	r.addText(src, body, perRefChars, false)
	return !r.spent
}

// visible reports whether the actor may view a related note. A note the
// actor cannot view is dropped without a trace in the sources.
func (r *contextResolver) visible(ctx context.Context, note *core.Note) bool {
	if note == nil || note.Deleted {
		return false
	}
	grant, err := r.cache.note(ctx, note)
	if err != nil {
		r.a.log.Warn("ai context grant check failed", "err", err)
		return false
	}
	return grant.Access.AtLeast(core.AccessView)
}

// linked resolves the notes a note links to, one level deep.
func (r *contextResolver) linked(ctx context.Context, ref core.AIContextRef) {
	note, ok := r.viewNote(ctx, refSource(ref), ref.NoteID)
	if !ok {
		return
	}
	src := noteSource(core.ContextLinked, note)
	body, err := r.body(ctx, note)
	if err != nil {
		r.a.log.Warn("ai context note body unreadable", "noteId", note.ID, "err", err)
		r.skip(src, skipUnreadable)
		return
	}
	seen := map[string]bool{note.ID: true}
	added := 0
	for _, link := range r.a.rend.Links(body) {
		if added >= maxRelatedNotes {
			break
		}
		target := r.resolveLink(ctx, note, link.Target)
		if target == nil || seen[target.ID] {
			continue
		}
		seen[target.ID] = true
		if !r.visible(ctx, target) {
			continue
		}
		added++
		if !r.related(ctx, core.ContextLinked, target) {
			break
		}
	}
	if added == 0 {
		r.skip(src, skipNoLinks)
	}
}

// resolveLink finds the note a link target names, first as written and then
// relative to the directory of the linking note.
func (r *contextResolver) resolveLink(ctx context.Context, from *core.Note, target string) *core.Note {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	candidates := []string{target}
	if dir := path.Dir(from.Path); dir != "." && dir != "/" && !strings.HasPrefix(target, "/") {
		if joined := path.Join(dir, target); joined != target && !strings.HasPrefix(joined, "..") {
			candidates = append(candidates, joined)
		}
	}
	if cleaned := path.Clean(target); cleaned != target && !strings.HasPrefix(cleaned, "..") {
		candidates = append(candidates, cleaned)
	}
	for _, candidate := range candidates {
		note, err := r.a.db.ResolveLink(ctx, from.SpaceID, candidate)
		if err == nil && note != nil {
			return note
		}
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			r.a.log.Warn("ai context link resolution failed", "noteId", from.ID, "err", err)
		}
	}
	return nil
}

// backlinks resolves the notes that link to a note.
func (r *contextResolver) backlinks(ctx context.Context, ref core.AIContextRef) {
	note, ok := r.viewNote(ctx, refSource(ref), ref.NoteID)
	if !ok {
		return
	}
	src := noteSource(core.ContextBacklinks, note)
	links, err := r.a.db.Backlinks(ctx, note.ID)
	if err != nil {
		r.a.log.Warn("ai context backlinks failed", "noteId", note.ID, "err", err)
		r.skip(src, skipUnreadable)
		return
	}
	seen := map[string]bool{note.ID: true}
	added := 0
	for _, link := range links {
		if added >= maxRelatedNotes {
			break
		}
		if link == nil || link.FromNote == nil || seen[link.FromNote.ID] {
			continue
		}
		from := link.FromNote
		seen[from.ID] = true
		if !r.visible(ctx, from) {
			continue
		}
		added++
		if !r.related(ctx, core.ContextBacklinks, from) {
			break
		}
	}
	if added == 0 {
		r.skip(src, skipNoBacklinks)
	}
}

// readCapped reads at most limit bytes and reports whether more followed.
func readCapped(rd io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(rd, limit+1))
	if int64(len(data)) > limit {
		return data[:limit], true, err
	}
	return data, false, err
}

// file resolves a file: an image as an image, a PDF as its text and a text
// file as its content. Access follows OpenFile.
func (r *contextResolver) file(ctx context.Context, ref core.AIContextRef) {
	src := refSource(ref)
	if ref.FileID == "" {
		r.skip(src, skipIncomplete)
		return
	}
	f, _, rc, err := r.a.OpenFile(ctx, r.actor, ref.FileID, "")
	if err != nil {
		if isAccessErr(err) || errors.Is(err, core.ErrConflict) {
			r.skip(src, skipNotFound)
		} else {
			r.a.log.Warn("ai context file open failed", "fileId", ref.FileID, "err", err)
			r.skip(src, skipUnreadable)
		}
		return
	}
	defer rc.Close()
	src.Title, src.Path = f.Name, f.URL

	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(rc, head)
	head = head[:n]
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	if err != nil {
		r.a.log.Warn("ai context file read failed", "fileId", f.ID, "err", err)
		r.skip(src, skipUnreadable)
		return
	}
	rest := io.MultiReader(bytes.NewReader(head), rc)

	switch {
	case extract.ImageType(head) != "":
		r.image(src, f, rest)
	case extract.IsPDF(head):
		data, over, err := readCapped(rest, maxFileBytes)
		if err != nil {
			r.skip(src, skipUnreadable)
			return
		}
		if over {
			r.skip(src, skipFileTooLarge)
			return
		}
		pdfCtx, cancel := context.WithTimeout(ctx, pdfTimeout)
		text, truncated, err := extract.PDFText(pdfCtx, data, 0, maxFileTextBytes)
		cancel()
		if err != nil {
			r.a.log.Info("ai context PDF unreadable", "fileId", f.ID, "err", err)
			r.skip(src, skipUnreadable)
			return
		}
		r.addText(src, text, perRefChars, truncated)
	case extract.IsText(f.MIME, f.Name):
		data, over, err := readCapped(rest, maxFileTextBytes)
		if err != nil {
			r.skip(src, skipUnreadable)
			return
		}
		text, err := extract.DecodeText(data)
		if err != nil {
			r.skip(src, skipNotUTF8)
			return
		}
		r.addText(src, text, perRefChars, over)
	default:
		r.skip(src, skipUnsupported)
	}
}

// image adds an image file for a vision model.
func (r *contextResolver) image(src core.AISource, f *core.File, rd io.Reader) {
	switch {
	case !r.vision:
		r.skip(src, skipNoVision)
		return
	case len(r.images) >= maxContextImages:
		r.skip(src, skipImageLimit)
		return
	}
	data, over, err := readCapped(rd, maxFileBytes)
	if err != nil {
		r.skip(src, skipUnreadable)
		return
	}
	if over {
		r.skip(src, skipImageTooLarge)
		return
	}
	img, err := extract.NormalizeImage(data)
	if err != nil {
		if errors.Is(err, core.ErrTooLarge) {
			r.skip(src, skipImageTooLarge)
			return
		}
		r.a.log.Info("ai context image unreadable", "fileId", f.ID, "err", err)
		r.skip(src, skipUnreadable)
		return
	}
	r.images = append(r.images, core.AIImage{MediaType: img.MediaType, Data: img.Data, Name: f.Name})
	r.sources = append(r.sources, src)
}

// url fetches a web page under the address rules.
func (r *contextResolver) url(ctx context.Context, ref core.AIContextRef) {
	src := refSource(ref)
	src.Title = ref.URL
	if strings.TrimSpace(ref.URL) == "" {
		r.skip(src, skipIncomplete)
		return
	}
	if r.urls >= maxURLRefs {
		r.skip(src, skipTooManyURLs)
		return
	}
	r.urls++
	if r.fetcher == nil {
		r.fetcher = fetch.New(fetch.Options{AllowPrivate: r.a.cfg.AI.AllowPrivateURLs})
	}
	page, err := r.fetcher.Fetch(ctx, ref.URL)
	if err != nil {
		r.a.log.Info("ai context URL skipped", "err", err)
		r.skip(src, fetchSkipReason(err))
		return
	}
	if page.Title != "" {
		src.Title = page.Title
	}
	r.addText(src, page.Text, perRefChars, page.Truncated)
}

// fetchSkipReason maps a fetch error to a skip reason.
func fetchSkipReason(err error) string {
	switch {
	case errors.Is(err, fetch.ErrPrivateAddress):
		return skipPrivateURL
	case errors.Is(err, fetch.ErrScheme):
		return skipURLScheme
	case errors.Is(err, fetch.ErrTooManyRedirects):
		return skipRedirects
	case errors.Is(err, fetch.ErrContentType):
		return skipContentType
	case errors.Is(err, context.DeadlineExceeded):
		return skipTimeout
	default:
		return skipUnreachable
	}
}

// search resolves the best matching chunks of the space, by embedding
// similarity when an embedding provider exists and by full-text rank
// otherwise.
func (r *contextResolver) search(ctx context.Context, ref core.AIContextRef) {
	query := strings.TrimSpace(ref.Query)
	src := refSource(ref)
	src.Title = query
	if query == "" {
		r.skip(src, skipIncomplete)
		return
	}
	if r.spaceID == "" {
		r.skip(src, skipNoSpace)
		return
	}
	hits, err := r.a.retrieveContext(ctx, r.actor, r.spaceID, query)
	if err != nil && !errors.Is(err, errNoEmbeddings) {
		r.a.log.Warn("ai context semantic search failed, using full-text search", "err", err)
	}
	if err != nil || len(hits) == 0 {
		hits, err = r.textSearch(ctx, query)
		if err != nil {
			r.a.log.Warn("ai context full-text search failed", "err", err)
			r.skip(src, skipUnreadable)
			return
		}
	}
	if len(hits) == 0 {
		r.skip(src, skipNoMatches)
		return
	}
	for _, hit := range hits {
		if r.spent {
			r.skip(hit, skipBudget)
			return
		}
		r.addText(hit, hit.Excerpt, 0, false)
	}
}

// textSearch returns the top full-text hits of the space the actor may view,
// each as the chunk of the note body that best matches the query.
func (r *contextResolver) textSearch(ctx context.Context, query string) ([]core.AISource, error) {
	limit := r.a.cfg.AI.ContextChunks
	if limit <= 0 {
		limit = 8
	}
	spaces := []string{r.spaceID}
	hits, err := r.a.db.SearchNotes(ctx, spaces, core.SearchQuery{Text: query, SpaceIDs: spaces, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("search notes: %w", err)
	}
	hits, err = r.a.filterHits(ctx, r.actor, hits)
	if err != nil {
		return nil, err
	}
	out := make([]core.AISource, 0, len(hits))
	for _, hit := range hits {
		body, err := r.body(ctx, hit.Note)
		if err != nil {
			r.a.log.Debug("ai context search hit unreadable", "noteId", hit.Note.ID, "err", err)
			continue
		}
		src := noteSource(core.ContextSearch, hit.Note)
		src.Score = hit.Score
		src.Excerpt = bestChunk(r.a.rend.Chunks(body, embedChunkRunes), query)
		out = append(out, src)
	}
	return out, nil
}

// bestChunk returns the chunk holding the most query terms of three or more
// letters, the earliest on a tie, or the first chunk when none matches.
func bestChunk(chunks []string, query string) string {
	if len(chunks) == 0 {
		return ""
	}
	var terms []string
	for _, field := range strings.Fields(strings.ToLower(query)) {
		field = strings.Trim(field, ".,;:!?\"'()[]{}")
		if utf8.RuneCountInString(field) >= 3 {
			terms = append(terms, field)
		}
	}
	best, bestScore := 0, 0
	for i, chunk := range chunks {
		lower := strings.ToLower(chunk)
		score := 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return chunks[best]
}

// publicSources copies sources for the client with each excerpt cut to a
// short preview.
func publicSources(in []core.AISource) []core.AISource {
	if len(in) == 0 {
		return nil
	}
	out := make([]core.AISource, len(in))
	for i, src := range in {
		src.Excerpt = truncateRunes(src.Excerpt, sourceExcerptRunes)
		out[i] = src
	}
	return out
}

// headingLine is one heading found in a markdown body.
type headingLine struct {
	line  int
	level int
	text  string
}

// findSection returns the lines under the heading that matches heading,
// ignoring case, # marks and emphasis, from the heading line down to the next
// heading of the same or a higher level. It also returns the heading text as
// written. ATX and setext headings count, and lines inside fenced code blocks
// are never headings.
func findSection(body, heading string) (string, string, bool) {
	want := normalizeHeading(heading)
	if want == "" {
		return "", "", false
	}
	lines := strings.SplitAfter(body, "\n")
	headings := scanHeadings(lines)
	for i, h := range headings {
		if normalizeHeading(h.text) != want {
			continue
		}
		end := len(lines)
		for _, next := range headings[i+1:] {
			if next.level <= h.level {
				end = next.line
				break
			}
		}
		return strings.TrimSpace(strings.Join(lines[h.line:end], "")), headingDisplay(h.text), true
	}
	return "", "", false
}

// scanHeadings lists the headings of a body split into lines.
func scanHeadings(lines []string) []headingLine {
	var out []headingLine
	fence := ""
	paragraph := false
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r\n")
		trimmed := strings.TrimLeft(line, " ")
		indented := len(line)-len(trimmed) > 3
		if fence != "" {
			if !indented && closesFence(trimmed, fence) {
				fence = ""
			}
			paragraph = false
			continue
		}
		if !indented {
			if open := fenceMarker(trimmed); open != "" {
				fence = open
				paragraph = false
				continue
			}
			if level, text, ok := atxHeading(trimmed); ok {
				out = append(out, headingLine{line: i, level: level, text: text})
				paragraph = false
				continue
			}
			if paragraph {
				if level := setextLevel(trimmed); level > 0 {
					out = append(out, headingLine{line: i - 1, level: level, text: strings.TrimSpace(lines[i-1])})
					paragraph = false
					continue
				}
			}
		}
		paragraph = strings.TrimSpace(line) != "" && !indented && !startsBlock(trimmed)
	}
	return out
}

// fenceMarker returns the backtick or tilde run that opens a fenced code
// block, or the empty string.
func fenceMarker(line string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n >= 3 {
			if c == '`' && strings.Contains(line[n:], "`") {
				return ""
			}
			return line[:n]
		}
	}
	return ""
}

// closesFence reports whether line closes a block opened with fence.
func closesFence(line, fence string) bool {
	n := 0
	for n < len(line) && line[n] == fence[0] {
		n++
	}
	return n >= len(fence) && strings.TrimSpace(line[n:]) == ""
}

// atxHeading parses "## Heading ##" and returns the level and the text.
func atxHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	rest := line[level:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	text := strings.TrimSpace(rest)
	if trimmed := strings.TrimRight(text, "#"); trimmed != text && (trimmed == "" || strings.HasSuffix(trimmed, " ") || strings.HasSuffix(trimmed, "\t")) {
		text = strings.TrimSpace(trimmed)
	}
	return level, text, true
}

// setextLevel returns 1 for an "===" underline, 2 for "---", and 0 otherwise.
func setextLevel(line string) int {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0
	}
	switch {
	case strings.Trim(line, "=") == "":
		return 1
	case strings.Trim(line, "-") == "":
		return 2
	default:
		return 0
	}
}

// startsBlock reports whether a line opens a list item, quote or table row,
// after which an underline is not a setext heading.
func startsBlock(line string) bool {
	if strings.HasPrefix(line, ">") || strings.HasPrefix(line, "|") {
		return true
	}
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && (line[1] == ' ' || line[1] == '\t') {
		return true
	}
	digits := 0
	for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < len(line) && (line[digits] == '.' || line[digits] == ')')
}

// headingDisplay strips # marks and emphasis characters and collapses spaces.
func headingDisplay(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#")
	s = strings.TrimRight(strings.TrimSpace(s), "#")
	s = strings.NewReplacer("*", "", "_", "", "`", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// normalizeHeading is headingDisplay in lower case, for matching.
func normalizeHeading(s string) string {
	return strings.ToLower(headingDisplay(s))
}
