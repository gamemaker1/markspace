// Package notes is the built-in module for notes: create, read, update,
// move, trash, stars, recent notes, versions, the space tree, tags and
// markdown rendering.
package notes

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the note routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "notes", Name: "Notes", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("POST /api/notes", m.createNote)
	r.API("GET /api/notes/{id}", m.getNote)
	r.API("PATCH /api/notes/{id}", m.patchNote)
	r.API("DELETE /api/notes/{id}", m.deleteNote)
	r.API("POST /api/notes/{id}/move", m.moveNote)
	r.API("POST /api/notes/{id}/duplicate", m.duplicateNote)
	r.API("POST /api/notes/{id}/trash", m.trashNote)
	r.API("POST /api/notes/{id}/restore", m.restoreNote)
	r.API("PUT /api/notes/{id}/star", m.setStar)
	r.API("DELETE /api/notes/{id}/star", m.clearStar)
	r.API("GET /api/notes/{id}/backlinks", m.backlinks)
	r.API("GET /api/notes/{id}/versions", m.listVersions)
	r.API("GET /api/notes/{id}/versions/{versionId}", m.getVersion)
	r.API("GET /api/notes/{id}/versions/{versionId}/diff", m.diffVersion)
	r.API("POST /api/notes/{id}/versions/{versionId}/restore", m.restoreVersion)
	r.API("GET /api/starred", m.listStarred)
	r.API("GET /api/recent", m.listRecent)
	r.API("GET /api/spaces/{id}/notes", m.listSpaceNotes)
	r.API("GET /api/spaces/{id}/tree", m.spaceTree)
	r.API("GET /api/spaces/{id}/tags", m.spaceTags)
	r.API("GET /api/spaces/{id}/trash", m.spaceTrash)
	r.API("POST /api/spaces/{id}/trash/empty", m.emptyTrash)
	r.API("POST /api/render", m.render)
	return nil
}
