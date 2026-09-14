package system

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
)

// SnapshotName is the name of the database snapshot inside a backup archive.
const SnapshotName = "markspace.db"

// snapshotPrefix starts the name of the temporary snapshot file in the data
// directory. The walk skips it.
const snapshotPrefix = ".backup-"

// backup serves GET /api/admin/backup. It answers a gzip tar with a
// consistent snapshot of the database at markspace.db and every other file
// of the data directory. Files are read as they are during the walk, so a
// note saved during the backup may be in its old or new state.
//
// An error after the first byte cannot change the status code. The handler
// then stops writing, the gzip stream has no trailer, and the reader sees a
// truncated archive.
func (m *Module) backup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := api.ActorFrom(ctx)
	if err := api.RequireAdmin(actor); err != nil {
		api.WriteError(w, r, m.log, err)
		return
	}
	snap, err := m.snapshot(ctx)
	if err != nil {
		api.WriteError(w, r, m.log, err)
		return
	}
	defer os.Remove(snap)

	name := "markspace-backup-" + m.opts.Now().UTC().Format("20060102T150405Z") + ".tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	n, err := m.writeArchive(ctx, w, snap)
	if err != nil {
		m.log.Error("backup failed", "by", actor.UserID(), "err", err)
		return
	}
	m.log.Info("backup written", "by", actor.UserID(), "files", n)
}

// snapshot writes a consistent copy of the database with VACUUM INTO to a
// temporary file in the data directory and returns its path.
func (m *Module) snapshot(ctx context.Context) (string, error) {
	if m.opts.DB == nil {
		return "", errors.New("backup: no database")
	}
	f, err := os.CreateTemp(m.opts.DataDir, snapshotPrefix+"*.db")
	if err != nil {
		return "", fmt.Errorf("backup: create snapshot file: %w", err)
	}
	path := f.Name()
	f.Close()
	// VACUUM INTO needs a missing or empty file. CreateTemp leaves it empty.
	if _, err := m.opts.DB.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("backup: snapshot database: %w", err)
	}
	return path, nil
}

// writeArchive writes the gzip tar and returns the number of files in it.
func (m *Module) writeArchive(ctx context.Context, w io.Writer, snap string) (int, error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	files := 0
	if err := addFile(tw, snap, SnapshotName); err != nil {
		return 0, err
	}
	files++

	skip := m.skipped()
	root := m.opts.DataDir
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip(rel, d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = rel + "/"
			return tw.WriteHeader(hdr)
		case d.Type().IsRegular():
			if err := addFile(tw, path, rel); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			files++
		}
		// Sockets, pipes and symbolic links are left out.
		return nil
	})
	if err != nil {
		return files, fmt.Errorf("backup: walk data dir: %w", err)
	}
	if err := tw.Close(); err != nil {
		return files, fmt.Errorf("backup: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return files, fmt.Errorf("backup: close gzip: %w", err)
	}
	return files, nil
}

// skipped returns the filter of names the walk leaves out: the live database
// and its journal files, the snapshot, the configured names, and the
// temporary files of the store and the plugin host.
func (m *Module) skipped() func(rel, base string) bool {
	names := map[string]bool{}
	for _, s := range m.opts.Skip {
		names[filepath.ToSlash(s)] = true
	}
	if rel, err := filepath.Rel(m.opts.DataDir, m.opts.DBPath); err == nil && !strings.HasPrefix(rel, "..") {
		rel = filepath.ToSlash(rel)
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			names[rel+suffix] = true
		}
	}
	return func(rel, base string) bool {
		return names[rel] ||
			strings.HasPrefix(base, snapshotPrefix) ||
			strings.HasPrefix(base, ".upload-") ||
			strings.Contains(base, ".tmp-")
	}
}

// addFile copies one regular file into the archive under name. The size in
// the header comes from the open file, so a file replaced by a rename during
// the walk is copied whole. A file that shrinks while it is read fails the
// backup.
func addFile(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Uname, hdr.Gname = "", ""
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.CopyN(tw, f, info.Size()); err != nil {
		return fmt.Errorf("copy %s, it changed during the backup: %w", name, err)
	}
	return nil
}
