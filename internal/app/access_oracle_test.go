package app

// FuzzAccessOracle builds a random world of spaces, folders, notes, folder
// notes, files, users, roles, shares and public links on every kind, moves,
// renames, trashes and restores items, and compares the access the
// application resolves for every actor and item with a reference written here
// from the access rules in docs/files.md and docs/plugins.md. The actors include
// plugins with random grants, reached through plugin tokens, and acting
// tokens that pair a person with a plugin. The reference shares no code with the
// application. It walks parent ids through the folder rows where the
// application matches path prefixes, and it recognises a folder note by its
// name and parent id, so an item whose path and parent disagree shows up as a
// mismatch too.

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// byteSource hands out fuzz input bytes, then zeros.
type byteSource struct {
	data []byte
	pos  int
}

func (s *byteSource) next() byte {
	if s.pos >= len(s.data) {
		return 0
	}
	b := s.data[s.pos]
	s.pos++
	return b
}

func (s *byteSource) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(s.next()) % n
}

var (
	oracleTitles      = []string{"Guide", "Guidebook", "Guide 2", "Docs", "Docs-2"}
	oracleFolderNames = []string{"Guide", "Guidebook", "guide", "Docs", "Docs 2"}
	oracleFileNames   = []string{"a.png", "Guide.txt", "b.pdf", "guide.txt"}
)

// oracleItem is the reference view of one row: kind, parent id and name.
type oracleItem struct {
	kind     core.ItemKind
	id       string
	spaceID  string
	parentID string
	name     string
	path     string
	deleted  bool
}

// oracleShare is one grant on an item.
type oracleShare struct {
	userID   string
	targetID string
	access   core.Access
	children bool
}

// oraclePlugin is one installed plugin: its user and the grant roles of its
// identity, which may hold owner or admin to check that they grant nothing.
type oraclePlugin struct {
	id     string
	user   *core.User
	grants map[string]core.Role
}

// oracleActing pairs a person with a plugin for an acting token.
type oracleActing struct {
	person *core.User
	plugin *oraclePlugin
}

type oracleWorld struct {
	h       *rharness
	spaces  []*core.Space
	users   []*core.User
	items   []*oracleItem
	byID    map[string]*oracleItem
	roles   map[string]core.Role // spaceID|userID
	shares  []oracleShare
	links   []*core.PublicLink
	names   map[string]string
	plugins []*oraclePlugin
	acting  []oracleActing
}

func FuzzAccessOracle(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("markspace access oracle: folders, files, trash, shares, links and moves"))
	seq := make([]byte, 256)
	for i := range seq {
		seq[i] = byte(i)
	}
	f.Add(seq)
	f.Add([]byte{
		0, 1, 3, 1, // two spaces, one user with roles
		12,                              // operations
		0, 0, 9, 0, 0, 0, 1, 2, 0, 0, 0, // folders, notes
		2, 0, 1, 0, 0, 4, 0, 2, 0, 0, 3, 1, 0, 0,
		4, 0, 0, 1, 2, 1, 0, 1, 1, // files
		5, 0, 1, 0, 6, 1, 7, 1, 8, 0, 0, // move, trash, restore, rename
		6, 0, 1, 2, 1, 1, 0, 2, 3, 0, // shares
		3, 0, 1, 1, 1, 2, 1, // links
		4, 0, 1, 1, 6, 2, 5, 3, 0, 7, 2, // mutations
	})
	// A world whose tail builds plugins with every role, shares to plugin
	// users and admin plugin users.
	plugins := make([]byte, 300)
	for i := range plugins {
		plugins[i] = byte(i*37 + 11)
	}
	f.Add(plugins)
	f.Fuzz(func(t *testing.T, data []byte) {
		runOracle(t, data)
	})
}

// accTB gives the harness a temporary directory without "#" in its path.
// Fuzz seed names such as "seed#1" end up in t.TempDir, and the index opens
// its database through a file: URI, where "#" starts a fragment.
type accTB struct {
	testing.TB
	dir string
}

func (tb accTB) TempDir() string { return tb.dir }

func buildOracleWorld(t *testing.T, data []byte) *oracleWorld {
	dir, err := os.MkdirTemp("", "markspace-oracle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	h := newRealHarness(accTB{TB: t, dir: dir})
	ctx := h.ctx
	src := &byteSource{data: data}
	w := &oracleWorld{h: h, byID: map[string]*oracleItem{}, roles: map[string]core.Role{}, names: map[string]string{}}

	creator := h.user("Creator", true)
	w.users = append(w.users, creator.User)
	w.names[creator.UserID()] = "creator"

	nSpaces := 2 + src.intn(3)
	for i := 0; i < nSpaces; i++ {
		sp := h.space(creator, fmt.Sprintf("Space %d", i))
		w.spaces = append(w.spaces, sp)
		w.names[sp.ID] = fmt.Sprintf("S%d", i)
	}
	nUsers := 1 + src.intn(5)
	for i := 0; i < nUsers; i++ {
		u := h.user(fmt.Sprintf("U%d", i), src.intn(8) == 0)
		w.users = append(w.users, u.User)
		w.names[u.UserID()] = fmt.Sprintf("U%d", i)
		for _, sp := range w.spaces {
			if r := src.intn(8); r < len(core.Roles) {
				h.member(sp, u, core.Roles[r])
			}
		}
	}

	var ids []string
	add := func(id string) {
		if id == "" || w.names[id] != "" {
			return
		}
		ids = append(ids, id)
		w.names[id] = fmt.Sprintf("i%d", len(ids)-1)
	}
	pick := func() string {
		if len(ids) == 0 {
			return ""
		}
		return ids[src.intn(len(ids))]
	}
	kindOf := func(id string) core.ItemKind {
		k, _, err := h.db.ItemKind(ctx, id)
		if err != nil {
			return ""
		}
		return k
	}
	// parentIn picks a folder anywhere, or the root of a random space.
	parentIn := func(allowNote bool) (string, string) {
		sp := w.spaces[src.intn(len(w.spaces))]
		id := pick()
		if id == "" || src.intn(3) == 0 {
			return sp.ID, ""
		}
		k, spaceID, err := h.db.ItemKind(ctx, id)
		if err != nil || (k != core.KindFolder && !(allowNote && k == core.KindNote)) {
			return sp.ID, ""
		}
		return spaceID, id
	}
	mutate := func(op int) {
		id := pick()
		if id == "" {
			return
		}
		switch op {
		case 0:
			target := pick()
			if kindOf(target) != core.KindFolder || src.intn(4) == 0 {
				target = ""
			}
			_, _ = h.app.MoveItem(ctx, creator, id, target, 0)
		case 1:
			_ = h.app.TrashItem(ctx, creator, id)
		case 2:
			_, _ = h.app.RestoreItem(ctx, creator, id)
		case 3:
			switch kindOf(id) {
			case core.KindFolder:
				_, _ = h.app.RenameFolder(ctx, creator, id, oracleFolderNames[src.intn(len(oracleFolderNames))])
			case core.KindFile:
				_, _ = h.app.RenameFile(ctx, creator, id, oracleFileNames[src.intn(len(oracleFileNames))])
			case core.KindNote:
				title := oracleTitles[src.intn(len(oracleTitles))]
				_, _ = h.app.UpdateNote(ctx, creator, id, core.NoteUpdate{Title: &title})
			}
		}
	}

	nOps := src.intn(41)
	for i := 0; i < nOps; i++ {
		switch src.intn(10) {
		case 0, 1:
			spaceID, parent := parentIn(false)
			if f, err := h.app.CreateFolder(ctx, creator, core.FolderInput{SpaceID: spaceID, ParentID: parent, Name: oracleFolderNames[src.intn(len(oracleFolderNames))]}); err == nil {
				add(f.ID)
			}
		case 2, 3:
			spaceID, parent := parentIn(true)
			if n, err := h.app.CreateNote(ctx, creator, core.NoteInput{SpaceID: spaceID, ParentID: parent, Title: oracleTitles[src.intn(len(oracleTitles))]}); err == nil {
				add(n.ID)
			}
		case 4:
			spaceID, parent := parentIn(false)
			if f, err := h.app.UploadFile(ctx, creator, core.FileUpload{SpaceID: spaceID, ParentID: parent, Name: oracleFileNames[src.intn(len(oracleFileNames))]}, strings.NewReader("x")); err == nil {
				add(f.ID)
			}
		case 5:
			mutate(0)
		case 6:
			mutate(1)
		case 7:
			mutate(2)
		case 8:
			mutate(3)
		case 9:
			id := pick()
			spaceID, parent := parentIn(false)
			if id != "" {
				if it, err := h.app.CopyItem(ctx, creator, id, core.CopyInput{SpaceID: spaceID, ParentID: parent}); err == nil {
					add(it.ID())
				}
			}
		}
	}

	// Copies and conversions create items the operations did not return.
	for _, sp := range w.spaces {
		rows, err := h.db.TreeItems(ctx, sp.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range rows.Folders {
			add(f.ID)
		}
		for _, n := range rows.Notes {
			add(n.ID)
		}
		for _, f := range rows.Files {
			add(f.ID)
		}
	}

	userAt := func() *core.User { return w.users[1+src.intn(len(w.users)-1)] }
	nShares := src.intn(10)
	for i := 0; i < nShares && len(ids) > 0; i++ {
		id := pick()
		kind, spaceID, err := h.db.ItemKind(ctx, id)
		if err != nil {
			continue
		}
		u := userAt()
		if existing, _ := h.db.FindShare(ctx, id, u.ID); existing != nil {
			continue
		}
		s := &core.Share{ID: core.NewID(), NoteID: id, SpaceID: spaceID, UserID: u.ID,
			Access: core.Access(1 + src.intn(4)), IncludeChildren: src.intn(2) == 1, CreatedBy: creator.UserID()}
		if err := h.db.CreateItemShare(ctx, kind, s); err != nil {
			t.Fatal(err)
		}
	}
	nLinks := src.intn(5)
	for i := 0; i < nLinks && len(ids) > 0; i++ {
		id := pick()
		kind, spaceID, err := h.db.ItemKind(ctx, id)
		if err != nil {
			continue
		}
		l := &core.PublicLink{
			ID: core.NewID(), Token: core.NewToken(8), NoteID: id, SpaceID: spaceID,
			Access: core.Access(1 + src.intn(4)), IncludeChildren: src.intn(2) == 1, CreatedBy: creator.UserID(),
		}
		if err := h.db.CreateItemPublicLink(ctx, kind, l); err != nil {
			t.Fatal(err)
		}
		w.names[l.ID] = fmt.Sprintf("L%d", i)
	}

	nMutations := src.intn(10)
	for i := 0; i < nMutations; i++ {
		mutate(src.intn(4))
	}

	// Plugins: a plugin-kind user, grant roles per space, shares to the
	// plugin user that must not count, and sometimes an admin flag that must
	// not count either.
	pluginRoles := []core.Role{"", core.RoleViewer, core.RoleCommenter, core.RoleEditor, core.RoleOwner, core.RoleAdmin}
	nPlugins := 1 + src.intn(3)
	for i := 0; i < nPlugins; i++ {
		u := h.user(fmt.Sprintf("P%d", i), false)
		if err := h.db.SetUserKind(ctx, u.User.ID, core.UserKindPlugin); err != nil {
			t.Fatal(err)
		}
		if src.intn(3) == 0 {
			u.User.IsAdmin = true
			if err := h.db.UpdateUser(ctx, u.User); err != nil {
				t.Fatal(err)
			}
		}
		p := &oraclePlugin{id: fmt.Sprintf("plugin-%d", i), user: u.User, grants: map[string]core.Role{}}
		for _, sp := range w.spaces {
			if r := pluginRoles[src.intn(len(pluginRoles))]; r != "" {
				p.grants[sp.ID] = r
			}
		}
		w.plugins = append(w.plugins, p)
		w.names[u.User.ID] = fmt.Sprintf("P%d", i)
		for j, n := 0, src.intn(3); j < n && len(ids) > 0; j++ {
			id := pick()
			kind, spaceID, err := h.db.ItemKind(ctx, id)
			if err != nil {
				continue
			}
			if existing, _ := h.db.FindShare(ctx, id, u.User.ID); existing != nil {
				continue
			}
			sh := &core.Share{ID: core.NewID(), NoteID: id, SpaceID: spaceID, UserID: u.User.ID,
				Access: core.Access(1 + src.intn(4)), IncludeChildren: src.intn(2) == 1, CreatedBy: creator.UserID()}
			if err := h.db.CreateItemShare(ctx, kind, sh); err != nil {
				t.Fatal(err)
			}
		}
		if src.intn(2) == 0 {
			if err := h.db.AddMember(ctx, &core.SpaceMember{SpaceID: w.spaces[src.intn(len(w.spaces))].ID, UserID: u.User.ID, Role: core.RoleOwner}); err != nil {
				t.Fatal(err)
			}
		}
		for j, n := 0, 1+src.intn(2); j < n; j++ {
			w.acting = append(w.acting, oracleActing{person: w.users[src.intn(len(w.users))], plugin: p})
		}
	}
	for _, sp := range w.spaces {
		rows, err := h.db.TreeItems(ctx, sp.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range rows.Notes {
			add(n.ID)
		}
		for _, f := range rows.Folders {
			add(f.ID)
		}
	}

	for _, sp := range w.spaces {
		folders, err := h.db.ListFolders(ctx, sp.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range folders {
			w.addItem(&oracleItem{kind: core.KindFolder, id: f.ID, spaceID: f.SpaceID, parentID: f.ParentID, name: f.Name, path: f.Path, deleted: f.Deleted})
		}
		notes, err := h.db.ListNotes(ctx, sp.ID, core.NoteListOptions{IncludeTrash: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notes {
			w.addItem(&oracleItem{kind: core.KindNote, id: n.ID, spaceID: n.SpaceID, parentID: n.ParentID, name: path.Base(n.Path), path: n.Path, deleted: n.Deleted})
		}
		files, err := h.db.ListFiles(ctx, sp.ID, ports.FileListOptions{IncludeDeleted: true, Sort: "path"})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			w.addItem(&oracleItem{kind: core.KindFile, id: f.ID, spaceID: f.SpaceID, parentID: f.ParentID, name: f.Name, path: f.Path, deleted: f.Deleted})
		}
		members, _ := h.db.ListMembers(ctx, sp.ID)
		for _, m := range members {
			w.roles[sp.ID+"|"+m.UserID] = m.Role
		}
		for _, p := range w.plugins {
			delete(w.roles, sp.ID+"|"+p.user.ID)
		}
	}
	for _, it := range w.items {
		if w.names[it.id] == "" {
			w.names[it.id] = "x" + it.id[len(it.id)-4:]
		}
		shares, err := h.db.ListItemShares(ctx, it.kind, it.id)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range shares {
			w.shares = append(w.shares, oracleShare{userID: s.UserID, targetID: s.NoteID, access: s.Access, children: s.IncludeChildren})
		}
		links, err := h.db.ListItemPublicLinks(ctx, it.kind, it.id)
		if err != nil {
			t.Fatal(err)
		}
		w.links = append(w.links, links...)
	}
	sort.Slice(w.items, func(i, j int) bool {
		if w.items[i].spaceID != w.items[j].spaceID {
			return w.names[w.items[i].spaceID] < w.names[w.items[j].spaceID]
		}
		return w.items[i].path < w.items[j].path
	})
	sort.Slice(w.links, func(i, j int) bool { return w.names[w.links[i].ID] < w.names[w.links[j].ID] })
	return w
}

func (w *oracleWorld) addItem(it *oracleItem) {
	w.items = append(w.items, it)
	w.byID[it.id] = it
}

// hasAncestor walks parent ids from it through folder rows and reports
// whether it meets folderID.
func (w *oracleWorld) hasAncestor(folderID string, it *oracleItem) bool {
	seen := map[string]bool{it.id: true}
	for cur := it.parentID; cur != "" && !seen[cur]; {
		if cur == folderID {
			return true
		}
		seen[cur] = true
		p, ok := w.byID[cur]
		if !ok || p.kind != core.KindFolder {
			return false
		}
		cur = p.parentID
	}
	return false
}

// reaches reports whether a grant with children on target covers it. A
// folder covers its descendants. A note named index.md inside a folder covers
// that folder and its descendants. Anything else covers nothing below it.
func (w *oracleWorld) reaches(targetID string, it *oracleItem) bool {
	target, ok := w.byID[targetID]
	if !ok || target.id == it.id {
		return false
	}
	switch target.kind {
	case core.KindFolder:
		return w.hasAncestor(target.id, it)
	case core.KindNote:
		if target.name != "index.md" || target.parentID == "" {
			return false
		}
		return it.id == target.parentID || w.hasAncestor(target.parentID, it)
	}
	return false
}

// refUser is the highest of: manage for an instance admin, the space role, a
// direct share, and a share with children that reaches the item. For a
// signed-in user the trash does not change what they may view.
func (w *oracleWorld) refUser(u *core.User, it *oracleItem) core.Access {
	if u.IsAdmin {
		return core.AccessManage
	}
	best := core.AccessNone
	if r, ok := w.roles[it.spaceID+"|"+u.ID]; ok {
		switch r {
		case core.RoleOwner, core.RoleAdmin:
			best = core.AccessManage
		case core.RoleEditor:
			best = core.AccessEdit
		case core.RoleCommenter:
			best = core.AccessComment
		case core.RoleViewer:
			best = core.AccessView
		}
	}
	for _, s := range w.shares {
		if s.userID != u.ID {
			continue
		}
		target, ok := w.byID[s.targetID]
		if !ok || target.spaceID != it.spaceID {
			continue
		}
		if s.targetID == it.id || (s.children && w.reaches(s.targetID, it)) {
			if s.access > best {
				best = s.access
			}
		}
	}
	return best
}

// refLink is the link's access capped at comment, on the live target and,
// with children, what the target reaches, and nothing on a trashed item.
func (w *oracleWorld) refLink(l *core.PublicLink, it *oracleItem) core.Access {
	if it.deleted || l.SpaceID != it.spaceID {
		return core.AccessNone
	}
	target, ok := w.byID[l.NoteID]
	if !ok || target.deleted {
		return core.AccessNone
	}
	if l.NoteID != it.id && !(l.IncludeChildren && w.reaches(l.NoteID, it)) {
		return core.AccessNone
	}
	if l.Access > core.AccessComment {
		return core.AccessComment
	}
	if l.Access < core.AccessView {
		return core.AccessView
	}
	return l.Access
}

// refPlugin is the plugin's grant role on the item's space: viewer views,
// commenter comments, editor edits, and any other role or no grant gives
// nothing. Shares, memberships and the admin flag of the plugin user never
// count. As for a person, the trash does not change what the plugin views.
func (w *oracleWorld) refPlugin(p *oraclePlugin, it *oracleItem) core.Access {
	switch p.grants[it.spaceID] {
	case core.RoleViewer:
		return core.AccessView
	case core.RoleCommenter:
		return core.AccessComment
	case core.RoleEditor:
		return core.AccessEdit
	}
	return core.AccessNone
}

// refActing is the lower of the person's access and the plugin's.
func (w *oracleWorld) refActing(a oracleActing, it *oracleItem) core.Access {
	return min(w.refUser(a.person, it), w.refPlugin(a.plugin, it))
}

// refPluginSpaces is the set of spaces a plugin actor may list: the spaces
// with a viewer, commenter or editor grant, and for an acting token only
// those the person reaches through a role, a share or the admin flag.
func (w *oracleWorld) refPluginSpaces(p *oraclePlugin, person *core.User) map[string]bool {
	out := map[string]bool{}
	for _, sp := range w.spaces {
		switch p.grants[sp.ID] {
		case core.RoleViewer, core.RoleCommenter, core.RoleEditor:
		default:
			continue
		}
		if person != nil && !person.IsAdmin {
			_, member := w.roles[sp.ID+"|"+person.ID]
			shared := false
			for _, s := range w.shares {
				if t, ok := w.byID[s.targetID]; ok && s.userID == person.ID && t.spaceID == sp.ID {
					shared = true
				}
			}
			if !member && !shared {
				continue
			}
		}
		out[sp.ID] = true
	}
	return out
}

// pluginIdentity is the identity of a plugin as the host resolves it.
func (p *oraclePlugin) identity() *core.PluginIdentity {
	grants := make(map[string]core.Role, len(p.grants))
	for k, v := range p.grants {
		grants[k] = v
	}
	return &core.PluginIdentity{ID: p.id, UserID: p.user.ID, Status: core.PluginStatusActive, Grants: grants}
}

type oracleActor struct {
	name  string
	actor *core.Actor
	ref   func(*oracleItem) core.Access
	// spaces, when set, is the reference of accessibleSpaceIDs.
	spaces map[string]bool
}

func runOracle(t *testing.T, data []byte) {
	w := buildOracleWorld(t, data)
	ctx, app := w.h.ctx, w.h.app

	var actors []oracleActor
	for _, u := range w.users {
		actors = append(actors, oracleActor{name: w.names[u.ID], actor: &core.Actor{User: u}, ref: func(it *oracleItem) core.Access { return w.refUser(u, it) }})
	}
	for _, l := range w.links {
		held := *l
		actors = append(actors, oracleActor{name: "link " + w.names[l.ID], actor: &core.Actor{Link: &held}, ref: func(it *oracleItem) core.Access { return w.refLink(l, it) }})
	}
	for _, p := range w.plugins {
		row, err := w.h.db.GetUser(ctx, p.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		actors = append(actors, oracleActor{
			name: "plugin " + w.names[p.user.ID], actor: &core.Actor{User: row, Plugin: p.identity()},
			ref: func(it *oracleItem) core.Access { return w.refPlugin(p, it) }, spaces: w.refPluginSpaces(p, nil),
		})
	}
	for _, a := range w.acting {
		row, err := w.h.db.GetUser(ctx, a.person.ID)
		if err != nil {
			t.Fatal(err)
		}
		id := a.plugin.identity()
		actors = append(actors, oracleActor{
			name: "acting " + w.names[a.person.ID] + " via " + w.names[a.plugin.user.ID], actor: &core.Actor{User: row, Plugin: id, Via: id.ID},
			ref: func(it *oracleItem) core.Access { return w.refActing(a, it) }, spaces: w.refPluginSpaces(a.plugin, row),
		})
	}

	var mismatches []string
	bad := func(format string, args ...any) { mismatches = append(mismatches, fmt.Sprintf(format, args...)) }
	checkRead := func(what string, a oracleActor, it *oracleItem, want core.Access, got core.Access, err error) {
		if want == core.AccessNone {
			if !errors.Is(err, core.ErrNotFound) {
				bad("%s(%s, %s) err = %v, reference none so not found", what, a.name, w.names[it.id], err)
			}
			return
		}
		if err != nil || got != want {
			bad("%s(%s, %s) = %s %v, reference %s", what, a.name, w.names[it.id], got, err, want)
		}
	}
	for _, a := range actors {
		if a.actor.IsPlugin() && a.actor.IsAdmin() {
			bad("%s reports admin", a.name)
		}
		if a.spaces != nil {
			got, err := app.accessibleSpaceIDs(ctx, a.actor)
			if err != nil {
				bad("accessibleSpaceIDs(%s) err = %v", a.name, err)
			}
			seen := map[string]bool{}
			for _, id := range got {
				seen[id] = true
				if !a.spaces[id] {
					bad("accessibleSpaceIDs(%s) holds %s, which the reference does not grant", a.name, w.names[id])
				}
			}
			for id := range a.spaces {
				if !seen[id] {
					bad("accessibleSpaceIDs(%s) lacks %s", a.name, w.names[id])
				}
			}
			listed, err := app.ListSpaces(ctx, a.actor)
			if err != nil {
				bad("ListSpaces(%s) err = %v", a.name, err)
			}
			for _, sp := range listed {
				if !a.spaces[sp.ID] {
					bad("ListSpaces(%s) reveals %s", a.name, w.names[sp.ID])
				}
			}
		}
		for _, it := range w.items {
			want := a.ref(it)
			ref := itemRef{kind: it.kind, id: it.id, spaceID: it.spaceID, path: it.path, deleted: it.deleted}
			g, err := app.grants(a.actor).item(ctx, ref)
			if err != nil || g.Access != want {
				bad("item grant(%s, %s) = %v %v, reference %s", a.name, w.names[it.id], grantAccess(g), err, want)
			}
			got, err := app.GetItem(ctx, a.actor, it.id)
			access := core.AccessNone
			if got != nil {
				access = got.Access
				if got.Kind != it.kind {
					bad("GetItem(%s, %s) kind %s, want %s", a.name, w.names[it.id], got.Kind, it.kind)
				}
			}
			checkRead("GetItem", a, it, want, access, err)
			var grant *core.Grant
			switch it.kind {
			case core.KindFolder:
				_, grant, err = app.GetFolder(ctx, a.actor, it.id)
			case core.KindFile:
				_, grant, err = app.GetFile(ctx, a.actor, it.id)
			case core.KindNote:
				_, grant, err = app.GetNote(ctx, a.actor, it.id)
			}
			access = core.AccessNone
			if grant != nil {
				access = grant.Access
			}
			checkRead("Get "+string(it.kind), a, it, want, access, err)

			var wantErr error
			switch {
			case want == core.AccessNone:
				wantErr = core.ErrNotFound
			case want < core.AccessEdit:
				wantErr = core.ErrForbidden
			case it.deleted:
				wantErr = core.ErrConflict
			}
			_, _, err = app.requireItem(ctx, a.actor, it.id, core.AccessEdit)
			if (wantErr == nil && err != nil) || (wantErr != nil && !errors.Is(err, wantErr)) {
				bad("edit check(%s, %s) err = %v, want %v", a.name, w.names[it.id], err, wantErr)
			}
		}
		for _, sp := range w.spaces {
			visible := map[string]bool{}
			for _, it := range w.items {
				if it.spaceID == sp.ID && !it.deleted && a.ref(it) >= core.AccessView {
					visible[it.id] = true
				}
			}
			var want []string
			for _, it := range w.items {
				if !visible[it.id] {
					continue
				}
				if it.kind == core.KindNote && it.name == "index.md" && visible[it.parentID] {
					continue
				}
				want = append(want, w.names[it.id])
			}
			sort.Strings(want)
			tree, err := app.SpaceTree(ctx, a.actor, sp.ID)
			if err != nil {
				if !errors.Is(err, core.ErrNotFound) || len(want) != 0 {
					bad("SpaceTree(%s, %s) err = %v, reference %v", a.name, w.names[sp.ID], err, want)
				}
				continue
			}
			var got []string
			for _, id := range accTreeIDs(tree) {
				got = append(got, w.names[id])
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				bad("SpaceTree(%s, %s) = %v, reference %v", a.name, w.names[sp.ID], got, want)
			}
		}
		if len(mismatches) > 20 {
			break
		}
	}
	if len(mismatches) > 0 {
		t.Fatalf("resolved access differs from the reference\n%s\nmismatches:\n  %s", w.describe(), strings.Join(mismatches, "\n  "))
	}
}

func grantAccess(g *core.Grant) string {
	if g == nil {
		return "<nil>"
	}
	return g.Access.String() + "/" + g.Source
}

// describe prints the world in a form a person can rebuild by hand.
func (w *oracleWorld) describe() string {
	var b strings.Builder
	b.WriteString("world:\n  spaces:")
	for _, sp := range w.spaces {
		fmt.Fprintf(&b, " %s=%s", w.names[sp.ID], sp.Slug)
	}
	b.WriteString("\n  users:\n")
	for _, u := range w.users {
		fmt.Fprintf(&b, "    %s admin=%v roles:", w.names[u.ID], u.IsAdmin)
		for _, sp := range w.spaces {
			if r, ok := w.roles[sp.ID+"|"+u.ID]; ok {
				fmt.Fprintf(&b, " %s=%s", w.names[sp.ID], r)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("  items:\n")
	for _, it := range w.items {
		parent := "-"
		if it.parentID != "" {
			parent = w.names[it.parentID]
			if parent == "" {
				parent = "missing:" + it.parentID
			}
		}
		state := "live"
		if it.deleted {
			state = "trashed"
		}
		fmt.Fprintf(&b, "    %s %s %s path=%s parent=%s %s\n", w.names[it.spaceID], it.kind, w.names[it.id], it.path, parent, state)
	}
	b.WriteString("  shares:\n")
	for _, s := range w.shares {
		fmt.Fprintf(&b, "    %s on %s access=%s children=%v\n", w.names[s.userID], w.names[s.targetID], s.access, s.children)
	}
	b.WriteString("  plugins:\n")
	for _, p := range w.plugins {
		fmt.Fprintf(&b, "    %s admin=%v grants:", w.names[p.user.ID], p.user.IsAdmin)
		for _, sp := range w.spaces {
			if r, ok := p.grants[sp.ID]; ok {
				fmt.Fprintf(&b, " %s=%s", w.names[sp.ID], r)
			}
		}
		b.WriteString("\n")
	}
	for _, a := range w.acting {
		fmt.Fprintf(&b, "    acting %s via %s\n", w.names[a.person.ID], w.names[a.plugin.user.ID])
	}
	b.WriteString("  links:\n")
	for _, l := range w.links {
		fmt.Fprintf(&b, "    %s on %s access=%s children=%v\n", w.names[l.ID], w.names[l.NoteID], l.Access, l.IncludeChildren)
	}
	return b.String()
}
