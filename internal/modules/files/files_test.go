package files

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// serve builds the API handler over b with this module registered.
func serve(t *testing.T, b core.Backend) http.Handler {
	t.Helper()
	env := testkit.Env(b)
	return testkit.Serve(t, env, New(env))
}

func TestUploadFileStreamsPart(t *testing.T) {
	var got core.FileUpload
	var gotContent string
	fake := &testkit.Fake{
		UploadFileFn: func(_ context.Context, _ *core.Actor, in core.FileUpload, r io.Reader) (*core.File, error) {
			got = in
			b, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			gotContent = string(b)
			return &core.File{ID: "f1", SpaceID: in.SpaceID, Name: in.Name, Size: int64(len(b))}, nil
		},
	}
	body, ct := testkit.MultipartBody(t, [][2]string{
		{"spaceId", "sp1"}, {"parentId", "fo1"}, {"id", "client1"}, {"onConflict", "rename"}, {"pasteFor", "n1"},
	}, "dir/report.pdf", "%PDF-1.4 body")
	rec := testkit.ServeBody(t, serve(t, fake), "POST", "/api/files", body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	want := core.FileUpload{ID: "client1", SpaceID: "sp1", ParentID: "fo1", Name: "report.pdf", OnConflict: "rename", PasteFor: "n1"}
	if got != want {
		t.Errorf("upload input = %+v, want %+v", got, want)
	}
	if gotContent != "%PDF-1.4 body" {
		t.Errorf("content = %q", gotContent)
	}
	var file core.File
	testkit.DecodeBody(t, rec, &file)
	if file.ID != "f1" || file.Name != "report.pdf" {
		t.Errorf("response = %+v", file)
	}
}

func TestUploadFileWithoutFilePartIs400(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        io.Reader
		contentType string
	}{
		{"fields only", nil, ""},
		{"not multipart", strings.NewReader(`{"spaceId":"sp1"}`), "application/json"},
		{"broken multipart", strings.NewReader("--x\r\ngarbage"), "multipart/form-data; boundary=x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, ct := tc.body, tc.contentType
			if body == nil {
				buf, c := testkit.MultipartBody(t, [][2]string{{"spaceId", "sp1"}}, "", "")
				body, ct = buf, c
			}
			rec := testkit.ServeBody(t, serve(t, &testkit.Fake{}), "POST", "/api/files", body, ct)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestUploadFileOverLimitIs413(t *testing.T) {
	fake := &testkit.Fake{
		UploadFileFn: func(_ context.Context, _ *core.Actor, _ core.FileUpload, r io.Reader) (*core.File, error) {
			if _, err := io.Copy(io.Discard, r); err != nil {
				return nil, fmt.Errorf("save file: %w", err)
			}
			return &core.File{ID: "f1"}, nil
		},
	}
	// The test server allows 1 MiB plus 1 MiB of multipart overhead.
	body, ct := testkit.MultipartBody(t, [][2]string{{"spaceId", "sp1"}}, "big.bin", strings.Repeat("x", 3<<20))
	rec := testkit.ServeBody(t, serve(t, fake), "POST", "/api/files", body, ct)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body)
	}
}

func TestReplaceFileContent(t *testing.T) {
	var gotID string
	var got core.FileReplace
	fake := &testkit.Fake{
		ReplaceFileFn: func(_ context.Context, _ *core.Actor, id string, in core.FileReplace, r io.Reader) (*core.File, error) {
			gotID, got = id, in
			if _, err := io.Copy(io.Discard, r); err != nil {
				return nil, err
			}
			if in.BaseSHA256 != "abc" {
				return nil, fmt.Errorf("stale base: %w", core.ErrConflict)
			}
			return &core.File{ID: id, SHA256: "def"}, nil
		},
	}
	h := serve(t, fake)
	body, ct := testkit.MultipartBody(t, [][2]string{{"baseSha256", "abc"}}, "photo.png", "png")
	rec := testkit.ServeBody(t, h, "PUT", "/api/files/f1/content", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if gotID != "f1" || got != (core.FileReplace{Name: "photo.png", BaseSHA256: "abc"}) {
		t.Errorf("replace args = %q %+v", gotID, got)
	}
	body, ct = testkit.MultipartBody(t, [][2]string{{"baseSha256", "old"}}, "photo.png", "png")
	rec = testkit.ServeBody(t, h, "PUT", "/api/files/f1/content", body, ct)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale base status = %d, want 409: %s", rec.Code, rec.Body)
	}
}

func TestItemRouteStatuses(t *testing.T) {
	var moved struct {
		id, parent string
		pos        int
	}
	var copied core.CopyInput
	fake := &testkit.Fake{
		MoveItemFn: func(_ context.Context, _ *core.Actor, id, parentID string, position int) (*core.Item, error) {
			moved.id, moved.parent, moved.pos = id, parentID, position
			return &core.Item{Kind: core.KindFile, File: &core.File{ID: id, ParentID: parentID}}, nil
		},
		CopyItemFn: func(_ context.Context, _ *core.Actor, id string, in core.CopyInput) (*core.Item, error) {
			copied = in
			return &core.Item{Kind: core.KindFolder, Folder: &core.Folder{ID: "copy1"}}, nil
		},
	}
	h := serve(t, fake)
	rec := testkit.Do(t, h, "POST", "/api/items/i1/move", `{"parentId":"fo2","position":300}`, "")
	if rec.Code != http.StatusOK || moved.id != "i1" || moved.parent != "fo2" || moved.pos != 300 {
		t.Errorf("move = %d %+v: %s", rec.Code, moved, rec.Body)
	}
	rec = testkit.Do(t, h, "POST", "/api/items/i1/copy", `{"spaceId":"sp2","parentId":"fo3","name":"x copy"}`, "")
	if rec.Code != http.StatusCreated || copied != (core.CopyInput{SpaceID: "sp2", ParentID: "fo3", Name: "x copy"}) {
		t.Errorf("copy = %d %+v: %s", rec.Code, copied, rec.Body)
	}
	var item core.Item
	testkit.DecodeBody(t, rec, &item)
	if item.Kind != core.KindFolder || item.Folder == nil || item.Folder.ID != "copy1" {
		t.Errorf("copy response = %+v", item)
	}
	if rec := testkit.Do(t, h, "POST", "/api/items/i1/move", `{"bogus":1}`, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("move with unknown field = %d, want 400", rec.Code)
	}

	calls := map[string]int{}
	ok := &testkit.Fake{}
	statusBackend := &itemStatusBackend{Fake: ok, calls: calls}
	okH := serve(t, statusBackend)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/items/i1/trash", http.StatusNoContent},
		{"POST", "/api/items/i1/restore", http.StatusOK},
		{"DELETE", "/api/items/i1", http.StatusNoContent},
		{"GET", "/api/items/i1", http.StatusOK},
	} {
		if rec := testkit.Do(t, okH, tc.method, tc.path, "", ""); rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d: %s", tc.method, tc.path, rec.Code, tc.want, rec.Body)
		}
	}
	if calls["trash"] != 1 || calls["restore"] != 1 || calls["delete"] != 1 {
		t.Errorf("calls = %v", calls)
	}
	if rec := testkit.Do(t, h, "POST", "/api/items/i1/trash", "", ""); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("stub trash = %d, want the mapped stub error 415", rec.Code)
	}
}

// itemStatusBackend answers the item routes with success and counts calls.
type itemStatusBackend struct {
	*testkit.Fake
	calls map[string]int
}

func (b *itemStatusBackend) TrashItem(context.Context, *core.Actor, string) error {
	b.calls["trash"]++
	return nil
}

func (b *itemStatusBackend) RestoreItem(_ context.Context, _ *core.Actor, id string) (*core.Item, error) {
	b.calls["restore"]++
	return &core.Item{Kind: core.KindNote, Note: &core.Note{ID: id}}, nil
}

func (b *itemStatusBackend) DeleteItem(context.Context, *core.Actor, string) error {
	b.calls["delete"]++
	return nil
}

func (b *itemStatusBackend) GetItem(_ context.Context, _ *core.Actor, id string) (*core.Item, error) {
	return &core.Item{Kind: core.KindFile, File: &core.File{ID: id}, Access: core.AccessView}, nil
}

func TestResolveEnvelope(t *testing.T) {
	var got core.ResolveRequest
	fake := &testkit.Fake{
		ResolveLinksFn: func(_ context.Context, _ *core.Actor, in core.ResolveRequest) ([]*core.ResolvedTarget, error) {
			got = in
			return []*core.ResolvedTarget{
				{Target: "east wall.jpg", Kind: core.KindFile, ID: "f1", Path: "Projects/east wall.jpg", URL: "/files/demo/f1/east%20wall.jpg"},
				{Target: "missing"},
			}, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/resolve",
		`{"spaceId":"sp1","fromNoteId":"n1","targets":["east wall.jpg","missing"]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if got.SpaceID != "sp1" || got.FromNoteID != "n1" || !reflect.DeepEqual(got.Targets, []string{"east wall.jpg", "missing"}) {
		t.Errorf("request = %+v", got)
	}
	var env struct {
		Items []core.ResolvedTarget `json:"items"`
	}
	testkit.DecodeBody(t, rec, &env)
	if len(env.Items) != 2 || env.Items[0].ID != "f1" || env.Items[1].ID != "" {
		t.Errorf("envelope = %+v", env)
	}
}
