package index

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestVectorEncoding(t *testing.T) {
	tests := []struct {
		name string
		in   []float32
	}{
		{"nil", nil},
		{"one", []float32{1}},
		{"mixed", []float32{0, -1.5, 3.25, math.MaxFloat32, -math.SmallestNonzeroFloat32}},
		{"special", []float32{float32(math.Inf(1)), float32(math.Inf(-1))}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := EncodeVector(tc.in)
			if len(b) != len(tc.in)*4 {
				t.Fatalf("encoded %d floats into %d bytes", len(tc.in), len(b))
			}
			got, err := DecodeVector(b)
			if err != nil {
				t.Fatalf("DecodeVector: %v", err)
			}
			if !slices.Equal(got, tc.in) {
				t.Fatalf("round trip gave %v, want %v", got, tc.in)
			}
		})
	}

	// Little-endian layout check: 1.0 is 0x3f800000.
	if got := EncodeVector([]float32{1}); !slices.Equal(got, []byte{0x00, 0x00, 0x80, 0x3f}) {
		t.Fatalf("1.0 encoded as % x, want 00 00 80 3f", got)
	}

	nan, err := DecodeVector(EncodeVector([]float32{float32(math.NaN())}))
	if err != nil || len(nan) != 1 || !math.IsNaN(float64(nan[0])) {
		t.Fatalf("NaN did not round trip: %v, %v", nan, err)
	}

	if _, err := DecodeVector([]byte{1, 2, 3}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("a 3-byte vector gave %v, want core.ErrInvalid", err)
	}
}

func TestEmbeddingsReplaceAndList(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	other := mustSpace(t, db, "other")
	a := mustNote(t, db, space.ID, "A", "")
	b := mustNote(t, db, space.ID, "B", "")
	c := mustNote(t, db, other.ID, "C", "")

	first := []*core.EmbeddingChunk{
		{Ordinal: 0, Text: "chunk zero", Vector: []float32{0.1, 0.2, 0.3}, Model: "m1", Hash: "h1"},
		{Ordinal: 1, Text: "chunk one", Vector: []float32{0.4, 0.5, 0.6}, Model: "m1", Hash: "h1"},
	}
	if err := db.ReplaceEmbeddings(ctx, a.ID, space.ID, first); err != nil {
		t.Fatalf("ReplaceEmbeddings: %v", err)
	}
	if err := db.ReplaceEmbeddings(ctx, b.ID, space.ID, []*core.EmbeddingChunk{
		{Text: "b", Vector: []float32{1, 2}, Model: "m2", Hash: "hb"},
	}); err != nil {
		t.Fatalf("ReplaceEmbeddings b: %v", err)
	}
	if err := db.ReplaceEmbeddings(ctx, c.ID, other.ID, []*core.EmbeddingChunk{
		{Text: "c", Vector: []float32{9}, Model: "m1", Hash: "hc"},
	}); err != nil {
		t.Fatalf("ReplaceEmbeddings c: %v", err)
	}

	list, err := db.ListEmbeddings(ctx, []string{space.ID}, "m1")
	if err != nil {
		t.Fatalf("ListEmbeddings: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListEmbeddings gave %d chunks, want 2", len(list))
	}
	if list[0].Ordinal != 0 || list[1].Ordinal != 1 || list[0].NoteID != a.ID || list[0].SpaceID != space.ID {
		t.Fatalf("chunks came back out of order or unlabelled: %+v", list[0])
	}
	if !slices.Equal(list[1].Vector, []float32{0.4, 0.5, 0.6}) {
		t.Fatalf("vector read back as %v", list[1].Vector)
	}
	if list[0].ID == "" || list[0].UpdatedAt.IsZero() {
		t.Fatal("chunks should get an id and an update time")
	}

	if all, _ := db.ListEmbeddings(ctx, []string{space.ID}, ""); len(all) != 3 {
		t.Fatalf("an empty model should list every model, got %d chunks", len(all))
	}
	if none, err := db.ListEmbeddings(ctx, nil, "m1"); err != nil || len(none) != 0 {
		t.Fatalf("no spaces gave %d chunks, %v", len(none), err)
	}

	// Replacing swaps the old chunks out entirely.
	if err := db.ReplaceEmbeddings(ctx, a.ID, space.ID, []*core.EmbeddingChunk{
		{Text: "new", Vector: []float32{7, 8, 9}, Model: "m1", Hash: "h2"},
	}); err != nil {
		t.Fatalf("second ReplaceEmbeddings: %v", err)
	}
	list, _ = db.ListEmbeddings(ctx, []string{space.ID}, "m1")
	if len(list) != 1 || list[0].Text != "new" || !slices.Equal(list[0].Vector, []float32{7, 8, 9}) {
		t.Fatalf("after replace got %d chunks: %+v", len(list), list)
	}

	hashes, err := db.EmbeddingHashes(ctx, space.ID)
	if err != nil {
		t.Fatalf("EmbeddingHashes: %v", err)
	}
	if len(hashes) != 2 || hashes[a.ID] != "h2" || hashes[b.ID] != "hb" {
		t.Fatalf("EmbeddingHashes gave %v", hashes)
	}

	if n, err := db.CountEmbeddings(ctx); err != nil || n != 3 {
		t.Fatalf("CountEmbeddings = %d, %v, want 3", n, err)
	}

	if err := db.DeleteEmbeddings(ctx, a.ID); err != nil {
		t.Fatalf("DeleteEmbeddings: %v", err)
	}
	if hashes, _ = db.EmbeddingHashes(ctx, space.ID); len(hashes) != 1 {
		t.Fatalf("after delete %d notes have hashes, want 1", len(hashes))
	}

	// Removing a note row removes its embeddings too.
	if err := db.DeleteNoteRow(ctx, b.ID); err != nil {
		t.Fatalf("DeleteNoteRow: %v", err)
	}
	if n, _ := db.CountEmbeddings(ctx); n != 1 {
		t.Fatalf("CountEmbeddings after DeleteNoteRow = %d, want 1", n)
	}

	if err := db.ReplaceEmbeddings(ctx, "", space.ID, nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("an empty note id gave %v, want core.ErrInvalid", err)
	}
}
