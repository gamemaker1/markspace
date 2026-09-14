// Package files is the built-in module for folders, files and the
// operations on items of any kind: move, copy, trash, restore, delete,
// backlinks and link resolution.
package files

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the folder, file and item routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "files", Name: "Files", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("POST /api/folders", m.createFolder)
	r.API("GET /api/folders/{id}", m.getFolder)
	r.API("PATCH /api/folders/{id}", m.renameFolder)
	r.API("POST /api/files", m.uploadFile)
	r.API("GET /api/files/{id}", m.getFile)
	r.API("PATCH /api/files/{id}", m.renameFile)
	r.API("PUT /api/files/{id}/content", m.replaceFileContent)
	r.API("GET /api/files/{id}/versions", m.listFileVersions)
	r.API("POST /api/files/{id}/versions/{versionId}/restore", m.restoreFileVersion)
	r.API("GET /api/files/{id}/backlinks", m.itemBacklinks)
	r.API("GET /api/spaces/{id}/files", m.listSpaceFiles)
	r.API("GET /api/items/{id}", m.getItem)
	r.API("POST /api/items/{id}/move", m.moveItem)
	r.API("POST /api/items/{id}/copy", m.copyItem)
	r.API("POST /api/items/{id}/trash", m.trashItem)
	r.API("POST /api/items/{id}/restore", m.restoreItem)
	r.API("DELETE /api/items/{id}", m.deleteItem)
	r.API("GET /api/items/{id}/backlinks", m.itemBacklinks)
	r.API("POST /api/resolve", m.resolveLinks)
	return nil
}
