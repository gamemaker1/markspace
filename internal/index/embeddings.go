package index

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// vectorElementSize is the byte width of one stored float32.
const vectorElementSize = 4

// EncodeVector packs an embedding as little-endian float32 bytes. A nil or
// empty vector encodes as nil, which stores a SQL NULL.
func EncodeVector(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, len(v)*vectorElementSize)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*vectorElementSize:], math.Float32bits(f))
	}
	return buf
}

// DecodeVector unpacks little-endian float32 bytes into an embedding. A
// length that is not a multiple of four is a corrupt row and reports
// core.ErrInvalid.
func DecodeVector(b []byte) ([]float32, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if len(b)%vectorElementSize != 0 {
		return nil, fmt.Errorf("index: decode vector: %d bytes is not a multiple of %d: %w",
			len(b), vectorElementSize, core.ErrInvalid)
	}
	out := make([]float32, len(b)/vectorElementSize)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*vectorElementSize:]))
	}
	return out, nil
}

// ReplaceEmbeddings swaps the stored chunks of one note for the ones given,
// in a single transaction. An empty chunk list removes the note's
// embeddings.
func (d *DB) ReplaceEmbeddings(ctx context.Context, noteID, spaceID string, chunks []*core.EmbeddingChunk) error {
	if noteID == "" {
		return fmt.Errorf("index: replace embeddings: %w", core.Invalid("noteId", "is required"))
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		if _, err := t.ExecContext(ctx, `DELETE FROM embeddings WHERE note_id = ?`, noteID); err != nil {
			return writeErr("replace embeddings", err)
		}
		const q = `INSERT INTO embeddings (id, note_id, space_id, ordinal, text, vector,
			model, hash, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
		now := time.Now().UTC()
		for i, c := range chunks {
			if c == nil {
				continue
			}
			if c.ID == "" {
				c.ID = core.NewID()
			}
			c.NoteID = noteID
			c.SpaceID = spaceID
			if c.Ordinal == 0 {
				c.Ordinal = i
			}
			if c.UpdatedAt.IsZero() {
				c.UpdatedAt = now
			}
			_, err := t.ExecContext(ctx, q, c.ID, c.NoteID, c.SpaceID, c.Ordinal, c.Text,
				EncodeVector(c.Vector), c.Model, c.Hash, fmtTime(c.UpdatedAt))
			if err != nil {
				return writeErr("replace embeddings", err)
			}
		}
		return nil
	})
}

// DeleteEmbeddings removes every chunk of one note.
func (d *DB) DeleteEmbeddings(ctx context.Context, noteID string) error {
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM embeddings WHERE note_id = ?`, noteID); err != nil {
		return writeErr("delete embeddings", err)
	}
	return nil
}

// ListEmbeddings returns every chunk in the given spaces, ordered by note and
// then by chunk position. An empty model lists every model, which lets a
// caller inspect what is stored. An empty space list returns no chunks.
func (d *DB) ListEmbeddings(ctx context.Context, spaceIDs []string, model string) ([]*core.EmbeddingChunk, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	where := []string{`space_id IN (` + placeholders(len(spaceIDs)) + `)`}
	args := stringArgs(spaceIDs)
	if m := strings.TrimSpace(model); m != "" {
		where = append(where, `model = ?`)
		args = append(args, m)
	}
	q := `SELECT id, note_id, space_id, ordinal, text, vector, model, hash, updated_at
		FROM embeddings WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY note_id, ordinal`

	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, readErr("list embeddings", err)
	}
	defer rows.Close()

	var out []*core.EmbeddingChunk
	for rows.Next() {
		var (
			c       core.EmbeddingChunk
			blob    []byte
			updated string
		)
		err := rows.Scan(&c.ID, &c.NoteID, &c.SpaceID, &c.Ordinal, &c.Text, &blob,
			&c.Model, &c.Hash, &updated)
		if err != nil {
			return nil, readErr("list embeddings", err)
		}
		vector, err := DecodeVector(blob)
		if err != nil {
			return nil, err
		}
		c.Vector = vector
		c.UpdatedAt = parseTime(updated)
		out = append(out, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list embeddings", err)
	}
	return out, nil
}

// EmbeddingHashes maps note id to the content hash last embedded in one
// space. Every chunk of a note carries the same hash, so one row per note is
// enough to decide whether the note needs embedding again.
func (d *DB) EmbeddingHashes(ctx context.Context, spaceID string) (map[string]string, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT note_id, MIN(hash) FROM embeddings WHERE space_id = ? GROUP BY note_id`, spaceID)
	if err != nil {
		return nil, readErr("embedding hashes", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var noteID, hash string
		if err := rows.Scan(&noteID, &hash); err != nil {
			return nil, readErr("embedding hashes", err)
		}
		out[noteID] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("embedding hashes", err)
	}
	return out, nil
}

// CountEmbeddings returns the number of stored chunks.
func (d *DB) CountEmbeddings(ctx context.Context) (int, error) {
	return d.count(ctx, `SELECT COUNT(*) FROM embeddings`, "count embeddings")
}
