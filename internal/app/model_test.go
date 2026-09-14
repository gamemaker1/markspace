package app

// FuzzAdminOperations drives the application through random sequences of
// administrative and item operations over the real store and index. After
// every step, refused or not, it checks the invariants that must hold whatever
// callers do:
//
//   - at least one active admin exists;
//   - every space has an owner, and every membership names a real user;
//   - a live item's parent is the live folder at its parent directory, or the
//     root, and the entry exists on disk with the right kind;
//   - no two live items in one folder share a name ignoring case;
//   - a live folder points at its live index.md, and at nothing else;
//   - shares, links and comments point at items that exist, shares carry view
//     to manage and links at most comment;
//   - an admin can read every item, its comments and its shares, and the tree
//     shows every live item;
//   - a public link resolves exactly when its target is live, and never
//     reaches a trashed item;
//   - a refused operation changes no row and no file;
//   - every error wraps a core sentinel.
//
// It also checks rules about transitions that no snapshot shows: only an
// owner or an instance admin grants, changes or removes the owner role, and
// memberships and shares go to active users only.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
	"github.com/gamemaker1/markspace/internal/ports"
	"github.com/gamemaker1/markspace/internal/store"
)

// coreSentinels lists every error an application call may wrap.
var coreSentinels = []error{
	core.ErrNotFound, core.ErrForbidden, core.ErrUnauthorized, core.ErrConflict,
	core.ErrExists, core.ErrInvalid, core.ErrTooLarge, core.ErrUnsupported,
	core.ErrRateLimited, core.ErrUnavailable,
}

func wrapsSentinel(err error) bool {
	for _, s := range coreSentinels {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// Operation codes. An encoded operation is five bytes: the code, the actor,
// and three arguments that pick targets from the state when the step runs.
const (
	opCreateUser byte = iota
	opPromote
	opDemote
	opActivate
	opDeactivate
	opDeleteUser
	opCreateSpace
	opAddMember
	opChangeRole
	opRemoveMember
	opCreateNote
	opCreateFolder
	opMoveItem
	opRenameNote
	opRenameFolder
	opTrash
	opRestore
	opDeleteItem
	opEmptyTrash
	opShare
	opUnshare
	opCreateLink
	opRenameSpace
	opCopyItem
	opCount
)

var opNames = [opCount]string{
	"create-user", "promote", "demote", "activate", "deactivate", "delete-user",
	"create-space", "add-member", "change-role", "remove-member",
	"create-note", "create-folder", "move-item", "rename-note", "rename-folder",
	"trash", "restore", "delete-item", "empty-trash", "share", "unshare",
	"create-link", "rename-space", "copy-item",
}

// modelOp is one decoded operation.
type modelOp struct{ Code, Actor, A, B, C byte }

const (
	opWidth = 5
	maxOps  = 60
	// nilActor as the actor byte makes an anonymous call.
	nilActor = 255
	// rootBit in a parent argument means the space root.
	rootBit = 0x80
)

var (
	modelRoles       = []core.Role{core.RoleOwner, core.RoleAdmin, core.RoleEditor, core.RoleCommenter, core.RoleViewer, "bogus"}
	modelTitles      = []string{"Guide", "Guidebook", "Guide 2", "Docs"}
	modelFolderNames = []string{"docs", "Docs", "guide", "Sub"}
	modelSlugs       = []string{"home", "alpha", "beta", "!!!"}
)

// decodeOps turns fuzz input into 1 to 60 operations. The first byte sets the
// count, and missing bytes read as zero.
func decodeOps(data []byte) []modelOp {
	if len(data) == 0 {
		return []modelOp{{}}
	}
	n := 1 + int(data[0])%maxOps
	data = data[1:]
	ops := make([]modelOp, 0, n)
	for i := 0; i < n; i++ {
		var buf [opWidth]byte
		copy(buf[:], data)
		ops = append(ops, modelOp{buf[0], buf[1], buf[2], buf[3], buf[4]})
		if len(data) <= opWidth {
			break
		}
		data = data[opWidth:]
	}
	return ops
}

// encodeOps is the inverse of decodeOps, used to write seeds.
func encodeOps(ops ...modelOp) []byte {
	out := []byte{byte(len(ops) - 1)}
	for _, o := range ops {
		out = append(out, o.Code, o.Actor, o.A, o.B, o.C)
	}
	return out
}

func mop(code, actor, a, b, c byte) modelOp { return modelOp{code, actor, a, b, c} }

// modelSeeds cover the bugs the model found in the note tree and the item
// operations that replaced it. The initial state has the active users Root
// (admin), Uma and Vic, in that order, the space Home owned by Uma, and the
// note Start in it as item 0. Items are numbered in the order they appear.
func modelSeeds() [][]byte {
	return [][]byte{
		// The only admin deletes, demotes or deactivates themselves.
		encodeOps(mop(opDeleteUser, 0, 0, 0, 0)),
		encodeOps(mop(opDemote, 0, 0, 0, 0)),
		encodeOps(mop(opDeactivate, 0, 0, 0, 0)),
		// A second admin demotes the first, then themselves.
		encodeOps(mop(opPromote, 0, 1, 0, 0), mop(opDemote, 1, 0, 0, 0), mop(opDemote, 1, 1, 0, 0)),
		// Deleting the sole owner of a space.
		encodeOps(mop(opDeleteUser, 0, 1, 0, 0)),
		// The last owner removes or demotes themselves.
		encodeOps(mop(opRemoveMember, 1, 1, 0, 0), mop(opChangeRole, 1, 1, 0, 2)),
		// A space admin promotes themselves, then demotes and removes the owner.
		encodeOps(mop(opAddMember, 1, 2, 0, 1), mop(opChangeRole, 2, 2, 0, 0), mop(opChangeRole, 2, 1, 0, 4), mop(opRemoveMember, 2, 1, 0, 0)),
		// A note restored after its folder went to the trash, then the folder.
		encodeOps(mop(opCreateFolder, 1, 0, rootBit, 0), mop(opCreateNote, 1, 0, 1, 0), mop(opTrash, 1, 2, 0, 0),
			mop(opTrash, 1, 1, 0, 0), mop(opRestore, 1, 2, 0, 0), mop(opRestore, 1, 1, 0, 0)),
		// A note restored from inside a trashed folder.
		encodeOps(mop(opCreateFolder, 1, 0, rootBit, 0), mop(opCreateNote, 1, 0, 1, 0), mop(opTrash, 1, 1, 0, 0), mop(opRestore, 1, 2, 0, 0)),
		// A new page inside a note turns it into a folder note, then its folder
		// goes to the trash and back.
		encodeOps(mop(opCreateNote, 1, 0, 0, 1), mop(opTrash, 1, 2, 0, 0), mop(opRestore, 1, 2, 0, 0), mop(opRenameNote, 1, 0, 3, 0)),
		// A folder moved into itself and into its child, and a note under a note.
		encodeOps(mop(opCreateFolder, 1, 0, rootBit, 0), mop(opCreateFolder, 1, 0, 1, 3), mop(opMoveItem, 1, 1, 2, 0),
			mop(opMoveItem, 1, 1, 1, 0), mop(opMoveItem, 1, 0, 0, 0), mop(opMoveItem, 1, 2, rootBit, 1)),
		// Deleting an item that carries a share and a public link.
		encodeOps(mop(opShare, 1, 0, 2, 0x83), mop(opCreateLink, 1, 0, 1, 1), mop(opDeleteItem, 1, 0, 0, 0)),
		// A public link with children on a folder over a child that goes to the trash.
		encodeOps(mop(opCreateFolder, 1, 0, rootBit, 0), mop(opCreateLink, 1, 1, 2, 1), mop(opCreateNote, 1, 0, 1, 1), mop(opTrash, 1, 2, 0, 0)),
		// A second space, a copy across spaces, the trash emptied.
		encodeOps(mop(opCreateSpace, 2, 0, 0, 0), mop(opCreateFolder, 1, 0, rootBit, 0), mop(opCreateNote, 1, 0, 1, 0),
			mop(opCopyItem, 0, 1, rootBit, 1), mop(opCopyItem, 1, 1, rootBit, 0), mop(opTrash, 1, 1, 0, 0), mop(opEmptyTrash, 1, 0, 0, 0)),
		// Slug changes, including a taken and an empty one.
		encodeOps(mop(opCreateSpace, 1, 0, 0, 0), mop(opRenameSpace, 1, 1, 0, 0), mop(opRenameSpace, 1, 1, 3, 0), mop(opRenameSpace, 1, 1, 1, 0)),
		// A new admin, then the original admin deletes themselves twice.
		encodeOps(mop(opCreateUser, 0, 0, 0, 0), mop(opDeleteUser, 0, 0, 0, 0), mop(opDeleteUser, 0, 0, 0, 0)),
		// Renames and moves that collide on names ignoring case.
		encodeOps(mop(opCreateFolder, 1, 0, rootBit, 0), mop(opCreateFolder, 1, 0, rootBit, 1), mop(opRenameFolder, 1, 2, 0, 0),
			mop(opRenameNote, 1, 0, 1, 0), mop(opMoveItem, 1, 0, 1, 0), mop(opTrash, 1, 1, 0, 0), mop(opCreateFolder, 1, 0, rootBit, 0),
			mop(opRestore, 1, 1, 0, 0)),
		// A membership and a share for a deactivated user.
		encodeOps(mop(opDeactivate, 0, 2, 0, 0), mop(opAddMember, 1, 2, 0, 4), mop(opShare, 1, 0, 2, 1)),
	}
}

func FuzzAdminOperations(f *testing.F) {
	for _, seed := range modelSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		runModel(t, decodeOps(ops))
	})
}

type model struct {
	h          *rharness
	users      []string
	spaces     []string
	items      []string
	seq        int
	trace      []string
	violations []string
}

func itStrp(s string) *string { return &s }
func itBoolp(b bool) *bool    { return &b }

// newModelHarness is newRealHarness in a directory made with os.MkdirTemp.
// A fuzz seed's t.TempDir holds "#", which the index DSN reads as the start
// of a URI fragment, so every seed would share one database.
func newModelHarness(t *testing.T) *rharness {
	t.Helper()
	dir, err := os.MkdirTemp("", "markspace-model-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.DBPath = filepath.Join(dir, "markspace.db")
	cfg.WatchFiles = false
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.New(dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(cfg.DBPath, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := newFakeClock()
	h := &rharness{t: t, ctx: context.Background(), files: st, db: db, ai: &fakeAI{}, clock: clock, cfg: cfg, dir: dir}
	a, err := New(Deps{Files: st, DB: db, Renderer: markdown.New(logger), AI: h.ai, Hasher: fakeHasher{}, Config: cfg, Logger: logger, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	h.app = a
	t.Cleanup(func() { _ = a.Close() })
	return h
}

func newModel(t *testing.T) *model {
	h := newModelHarness(t)
	m := &model{h: h}
	root := h.user("Root", true)
	uma := h.user("Uma", false)
	vic := h.user("Vic", false)
	m.users = []string{root.UserID(), uma.UserID(), vic.UserID()}
	sp := h.space(uma, "Home")
	m.spaces = []string{sp.ID}
	n := h.note(uma, sp, "", "Start", "start")
	m.items = []string{n.ID}
	return m
}

func runModel(t *testing.T, ops []modelOp) {
	m := newModel(t)
	for i, op := range ops {
		before := m.snapshot()
		desc, err := m.apply(op)
		result := "ok"
		if err != nil {
			result = err.Error()
		}
		m.trace = append(m.trace, fmt.Sprintf("  %2d %s -> %s", i, desc, result))
		problems := m.violations
		m.violations = nil
		if err != nil && !wrapsSentinel(err) {
			problems = append(problems, "error wraps no core sentinel: "+err.Error())
		}
		if err != nil {
			if after := m.snapshot(); after != before {
				problems = append(problems, "refused operation changed state:\n"+diffLines(before, after))
			}
		}
		m.refresh()
		problems = append(problems, m.check()...)
		if len(problems) > 0 {
			t.Fatalf("invariant broken after step %d\noperations:\n%s\nproblems:\n  %s",
				i, strings.Join(m.trace, "\n"), strings.Join(problems, "\n  "))
		}
	}
}

func (m *model) violate(format string, args ...any) {
	m.violations = append(m.violations, fmt.Sprintf(format, args...))
}

// actor returns an active user chosen by b, freshly loaded, as the
// authentication layer would. Deactivated users cannot act.
func (m *model) actor(b byte) (*core.Actor, string) {
	if b == nilActor {
		return nil, "anonymous"
	}
	var active []*core.User
	for _, id := range m.users {
		if u, err := m.h.db.GetUser(m.h.ctx, id); err == nil && u.IsActive {
			active = append(active, u)
		}
	}
	if len(active) == 0 {
		return nil, "anonymous"
	}
	u := active[int(b)%len(active)]
	return &core.Actor{User: u}, u.Name
}

func pick(list []string, b byte) string {
	if len(list) == 0 {
		return "missing"
	}
	return list[int(b)%len(list)]
}

// parentArg picks a parent item, or the space root when rootBit is set.
func (m *model) parentArg(b byte) string {
	if b&rootBit != 0 {
		return ""
	}
	return pick(m.items, b)
}

// roleIn returns the stored role of a user in a space, or "".
func (m *model) roleIn(spaceID, userID string) core.Role {
	mem, err := m.h.db.GetMember(m.h.ctx, spaceID, userID)
	if err != nil || mem == nil {
		return ""
	}
	return mem.Role
}

// ownerPower reports whether the actor may touch the owner role of a space.
func (m *model) ownerPower(actor *core.Actor, spaceID string) bool {
	return actor.IsAdmin() || (actor.IsUser() && m.roleIn(spaceID, actor.User.ID) == core.RoleOwner)
}

// userActive reports whether a user exists and is active.
func (m *model) userActive(id string) bool {
	u, err := m.h.db.GetUser(m.h.ctx, id)
	return err == nil && u.IsActive
}

// label names an id for the trace.
func (m *model) label(id string) string {
	ctx := m.h.ctx
	if id == "" {
		return "root"
	}
	if u, err := m.h.db.GetUser(ctx, id); err == nil {
		return "user " + u.Name
	}
	if s, err := m.h.db.GetSpace(ctx, id); err == nil {
		return "space " + s.Slug
	}
	if it, err := m.h.app.loadItem(ctx, id); err == nil {
		return string(it.Kind) + " " + it.Path()
	}
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

func (m *model) apply(op modelOp) (string, error) {
	ctx, app := m.h.ctx, m.h.app
	actor, who := m.actor(op.Actor)
	code := op.Code % opCount
	name := opNames[code]
	switch code {
	case opCreateUser:
		m.seq++
		email := fmt.Sprintf("u%d@example.test", m.seq)
		admin := op.B%2 == 0
		u, err := app.CreateUser(ctx, actor, core.UserInput{Email: &email, Password: itStrp("password123"), IsAdmin: &admin})
		if err == nil {
			m.users = append(m.users, u.ID)
		}
		return fmt.Sprintf("%s %s admin=%v by %s", name, email, admin, who), err
	case opPromote, opDemote, opActivate, opDeactivate:
		user := pick(m.users, op.A)
		var in core.UserInput
		switch code {
		case opPromote:
			in.IsAdmin = itBoolp(true)
		case opDemote:
			in.IsAdmin = itBoolp(false)
		case opActivate:
			in.IsActive = itBoolp(true)
		default:
			in.IsActive = itBoolp(false)
		}
		desc := fmt.Sprintf("%s %s by %s", name, m.label(user), who)
		_, err := app.UpdateUser(ctx, actor, user, in)
		return desc, err
	case opDeleteUser:
		user := pick(m.users, op.A)
		desc := fmt.Sprintf("%s %s by %s", name, m.label(user), who)
		return desc, app.DeleteUser(ctx, actor, user)
	case opCreateSpace:
		m.seq++
		spaceName := fmt.Sprintf("Space %d", m.seq)
		sp, err := app.CreateSpace(ctx, actor, core.SpaceInput{Name: &spaceName})
		if err == nil {
			m.spaces = append(m.spaces, sp.ID)
		}
		return fmt.Sprintf("%s %q by %s", name, spaceName, who), err
	case opAddMember, opChangeRole:
		user, space := pick(m.users, op.A), pick(m.spaces, op.B)
		role := modelRoles[int(op.C)%len(modelRoles)]
		desc := fmt.Sprintf("%s %s in %s as %s by %s", name, m.label(user), m.label(space), role, who)
		power, before, active := m.ownerPower(actor, space), m.roleIn(space, user), m.userActive(user)
		var err error
		if code == opAddMember {
			_, err = app.AddMember(ctx, actor, space, user, role)
		} else {
			_, err = app.UpdateMember(ctx, actor, space, user, role)
		}
		if err == nil {
			if (role == core.RoleOwner || before == core.RoleOwner) && !power {
				m.violate("%s changed an owner role without being an owner or an instance admin", who)
			}
			if code == opAddMember && !active {
				m.violate("%s added a deactivated or missing user to a space", who)
			}
		}
		return desc, err
	case opRemoveMember:
		user, space := pick(m.users, op.A), pick(m.spaces, op.B)
		desc := fmt.Sprintf("%s %s from %s by %s", name, m.label(user), m.label(space), who)
		power, before := m.ownerPower(actor, space), m.roleIn(space, user)
		err := app.RemoveMember(ctx, actor, space, user)
		if err == nil && before == core.RoleOwner && !power {
			m.violate("%s removed an owner without being an owner or an instance admin", who)
		}
		return desc, err
	case opCreateNote:
		space, parent := pick(m.spaces, op.A), m.parentArg(op.B)
		title := modelTitles[int(op.C)%len(modelTitles)]
		desc := fmt.Sprintf("%s %q in %s under %s by %s", name, title, m.label(space), m.label(parent), who)
		_, err := app.CreateNote(ctx, actor, core.NoteInput{SpaceID: space, ParentID: parent, Title: title})
		return desc, err
	case opCreateFolder:
		space, parent := pick(m.spaces, op.A), m.parentArg(op.B)
		folder := modelFolderNames[int(op.C)%len(modelFolderNames)]
		desc := fmt.Sprintf("%s %q in %s under %s by %s", name, folder, m.label(space), m.label(parent), who)
		_, err := app.CreateFolder(ctx, actor, core.FolderInput{SpaceID: space, ParentID: parent, Name: folder})
		return desc, err
	case opMoveItem:
		item, parent := pick(m.items, op.A), m.parentArg(op.B)
		position := int(op.C) % 3 * 100
		desc := fmt.Sprintf("%s %s under %s at %d by %s", name, m.label(item), m.label(parent), position, who)
		_, err := app.MoveItem(ctx, actor, item, parent, position)
		return desc, err
	case opRenameNote:
		item := pick(m.items, op.A)
		title := modelTitles[int(op.B)%len(modelTitles)]
		desc := fmt.Sprintf("%s %s to %q by %s", name, m.label(item), title, who)
		_, err := app.UpdateNote(ctx, actor, item, core.NoteUpdate{Title: &title})
		return desc, err
	case opRenameFolder:
		item := pick(m.items, op.A)
		folder := modelFolderNames[int(op.B)%len(modelFolderNames)]
		desc := fmt.Sprintf("%s %s to %q by %s", name, m.label(item), folder, who)
		_, err := app.RenameFolder(ctx, actor, item, folder)
		return desc, err
	case opTrash, opRestore, opDeleteItem:
		item := pick(m.items, op.A)
		desc := fmt.Sprintf("%s %s by %s", name, m.label(item), who)
		switch code {
		case opTrash:
			return desc, app.TrashItem(ctx, actor, item)
		case opRestore:
			_, err := app.RestoreItem(ctx, actor, item)
			return desc, err
		default:
			return desc, app.DeleteItem(ctx, actor, item)
		}
	case opEmptyTrash:
		space := pick(m.spaces, op.A)
		return fmt.Sprintf("%s %s by %s", name, m.label(space), who), app.EmptyTrash(ctx, actor, space)
	case opShare:
		item, user := pick(m.items, op.A), pick(m.users, op.B)
		access := core.Access(int(op.C&^rootBit) % 6)
		children := op.C&rootBit != 0
		desc := fmt.Sprintf("%s %s with %s access=%d children=%v by %s", name, m.label(item), m.label(user), access, children, who)
		active := m.userActive(user)
		_, err := app.CreateShare(ctx, actor, core.ShareInput{NoteID: item, UserID: user, Access: access, IncludeChildren: children})
		if err == nil && !active {
			m.violate("%s shared an item with a deactivated or missing user", who)
		}
		return desc, err
	case opUnshare:
		id := pick(m.column("SELECT id FROM shares ORDER BY id"), op.A)
		return fmt.Sprintf("%s %s by %s", name, m.label(id), who), app.DeleteShare(ctx, actor, id)
	case opCreateLink:
		item := pick(m.items, op.A)
		access := core.Access(int(op.B) % 6)
		children := op.C%2 == 1
		desc := fmt.Sprintf("%s %s access=%d children=%v by %s", name, m.label(item), access, children, who)
		_, err := app.CreatePublicLink(ctx, actor, core.PublicLinkInput{NoteID: item, Access: access, IncludeChildren: children})
		return desc, err
	case opRenameSpace:
		space := pick(m.spaces, op.A)
		slug := modelSlugs[int(op.B)%len(modelSlugs)]
		desc := fmt.Sprintf("%s %s to %q by %s", name, m.label(space), slug, who)
		_, err := app.UpdateSpace(ctx, actor, space, core.SpaceInput{Slug: &slug})
		return desc, err
	case opCopyItem:
		item, parent := pick(m.items, op.A), m.parentArg(op.B)
		in := core.CopyInput{ParentID: parent}
		if parent == "" {
			in.SpaceID = pick(m.spaces, op.C)
		}
		desc := fmt.Sprintf("%s %s under %s in %s by %s", name, m.label(item), m.label(parent), m.label(in.SpaceID), who)
		_, err := app.CopyItem(ctx, actor, item, in)
		return desc, err
	}
	return "unknown", nil
}

// column runs a query returning one text column.
func (m *model) column(query string) []string {
	rows, err := m.h.db.SQL().QueryContext(m.h.ctx, query)
	if err != nil {
		m.violate("query %q: %v", query, err)
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// refresh drops ids whose rows are gone and appends items that appeared, such
// as copies and the folders that conversions create, in id order.
func (m *model) refresh() {
	ctx, d := m.h.ctx, m.h.db
	keep := func(list []string, exists func(string) bool) []string {
		out := list[:0]
		for _, id := range list {
			if exists(id) {
				out = append(out, id)
			}
		}
		return out
	}
	m.users = keep(m.users, func(id string) bool { _, err := d.GetUser(ctx, id); return err == nil })
	m.spaces = keep(m.spaces, func(id string) bool { _, err := d.GetSpace(ctx, id); return err == nil })
	m.items = keep(m.items, func(id string) bool { _, _, err := d.ItemKind(ctx, id); return err == nil })
	known := map[string]bool{}
	for _, id := range m.items {
		known[id] = true
	}
	for _, id := range m.column("SELECT id FROM folders UNION SELECT id FROM notes UNION SELECT id FROM files ORDER BY 1") {
		if !known[id] {
			m.items = append(m.items, id)
		}
	}
}

// snapshotTables are the tables an operation can change.
var snapshotTables = []string{
	"users", "sessions", "api_tokens", "invites", "spaces", "space_members", "notes", "folders",
	"files", "shares", "public_links", "comments", "stars", "note_links", "note_tags",
}

// snapshot serializes every row an operation could change, except link view
// counts, which the invariant checks bump, and every file under the data
// directory except the database.
func (m *model) snapshot() string {
	var b strings.Builder
	for _, table := range snapshotTables {
		rows, err := m.h.db.SQL().QueryContext(m.h.ctx, "SELECT * FROM "+table+" ORDER BY 1")
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", table, err)
			continue
		}
		cols, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			parts := make([]string, 0, len(cols))
			for i, c := range cols {
				if table == "public_links" && c == "views" {
					continue
				}
				v := vals[i]
				if bs, ok := v.([]byte); ok {
					v = string(bs)
				}
				parts = append(parts, fmt.Sprintf("%s=%v", c, v))
			}
			lines = append(lines, table+" "+strings.Join(parts, " "))
		}
		rows.Close()
		sort.Strings(lines)
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	_ = filepath.WalkDir(m.h.dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(m.h.dir, full)
		if strings.HasPrefix(d.Name(), "markspace.db") {
			return nil
		}
		if d.IsDir() {
			fmt.Fprintf(&b, "dir %s\n", filepath.ToSlash(rel))
			return nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "file %s %s\n", filepath.ToSlash(rel), hex.EncodeToString(sum[:8]))
		return nil
	})
	return b.String()
}

// diffLines lists the lines that appear in only one of two snapshots.
func diffLines(before, after string) string {
	count := func(s string) map[string]int {
		out := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			out[l]++
		}
		return out
	}
	b, a := count(before), count(after)
	var out []string
	for l, n := range b {
		if a[l] < n {
			out = append(out, "    - "+strings.TrimSpace(l))
		}
	}
	for l, n := range a {
		if b[l] < n {
			out = append(out, "    + "+strings.TrimSpace(l))
		}
	}
	sort.Strings(out)
	if len(out) > 30 {
		out = append(out[:30], "    ...")
	}
	return strings.Join(out, "\n")
}

// check returns every broken invariant.
func (m *model) check() []string {
	ctx, app, d := m.h.ctx, m.h.app, m.h.db
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	users, _, err := d.ListUsers(ctx, "", 1000, 0)
	if err != nil {
		return []string{"list users: " + err.Error()}
	}
	userByID := map[string]*core.User{}
	var admin *core.User
	for _, u := range users {
		userByID[u.ID] = u
		if admin == nil && u.IsAdmin && u.IsActive {
			admin = u
		}
	}
	if admin == nil {
		return []string{"no active admin is left"}
	}
	adminActor := &core.Actor{User: admin}

	spaces, err := d.ListSpaces(ctx)
	if err != nil {
		return []string{"list spaces: " + err.Error()}
	}
	type row struct {
		ref  itemRef
		kind core.ItemKind
	}
	allItems := map[string]row{}
	for _, sp := range spaces {
		members, _ := d.ListMembers(ctx, sp.ID)
		owners := 0
		for _, mem := range members {
			if userByID[mem.UserID] == nil {
				bad("space %s has a membership for missing user %s", sp.Slug, mem.UserID)
			}
			if mem.Role == core.RoleOwner {
				owners++
			}
		}
		if owners == 0 {
			bad("space %s has no owner", sp.Slug)
		}

		folders, _ := d.ListFolders(ctx, sp.ID, true)
		notes, _ := d.ListNotes(ctx, sp.ID, core.NoteListOptions{IncludeTrash: true})
		files, _ := d.ListFiles(ctx, sp.ID, ports.FileListOptions{IncludeDeleted: true, Sort: "path"})
		type live struct {
			id, parent, path string
			kind             core.ItemKind
		}
		var lives []live
		liveFolders := map[string]*core.Folder{}
		for _, f := range folders {
			allItems[f.ID] = row{folderRef(f), core.KindFolder}
			if !f.Deleted {
				liveFolders[f.Path] = f
				lives = append(lives, live{f.ID, f.ParentID, f.Path, core.KindFolder})
			}
		}
		liveNotes := map[string]*core.Note{}
		for _, n := range notes {
			allItems[n.ID] = row{noteRef(n), core.KindNote}
			if !n.Deleted {
				liveNotes[n.Path] = n
				lives = append(lives, live{n.ID, n.ParentID, n.Path, core.KindNote})
			}
		}
		for _, f := range files {
			allItems[f.ID] = row{fileRef(f), core.KindFile}
			if !f.Deleted {
				lives = append(lives, live{f.ID, f.ParentID, f.Path, core.KindFile})
			}
		}
		names := map[string]string{}
		folderNotes := 0
		for _, it := range lives {
			dir := dirOf(it.path)
			want := ""
			if dir != "" {
				pf, ok := liveFolders[dir]
				if !ok {
					bad("live %s %s in %s has no live folder at %q", it.kind, it.path, sp.Slug, dir)
				} else {
					want = pf.ID
				}
			}
			if it.parent != want {
				bad("live %s %s in %s has parent %q, its path implies %q", it.kind, it.path, sp.Slug, it.parent, want)
			}
			key := strings.ToLower(it.path)
			if other, dup := names[key]; dup {
				bad("live items %s and %s share the name %s in %s", other, it.id, it.path, sp.Slug)
			}
			names[key] = it.id
			info, err := os.Lstat(m.h.diskPath(sp, it.path))
			switch {
			case err != nil:
				bad("live %s %s in %s has no entry on disk", it.kind, it.path, sp.Slug)
			case info.IsDir() != (it.kind == core.KindFolder):
				bad("live %s %s in %s is a dir=%v on disk", it.kind, it.path, sp.Slug, info.IsDir())
			}
			if it.kind == core.KindNote && isFolderNotePath(it.path) {
				if _, ok := liveFolders[dir]; ok {
					folderNotes++
				}
			}
		}
		for _, f := range liveFolders {
			want := ""
			if n, ok := liveNotes[joinRel(f.Path, folderNoteName)]; ok {
				want = n.ID
			}
			if f.IndexNoteID != want {
				bad("folder %s in %s points at folder note %q, want %q", f.Path, sp.Slug, f.IndexNoteID, want)
			}
		}
		for _, n := range notes {
			if _, _, err := app.GetNote(ctx, adminActor, n.ID); err != nil {
				bad("admin cannot read note %s in %s: %v", n.Path, sp.Slug, err)
			}
		}
		for id, r := range allItems {
			if r.ref.spaceID != sp.ID {
				continue
			}
			if _, err := app.GetItem(ctx, adminActor, id); err != nil {
				bad("admin cannot read %s %s: %v", r.kind, r.ref.path, err)
			}
			if _, err := app.ListComments(ctx, adminActor, id); err != nil {
				bad("admin cannot list comments of %s: %v", r.ref.path, err)
			}
			if _, err := app.ListShares(ctx, adminActor, id); err != nil {
				bad("admin cannot list shares of %s: %v", r.ref.path, err)
			}
		}
		tree, err := app.SpaceTree(ctx, adminActor, sp.ID)
		if err != nil {
			bad("admin cannot load the tree of %s: %v", sp.Slug, err)
		} else if got, want := countTree(tree), len(lives)-folderNotes; got != want {
			bad("tree of %s shows %d rows, want %d", sp.Slug, got, want)
		}
	}

	type shareRow struct {
		id, item, user string
		access         int
	}
	var shares []shareRow
	if rows, err := d.SQL().QueryContext(ctx, "SELECT id, note_id, user_id, access FROM shares"); err == nil {
		for rows.Next() {
			var s shareRow
			if rows.Scan(&s.id, &s.item, &s.user, &s.access) == nil {
				shares = append(shares, s)
			}
		}
		rows.Close()
	}
	for _, s := range shares {
		if _, ok := allItems[s.item]; !ok {
			bad("share %s points at missing item %s", s.id, s.item)
		}
		if userByID[s.user] == nil {
			bad("share %s points at missing user %s", s.id, s.user)
		}
		if core.Access(s.access) < core.AccessView || core.Access(s.access) > core.AccessManage {
			bad("share %s carries access %d", s.id, s.access)
		}
	}
	for _, id := range m.column("SELECT note_id FROM comments") {
		if _, ok := allItems[id]; !ok {
			bad("a comment points at missing item %s", id)
		}
	}
	for _, id := range m.column("SELECT user_id FROM sessions UNION ALL SELECT user_id FROM api_tokens") {
		if userByID[id] == nil {
			bad("a session or token belongs to missing user %s", id)
		}
	}
	for _, id := range m.column("SELECT id FROM public_links ORDER BY id") {
		l, err := d.GetPublicLink(ctx, id)
		if err != nil {
			bad("load public link %s: %v", id, err)
			continue
		}
		if l.Access < core.AccessView || l.Access > core.AccessComment {
			bad("public link %s carries access %d", l.ID, l.Access)
		}
		target, ok := allItems[l.NoteID]
		if !ok {
			bad("public link %s points at missing item %s", l.ID, l.NoteID)
		}
		_, _, err = app.ResolvePublicItem(ctx, l.Token, "")
		switch {
		case ok && !target.ref.deleted && err != nil:
			bad("public link to live item %s does not resolve: %v", target.ref.path, err)
		case (!ok || target.ref.deleted) && !errors.Is(err, core.ErrNotFound):
			bad("public link to a missing or trashed item resolves with err %v", err)
		}
		held := *l
		for itemID, r := range allItems {
			if r.ref.spaceID != l.SpaceID || !r.ref.deleted {
				continue
			}
			if _, err := app.GetItem(ctx, &core.Actor{Link: &held}, itemID); !errors.Is(err, core.ErrNotFound) {
				bad("public link %s reaches trashed %s %s, err %v", l.ID, r.kind, r.ref.path, err)
			}
		}
	}
	return out
}

func countTree(level []*core.TreeItem) int {
	n := 0
	for _, node := range level {
		n += 1 + countTree(node.Children)
	}
	return n
}
