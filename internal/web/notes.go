package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// tocMinHeadings is how many headings a note needs before the table of
// contents appears.
const tocMinHeadings = 3

// attachSpace loads the space named by slug into the page, along with the
// actor's access and the sidebar note tree. It returns the backend error when
// the space is missing or closed to the actor.
func (s *Server) attachSpace(r *http.Request, p *Page, slug string) error {
	ctx := r.Context()
	space, err := s.backend.GetSpace(ctx, p.Actor, slug)
	if err != nil {
		return err
	}
	p.Space = space
	p.SpaceAccess = space.MyRole.Access()
	if grant, err := s.backend.SpaceAccess(ctx, p.Actor, space.ID); err == nil && grant != nil {
		p.SpaceAccess = grant.Access
	}
	if tree, err := s.backend.SpaceTree(ctx, p.Actor, space.ID); err == nil {
		p.Tree = tree
	} else {
		s.log.Debug("web: space tree unavailable", "space", space.ID, "err", err)
	}
	return nil
}

// expandedFor marks every folder on the path to itemID, and the row of the
// item itself, so the sidebar opens the branch holding the current item. A
// folder whose folder note is itemID counts as the item's row.
func expandedFor(tree []*core.TreeItem, itemID string) map[string]bool {
	out := map[string]bool{}
	if itemID == "" {
		return out
	}
	var walk func(nodes []*core.TreeItem, depth int) bool
	walk = func(nodes []*core.TreeItem, depth int) bool {
		if depth > 256 {
			return false
		}
		for _, node := range nodes {
			id := treeID(node)
			if id == "" {
				continue
			}
			if id == itemID || (node.Folder != nil && node.Folder.IndexNoteID == itemID) {
				out[id] = true
				return true
			}
			if walk(node.Children, depth+1) {
				out[id] = true
				return true
			}
		}
		return false
	}
	walk(tree, 0)
	return out
}

// crumbsFor builds the breadcrumb trail of a note from its path. Ancestor
// labels come from the path segments, because the titles are not loaded.
func crumbsFor(space *core.Space, note *core.Note) []Crumb {
	if note == nil {
		return nil
	}
	clean := strings.Trim(strings.TrimSuffix(note.Path, ".md"), "/")
	if clean == "" {
		return nil
	}
	segments := strings.Split(clean, "/")
	out := make([]Crumb, 0, len(segments))
	for i, segment := range segments {
		crumb := Crumb{Name: unslug(segment)}
		if i < len(segments)-1 {
			crumb.URL = noteURL(space, strings.Join(segments[:i+1], "/"))
		} else {
			crumb.Name = note.Title
		}
		out = append(out, crumb)
	}
	return out
}

// unslug turns a path segment back into a readable label.
func unslug(segment string) string {
	s := strings.ReplaceAll(strings.ReplaceAll(segment, "-", " "), "_", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return segment
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// handleSpaceHome renders the landing page of one space, with the top-level
// notes, the recently changed notes and the tag cloud.
func (s *Server) handleSpaceHome(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	p := s.newPage(r, "", "space")
	p.Actor, p.User, p.IsAdmin = actor, actor.User, actor.IsAdmin()
	if err := s.attachSpace(r, p, r.PathValue("space")); err != nil {
		s.fail(w, r, err)
		return
	}
	p.Title = p.Space.Name
	roots, err := s.backend.ListNotes(ctx, actor, p.Space.ID, core.NoteListOptions{
		ParentID: "", HasParent: true, Sort: "position", Limit: 200,
	})
	if err != nil {
		s.log.Debug("web: root note listing failed", "space", p.Space.ID, "err", err)
	}
	p.Set("Roots", roots)
	if recent, err := s.backend.ListNotes(ctx, actor, p.Space.ID, core.NoteListOptions{
		Sort: "updated", Limit: 10,
	}); err == nil {
		p.Set("Recent", recent)
	}
	if tags, err := s.backend.ListTags(ctx, actor, p.Space.ID); err == nil {
		p.Set("Tags", tags)
	}
	s.Render(w, r, http.StatusOK, "space/home", p)
}

// handleTag lists every note in a space carrying one tag.
func (s *Server) handleTag(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	tag := r.PathValue("tag")
	p := s.newPage(r, "#"+tag, "space")
	if err := s.attachSpace(r, p, r.PathValue("space")); err != nil {
		s.fail(w, r, err)
		return
	}
	notes, err := s.backend.ListNotes(r.Context(), actor, p.Space.ID, core.NoteListOptions{
		Tag: tag, Sort: "updated", Limit: 200,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Tag", tag)
	p.Set("Notes", notes)
	if tags, err := s.backend.ListTags(r.Context(), actor, p.Space.ID); err == nil {
		p.Set("Tags", tags)
	}
	s.Render(w, r, http.StatusOK, "tag", p)
}

// handleNote renders one note, or the editor when the edit query parameter is
// set. Every control is gated on the grant the backend returned.
func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	slug := r.PathValue("space")
	notePath := strings.TrimSuffix(r.PathValue("path"), ".md")

	p := s.newPage(r, "", "space")
	if err := s.attachSpace(r, p, slug); err != nil {
		s.fail(w, r, err)
		return
	}
	note, grant, err := s.backend.GetNoteByPath(ctx, actor, slug, notePath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Note, p.Grant = note, grant
	p.Title = note.Title
	p.Expanded = expandedFor(p.Tree, note.ID)
	p.Crumbs = crumbsFor(p.Space, note)

	if note.UpdatedBy != "" {
		if author, err := s.backend.GetUser(ctx, actor, note.UpdatedBy); err == nil {
			p.Set("Author", author)
		}
	}
	if r.URL.Query().Get("edit") != "" {
		s.renderEditor(w, r, p)
		return
	}
	rendered, err := s.backend.RenderNote(ctx, actor, note)
	if err != nil {
		s.log.Warn("web: note render failed", "note", note.ID, "err", err)
		rendered = &core.Rendered{}
	}
	p.Set("Rendered", rendered)
	p.Set("ShowTOC", len(rendered.TOC) >= tocMinHeadings)
	if c := confirmationFor(r.URL.Query()); c != nil {
		p.Set("Confirm", c)
	}

	if children, err := s.backend.ListNotes(ctx, actor, note.SpaceID, core.NoteListOptions{
		ParentID: note.ID, HasParent: true, Sort: "position", Limit: 200,
	}); err == nil {
		p.Set("Children", children)
	}
	if backlinks, err := s.backend.Backlinks(ctx, actor, note.ID); err == nil {
		p.Set("Backlinks", backlinks)
	}
	if comments, err := s.backend.ListComments(ctx, actor, note.ID); err == nil {
		p.Set("Comments", comments)
	}
	if hasAccess(grant, core.AccessManage) {
		if shares, err := s.backend.ListShares(ctx, actor, note.ID); err == nil {
			p.Set("Shares", shares)
		}
		if links, err := s.backend.ListPublicLinks(ctx, actor, note.ID); err == nil {
			p.Set("Links", links)
		}
	}
	s.Render(w, r, http.StatusOK, "note", p)
}

// renderEditor renders the markdown editor for a note the actor may change.
func (s *Server) renderEditor(w http.ResponseWriter, r *http.Request, p *Page) {
	if !hasAccess(p.Grant, core.AccessEdit) {
		s.fail(w, r, core.ErrForbidden)
		return
	}
	ctx := r.Context()
	p.Title = "Editing " + p.Note.Title
	p.Set("Editing", true)
	if p.Settings == nil || p.Settings.AIEnabled {
		if providers, err := s.backend.ListAIProviders(ctx, p.Actor); err == nil {
			p.Set("AIProviders", providers)
		}
		p.Set("AITasks", core.AITasks)
	}
	if tags, err := s.backend.ListTags(ctx, p.Actor, p.Note.SpaceID); err == nil {
		p.Set("Tags", tags)
	}
	s.Render(w, r, http.StatusOK, "editor", p)
}

// handleNotePost is the scripting-off fallback for the note page. The action
// field selects between saving the editor form, posting a comment, starring,
// unstarring and trashing.
func (s *Server) handleNotePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	slug := r.PathValue("space")
	if r.PostFormValue("action") == "restore" {
		// The Undo of Move to trash posts here, after the note left its path.
		s.restoreNoteForm(w, r, actor, slug)
		return
	}
	notePath := strings.TrimSuffix(r.PathValue("path"), ".md")
	note, grant, err := s.backend.GetNoteByPath(ctx, actor, slug, notePath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	back := noteURL(slug, note)

	switch r.PostFormValue("action") {
	case "comment":
		if !hasAccess(grant, core.AccessComment) {
			s.fail(w, r, core.ErrForbidden)
			return
		}
		in := core.CommentInput{
			NoteID:   note.ID,
			ParentID: strings.TrimSpace(r.PostFormValue("parentId")),
			Body:     r.PostFormValue("body"),
			Anchor:   anchorFromForm(r.PostFormValue("anchor")),
		}
		created, err := s.backend.CreateComment(ctx, actor, in)
		if err != nil {
			s.formFail(w, r, back+"#comments", err, "The comment could not be posted.")
			return
		}
		target := back + "#comments"
		if created != nil && created.ID != "" {
			target = back + "#comment-" + created.ID
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	case "comment-resolve":
		if !hasAccess(grant, core.AccessComment) {
			s.fail(w, r, core.ErrForbidden)
			return
		}
		id := strings.TrimSpace(r.PostFormValue("commentId"))
		if _, err := s.backend.ResolveComment(ctx, actor, id, formBool(r, "resolved")); err != nil {
			s.formFail(w, r, back+"#comments", err, "The thread could not be updated.")
			return
		}
		http.Redirect(w, r, back+"#comment-"+url.PathEscape(id), http.StatusSeeOther)
	case "comment-delete":
		if !hasAccess(grant, core.AccessComment) {
			s.fail(w, r, core.ErrForbidden)
			return
		}
		id := strings.TrimSpace(r.PostFormValue("commentId"))
		if s.needsConfirmation(w, r, back, "comment-delete", id) {
			return
		}
		if err := s.backend.DeleteComment(ctx, actor, id); err != nil {
			s.formFail(w, r, back+"#comments", err, "The comment could not be deleted.")
			return
		}
		http.Redirect(w, r, back+"#comments", http.StatusSeeOther)
	case "star", "unstar":
		starred := r.PostFormValue("action") == "star"
		if err := s.backend.SetStar(ctx, actor, note.ID, starred); err != nil {
			s.fail(w, r, err)
			return
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	case "trash":
		if !hasAccess(grant, core.AccessEdit) {
			s.fail(w, r, core.ErrForbidden)
			return
		}
		if err := s.backend.TrashNote(ctx, actor, note.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		http.Redirect(w, r, spaceURL(slug), http.StatusSeeOther)
	case "share-add", "share-remove", "link-create", "link-delete":
		if !hasAccess(grant, core.AccessManage) {
			s.fail(w, r, core.ErrForbidden)
			return
		}
		if r.PostFormValue("action") == "link-delete" && s.needsConfirmation(w, r, back, "link-delete", strings.TrimSpace(r.PostFormValue("linkId"))) {
			return
		}
		if err := s.shareFromForm(r, actor, note, r.PostFormValue("action")); err != nil {
			s.formFail(w, r, back+"#share", err, "The sharing change could not be saved.")
			return
		}
		http.Redirect(w, r, back+"#share", http.StatusSeeOther)
	case "child":
		if !hasAccess(grant, core.AccessEdit) {
			s.fail(w, r, core.ErrForbidden)
			return
		}
		child, err := s.backend.CreateNote(ctx, actor, core.NoteInput{
			SpaceID:  note.SpaceID,
			ParentID: note.ID,
			Title:    orDefault(strings.TrimSpace(r.PostFormValue("title")), "Untitled"),
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		http.Redirect(w, r, noteURL(slug, child)+"?edit=1", http.StatusSeeOther)
	default:
		if !hasAccess(grant, core.AccessEdit) {
			s.fail(w, r, core.ErrForbidden)
			return
		}
		s.saveNoteForm(w, r, actor, note, slug)
	}
}

// saveNoteForm applies the plain editor form. It sends BaseUpdatedAt so a
// concurrent change is reported as a conflict rather than overwritten, unless
// the form asked to overwrite.
func (s *Server) saveNoteForm(w http.ResponseWriter, r *http.Request, actor *core.Actor, note *core.Note, slug string) {
	title := strings.TrimSpace(r.PostFormValue("title"))
	content := strings.ReplaceAll(r.PostFormValue("content"), "\r\n", "\n")
	tags := splitTags(r.PostFormValue("tags"))
	icon := strings.TrimSpace(r.PostFormValue("icon"))

	update := core.NoteUpdate{Content: &content, Tags: &tags, Icon: &icon}
	if title != "" {
		update.Title = &title
	}
	if !formBool(r, "overwrite") {
		base := note.UpdatedAt
		if raw := strings.TrimSpace(r.PostFormValue("baseUpdatedAt")); raw != "" {
			if parsed, err := parseStamp(raw); err == nil {
				base = parsed
			}
		}
		update.BaseUpdatedAt = &base
	}
	saved, err := s.backend.UpdateNote(r.Context(), actor, note.ID, update)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	target := note
	if saved != nil {
		target = saved
	}
	http.Redirect(w, r, noteURL(slug, target), http.StatusSeeOther)
}

// handleTrash lists the trashed notes of a space.
func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "Trash", "trash")
	if err := s.attachSpace(r, p, r.PathValue("space")); err != nil {
		s.fail(w, r, err)
		return
	}
	notes, err := s.backend.ListTrash(r.Context(), actor, p.Space.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Set("Notes", notes)
	s.Render(w, r, http.StatusOK, "trash", p)
}

// handleTrashPost restores one note, deletes one note forever or empties the
// whole trash. It is the scripting-off fallback for the trash page.
func (s *Server) handleTrashPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	slug := r.PathValue("space")
	space, err := s.backend.GetSpace(ctx, actor, slug)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noteID := strings.TrimSpace(r.PostFormValue("noteId"))
	action := r.PostFormValue("action")
	// Deleting forever and emptying the trash cannot be undone. With scripting
	// off the request renders a confirmation page first.
	if action != "restore" && needsConfirm(r) {
		c, err := s.trashConfirm(ctx, actor, space, action, noteID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.renderConfirm(w, r, "trash", space, c)
		return
	}
	switch action {
	case "restore":
		err = s.backend.RestoreNote(ctx, actor, noteID)
	case "delete":
		err = s.backend.DeleteNote(ctx, actor, noteID)
	case "empty":
		err = s.backend.EmptyTrash(ctx, actor, space.ID)
	default:
		err = core.Invalid("action", "unknown trash action")
	}
	if err != nil {
		s.formFail(w, r, spaceURL(space)+"/trash", err, "That change could not be saved.")
		return
	}
	http.Redirect(w, r, spaceURL(space)+"/trash", http.StatusSeeOther)
}

// handlePermalink resolves a note id and redirects to the canonical path.
func (s *Server) handlePermalink(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	note, _, err := s.backend.GetNote(ctx, actor, r.PathValue("noteId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slug := note.SpaceSlug
	if slug == "" {
		space, err := s.backend.GetSpace(ctx, actor, note.SpaceID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		slug = space.Slug
	}
	http.Redirect(w, r, noteURL(slug, note), http.StatusSeeOther)
}

// handleItemPermalink resolves an item id of any kind and redirects to its
// canonical view.
func (s *Server) handleItemPermalink(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	item, err := s.backend.GetItem(ctx, actor, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slug := ""
	switch {
	case item.Note != nil:
		slug = item.Note.SpaceSlug
	case item.Folder != nil:
		slug = item.Folder.SpaceSlug
	case item.File != nil:
		slug = item.File.SpaceSlug
	}
	if slug == "" {
		space, err := s.backend.GetSpace(ctx, actor, item.SpaceID())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		slug = space.Slug
	}
	var dest string
	switch {
	case item.Note != nil:
		dest = noteURL(slug, item.Note)
	case item.Folder != nil:
		dest = entryURL(slug, "d", item.Folder.Path)
	case item.File != nil:
		dest = entryURL(slug, "f", item.File.Path)
	default:
		s.fail(w, r, core.ErrNotFound)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// parseStamp reads an RFC 3339 timestamp from a form field.
func parseStamp(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, raw)
}

// handleSpaceHomePost creates a top-level note in the space. It backs the
// quick-create button when scripting is off, because the JSON API is not
// reachable from a plain form.
func (s *Server) handleSpaceHomePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	slug := r.PathValue("space")
	space, err := s.backend.GetSpace(ctx, actor, slug)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	note, err := s.backend.CreateNote(ctx, actor, core.NoteInput{
		SpaceID: space.ID,
		Title:   orDefault(strings.TrimSpace(r.PostFormValue("title")), "Untitled"),
		Tags:    splitTags(r.PostFormValue("tags")),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, noteURL(space, note)+"?edit=1", http.StatusSeeOther)
}

// shareFromForm applies a share or public link action from the note page, so
// the share panel works with scripting off.
func (s *Server) shareFromForm(r *http.Request, actor *core.Actor, note *core.Note, action string) error {
	ctx := r.Context()
	switch action {
	case "share-add":
		access, ok := core.ParseAccess(strings.TrimSpace(r.PostFormValue("access")))
		if !ok {
			access = core.AccessView
		}
		_, err := s.backend.CreateShare(ctx, actor, core.ShareInput{
			NoteID:          note.ID,
			Email:           strings.TrimSpace(r.PostFormValue("email")),
			UserID:          strings.TrimSpace(r.PostFormValue("userId")),
			Access:          access,
			IncludeChildren: formBool(r, "includeChildren"),
		})
		return err
	case "share-remove":
		return s.backend.DeleteShare(ctx, actor, strings.TrimSpace(r.PostFormValue("shareId")))
	case "link-create":
		access, ok := core.ParseAccess(strings.TrimSpace(r.PostFormValue("access")))
		if !ok {
			access = core.AccessView
		}
		in := core.PublicLinkInput{
			NoteID:          note.ID,
			Access:          access,
			IncludeChildren: formBool(r, "includeChildren"),
		}
		if pw := r.PostFormValue("password"); pw != "" {
			in.Password = &pw
		}
		_, err := s.backend.CreatePublicLink(ctx, actor, in)
		return err
	case "link-delete":
		return s.backend.DeletePublicLink(ctx, actor, strings.TrimSpace(r.PostFormValue("linkId")))
	}
	return core.Invalid("action", "unknown share action")
}

// anchorFromForm reads the optional quoted passage of a comment posted without
// JavaScript. It carries no offsets, so the server finds the text by search.
func anchorFromForm(v string) *core.CommentAnchor {
	if v = strings.TrimSpace(v); v == "" {
		return nil
	}
	return &core.CommentAnchor{Exact: v}
}

// noteConfirm is the confirmation step of a permanent note action in a
// browser without scripting. With scripting, app.js asks in a
// ConfirmationDialog and the form posts without the nojs field.
type noteConfirm struct {
	Action string
	Field  string
	Target string
	Title  string
	Body   string
	Label  string
}

// noteConfirmations lists the permanent note actions that ask first
// (docs/primer.md 6.2). Field names the form field that carries the target id.
var noteConfirmations = map[string]noteConfirm{
	"comment-delete": {Field: "commentId", Title: "Delete comment?", Body: "The comment is removed for everyone. This cannot be undone.", Label: "Delete"},
	"link-delete":    {Field: "linkId", Title: "Revoke public link?", Body: "People who have the link can no longer open it. This cannot be undone.", Label: "Revoke link"},
}

// confirmationFor returns the confirmation step that the confirm and target
// query values name, or nil.
func confirmationFor(q url.Values) *noteConfirm {
	action := q.Get("confirm")
	c, ok := noteConfirmations[action]
	target := strings.TrimSpace(q.Get("target"))
	if !ok || target == "" {
		return nil
	}
	c.Action, c.Target = action, target
	return &c
}

// needsConfirmation redirects to the confirmation step of a permanent action
// that came from a page without scripting and carries no confirmed field.
// The nojs field sits in a noscript element, so only such a page sends it.
// It reports whether it redirected.
func (s *Server) needsConfirmation(w http.ResponseWriter, r *http.Request, back, action, target string) bool {
	if r.PostFormValue("nojs") == "" || r.PostFormValue("confirmed") != "" || target == "" {
		return false
	}
	v := url.Values{"confirm": {action}, "target": {target}}
	http.Redirect(w, r, withQuery(back+"#confirm", v), http.StatusSeeOther)
	return true
}

// restoreNoteForm brings a trashed note back and opens it. It is the inverse
// of Move to trash, which the Undo Banner of app.js posts to the note
// address. Without the note it opens the space.
func (s *Server) restoreNoteForm(w http.ResponseWriter, r *http.Request, actor *core.Actor, slug string) {
	ctx := r.Context()
	id := strings.TrimSpace(r.PostFormValue("noteId"))
	if id == "" {
		s.fail(w, r, core.Invalid("noteId", "is required"))
		return
	}
	if err := s.backend.RestoreNote(ctx, actor, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if note, _, err := s.backend.GetNote(ctx, actor, id); err == nil && note != nil {
		http.Redirect(w, r, noteURL(slug, note), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, spaceURL(slug), http.StatusSeeOther)
}
