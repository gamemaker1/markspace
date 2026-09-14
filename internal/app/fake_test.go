package app

// The fakes in this file stand in for the real ports so the application logic
// can be tested offline and without the other packages. They are NOT the real
// behaviour. They keep only enough fidelity to exercise the app layer:
//
//   - fakeFiles keeps notes in memory keyed by space slug and path. AllocPath,
//     ChildDir, ParentPath, Rename, Trash and Restore follow the layout in
//     docs/design.md, but nothing touches a disk and frontmatter is not serialized.
//   - fakeDB keeps rows in maps and filters with plain loops. Search is a
//     substring match, not FTS5.
//   - fakeRenderer does trivial string work instead of goldmark.
//   - fakeAI embeds text as letter counts.
//   - fakeHasher uses sha256 with a prefix. It is not a password hash.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// ---------------------------------------------------------------------------
// clock

// fakeClock is a settable clock shared by the app and the fakes.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ---------------------------------------------------------------------------
// notes

// cloneNote copies a note deeply enough that callers cannot alias the fake.
func cloneNote(n *core.Note) *core.Note {
	if n == nil {
		return nil
	}
	cp := *n
	if n.Tags != nil {
		cp.Tags = append([]string(nil), n.Tags...)
	}
	if n.Extra != nil {
		cp.Extra = make(map[string]any, len(n.Extra))
		for k, v := range n.Extra {
			cp.Extra[k] = v
		}
	}
	if n.DeletedAt != nil {
		t := *n.DeletedAt
		cp.DeletedAt = &t
	}
	return &cp
}

// ---------------------------------------------------------------------------
// fakeFiles

type fileEntry struct {
	note    *core.Note
	modTime time.Time
	size    int64
}

type fakeFiles struct {
	mu       sync.Mutex
	clock    *fakeClock
	tick     int64
	spaces   map[string]map[string]*fileEntry // slug -> path -> file
	trash    map[string]map[string]*fileEntry // slug -> trash path -> file
	versions map[string][]*core.NoteVersion   // spaceID/noteID -> newest last
	watch    chan ports.FileEvent
	writes   int
}

func newFakeFiles(clock *fakeClock) *fakeFiles {
	return &fakeFiles{
		clock:    clock,
		spaces:   map[string]map[string]*fileEntry{},
		trash:    map[string]map[string]*fileEntry{},
		versions: map[string][]*core.NoteVersion{},
		watch:    make(chan ports.FileEvent, 16),
	}
}

// stamp returns a strictly increasing modification time.
func (f *fakeFiles) stamp() time.Time {
	f.tick++
	return f.clock.Now().Add(time.Duration(f.tick) * time.Millisecond)
}

func (f *fakeFiles) space(slug string) map[string]*fileEntry {
	m, ok := f.spaces[slug]
	if !ok {
		m = map[string]*fileEntry{}
		f.spaces[slug] = m
	}
	return m
}

func (f *fakeFiles) EnsureSpace(slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.space(slug)
	return nil
}

func (f *fakeFiles) RenameSpace(oldSlug, newSlug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.spaces[newSlug]; ok {
		return core.ErrExists
	}
	f.spaces[newSlug] = f.space(oldSlug)
	delete(f.spaces, oldSlug)
	return nil
}

func (f *fakeFiles) ArchiveSpace(slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.spaces, slug)
	return nil
}

func (f *fakeFiles) ListSpaceDirs() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.spaces))
	for s := range f.spaces {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeFiles) Scan(spaceSlug string) ([]ports.ScannedNote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ports.ScannedNote
	for p, e := range f.space(spaceSlug) {
		n := cloneNote(e.note)
		n.Path = p
		if pp := f.parentPath(p); pp != "" {
			if n.Extra == nil {
				n.Extra = map[string]any{}
			}
			n.Extra["_parent_path"] = pp
		}
		out = append(out, ports.ScannedNote{Note: n, ModTime: e.modTime, Size: e.size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Note.Path < out[j].Note.Path })
	return out, nil
}

func (f *fakeFiles) Read(spaceSlug, relPath string) (*core.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.space(spaceSlug)[relPath]; ok {
		n := cloneNote(e.note)
		n.Path = relPath
		return n, nil
	}
	if e, ok := f.trash[spaceSlug][relPath]; ok {
		n := cloneNote(e.note)
		return n, nil
	}
	return nil, fmt.Errorf("fake read %s: %w", relPath, core.ErrNotFound)
}

// frontmatterOnly drops the fields a real store would not persist.
func frontmatterOnly(n *core.Note) *core.Note {
	cp := cloneNote(n)
	cp.Excerpt = ""
	cp.Words = 0
	cp.SpaceSlug = ""
	cp.Starred = false
	return cp
}

func (f *fakeFiles) Write(spaceSlug string, note *core.Note) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if note.Path == "" || !strings.HasSuffix(note.Path, ".md") {
		return core.Invalid("path", "bad path")
	}
	f.writes++
	f.space(spaceSlug)[note.Path] = &fileEntry{note: frontmatterOnly(note), modTime: f.stamp(), size: int64(len(note.Content))}
	return nil
}

// put simulates an edit made outside the application.
func (f *fakeFiles) put(spaceSlug string, note *core.Note) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.space(spaceSlug)[note.Path] = &fileEntry{note: cloneNote(note), modTime: f.stamp(), size: int64(len(note.Content))}
}

// drop simulates a file deleted outside the application.
func (f *fakeFiles) drop(spaceSlug, relPath string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.space(spaceSlug), relPath)
}

// moveTree moves a file and its child directory inside one map.
func (f *fakeFiles) moveTree(from, to map[string]*fileEntry, oldPath, newPath string) {
	if e, ok := from[oldPath]; ok {
		delete(from, oldPath)
		to[newPath] = e
	}
	oldDir := strings.TrimSuffix(oldPath, ".md") + "/"
	newDir := strings.TrimSuffix(newPath, ".md") + "/"
	for p, e := range from {
		if strings.HasPrefix(p, oldDir) {
			delete(from, p)
			to[newDir+strings.TrimPrefix(p, oldDir)] = e
		}
	}
}

func (f *fakeFiles) Rename(spaceSlug, oldPath, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.space(spaceSlug)
	if _, ok := m[oldPath]; !ok {
		return fmt.Errorf("fake rename %s: %w", oldPath, core.ErrNotFound)
	}
	if _, ok := m[newPath]; ok {
		return fmt.Errorf("fake rename %s: %w", newPath, core.ErrExists)
	}
	f.moveTree(m, m, oldPath, newPath)
	return nil
}

func (f *fakeFiles) Remove(spaceSlug, relPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.space(spaceSlug)
	if _, ok := m[relPath]; !ok {
		return fmt.Errorf("fake remove %s: %w", relPath, core.ErrNotFound)
	}
	delete(m, relPath)
	dir := strings.TrimSuffix(relPath, ".md") + "/"
	for p := range m {
		if strings.HasPrefix(p, dir) {
			delete(m, p)
		}
	}
	return nil
}

func (f *fakeFiles) Exists(spaceSlug, relPath string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.space(spaceSlug)[relPath]
	return ok
}

func (f *fakeFiles) Stat(spaceSlug, relPath string) (time.Time, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.space(spaceSlug)[relPath]
	if !ok {
		return time.Time{}, 0, fmt.Errorf("fake stat %s: %w", relPath, core.ErrNotFound)
	}
	return e.modTime, e.size, nil
}

func (f *fakeFiles) AllocPath(spaceSlug, parentPath, title, exceptPath string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir := ""
	if parentPath != "" {
		dir = f.childDir(parentPath) + "/"
	}
	base := core.Slugify(title)
	m := f.space(spaceSlug)
	candidate := dir + base + ".md"
	for i := 2; ; i++ {
		if _, taken := m[candidate]; !taken || candidate == exceptPath {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s%s-%d.md", dir, base, i)
	}
}

func (f *fakeFiles) childDir(relPath string) string { return strings.TrimSuffix(relPath, ".md") }

func (f *fakeFiles) parentPath(relPath string) string {
	i := strings.LastIndex(relPath, "/")
	if i < 0 {
		return ""
	}
	return relPath[:i] + ".md"
}

func (f *fakeFiles) ChildDir(relPath string) string   { return f.childDir(relPath) }
func (f *fakeFiles) ParentPath(relPath string) string { return f.parentPath(relPath) }

func (f *fakeFiles) Trash(spaceSlug, relPath, noteID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.space(spaceSlug)
	if _, ok := m[relPath]; !ok {
		return "", fmt.Errorf("fake trash %s: %w", relPath, core.ErrNotFound)
	}
	t, ok := f.trash[spaceSlug]
	if !ok {
		t = map[string]*fileEntry{}
		f.trash[spaceSlug] = t
	}
	trashPath := ".trash/" + noteID + "/" + relPath
	f.moveTree(m, t, relPath, trashPath)
	return trashPath, nil
}

func (f *fakeFiles) Restore(spaceSlug, trashPath, targetPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.trash[spaceSlug]
	if _, ok := t[trashPath]; !ok {
		return fmt.Errorf("fake restore %s: %w", trashPath, core.ErrNotFound)
	}
	m := f.space(spaceSlug)
	if _, ok := m[targetPath]; ok {
		return fmt.Errorf("fake restore %s: %w", targetPath, core.ErrExists)
	}
	f.moveTree(t, m, trashPath, targetPath)
	return nil
}

func (f *fakeFiles) ScanTrash(spaceSlug string) ([]ports.ScannedNote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ports.ScannedNote
	for p, e := range f.trash[spaceSlug] {
		parts := strings.SplitN(p, "/", 3)
		if len(parts) != 3 || parts[1] != e.note.ID {
			continue // only the root of each trashed subtree
		}
		n := cloneNote(e.note)
		n.Path = p
		if n.Extra == nil {
			n.Extra = map[string]any{}
		}
		n.Extra["trashed_from"] = parts[2]
		out = append(out, ports.ScannedNote{Note: n, ModTime: e.modTime, Size: e.size})
	}
	return out, nil
}

func (f *fakeFiles) PurgeTrash(spaceSlug, trashPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.SplitN(trashPath, "/", 3)
	if len(parts) < 2 {
		return core.ErrInvalid
	}
	prefix := parts[0] + "/" + parts[1] + "/"
	for p := range f.trash[spaceSlug] {
		if strings.HasPrefix(p, prefix) {
			delete(f.trash[spaceSlug], p)
		}
	}
	return nil
}

func (f *fakeFiles) EmptyTrash(spaceSlug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.trash, spaceSlug)
	return nil
}

func (f *fakeFiles) SaveVersion(spaceID string, note *core.Note, label string) (*core.NoteVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := spaceID + "/" + note.ID
	f.tick++
	v := &core.NoteVersion{
		ID:        fmt.Sprintf("v%06d", f.tick),
		NoteID:    note.ID,
		SpaceID:   spaceID,
		Title:     note.Title,
		Content:   note.Content,
		Size:      len(note.Content),
		AuthorID:  note.UpdatedBy,
		Label:     label,
		CreatedAt: f.clock.Now(),
	}
	f.versions[key] = append(f.versions[key], v)
	cp := *v
	return &cp, nil
}

func (f *fakeFiles) ListVersions(spaceID, noteID string) ([]*core.NoteVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.versions[spaceID+"/"+noteID]
	out := make([]*core.NoteVersion, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		cp := *list[i]
		cp.Content = ""
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeFiles) ReadVersion(spaceID, noteID, versionID string) (*core.NoteVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.versions[spaceID+"/"+noteID] {
		if v.ID == versionID {
			cp := *v
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

func (f *fakeFiles) PruneVersions(spaceID, noteID string, keep int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := spaceID + "/" + noteID
	if list := f.versions[key]; len(list) > keep {
		f.versions[key] = list[len(list)-keep:]
	}
	return nil
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

func (f *fakeFiles) ExportSpace(spaceSlug string, notes []*core.Note, w io.Writer) error {
	for _, n := range notes {
		if _, err := fmt.Fprintf(w, "%s\n%s\n", n.Path, n.Content); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeFiles) ImportZip(spaceSlug string, r io.ReaderAt, size int64) ([]ports.ImportedNote, error) {
	return nil, core.ErrUnsupported
}

func (f *fakeFiles) ImportMarkdown(spaceSlug, filename string, data []byte) (*ports.ImportedNote, error) {
	n := &core.Note{Title: strings.TrimSuffix(filename, ".md"), Content: string(data)}
	path, err := f.AllocPath(spaceSlug, "", n.Title, "")
	if err != nil {
		return nil, err
	}
	n.Path = path
	if err := f.Write(spaceSlug, n); err != nil {
		return nil, err
	}
	return &ports.ImportedNote{RelPath: path, Note: cloneNote(n)}, nil
}

func (f *fakeFiles) Watch(ctx context.Context) (<-chan ports.FileEvent, error) {
	return f.watch, nil
}

func (f *fakeFiles) DiskUsage() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var total int64
	for _, m := range f.spaces {
		for _, e := range m {
			total += e.size
		}
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// fakeDB

type noteRecord struct {
	note    *core.Note
	plain   string
	modTime time.Time
	size    int64
	links   []string
}

type fakeDB struct {
	mu         sync.Mutex
	users      map[string]*core.User
	sessions   map[string]*core.Session
	tokens     map[string]*core.APIToken
	invites    map[string]*core.Invite
	spaces     map[string]*core.Space
	members    map[string]*core.SpaceMember // spaceID|userID
	notes      map[string]*noteRecord
	stars      map[string]bool // userID|noteID
	comments   map[string]*core.Comment
	shares     map[string]*core.Share
	links      map[string]*core.PublicLink
	settings   *core.Settings
	providers  map[string]*core.AIProviderConfig
	embeddings map[string][]*core.EmbeddingChunk // noteID -> chunks
	upserts    int
	usage      []core.AIUsageRecord
	budgets    map[string]*core.AIBudget // kind|id
	// usageErr fails every RecordAIUsage call when set.
	usageErr error
	// rollupBefore records the cutoff of every RollupAIUsage call.
	rollupBefore []time.Time
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		users:      map[string]*core.User{},
		sessions:   map[string]*core.Session{},
		tokens:     map[string]*core.APIToken{},
		invites:    map[string]*core.Invite{},
		spaces:     map[string]*core.Space{},
		members:    map[string]*core.SpaceMember{},
		notes:      map[string]*noteRecord{},
		stars:      map[string]bool{},
		comments:   map[string]*core.Comment{},
		shares:     map[string]*core.Share{},
		links:      map[string]*core.PublicLink{},
		providers:  map[string]*core.AIProviderConfig{},
		embeddings: map[string][]*core.EmbeddingChunk{},
		budgets:    map[string]*core.AIBudget{},
	}
}

func (d *fakeDB) Close() error                   { return nil }
func (d *fakeDB) Ping(ctx context.Context) error { return nil }

func cpUser(u *core.User) *core.User { cp := *u; return &cp }

// CreateUser mirrors the real schema, where email is UNIQUE COLLATE NOCASE.
func (d *fakeDB) CreateUser(ctx context.Context, u *core.User) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.users {
		if strings.EqualFold(o.Email, u.Email) {
			return core.ErrExists
		}
	}
	d.users[u.ID] = cpUser(u)
	return nil
}

func (d *fakeDB) UpdateUser(ctx context.Context, u *core.User) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.users[u.ID]; !ok {
		return core.ErrNotFound
	}
	for id, o := range d.users {
		if id != u.ID && strings.EqualFold(o.Email, u.Email) {
			return core.ErrExists
		}
	}
	d.users[u.ID] = cpUser(u)
	return nil
}

// DeleteUser mirrors the real schema, where sessions, API tokens and space
// memberships reference users ON DELETE CASCADE.
func (d *fakeDB) DeleteUser(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.users[id]; !ok {
		return core.ErrNotFound
	}
	delete(d.users, id)
	for k, s := range d.sessions {
		if s.UserID == id {
			delete(d.sessions, k)
		}
	}
	for k, t := range d.tokens {
		if t.UserID == id {
			delete(d.tokens, k)
		}
	}
	for k, m := range d.members {
		if m.UserID == id {
			delete(d.members, k)
		}
	}
	return nil
}

func (d *fakeDB) GetUser(ctx context.Context, id string) (*core.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if u, ok := d.users[id]; ok {
		return cpUser(u), nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) GetUserByEmail(ctx context.Context, email string) (*core.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, u := range d.users {
		if strings.EqualFold(u.Email, strings.TrimSpace(email)) {
			return cpUser(u), nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListUsers(ctx context.Context, query string, limit, offset int) ([]*core.User, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var all []*core.User
	q := strings.ToLower(query)
	for _, u := range d.users {
		if q == "" || strings.HasPrefix(strings.ToLower(u.Email), q) || strings.HasPrefix(strings.ToLower(u.Name), q) {
			all = append(all, cpUser(u))
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.Before(all[j].CreatedAt)
		}
		return all[i].ID < all[j].ID
	})
	total := len(all)
	if offset > len(all) {
		offset = len(all)
	}
	all = all[offset:]
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

func (d *fakeDB) CountUsers(ctx context.Context) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.users), nil
}

func (d *fakeDB) TouchUserLogin(ctx context.Context, id string, at time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if u, ok := d.users[id]; ok {
		u.LastLoginAt = at
	}
	return nil
}

func (d *fakeDB) CreateSession(ctx context.Context, s *core.Session) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := *s
	d.sessions[s.ID] = &cp
	return nil
}

func (d *fakeDB) GetSessionByHash(ctx context.Context, tokenHash string) (*core.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.sessions {
		if s.TokenHash == tokenHash {
			cp := *s
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) TouchSession(ctx context.Context, id string, at time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.sessions[id]; ok {
		s.LastSeen = at
	}
	return nil
}

func (d *fakeDB) DeleteSession(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sessions, id)
	return nil
}

func (d *fakeDB) DeleteSessionByHash(ctx context.Context, tokenHash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, s := range d.sessions {
		if s.TokenHash == tokenHash {
			delete(d.sessions, id)
		}
	}
	return nil
}

func (d *fakeDB) ListSessions(ctx context.Context, userID string) ([]*core.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Session
	for _, s := range d.sessions {
		if s.UserID == userID {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (d *fakeDB) PurgeExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for id, s := range d.sessions {
		if now.After(s.ExpiresAt) {
			delete(d.sessions, id)
			n++
		}
	}
	return n, nil
}

func (d *fakeDB) CreateAPIToken(ctx context.Context, t *core.APIToken) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := *t
	d.tokens[t.ID] = &cp
	return nil
}

func (d *fakeDB) GetAPITokenByHash(ctx context.Context, tokenHash string) (*core.APIToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, t := range d.tokens {
		if t.TokenHash == tokenHash {
			cp := *t
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListAPITokens(ctx context.Context, userID string) ([]*core.APIToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.APIToken
	for _, t := range d.tokens {
		if t.UserID == userID {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (d *fakeDB) DeleteAPIToken(ctx context.Context, id, userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.tokens[id]; ok && t.UserID == userID {
		delete(d.tokens, id)
		return nil
	}
	return core.ErrNotFound
}

func (d *fakeDB) ListSystemAPITokens(ctx context.Context) ([]*core.APIToken, error) {
	return d.ListAPITokens(ctx, "")
}

func (d *fakeDB) DeleteSystemAPIToken(ctx context.Context, id string) error {
	return d.DeleteAPIToken(ctx, id, "")
}

func (d *fakeDB) TouchAPIToken(ctx context.Context, id string, at time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.tokens[id]; ok {
		t.LastUsed = &at
	}
	return nil
}

func (d *fakeDB) CreateInvite(ctx context.Context, in *core.Invite) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := *in
	d.invites[in.ID] = &cp
	return nil
}

func (d *fakeDB) GetInviteByToken(ctx context.Context, token string) (*core.Invite, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, in := range d.invites {
		if in.Token == token {
			cp := *in
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListInvites(ctx context.Context) ([]*core.Invite, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Invite
	for _, in := range d.invites {
		cp := *in
		out = append(out, &cp)
	}
	return out, nil
}

func (d *fakeDB) DeleteInvite(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.invites, id)
	return nil
}

func (d *fakeDB) MarkInviteUsed(ctx context.Context, id string, at time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if in, ok := d.invites[id]; ok {
		in.UsedAt = &at
	}
	return nil
}

func (d *fakeDB) CreateSpace(ctx context.Context, s *core.Space) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.spaces {
		if o.Slug == s.Slug {
			return core.ErrExists
		}
	}
	cp := *s
	d.spaces[s.ID] = &cp
	return nil
}

// UpdateSpace mirrors the real schema, where slug is UNIQUE.
func (d *fakeDB) UpdateSpace(ctx context.Context, s *core.Space) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.spaces[s.ID]; !ok {
		return core.ErrNotFound
	}
	for id, o := range d.spaces {
		if id != s.ID && o.Slug == s.Slug {
			return core.ErrExists
		}
	}
	cp := *s
	d.spaces[s.ID] = &cp
	return nil
}

// DeleteSpace mirrors the real DeleteSpace: memberships and notes cascade, and
// comments, shares, links and stars of the space are removed.
func (d *fakeDB) DeleteSpace(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.spaces, id)
	for k, m := range d.members {
		if m.SpaceID == id {
			delete(d.members, k)
		}
	}
	for k := range d.stars {
		parts := strings.SplitN(k, "|", 2)
		if r, ok := d.notes[parts[1]]; ok && r.note.SpaceID == id {
			delete(d.stars, k)
		}
	}
	for k, r := range d.notes {
		if r.note.SpaceID == id {
			delete(d.notes, k)
			delete(d.embeddings, k)
		}
	}
	for k, c := range d.comments {
		if c.SpaceID == id {
			delete(d.comments, k)
		}
	}
	for k, s := range d.shares {
		if s.SpaceID == id {
			delete(d.shares, k)
		}
	}
	for k, l := range d.links {
		if l.SpaceID == id {
			delete(d.links, k)
		}
	}
	return nil
}

func (d *fakeDB) GetSpace(ctx context.Context, id string) (*core.Space, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.spaces[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) GetSpaceBySlug(ctx context.Context, slug string) (*core.Space, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.spaces {
		if s.Slug == slug {
			cp := *s
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListSpaces(ctx context.Context) ([]*core.Space, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Space
	for _, s := range d.spaces {
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (d *fakeDB) ListSpacesForUser(ctx context.Context, userID string) ([]*core.Space, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Space
	for _, m := range d.members {
		if m.UserID != userID {
			continue
		}
		if s, ok := d.spaces[m.SpaceID]; ok {
			cp := *s
			cp.MyRole = m.Role
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (d *fakeDB) CountSpaces(ctx context.Context) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.spaces), nil
}

// AddMember mirrors the real INSERT, which fails on the (space, user) key.
func (d *fakeDB) AddMember(ctx context.Context, m *core.SpaceMember) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.members[m.SpaceID+"|"+m.UserID]; ok {
		return core.ErrExists
	}
	cp := *m
	cp.User = nil
	d.members[m.SpaceID+"|"+m.UserID] = &cp
	return nil
}

func (d *fakeDB) UpdateMember(ctx context.Context, spaceID, userID string, role core.Role) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.members[spaceID+"|"+userID]
	if !ok {
		return core.ErrNotFound
	}
	m.Role = role
	return nil
}

func (d *fakeDB) RemoveMember(ctx context.Context, spaceID, userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.members, spaceID+"|"+userID)
	return nil
}

func (d *fakeDB) GetMember(ctx context.Context, spaceID, userID string) (*core.SpaceMember, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m, ok := d.members[spaceID+"|"+userID]; ok {
		cp := *m
		return &cp, nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListMembers(ctx context.Context, spaceID string) ([]*core.SpaceMember, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.SpaceMember
	for _, m := range d.members {
		if m.SpaceID == spaceID {
			cp := *m
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (d *fakeDB) ListMembershipsForUser(ctx context.Context, userID string) ([]*core.SpaceMember, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.SpaceMember
	for _, m := range d.members {
		if m.UserID == userID {
			cp := *m
			out = append(out, &cp)
		}
	}
	return out, nil
}

// UpsertNote mirrors the real schema, where (space_id, path) is UNIQUE and the
// upsert resolves conflicts on the id only.
func (d *fakeDB) UpsertNote(ctx context.Context, row ports.NoteRow) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, r := range d.notes {
		if id != row.Note.ID && r.note.SpaceID == row.Note.SpaceID && r.note.Path == row.Note.Path {
			return fmt.Errorf("fake upsert note %s: path %s is held by %s: %w", row.Note.ID, row.Note.Path, id, core.ErrExists)
		}
	}
	d.upserts++
	n := cloneNote(row.Note)
	n.Content = ""
	d.notes[n.ID] = &noteRecord{note: n, plain: row.Plain, modTime: row.ModTime, size: row.Size, links: row.Links}
	return nil
}

func (d *fakeDB) GetNote(ctx context.Context, id string) (*core.Note, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r, ok := d.notes[id]; ok {
		return cloneNote(r.note), nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) GetNoteByPath(ctx context.Context, spaceID, relPath string) (*core.Note, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.notes {
		if r.note.SpaceID == spaceID && r.note.Path == relPath {
			return cloneNote(r.note), nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListNotes(ctx context.Context, spaceID string, opts core.NoteListOptions) ([]*core.Note, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Note
	for _, r := range d.notes {
		n := r.note
		if n.SpaceID != spaceID {
			continue
		}
		if opts.OnlyTrash && !n.Deleted {
			continue
		}
		if !opts.OnlyTrash && !opts.IncludeTrash && n.Deleted {
			continue
		}
		if (opts.HasParent || opts.ParentID != "") && n.ParentID != opts.ParentID {
			continue
		}
		if opts.Tag != "" {
			found := false
			for _, t := range n.Tags {
				if t == opts.Tag {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, cloneNote(n))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func (d *fakeDB) ListNotesByIDs(ctx context.Context, ids []string) ([]*core.Note, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Note
	for _, id := range ids {
		if r, ok := d.notes[id]; ok {
			out = append(out, cloneNote(r.note))
		}
	}
	return out, nil
}

func (d *fakeDB) DeleteNoteRow(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.notes, id)
	return nil
}

func (d *fakeDB) DeleteNoteRowsUnder(ctx context.Context, spaceID, pathPrefix string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, r := range d.notes {
		if r.note.SpaceID == spaceID && strings.HasPrefix(r.note.Path, pathPrefix) {
			delete(d.notes, id)
		}
	}
	return nil
}

func (d *fakeDB) SetNoteDeleted(ctx context.Context, id string, deleted bool, at *time.Time, by string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.notes[id]
	if !ok {
		return core.ErrNotFound
	}
	r.note.Deleted = deleted
	r.note.DeletedAt = at
	r.note.DeletedBy = by
	return nil
}

// SetNotePath mirrors the real UPDATE, which fails on the (space_id, path) key.
func (d *fakeDB) SetNotePath(ctx context.Context, id, relPath, slug, parentID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.notes[id]
	if !ok {
		return core.ErrNotFound
	}
	for other, o := range d.notes {
		if other != id && o.note.SpaceID == r.note.SpaceID && o.note.Path == relPath {
			return fmt.Errorf("fake set note path %s: path %s is held by %s: %w", id, relPath, other, core.ErrExists)
		}
	}
	r.note.Path = relPath
	r.note.Slug = slug
	r.note.ParentID = parentID
	return nil
}

func (d *fakeDB) IndexState(ctx context.Context, spaceID string) (map[string]ports.IndexState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]ports.IndexState{}
	for _, r := range d.notes {
		if r.note.SpaceID == spaceID {
			out[r.note.Path] = ports.IndexState{NoteID: r.note.ID, ModTime: r.modTime, Size: r.size}
		}
	}
	return out, nil
}

func (d *fakeDB) CountNotes(ctx context.Context) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.notes), nil
}

func (d *fakeDB) NoteIDsInSpace(ctx context.Context, spaceID string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for id, r := range d.notes {
		if r.note.SpaceID == spaceID && !r.note.Deleted {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (d *fakeDB) Backlinks(ctx context.Context, noteID string) ([]*core.Backlink, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	target, ok := d.notes[noteID]
	if !ok {
		return nil, nil
	}
	var out []*core.Backlink
	for _, r := range d.notes {
		for _, l := range r.links {
			if strings.EqualFold(l, target.note.Title) {
				out = append(out, &core.Backlink{FromNote: cloneNote(r.note)})
				break
			}
		}
	}
	return out, nil
}

func (d *fakeDB) ResolveLink(ctx context.Context, spaceID, target string) (*core.Note, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.notes {
		n := r.note
		if n.SpaceID == spaceID && (strings.EqualFold(n.Title, target) || n.Slug == target || n.Path == target) {
			return cloneNote(n), nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListTags(ctx context.Context, spaceIDs []string) ([]*core.TagCount, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	in := map[string]bool{}
	for _, s := range spaceIDs {
		in[s] = true
	}
	counts := map[string]int{}
	for _, r := range d.notes {
		if in[r.note.SpaceID] && !r.note.Deleted {
			for _, t := range r.note.Tags {
				counts[t]++
			}
		}
	}
	var out []*core.TagCount
	for t, c := range counts {
		out = append(out, &core.TagCount{Tag: t, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

func (d *fakeDB) SearchNotes(ctx context.Context, spaceIDs []string, q core.SearchQuery) ([]*core.SearchHit, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	in := map[string]bool{}
	for _, s := range spaceIDs {
		in[s] = true
	}
	text := strings.ToLower(q.Text)
	var out []*core.SearchHit
	for _, r := range d.notes {
		n := r.note
		if !in[n.SpaceID] || (n.Deleted && !q.IncludeTrash) {
			continue
		}
		if strings.Contains(strings.ToLower(n.Title), text) || strings.Contains(strings.ToLower(r.plain), text) {
			out = append(out, &core.SearchHit{Note: cloneNote(n), Snippet: n.Title, Score: 1})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Note.Title < out[j].Note.Title })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (d *fakeDB) SuggestNotes(ctx context.Context, spaceIDs []string, prefix string, limit int) ([]*core.Note, error) {
	hits, err := d.SearchNotes(ctx, spaceIDs, core.SearchQuery{Text: prefix, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]*core.Note, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Note)
	}
	return out, nil
}

func (d *fakeDB) RecentNotes(ctx context.Context, spaceIDs []string, limit int) ([]*core.Note, error) {
	hits, err := d.SearchNotes(ctx, spaceIDs, core.SearchQuery{Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]*core.Note, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Note)
	}
	return out, nil
}

func (d *fakeDB) SetStar(ctx context.Context, userID, noteID string, starred bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if starred {
		d.stars[userID+"|"+noteID] = true
	} else {
		delete(d.stars, userID+"|"+noteID)
	}
	return nil
}

func (d *fakeDB) IsStarred(ctx context.Context, userID, noteID string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stars[userID+"|"+noteID], nil
}

func (d *fakeDB) ListStarred(ctx context.Context, userID string) ([]*core.Note, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Note
	for k := range d.stars {
		parts := strings.SplitN(k, "|", 2)
		if parts[0] == userID {
			if r, ok := d.notes[parts[1]]; ok && !r.note.Deleted {
				out = append(out, cloneNote(r.note))
			}
		}
	}
	return out, nil
}

func cpComment(c *core.Comment) *core.Comment { cp := *c; cp.Children = nil; return &cp }

func (d *fakeDB) CreateComment(ctx context.Context, c *core.Comment) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.comments[c.ID] = cpComment(c)
	return nil
}

func (d *fakeDB) GetComment(ctx context.Context, id string) (*core.Comment, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.comments[id]; ok {
		return cpComment(c), nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) UpdateComment(ctx context.Context, c *core.Comment) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.comments[c.ID] = cpComment(c)
	return nil
}

// UpdateCommentAnchor mirrors the real compare-and-swap on the anchor.
func (d *fakeDB) UpdateCommentAnchor(ctx context.Context, id string, prev, next *core.CommentAnchor) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.comments[id]
	if !ok {
		return false, nil
	}
	same := (c.Anchor == nil && prev == nil) || (c.Anchor != nil && prev != nil && *c.Anchor == *prev)
	if !same {
		return false, nil
	}
	cp := cpComment(c)
	if next == nil {
		cp.Anchor = nil
	} else {
		a := *next
		cp.Anchor = &a
	}
	d.comments[id] = cp
	return true, nil
}

// DeleteComment mirrors the real one, which also removes direct replies.
func (d *fakeDB) DeleteComment(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, c := range d.comments {
		if c.ParentID == id {
			delete(d.comments, k)
		}
	}
	delete(d.comments, id)
	return nil
}

func (d *fakeDB) ListComments(ctx context.Context, noteID string) ([]*core.Comment, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Comment
	for _, c := range d.comments {
		if c.NoteID == noteID {
			out = append(out, cpComment(c))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (d *fakeDB) CountComments(ctx context.Context) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.comments), nil
}

func cpShare(s *core.Share) *core.Share { cp := *s; cp.User = nil; cp.Note = nil; return &cp }

func (d *fakeDB) CreateShare(ctx context.Context, s *core.Share) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.shares[s.ID] = cpShare(s)
	return nil
}

func (d *fakeDB) GetShare(ctx context.Context, id string) (*core.Share, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.shares[id]; ok {
		return cpShare(s), nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) UpdateShare(ctx context.Context, s *core.Share) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.shares[s.ID] = cpShare(s)
	return nil
}

func (d *fakeDB) DeleteShare(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.shares, id)
	return nil
}

func (d *fakeDB) ListSharesForNote(ctx context.Context, noteID string) ([]*core.Share, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Share
	for _, s := range d.shares {
		if s.NoteID == noteID {
			out = append(out, cpShare(s))
		}
	}
	return out, nil
}

func (d *fakeDB) ListSharesForUser(ctx context.Context, userID string) ([]*core.Share, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.Share
	for _, s := range d.shares {
		if s.UserID == userID {
			out = append(out, cpShare(s))
		}
	}
	return out, nil
}

func (d *fakeDB) FindShare(ctx context.Context, noteID, userID string) (*core.Share, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.shares {
		if s.NoteID == noteID && s.UserID == userID {
			return cpShare(s), nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ShareGrantsForUser(ctx context.Context, userID string) ([]ports.ShareGrant, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []ports.ShareGrant
	for _, s := range d.shares {
		if s.UserID != userID {
			continue
		}
		r, ok := d.notes[s.NoteID]
		if !ok {
			continue
		}
		out = append(out, ports.ShareGrant{
			NoteID: s.NoteID, SpaceID: s.SpaceID, NotePath: r.note.Path,
			Access: s.Access, IncludeChildren: s.IncludeChildren,
		})
	}
	return out, nil
}

func cpLink(l *core.PublicLink) *core.PublicLink { cp := *l; return &cp }

func (d *fakeDB) CreatePublicLink(ctx context.Context, l *core.PublicLink) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.links[l.ID] = cpLink(l)
	return nil
}

func (d *fakeDB) GetPublicLink(ctx context.Context, id string) (*core.PublicLink, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l, ok := d.links[id]; ok {
		return cpLink(l), nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) GetPublicLinkByToken(ctx context.Context, token string) (*core.PublicLink, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, l := range d.links {
		if l.Token == token {
			return cpLink(l), nil
		}
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) UpdatePublicLink(ctx context.Context, l *core.PublicLink) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.links[l.ID] = cpLink(l)
	return nil
}

func (d *fakeDB) DeletePublicLink(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.links, id)
	return nil
}

func (d *fakeDB) ListPublicLinks(ctx context.Context, noteID string) ([]*core.PublicLink, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.PublicLink
	for _, l := range d.links {
		if l.NoteID == noteID {
			out = append(out, cpLink(l))
		}
	}
	return out, nil
}

func (d *fakeDB) IncrementLinkViews(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l, ok := d.links[id]; ok {
		l.Views++
	}
	return nil
}

func (d *fakeDB) GetSettings(ctx context.Context) (*core.Settings, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settings == nil {
		return nil, core.ErrNotFound
	}
	cp := *d.settings
	return &cp, nil
}

func (d *fakeDB) SaveSettings(ctx context.Context, s *core.Settings) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := *s
	d.settings = &cp
	return nil
}

func (d *fakeDB) CreateAIProvider(ctx context.Context, p *core.AIProviderConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := *p
	d.providers[p.ID] = &cp
	return nil
}

func (d *fakeDB) UpdateAIProvider(ctx context.Context, p *core.AIProviderConfig) error {
	return d.CreateAIProvider(ctx, p)
}

func (d *fakeDB) DeleteAIProvider(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.providers, id)
	return nil
}

func (d *fakeDB) GetAIProvider(ctx context.Context, id string) (*core.AIProviderConfig, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.providers[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListAIProviders(ctx context.Context) ([]*core.AIProviderConfig, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*core.AIProviderConfig
	for _, p := range d.providers {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (d *fakeDB) ReplaceEmbeddings(ctx context.Context, noteID, spaceID string, chunks []*core.EmbeddingChunk) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.embeddings[noteID] = chunks
	return nil
}

func (d *fakeDB) DeleteEmbeddings(ctx context.Context, noteID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.embeddings, noteID)
	return nil
}

func (d *fakeDB) ListEmbeddings(ctx context.Context, spaceIDs []string, model string) ([]*core.EmbeddingChunk, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	in := map[string]bool{}
	for _, s := range spaceIDs {
		in[s] = true
	}
	var out []*core.EmbeddingChunk
	for _, list := range d.embeddings {
		for _, c := range list {
			if in[c.SpaceID] && c.Model == model {
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (d *fakeDB) EmbeddingHashes(ctx context.Context, spaceID string) (map[string]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]string{}
	for id, list := range d.embeddings {
		if len(list) > 0 && list[0].SpaceID == spaceID {
			out[id] = list[0].Hash
		}
	}
	return out, nil
}

func (d *fakeDB) CountEmbeddings(ctx context.Context) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, l := range d.embeddings {
		n += len(l)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// fakeRenderer

var (
	fakeTagRe  = regexp.MustCompile(`(?:^|\s)#([A-Za-z][\w-]*)`)
	fakeLinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
)

type fakeRenderer struct{}

func (fakeRenderer) Render(md string, opts ports.RenderOptions) (*core.Rendered, error) {
	out := &core.Rendered{HTML: "<p>" + md + "</p>"}
	for _, l := range (fakeRenderer{}).Links(md) {
		if opts.ResolveLink != nil {
			if url, ok := opts.ResolveLink(l.Target); ok {
				out.HTML += `<a href="` + url + `">` + l.Target + `</a>`
			}
		}
		out.Links = append(out.Links, l)
	}
	return out, nil
}

func (fakeRenderer) PlainText(md string) string { return md }

func (fakeRenderer) Excerpt(md string, maxRunes int) string { return truncateRunes(md, maxRunes) }

func (fakeRenderer) WordCount(md string) int { return len(strings.Fields(md)) }

func (fakeRenderer) InlineTags(md string) []string {
	var out []string
	for _, m := range fakeTagRe.FindAllStringSubmatch(md, -1) {
		out = append(out, m[1])
	}
	return out
}

func (fakeRenderer) Links(md string) []core.WikiLink {
	var out []core.WikiLink
	for _, m := range fakeLinkRe.FindAllStringSubmatch(md, -1) {
		out = append(out, core.WikiLink{Target: m[1]})
	}
	return out
}

func (fakeRenderer) DiffHTML(oldText, newText string) string {
	return "<del>" + oldText + "</del><ins>" + newText + "</ins>"
}

func (fakeRenderer) Chunks(md string, targetRunes int) []string {
	r := []rune(md)
	var out []string
	for len(r) > 0 {
		n := targetRunes
		if n > len(r) {
			n = len(r)
		}
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return out
}

func (fakeRenderer) CSS() string { return "" }

// ---------------------------------------------------------------------------
// fakeAI

type fakeAI struct {
	mu     sync.Mutex
	embeds int
	// complete and stream replace the echo behaviour when set.
	complete func(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (*core.AIResult, error)
	stream   func(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error)
}

type fakeProvider struct {
	cfg *core.AIProviderConfig
	ai  *fakeAI
}

func (f *fakeAI) New(cfg *core.AIProviderConfig) (core.AIProvider, error) {
	return &fakeProvider{cfg: cfg, ai: f}, nil
}

func (f *fakeAI) BuildMessages(req core.AIRequest, sources []core.AISource) []core.AIMessage {
	content := string(req.Task) + ": " + req.Text + req.Question
	for _, s := range sources {
		content += "\n[" + s.Title + "] " + s.Excerpt
	}
	return []core.AIMessage{{Role: "user", Content: content}}
}

func (f *fakeAI) BuildPrompt(req core.AIRequest, in ports.PromptInput) []core.AIMessage {
	msgs := f.BuildMessages(req, in.Sources)
	msgs[len(msgs)-1].Images = in.Images
	return msgs
}

func (f *fakeAI) Similarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func (f *fakeAI) MaxContextRunes() int { return 10000 }

func (p *fakeProvider) ID() string                     { return p.cfg.ID }
func (p *fakeProvider) Kind() core.AIProviderKind      { return p.cfg.Kind }
func (p *fakeProvider) Config() *core.AIProviderConfig { return p.cfg }

func (p *fakeProvider) Complete(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (*core.AIResult, error) {
	p.ai.mu.Lock()
	fn := p.ai.complete
	p.ai.mu.Unlock()
	if fn != nil {
		return fn(ctx, msgs, opts)
	}
	return &core.AIResult{Text: "echo " + msgs[len(msgs)-1].Content}, nil
}

func (p *fakeProvider) Stream(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error) {
	p.ai.mu.Lock()
	fn := p.ai.stream
	p.ai.mu.Unlock()
	if fn != nil {
		return fn(ctx, msgs, opts)
	}
	ch := make(chan core.AIChunk, 2)
	ch <- core.AIChunk{Delta: "echo"}
	ch <- core.AIChunk{Done: true}
	close(ch)
	return ch, nil
}

// Embed maps text to a 26-dimension vector of letter counts.
func (p *fakeProvider) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	p.ai.mu.Lock()
	p.ai.embeds++
	p.ai.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 26)
		for _, r := range strings.ToLower(t) {
			if r >= 'a' && r <= 'z' {
				v[r-'a']++
			}
		}
		out[i] = v
	}
	return out, nil
}

func (p *fakeProvider) ListModels(ctx context.Context) ([]string, error) { return []string{"m"}, nil }
func (p *fakeProvider) Health(ctx context.Context) error                 { return nil }

// ---------------------------------------------------------------------------
// fakeHasher

type fakeHasher struct{}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (fakeHasher) Hash(plain string) (string, error) { return "fake$" + sum(plain), nil }

func (fakeHasher) Verify(encoded, plain string) (bool, error) {
	if !strings.HasPrefix(encoded, "fake$") {
		return false, core.ErrInvalid
	}
	return encoded == "fake$"+sum(plain), nil
}

func (fakeHasher) HashToken(plain string) string { return sum("token:" + plain) }

func (h fakeHasher) NewSessionToken() (string, string) {
	p := core.NewToken(16)
	return p, h.HashToken(p)
}

func (h fakeHasher) NewAPIToken() (string, string, string) {
	p := "ms_" + core.NewToken(16)
	return p, h.HashToken(p), p[:7]
}

func (fakeHasher) ValidatePassword(plain string) error {
	if len(plain) < 8 {
		return core.Invalid("password", "too short")
	}
	return nil
}

func (fakeHasher) NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func (fakeHasher) ValidateEmail(s string) error {
	if !strings.Contains(s, "@") {
		return core.Invalid("email", "missing @")
	}
	return nil
}

// ---------------------------------------------------------------------------
// harness

type harness struct {
	t     *testing.T
	ctx   context.Context
	app   *App
	files *fakeFiles
	db    *fakeDB
	ai    *fakeAI
	clock *fakeClock
	cfg   *config.Config
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := newFakeClock()
	cfg := config.Default()
	cfg.WatchFiles = false
	cfg.AI.Enabled = true
	cfg.AI.Providers = []core.AIProviderConfig{{
		ID: "local", Name: "Local", Kind: core.ProviderOpenAI, BaseURL: "http://127.0.0.1:1",
		Model: "chat", EmbedModel: "embed", APIKey: "secret", MaxTokens: 100, Enabled: true,
	}}
	h := &harness{
		t:     t,
		ctx:   context.Background(),
		files: newFakeFiles(clock),
		db:    newFakeDB(),
		ai:    &fakeAI{},
		clock: clock,
		cfg:   cfg,
	}
	app, err := New(Deps{
		Files:    h.files,
		DB:       h.db,
		Renderer: fakeRenderer{},
		AI:       h.ai,
		Hasher:   fakeHasher{},
		Config:   cfg,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.app = app
	t.Cleanup(func() { _ = app.Close() })
	return h
}

// user inserts an active account directly and returns its actor.
func (h *harness) user(name string, admin bool) *core.Actor {
	h.t.Helper()
	hash, _ := fakeHasher{}.Hash("password123")
	u := &core.User{
		ID: core.NewID(), Email: strings.ToLower(name) + "@example.test", Name: name,
		PasswordHash: hash, IsAdmin: admin, IsActive: true, CreatedAt: h.clock.Now(),
	}
	if err := h.db.CreateUser(h.ctx, u); err != nil {
		h.t.Fatalf("create user: %v", err)
	}
	return &core.Actor{User: u}
}

// space creates a space owned by owner.
func (h *harness) space(owner *core.Actor, name string) *core.Space {
	h.t.Helper()
	sp, err := h.app.CreateSpace(h.ctx, owner, core.SpaceInput{Name: &name})
	if err != nil {
		h.t.Fatalf("create space: %v", err)
	}
	return sp
}

// member adds actor to space with role.
func (h *harness) member(sp *core.Space, actor *core.Actor, role core.Role) {
	h.t.Helper()
	if err := h.db.AddMember(h.ctx, &core.SpaceMember{SpaceID: sp.ID, UserID: actor.User.ID, Role: role}); err != nil {
		h.t.Fatalf("add member: %v", err)
	}
}

// note creates a note through the app.
func (h *harness) note(actor *core.Actor, sp *core.Space, parentID, title, body string) *core.Note {
	h.t.Helper()
	n, err := h.app.CreateNote(h.ctx, actor, core.NoteInput{SpaceID: sp.ID, ParentID: parentID, Title: title, Content: body})
	if err != nil {
		h.t.Fatalf("create note %q: %v", title, err)
	}
	return n
}

// share grants a user direct access to a note.
func (h *harness) share(n *core.Note, actor *core.Actor, access core.Access, children bool) {
	h.t.Helper()
	if err := h.db.CreateShare(h.ctx, &core.Share{
		ID: core.NewID(), NoteID: n.ID, SpaceID: n.SpaceID, UserID: actor.User.ID,
		Access: access, IncludeChildren: children,
	}); err != nil {
		h.t.Fatalf("share: %v", err)
	}
}

// row loads the index row of a note.
func (h *harness) row(id string) *core.Note {
	h.t.Helper()
	n, err := h.db.GetNote(h.ctx, id)
	if err != nil {
		h.t.Fatalf("row %s: %v", id, err)
	}
	return n
}

var (
	_ ports.Files     = (*fakeFiles)(nil)
	_ ports.DB        = (*fakeDB)(nil)
	_ ports.Renderer  = fakeRenderer{}
	_ ports.AIFactory = (*fakeAI)(nil)
	_ Hasher          = fakeHasher{}
)

func (d *fakeDB) MaxNoteRev(ctx context.Context) (int64, error) { return 0, core.ErrUnsupported }

func (d *fakeDB) NotesChangedSince(ctx context.Context, spaceIDs, noteIDs []string, since, upTo int64, limit int) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}

func (d *fakeDB) NoteIDsInSpaces(ctx context.Context, spaceIDs []string) ([]string, error) {
	return nil, core.ErrUnsupported
}

func (d *fakeDB) SpaceLinks(ctx context.Context, spaceID string) ([]ports.SpaceLink, error) {
	return nil, core.ErrUnsupported
}

// Stubs for the ports.Files entry methods. They are not used by these tests.

func (f *fakeFiles) ValidName(name string) error { return core.ErrUnsupported }

func (f *fakeFiles) CaseCollision(spaceSlug, parentRel, name, exceptRel string) (string, bool) {
	return "", false
}

func (f *fakeFiles) AllocName(spaceSlug, parentRel, name, exceptRel string, isDir bool) (string, error) {
	return "", core.ErrUnsupported
}

func (f *fakeFiles) AllocCopyName(spaceSlug, parentRel, name string, isDir bool) (string, error) {
	return "", core.ErrUnsupported
}

func (f *fakeFiles) AllocNotePath(spaceSlug, folderRel, title, exceptRel string) (string, error) {
	return "", core.ErrUnsupported
}

func (f *fakeFiles) ScanTree(spaceSlug string) (ports.ScanResult, error) {
	return ports.ScanResult{}, core.ErrUnsupported
}

func (f *fakeFiles) StatEntry(spaceSlug, rel string) (fs.FileInfo, error) {
	return nil, core.ErrUnsupported
}

func (f *fakeFiles) MakeDir(spaceSlug, rel string) error { return core.ErrUnsupported }

func (f *fakeFiles) MoveEntry(spaceSlug, oldRel, newRel string) error { return core.ErrUnsupported }

func (f *fakeFiles) CopyEntry(spaceSlug, srcRel, dstSpace, dstRel string, rewriteNoteID func(oldID string) string) ([]ports.CopiedEntry, error) {
	return nil, core.ErrUnsupported
}

func (f *fakeFiles) RemoveEntry(spaceSlug, rel string) error { return core.ErrUnsupported }

func (f *fakeFiles) SaveFile(spaceSlug, rel string, r io.Reader, maxBytes int64) (ports.SavedFile, error) {
	return ports.SavedFile{}, core.ErrUnsupported
}

func (f *fakeFiles) ReplaceFile(spaceSlug, rel string, r io.Reader, maxBytes int64, key ports.FileHistoryKey) (ports.SavedFile, *core.FileVersion, error) {
	return ports.SavedFile{}, nil, core.ErrUnsupported
}

func (f *fakeFiles) OpenFile(spaceSlug, rel string) (io.ReadSeekCloser, fs.FileInfo, error) {
	return nil, nil, core.ErrUnsupported
}

func (f *fakeFiles) HashFile(spaceSlug, rel string) (string, error) { return "", core.ErrUnsupported }

func (f *fakeFiles) ListFileVersions(spaceID, fileID string) ([]*core.FileVersion, error) {
	return nil, core.ErrUnsupported
}

func (f *fakeFiles) OpenFileVersion(spaceID, fileID, versionID string) (io.ReadSeekCloser, *core.FileVersion, error) {
	return nil, nil, core.ErrUnsupported
}

func (f *fakeFiles) RestoreFileVersion(spaceSlug, rel, versionID string, key ports.FileHistoryKey) (ports.SavedFile, *core.FileVersion, error) {
	return ports.SavedFile{}, nil, core.ErrUnsupported
}

func (f *fakeFiles) PruneFileVersions(spaceID, fileID string, keep int, spaceByteBudget int64) ([]*core.FileVersion, error) {
	return nil, core.ErrUnsupported
}

func (f *fakeFiles) DeleteFileHistory(spaceID, fileID string) error { return core.ErrUnsupported }

func (f *fakeFiles) TrashEntry(spaceSlug, rel, id, deletedBy string) (string, error) {
	return "", core.ErrUnsupported
}

func (f *fakeFiles) RestoreEntry(spaceSlug, trashRel, targetRel string) error {
	return core.ErrUnsupported
}

func (f *fakeFiles) ScanTrashEntries(spaceSlug string) ([]ports.TrashedEntry, error) {
	return nil, core.ErrUnsupported
}

func (f *fakeFiles) PurgeTrashEntry(spaceSlug, trashRel string) error { return core.ErrUnsupported }

func (f *fakeFiles) Thumbnail(spaceSlug, rel, fileID, sha256 string, width int) (string, string, error) {
	return "", "", core.ErrUnsupported
}

func (f *fakeFiles) DeleteThumbnails(fileID string) error { return core.ErrUnsupported }

func (f *fakeFiles) ImportArchive(spaceSlug string, r io.ReaderAt, size int64) (*ports.ImportResult, error) {
	return nil, core.ErrUnsupported
}

// Item index stubs. The fake does not model folders and files yet.

func (d *fakeDB) CreateFolder(ctx context.Context, f *core.Folder) error { return core.ErrUnsupported }
func (d *fakeDB) GetFolder(ctx context.Context, id string) (*core.Folder, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) GetFolderByPath(ctx context.Context, spaceID, relPath string) (*core.Folder, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) UpdateFolder(ctx context.Context, f *core.Folder) error { return core.ErrUnsupported }
func (d *fakeDB) MoveFolder(ctx context.Context, id, parentID, relPath string) error {
	return core.ErrUnsupported
}
func (d *fakeDB) TrashFolder(ctx context.Context, id string, at time.Time, by string) error {
	return core.ErrUnsupported
}
func (d *fakeDB) RestoreFolder(ctx context.Context, id, parentID, relPath string) error {
	return core.ErrUnsupported
}
func (d *fakeDB) DeleteFolder(ctx context.Context, id string) error { return core.ErrUnsupported }
func (d *fakeDB) ListFolders(ctx context.Context, spaceID string, includeDeleted bool) ([]*core.Folder, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) CreateFile(ctx context.Context, f *core.File) error { return core.ErrUnsupported }
func (d *fakeDB) GetFile(ctx context.Context, id string) (*core.File, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) GetFileByPath(ctx context.Context, spaceID, relPath string) (*core.File, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) UpdateFile(ctx context.Context, f *core.File) error { return core.ErrUnsupported }
func (d *fakeDB) SetFileHash(ctx context.Context, id, sha256 string, size int64, modTime time.Time) error {
	return core.ErrUnsupported
}
func (d *fakeDB) ListFilesNeedingHash(ctx context.Context, limit int) ([]*core.File, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) FindFilesByHash(ctx context.Context, spaceID, sha256 string, size int64) ([]*core.File, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) TrashFile(ctx context.Context, id string, at time.Time, by string) error {
	return core.ErrUnsupported
}
func (d *fakeDB) RestoreFile(ctx context.Context, id, parentID, relPath string) error {
	return core.ErrUnsupported
}
func (d *fakeDB) DeleteFile(ctx context.Context, id string) error { return core.ErrUnsupported }
func (d *fakeDB) ListFiles(ctx context.Context, spaceID string, opts ports.FileListOptions) ([]*core.File, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) CountFiles(ctx context.Context) (int, error) { return 0, core.ErrUnsupported }
func (d *fakeDB) SetFileText(ctx context.Context, id, text string, state ports.FileTextState) error {
	return core.ErrUnsupported
}
func (d *fakeDB) ListFilesNeedingText(ctx context.Context, limit int) ([]*core.File, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) SearchFiles(ctx context.Context, spaceIDs []string, q core.SearchQuery) ([]*ports.FileHit, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) ItemKind(ctx context.Context, id string) (core.ItemKind, string, error) {
	return "", "", core.ErrUnsupported
}
func (d *fakeDB) TreeItems(ctx context.Context, spaceID string) (*ports.TreeRows, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) SiblingNameTaken(ctx context.Context, spaceID, parentPath, name, exceptID string) (bool, error) {
	return false, core.ErrUnsupported
}
func (d *fakeDB) Ancestors(ctx context.Context, spaceID, relPath string) ([]string, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) ItemsChangedSince(ctx context.Context, spaceIDs, itemIDs []string, since, upTo int64, limit int) ([]*core.TreeItem, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) ItemIDsInSpaces(ctx context.Context, spaceIDs []string) ([]string, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) OutboundLinks(ctx context.Context, noteID string) ([]ports.NoteLink, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) BacklinksTo(ctx context.Context, itemID string) ([]*core.Backlink, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) NotesLinkingToIDs(ctx context.Context, ids []string) ([]*core.Note, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) ResolveDanglingLinks(ctx context.Context, itemID string) error {
	return core.ErrUnsupported
}
func (d *fakeDB) CreateItemShare(ctx context.Context, kind core.ItemKind, s *core.Share) error {
	return core.ErrUnsupported
}
func (d *fakeDB) ListItemShares(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.Share, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) ItemShareGrantsForUser(ctx context.Context, userID string) ([]ports.ShareGrant, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) CreateItemPublicLink(ctx context.Context, kind core.ItemKind, l *core.PublicLink) error {
	return core.ErrUnsupported
}
func (d *fakeDB) ListItemPublicLinks(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.PublicLink, error) {
	return nil, core.ErrUnsupported
}
func (d *fakeDB) PublicLinkKind(ctx context.Context, id string) (core.ItemKind, error) {
	return "", core.ErrUnsupported
}
func (d *fakeDB) CreateItemComment(ctx context.Context, kind core.ItemKind, c *core.Comment) error {
	return core.ErrUnsupported
}
func (d *fakeDB) ListItemComments(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.Comment, error) {
	return nil, core.ErrUnsupported
}
