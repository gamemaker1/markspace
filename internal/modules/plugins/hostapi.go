package plugins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// APIPrefix is the path prefix of the host API.
const APIPrefix = "/api/plugin/v1"

// Host API limits.
const (
	maxIdempotencyKey  = 128
	maxRecordedBody    = 1 << 20
	maxUploadBytes     = 1 << 30
	maxMaskBytes       = 20 << 20
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

// hostHandler serves one host API route for an authenticated plugin actor.
type hostHandler func(w http.ResponseWriter, r *http.Request, actor *core.Actor)

// registerHostAPI adds the plugin host API under /api/plugin/v1/.
func (m *Module) registerHostAPI(r *module.Registry) {
	route := func(method, path, perm string, h hostHandler) {
		r.API(method+" "+APIPrefix+path, m.hostRoute(perm, h))
	}
	route("GET", "/me", "", m.me)
	route("GET", "/settings", "", m.settings)
	route("GET", "/secrets/{name}", "", m.secret)

	route("GET", "/spaces/{id}/tree", "items:read", m.tree)
	route("GET", "/items/{id}", "items:read", m.item)
	route("GET", "/files/{id}/content", "items:read", m.fileContent)
	route("GET", "/files/{id}/text", "items:read", m.fileText)
	route("POST", "/notes", "items:write", m.createNote)
	route("PATCH", "/notes/{id}", "items:write", m.updateNote)
	route("POST", "/folders", "items:write", m.createFolder)
	route("POST", "/files", "items:write", m.uploadFile)
	route("POST", "/items/{id}/move", "items:write", m.moveItem)
	route("POST", "/items/{id}/copy", "items:write", m.copyItem)
	route("POST", "/items/{id}/trash", "items:write", m.trashItem)

	route("GET", "/notes/{id}/links", "links:read", m.links)
	route("GET", "/items/{id}/backlinks", "links:read", m.backlinks)
	route("POST", "/resolve", "links:read", m.resolve)
	route("GET", "/search", "search", m.search)
	route("POST", "/comments", "comments", m.createComment)
	route("POST", "/suggestions", "suggestions", m.createSuggestion)
	route("GET", "/suggestions", "suggestions", m.listSuggestions)
	route("DELETE", "/suggestions/{id}", "suggestions", m.withdrawSuggestion)

	route("POST", "/ai/complete", "ai:complete", m.aiComplete)
	route("POST", "/ai/embed", "ai:embed", m.aiEmbed)
	route("POST", "/ai/image", "ai:image", m.aiImage)
	route("GET", "/ai/models", "ai:*", m.aiModels)
	route("POST", "/web/fetch", "web:fetch", m.webFetch)

	route("POST", "/jobs", "jobs", m.createJob)
	route("PATCH", "/jobs/{id}", "jobs", m.updateJob)
	route("POST", "/jobs/{id}/log", "jobs", m.jobLog)

	route("GET", "/kv", "storage", m.kvList)
	route("GET", "/kv/{key}", "storage", m.kvGet)
	route("PUT", "/kv/{key}", "storage", m.kvPut)
	route("DELETE", "/kv/{key}", "storage", m.kvDelete)
	route("PUT", "/blobs/{key}", "storage", m.blobPut)
	route("GET", "/blobs/{key}", "storage", m.blobGet)
	route("DELETE", "/blobs/{key}", "storage", m.blobDelete)

	route("POST", "/notify", "ui", m.notify)
	route("POST", "/events", "events", m.emit)
}

// allowsGroup reports whether a plugin holds a permission group. "ai:*"
// accepts any AI group, and an empty group needs none.
func allowsGroup(p *core.PluginIdentity, perm string) bool {
	switch perm {
	case "":
		return true
	case "ai:*":
		return p.Allows("ai:complete") || p.Allows("ai:embed") || p.Allows("ai:vision") || p.Allows("ai:image")
	}
	return p.Allows(perm)
}

// hostRoute authenticates a plugin actor, checks the permission group,
// applies the per-plugin rate limit and handles Idempotency-Key on POST.
func (m *Module) hostRoute(perm string, h hostHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		actor := api.ActorFrom(ctx)
		if actor.Plugin == nil {
			m.fail(w, r, fmt.Errorf("a plugin token or an acting token is required: %w", core.ErrUnauthorized))
			return
		}
		if m.opts.Host == nil {
			m.fail(w, r, fmt.Errorf("the plugin host is not running: %w", core.ErrUnavailable))
			return
		}
		if !allowsGroup(actor.Plugin, perm) {
			m.fail(w, r, fmt.Errorf("the plugin lacks the %s permission: %w", perm, core.ErrForbidden))
			return
		}
		pid := actor.Plugin.ID
		if ok, retry := m.opts.Host.Allow(pid); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
			m.fail(w, r, fmt.Errorf("too many requests: %w", core.ErrRateLimited))
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if r.Method != http.MethodPost || key == "" {
			h(w, r, actor)
			return
		}
		if len(key) > maxIdempotencyKey {
			m.fail(w, r, core.Invalid("Idempotency-Key", "is longer than 128 characters"))
			return
		}
		stored, err := m.opts.Host.BeginIdempotent(ctx, pid, key, r.Method, r.URL.Path)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		if stored != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(stored.Status)
			_, _ = w.Write(stored.Body)
			return
		}
		rec := &recorder{ResponseWriter: w}
		finished := false
		defer func() {
			if finished {
				return
			}
			// A panic leaves the key free for a retry.
			_ = m.opts.Host.ReleaseIdempotent(context.WithoutCancel(ctx), pid, key)
		}()
		h(rec, r, actor)
		finished = true
		bg := context.WithoutCancel(ctx)
		if rec.keep() {
			if err := m.opts.Host.CompleteIdempotent(bg, pid, key, rec.status, rec.buf.Bytes()); err != nil {
				m.log.Warn("store idempotent response failed", "plugin", pid, "err", err)
			}
			return
		}
		if err := m.opts.Host.ReleaseIdempotent(bg, pid, key); err != nil {
			m.log.Warn("release idempotency key failed", "plugin", pid, "err", err)
		}
	}
}

// recorder captures a response for replay while it writes it.
type recorder struct {
	http.ResponseWriter
	status   int
	buf      bytes.Buffer
	overflow bool
	stream   bool
}

func (rec *recorder) WriteHeader(code int) {
	if rec.status == 0 {
		rec.status = code
		rec.stream = strings.Contains(rec.Header().Get("Content-Type"), "event-stream")
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.WriteHeader(http.StatusOK)
	}
	if !rec.overflow && rec.buf.Len()+len(b) <= maxRecordedBody {
		rec.buf.Write(b)
	} else {
		rec.overflow = true
	}
	return rec.ResponseWriter.Write(b)
}

// Unwrap exposes the writer to http.ResponseController for flushing.
func (rec *recorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// keep reports whether a response may be replayed: a complete, non-streamed
// answer that is not a server error or a rate limit.
func (rec *recorder) keep() bool {
	return rec.status >= 200 && rec.status < 500 && rec.status != http.StatusTooManyRequests && !rec.overflow && !rec.stream
}

// page is the list envelope of the host API.
type page struct {
	Items      any    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func items[T any](list []T) page {
	if list == nil {
		list = []T{}
	}
	return page{Items: list}
}

// me serves GET /me.
func (m *Module) me(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	p := actor.Plugin
	grants := []map[string]string{}
	for _, id := range p.SpaceIDs() {
		grants = append(grants, map[string]string{"spaceId": id, "role": string(p.Grants[id])})
	}
	out := map[string]any{
		"pluginId": p.ID, "name": p.Name, "userId": p.UserID, "status": p.Status,
		"grants": grants, "permissions": nonNilStrings(p.Permissions),
	}
	if actor.Via != "" {
		out["actingFor"] = map[string]string{"id": actor.User.ID, "name": actor.User.Name}
	}
	api.WriteJSON(w, http.StatusOK, out)
}

func nonNilStrings(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// settings serves GET /settings.
func (m *Module) settings(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	raw, err := m.opts.Host.Settings(r.Context(), actor.Plugin.ID)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]json.RawMessage{"settings": raw})
}

// secret serves GET /secrets/{name}.
func (m *Module) secret(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	name := r.PathValue("name")
	v, err := m.opts.Host.Secret(r.Context(), actor.Plugin.ID, name)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"name": name, "value": v})
}

// tree serves GET /spaces/{id}/tree.
func (m *Module) tree(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	tree, err := m.opts.Backend.SpaceTree(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, items(tree))
}

// noteView is a note with its frontmatter as one object.
type noteView struct {
	*core.Note
	Frontmatter map[string]any `json:"frontmatter"`
}

func viewNote(n *core.Note) *noteView {
	if n == nil {
		return nil
	}
	fm := map[string]any{}
	for k, v := range n.Extra {
		fm[k] = v
	}
	fm["title"] = n.Title
	if n.Icon != "" {
		fm["icon"] = n.Icon
	}
	if len(n.Tags) > 0 {
		fm["tags"] = n.Tags
	}
	return &noteView{Note: n, Frontmatter: fm}
}

// itemView is an item whose note carries its body and frontmatter.
type itemView struct {
	Kind   core.ItemKind `json:"kind"`
	Folder *core.Folder  `json:"folder,omitempty"`
	Note   *noteView     `json:"note,omitempty"`
	File   *core.File    `json:"file,omitempty"`
	Access core.Access   `json:"access"`
}

// item serves GET /items/{id}. A note comes with its body.
func (m *Module) item(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	ctx := r.Context()
	id := r.PathValue("id")
	it, err := m.opts.Backend.GetItem(ctx, actor, id)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	v := itemView{Kind: it.Kind, Folder: it.Folder, File: it.File, Access: it.Access}
	if it.Kind == core.KindNote {
		note, _, err := m.opts.Backend.GetNote(ctx, actor, id)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		v.Note = viewNote(note)
	}
	api.WriteJSON(w, http.StatusOK, v)
}

// fileContent serves GET /files/{id}/content with Range support.
func (m *Module) fileContent(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	f, _, rc, err := m.opts.Backend.OpenFile(r.Context(), actor, r.PathValue("id"), "")
	if err != nil {
		m.fail(w, r, err)
		return
	}
	defer rc.Close()
	mt := f.MIME
	if mt == "" {
		mt = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if f.SHA256 != "" {
		w.Header().Set("ETag", `"`+f.SHA256+`"`)
	}
	http.ServeContent(w, r, "", f.ModTime, rc)
}

// fileText serves GET /files/{id}/text.
func (m *Module) fileText(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	text, truncated, err := m.opts.Backend.FileText(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"text": text, "truncated": truncated})
}

// frontmatterFields splits frontmatter into the owned title, tags and icon
// and the extra keys.
func frontmatterFields(fm map[string]any) (title, icon *string, tags *[]string, extra map[string]any, err error) {
	for k, v := range fm {
		switch strings.ToLower(k) {
		case "title":
			s, ok := v.(string)
			if !ok {
				return nil, nil, nil, nil, core.Invalid("frontmatter.title", "must be a string")
			}
			title = &s
		case "icon":
			s, ok := v.(string)
			if !ok {
				return nil, nil, nil, nil, core.Invalid("frontmatter.icon", "must be a string")
			}
			icon = &s
		case "tags":
			list, ok := v.([]any)
			if !ok {
				return nil, nil, nil, nil, core.Invalid("frontmatter.tags", "must be a list of strings")
			}
			out := make([]string, 0, len(list))
			for _, t := range list {
				s, ok := t.(string)
				if !ok {
					return nil, nil, nil, nil, core.Invalid("frontmatter.tags", "must be a list of strings")
				}
				out = append(out, s)
			}
			tags = &out
		default:
			if slices.Contains(core.OwnedFrontmatter, strings.ToLower(k)) {
				return nil, nil, nil, nil, core.Invalid("frontmatter."+k, "is set by Markspace")
			}
			if extra == nil {
				extra = map[string]any{}
			}
			extra[k] = v
		}
	}
	return title, icon, tags, extra, nil
}

type createNoteRequest struct {
	ID          string         `json:"id,omitempty"`
	SpaceID     string         `json:"spaceId"`
	ParentID    string         `json:"parentId,omitempty"`
	Title       string         `json:"title"`
	Content     string         `json:"content,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Position    int            `json:"position,omitempty"`
	FolderNote  bool           `json:"folderNote,omitempty"`
	FolderID    string         `json:"folderId,omitempty"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
}

// createNote serves POST /notes. With folderNote it creates <folder>/index.md
// of the folder named by folderId.
func (m *Module) createNote(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in createNoteRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	title, icon, tags, extra, err := frontmatterFields(in.Frontmatter)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	note := core.NoteInput{
		ID: in.ID, SpaceID: in.SpaceID, ParentID: in.ParentID, Title: in.Title, Content: in.Content,
		Icon: in.Icon, Tags: in.Tags, Position: in.Position, FolderNote: in.FolderNote, FolderID: in.FolderID,
		Extra: extra,
	}
	if note.Title == "" && title != nil {
		note.Title = *title
	}
	if note.Icon == "" && icon != nil {
		note.Icon = *icon
	}
	if note.Tags == nil && tags != nil {
		note.Tags = *tags
	}
	created, err := m.opts.Backend.CreateNote(r.Context(), actor, note)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, viewNote(created))
}

type updateNoteRequest struct {
	Title         *string        `json:"title,omitempty"`
	Content       *string        `json:"content,omitempty"`
	Icon          *string        `json:"icon,omitempty"`
	Tags          *[]string      `json:"tags,omitempty"`
	Frontmatter   map[string]any `json:"frontmatter,omitempty"`
	BaseUpdatedAt *time.Time     `json:"baseUpdatedAt"`
	Snapshot      bool           `json:"snapshot,omitempty"`
}

// updateNote serves PATCH /notes/{id}. baseUpdatedAt is required, so a
// plugin never overwrites an edit it did not see.
func (m *Module) updateNote(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in updateNoteRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	if in.BaseUpdatedAt == nil || in.BaseUpdatedAt.IsZero() {
		m.fail(w, r, core.Invalid("baseUpdatedAt", "is required"))
		return
	}
	title, icon, tags, extra, err := frontmatterFields(in.Frontmatter)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	upd := core.NoteUpdate{
		Title: in.Title, Content: in.Content, Icon: in.Icon, Tags: in.Tags,
		BaseUpdatedAt: in.BaseUpdatedAt, Snapshot: in.Snapshot, Extra: extra,
	}
	if upd.Title == nil {
		upd.Title = title
	}
	if upd.Icon == nil {
		upd.Icon = icon
	}
	if upd.Tags == nil {
		upd.Tags = tags
	}
	note, err := m.opts.Backend.UpdateNote(r.Context(), actor, r.PathValue("id"), upd)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, viewNote(note))
}

// createFolder serves POST /folders.
func (m *Module) createFolder(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in core.FolderInput
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	f, err := m.opts.Backend.CreateFolder(r.Context(), actor, in)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, f)
}

// uploadFile serves POST /files, a multipart body whose text fields spaceId,
// parentId, id, onConflict and name come before the part named file.
func (m *Module) uploadFile(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		m.fail(w, r, core.Invalid("body", "send multipart/form-data"))
		return
	}
	fields := map[string]string{}
	var file *multipart.Part
	for file == nil {
		part, err := mr.NextPart()
		if err != nil {
			m.fail(w, r, core.Invalid("file", "the part named file is missing"))
			return
		}
		if part.FormName() == "file" {
			file = part
			break
		}
		v, _ := io.ReadAll(io.LimitReader(part, 4096))
		fields[part.FormName()] = string(v)
		part.Close()
	}
	defer file.Close()
	name := fields["name"]
	if name == "" {
		name = file.FileName()
	}
	body := &readErrReader{r: file}
	f, err := m.opts.Backend.UploadFile(r.Context(), actor, core.FileUpload{
		ID: fields["id"], SpaceID: fields["spaceId"], ParentID: fields["parentId"], Name: name, OnConflict: fields["onConflict"],
	}, body)
	if err != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe) || errors.As(body.err, &mbe):
			err = fmt.Errorf("the upload is too large: %w", core.ErrTooLarge)
		case body.err != nil:
			// A broken multipart body is the client's fault, not the server's.
			err = core.Invalid("file", "could not read the upload: "+body.err.Error())
		}
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, f)
}

// readErrReader remembers the first read error other than io.EOF.
type readErrReader struct {
	r   io.Reader
	err error
}

func (rd *readErrReader) Read(p []byte) (int, error) {
	n, err := rd.r.Read(p)
	if err != nil && err != io.EOF && rd.err == nil {
		rd.err = err
	}
	return n, err
}

type moveRequest struct {
	ParentID string `json:"parentId"`
	Position int    `json:"position,omitempty"`
}

// moveItem serves POST /items/{id}/move.
func (m *Module) moveItem(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in moveRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	it, err := m.opts.Backend.MoveItem(r.Context(), actor, r.PathValue("id"), in.ParentID, in.Position)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, it)
}

// copyItem serves POST /items/{id}/copy.
func (m *Module) copyItem(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in core.CopyInput
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	it, err := m.opts.Backend.CopyItem(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, it)
}

// trashItem serves POST /items/{id}/trash.
func (m *Module) trashItem(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	if err := m.opts.Backend.TrashItem(r.Context(), actor, r.PathValue("id")); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// links serves GET /notes/{id}/links.
func (m *Module) links(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	list, err := m.opts.Backend.OutboundLinks(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, items(list))
}

// backlinks serves GET /items/{id}/backlinks.
func (m *Module) backlinks(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	list, err := m.opts.Backend.ItemBacklinks(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, items(list))
}

// resolve serves POST /resolve.
func (m *Module) resolve(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in core.ResolveRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	list, err := m.opts.Backend.ResolveLinks(r.Context(), actor, in)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, items(list))
}

// search serves GET /search?q=&mode=&space=&limit=&cursor=. The cursor is the
// offset of the next page.
func (m *Module) search(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	offset, _ := strconv.Atoi(q.Get("cursor"))
	offset = max(offset, 0)
	query := core.SearchQuery{Text: q.Get("q"), Mode: q.Get("mode"), Limit: limit, Offset: offset}
	if space := q.Get("space"); space != "" {
		query.SpaceIDs = []string{space}
	}
	hits, err := m.opts.Backend.Search(r.Context(), actor, query)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	out := items(hits)
	if len(hits) == limit {
		out.NextCursor = strconv.Itoa(offset + limit)
	}
	api.WriteJSON(w, http.StatusOK, out)
}

type commentRequest struct {
	ItemID   string              `json:"itemId"`
	ParentID string              `json:"parentId,omitempty"`
	Body     string              `json:"body"`
	Anchor   *core.CommentAnchor `json:"anchor,omitempty"`
}

// createComment serves POST /comments.
func (m *Module) createComment(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in commentRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	c, err := m.opts.Backend.CreateComment(r.Context(), actor, core.CommentInput{
		NoteID: in.ItemID, ParentID: in.ParentID, Body: in.Body, Anchor: in.Anchor,
	})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, c)
}

type suggestionRequest struct {
	NoteID    string                     `json:"noteId"`
	Title     string                     `json:"title"`
	Rationale string                     `json:"rationale,omitempty"`
	Edits     []core.SuggestionEditInput `json:"edits"`
}

// createSuggestion serves POST /suggestions.
func (m *Module) createSuggestion(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in suggestionRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	s, err := m.opts.Backend.CreateSuggestion(r.Context(), actor, core.SuggestionInput{
		NoteID: in.NoteID, Title: in.Title, Rationale: in.Rationale, Edits: in.Edits,
	})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, s)
}

// listSuggestions serves GET /suggestions?noteId=&status=&author=me&space=&limit=.
func (m *Module) listSuggestions(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := m.opts.Backend.SuggestionInbox(r.Context(), actor, core.SuggestionQuery{
		SpaceID: q.Get("space"), NoteID: q.Get("noteId"), AuthorID: q.Get("author"), Status: q.Get("status"), Limit: limit,
	})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, items(list))
}

// withdrawSuggestion serves DELETE /suggestions/{id}.
func (m *Module) withdrawSuggestion(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	if err := m.opts.Backend.WithdrawSuggestion(r.Context(), actor, r.PathValue("id")); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// completeRequest is an AI request with the optional purpose of docs/models.md.
type completeRequest struct {
	core.AIRequest
	Purpose string `json:"purpose,omitempty"`
}

// aiComplete serves POST /ai/complete. The call is charged to the plugin.
// The route defaults to plugin.<id>.<purpose> when the request names a
// purpose, and to default otherwise. A request may name default or one of
// the plugin's own routes, and nothing else.
func (m *Module) aiComplete(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	ctx := r.Context()
	var in completeRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	pid := actor.Plugin.ID
	req := in.AIRequest
	req.PluginID = pid
	switch {
	case req.Route != "":
		if req.Route != "default" && !strings.HasPrefix(req.Route, "plugin."+pid+".") {
			m.fail(w, r, core.Invalid("route", "a plugin may use the default route or its own plugin routes"))
			return
		}
	case in.Purpose != "":
		req.Route = "plugin." + pid + "." + in.Purpose
	default:
		req.Route = "default"
	}
	if req.Feature == "" {
		req.Feature = "plugin"
	}
	if (req.Task == core.TaskDescribe || req.Task == core.TaskExtract) && !actor.Plugin.Allows("ai:vision") {
		m.fail(w, r, fmt.Errorf("the plugin lacks the ai:vision permission: %w", core.ErrForbidden))
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		res, err := m.opts.Backend.RunAI(ctx, actor, req)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, res)
		return
	}
	ch, err := m.opts.Backend.StreamAI(ctx, actor, req)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	stream := api.NewSSE(w)
	streamCtx, cancelStream := api.StreamContext(r)
	defer cancelStream()
	for {
		select {
		case <-ctx.Done():
			return
		case <-streamCtx.Done():
			return
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			switch {
			case chunk.Err != nil:
				_ = stream.Send("error", map[string]string{"error": chunk.Err.Error()})
				return
			case chunk.Done:
				_ = stream.Send("done", map[string]any{
					"done": true, "model": chunk.Model, "provider": chunk.Provider, "attempts": chunk.Attempts,
					"promptTokens": chunk.PromptTokens, "completionTokens": chunk.CompletionTokens,
					"sources": chunk.Sources, "budget": chunk.Budget,
				})
				return
			case chunk.Delta != "":
				if err := stream.Send("delta", map[string]string{"delta": chunk.Delta}); err != nil {
					return
				}
			}
		}
	}
}

type embedRequest struct {
	Texts []string `json:"texts"`
}

// aiEmbed serves POST /ai/embed.
func (m *Module) aiEmbed(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in embedRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	res, err := m.opts.Backend.EmbedTexts(r.Context(), actor, in.Texts, actor.Plugin.ID)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, res)
}

type imageRequest struct {
	Prompt     string `json:"prompt"`
	Size       string `json:"size,omitempty"`
	N          int    `json:"n,omitempty"`
	FileID     string `json:"fileId,omitempty"`
	MaskFileID string `json:"maskFileId,omitempty"`
}

// aiImage serves POST /ai/image. The images come back as data URLs and
// nothing is saved. A mask file is read as a PNG data URL.
func (m *Module) aiImage(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	ctx := r.Context()
	var in imageRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	req := core.AIImageGenerateRequest{Prompt: in.Prompt, Size: in.Size, N: in.N, FileID: in.FileID, Save: core.AIImageSaveNone}
	if in.FileID != "" {
		f, _, err := m.opts.Backend.GetFile(ctx, actor, in.FileID)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		req.SpaceID = f.SpaceID
	}
	if in.MaskFileID != "" {
		mask, err := m.dataURL(ctx, actor, in.MaskFileID)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		req.Mask = mask
	}
	res, err := m.opts.Backend.GenerateImages(ctx, actor, req, nil)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, res)
}

// dataURL reads a file as a base64 data URL.
func (m *Module) dataURL(ctx context.Context, actor *core.Actor, id string) (string, error) {
	f, _, rc, err := m.opts.Backend.OpenFile(ctx, actor, id, "")
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxMaskBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxMaskBytes {
		return "", fmt.Errorf("the mask is larger than 20 MiB: %w", core.ErrTooLarge)
	}
	return "data:" + f.MIME + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}

// modelView is one row of GET /ai/models.
type modelView struct {
	ProviderID      string        `json:"providerId"`
	Model           string        `json:"model"`
	Name            string        `json:"name,omitempty"`
	ContextLength   int           `json:"contextLength,omitempty"`
	Vision          bool          `json:"vision"`
	ImageGeneration bool          `json:"imageGeneration"`
	ImageEditing    bool          `json:"imageEditing"`
	Embedding       bool          `json:"embedding"`
	Prices          *core.AIPrice `json:"prices,omitempty"`
	PriceSource     string        `json:"priceSource,omitempty"`
}

// aiModels serves GET /ai/models with the effective prices.
func (m *Module) aiModels(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	models, err := m.opts.Backend.ListAIModels(r.Context(), actor, core.AIModelQuery{})
	if err != nil {
		m.fail(w, r, err)
		return
	}
	out := make([]modelView, 0, len(models))
	for _, md := range models {
		imageOut := slices.Contains(md.Outputs, "image")
		imageIn := slices.Contains(md.Inputs, "image")
		out = append(out, modelView{
			ProviderID: md.ProviderID, Model: md.ModelID, Name: md.Name, ContextLength: md.ContextLength,
			Vision: imageIn && slices.Contains(md.Outputs, "text"), ImageGeneration: imageOut, ImageEditing: imageOut && imageIn,
			Embedding: slices.Contains(md.Outputs, "embedding"), Prices: md.EffectivePrice, PriceSource: md.EffectiveSource,
		})
	}
	api.WriteJSON(w, http.StatusOK, items(out))
}

// webFetch serves POST /web/fetch. A robots.txt refusal answers 403 with the
// code robots_disallowed.
func (m *Module) webFetch(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in FetchInput
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	res, err := m.opts.Host.WebFetch(r.Context(), actor, in)
	if errors.Is(err, ErrRobotsDisallowed) {
		api.WriteJSON(w, http.StatusForbidden, map[string]string{"error": ErrRobotsDisallowed.Error(), "code": "robots_disallowed"})
		return
	}
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, res)
}

// createJob serves POST /jobs.
func (m *Module) createJob(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in JobInput
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	j, err := m.opts.Host.CreateJob(r.Context(), actor, in)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, j)
}

// updateJob serves PATCH /jobs/{id}.
func (m *Module) updateJob(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in JobUpdate
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	j, err := m.opts.Host.UpdateJob(r.Context(), actor, r.PathValue("id"), in)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, j)
}

type logRequest struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// jobLog serves POST /jobs/{id}/log.
func (m *Module) jobLog(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in logRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	if err := m.opts.Host.JobLog(r.Context(), actor, r.PathValue("id"), in.Level, in.Message); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// kvList serves GET /kv?prefix=&cursor=&limit=.
func (m *Module) kvList(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := m.opts.Host.KVList(r.Context(), actor.Plugin.ID, q.Get("prefix"), q.Get("cursor"), limit)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	out := items(list)
	out.NextCursor = next
	api.WriteJSON(w, http.StatusOK, out)
}

// kvGet serves GET /kv/{key}.
func (m *Module) kvGet(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	e, err := m.opts.Host.KVGet(r.Context(), actor.Plugin.ID, r.PathValue("key"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, e)
}

type kvPutRequest struct {
	Value json.RawMessage `json:"value"`
}

// kvPut serves PUT /kv/{key} {value}.
func (m *Module) kvPut(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in kvPutRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	if err := m.opts.Host.KVPut(r.Context(), actor.Plugin.ID, r.PathValue("key"), in.Value); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// kvDelete serves DELETE /kv/{key}.
func (m *Module) kvDelete(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	if err := m.opts.Host.KVDelete(r.Context(), actor.Plugin.ID, r.PathValue("key")); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// blobPut serves PUT /blobs/{key} with the blob as the body.
func (m *Module) blobPut(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	info, err := m.opts.Host.BlobPut(r.Context(), actor.Plugin.ID, r.PathValue("key"), r.Header.Get("Content-Type"), r.Body, r.ContentLength)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, info)
}

// blobGet serves GET /blobs/{key}.
func (m *Module) blobGet(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	info, rc, err := m.opts.Host.BlobOpen(r.Context(), actor.Plugin.ID, r.PathValue("key"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	w.Header().Set("ETag", `"`+info.SHA256+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// blobDelete serves DELETE /blobs/{key}.
func (m *Module) blobDelete(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	if err := m.opts.Host.BlobDelete(r.Context(), actor.Plugin.ID, r.PathValue("key")); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// notify serves POST /notify.
func (m *Module) notify(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in Notification
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	if err := m.opts.Host.Notify(r.Context(), actor, in); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

type emitRequest struct {
	Type    string          `json:"type"`
	SpaceID string          `json:"spaceId,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// emit serves POST /events.
func (m *Module) emit(w http.ResponseWriter, r *http.Request, actor *core.Actor) {
	var in emitRequest
	if err := api.Decode(r, &in); err != nil {
		m.fail(w, r, err)
		return
	}
	if err := m.opts.Host.Emit(r.Context(), actor, in.Type, in.SpaceID, in.Data); err != nil {
		m.fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
