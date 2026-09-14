package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// signupOwner signs up the first user and returns the personal space.
func (h *harness) signupOwner() *core.Space {
	h.t.Helper()
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "owner@example.com", "name": "Olive Owner", "password": "correct horse battery",
	}, nil), 201, "signup")
	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)
	if len(spaces.Items) == 0 {
		h.t.Fatal("no personal space")
	}
	return spaces.Items[0]
}

// multipartBody writes the fields in order, then the file part.
func multipartBody(fields [][2]string, name string, body []byte) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		mw.WriteField(f[0], f[1])
	}
	part, _ := mw.CreateFormFile("file", name)
	part.Write(body)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

// send issues a multipart request and returns the status and body.
func (h *harness) send(method, path string, fields [][2]string, name string, body []byte) (int, []byte) {
	h.t.Helper()
	buf, ct := multipartBody(fields, name, body)
	req, _ := http.NewRequest(method, h.server.URL+path, buf)
	req.Header.Set("Content-Type", ct)
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// uploadTo posts a file into a folder, "" for the root, and fails unless it
// is created.
func (h *harness) uploadTo(spaceID, parentID, name string, body []byte) *core.File {
	h.t.Helper()
	status, data := h.send("POST", "/api/files", [][2]string{{"spaceId", spaceID}, {"parentId", parentID}}, name, body)
	if status != http.StatusCreated {
		h.t.Fatalf("upload %s: status %d: %s", name, status, data)
	}
	var f core.File
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("upload %s: decode: %v", name, err)
	}
	return &f
}

// get fetches a path with optional headers and returns the response and body.
func (h *harness) get(client *http.Client, path string, header map[string]string) (*http.Response, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest("GET", h.server.URL+path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func TestFileUploadConflictsAndReplay(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()

	first := h.uploadTo(space.ID, "", "report.pdf", []byte("%PDF-1.4\none\n%%EOF"))
	if first.Name != "report.pdf" || first.Path != "report.pdf" || first.SHA256 == "" || first.URL == "" {
		t.Fatalf("upload = %+v", first)
	}
	if status, _ := h.send("POST", "/api/files", [][2]string{{"spaceId", space.ID}}, "report.pdf", []byte("x")); status != http.StatusConflict {
		t.Fatalf("duplicate name status %d, want 409", status)
	}
	status, data := h.send("POST", "/api/files", [][2]string{{"spaceId", space.ID}, {"onConflict", "rename"}}, "report.pdf", []byte("two"))
	var renamed core.File
	json.Unmarshal(data, &renamed)
	if status != http.StatusCreated || renamed.Name != "report (2).pdf" {
		t.Fatalf("rename on conflict = %d %s", status, data)
	}
	status, data = h.send("POST", "/api/files", [][2]string{{"spaceId", space.ID}, {"onConflict", "replace"}}, "report.pdf", []byte("%PDF-1.4\nthree\n%%EOF"))
	var replaced core.File
	json.Unmarshal(data, &replaced)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("replace on conflict status %d: %s", status, data)
	}
	if replaced.ID != first.ID || replaced.SHA256 == first.SHA256 || replaced.Versions != 1 {
		t.Fatalf("replace on conflict = %+v, want the same id with a new hash and one version", replaced)
	}

	// Fields after the file part are not read, so the space is missing.
	buf, ct := multipartBody(nil, "late.txt", []byte("x"))
	late := bytes.NewBufferString(strings.TrimSuffix(buf.String(), "\r\n"))
	_ = late
	req, _ := http.NewRequest("POST", h.server.URL+"/api/files", buf)
	req.Header.Set("Content-Type", ct)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("upload without space status %d, want 4xx", resp.StatusCode)
	}

	offline := "0" + strings.Repeat("b", 25)
	fields := [][2]string{{"spaceId", space.ID}, {"id", offline}}
	status, data = h.send("POST", "/api/files", fields, "offline.txt", []byte("offline"))
	var once, twice core.File
	json.Unmarshal(data, &once)
	if status != http.StatusCreated || once.ID != offline {
		t.Fatalf("client id upload = %d %s", status, data)
	}
	status, data = h.send("POST", "/api/files", fields, "offline.txt", []byte("offline"))
	json.Unmarshal(data, &twice)
	if status >= 300 || twice.ID != offline || twice.Path != once.Path {
		t.Fatalf("replayed upload = %d %s", status, data)
	}
	var list struct{ Items []*core.File }
	h.mustStatus(h.do("GET", "/api/spaces/"+space.ID+"/files", nil, &list), 200, "list files")
	if len(list.Items) != 3 {
		t.Fatalf("space lists %d files, want 3", len(list.Items))
	}
}

func TestFileRangeVersionsAndConflict(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()
	f := h.uploadTo(space.ID, "", "data.txt", []byte("0123456789"))

	resp, body := h.get(h.client, f.URL, map[string]string{"Range": "bytes=2-5"})
	if resp.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("range = %d %q", resp.StatusCode, body)
	}

	path := "/api/files/" + f.ID + "/content"
	status, data := h.send("PUT", path, [][2]string{{"baseSha256", f.SHA256}}, "data.txt", []byte("abcdefghij"))
	var next core.File
	json.Unmarshal(data, &next)
	if status != http.StatusOK || next.SHA256 == f.SHA256 || next.ID != f.ID {
		t.Fatalf("replace = %d %s", status, data)
	}
	if status, data := h.send("PUT", path, [][2]string{{"baseSha256", f.SHA256}}, "data.txt", []byte("stale")); status != http.StatusConflict {
		t.Fatalf("stale replace = %d %s, want 409", status, data)
	}
	if _, body := h.get(h.client, f.URL, nil); string(body) != "abcdefghij" {
		t.Fatalf("content after a refused replace = %q", body)
	}

	var versions struct{ Items []*core.FileVersion }
	h.mustStatus(h.do("GET", "/api/files/"+f.ID+"/versions", nil, &versions), 200, "versions")
	if len(versions.Items) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions.Items))
	}
	v := versions.Items[0]
	if resp, body := h.get(h.client, f.URL+"?v="+v.ID, nil); resp.StatusCode != 200 || string(body) != "0123456789" {
		t.Fatalf("version content = %d %q", resp.StatusCode, body)
	}
	h.mustStatus(h.do("POST", "/api/files/"+f.ID+"/versions/"+v.ID+"/restore", nil, nil), 200, "restore version")
	if _, body := h.get(h.client, f.URL, nil); string(body) != "0123456789" {
		t.Fatalf("content after restore = %q", body)
	}
	h.do("GET", "/api/files/"+f.ID+"/versions", nil, &versions)
	if len(versions.Items) != 2 {
		t.Fatalf("versions after restore = %d, want 2", len(versions.Items))
	}
}

func TestThumbnailsAndPublicFileLink(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()
	png := aiCtxPNG(t, 300, 200)
	f := h.uploadTo(space.ID, "", "chart.png", png)
	if f.Thumb == "" || f.Width != 300 {
		t.Fatalf("image upload = %+v", f)
	}
	resp, body := h.get(h.client, f.Thumb+"?w=256", nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") || len(body) == 0 {
		t.Fatalf("thumbnail = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := h.get(h.client, f.Thumb+"?w=100", nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad width status %d, want 400", resp.StatusCode)
	}

	var link core.PublicLink
	h.mustStatus(h.do("POST", "/api/items/"+f.ID+"/links", core.PublicLinkInput{NoteID: f.ID, Access: core.AccessView}, &link), 201, "create link")
	jar, _ := cookiejar.New(nil)
	anon := &http.Client{Jar: jar}
	resp, page := h.get(anon, "/p/"+link.Token, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(page), "chart.png") {
		t.Fatalf("public file page = %d", resp.StatusCode)
	}
	resp, raw := h.get(anon, "/p/"+link.Token+"/raw/"+f.ID+"/chart.png", nil)
	if resp.StatusCode != 200 || !bytes.Equal(raw, png) {
		t.Fatalf("public raw = %d, %d bytes", resp.StatusCode, len(raw))
	}
	if _, body := h.get(anon, f.URL, nil); bytes.Equal(body, png) {
		t.Fatal("anonymous client read the file outside the link")
	}
}

func TestTrashRestoreAndDeleteFile(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()
	f := h.uploadTo(space.ID, "", "notes.txt", []byte("hello"))
	dir := filepath.Join(h.cfg.SpacesDir(), space.Slug)

	h.mustStatus(h.do("POST", "/api/items/"+f.ID+"/trash", nil, nil), 204, "trash")
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("trashed file still in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".trash", f.ID, "notes.txt")); err != nil {
		t.Fatalf("trash slot missing: %v", err)
	}
	var trashed core.File
	h.mustStatus(h.do("GET", "/api/files/"+f.ID, nil, &trashed), 200, "get trashed")
	if !trashed.Deleted {
		t.Fatal("file not marked deleted")
	}
	var trash struct{ Items []*core.TreeItem }
	h.mustStatus(h.do("GET", "/api/spaces/"+space.ID+"/trash?items=1", nil, &trash), 200, "trash items")
	if len(trash.Items) != 1 || trash.Items[0].File == nil || trash.Items[0].File.ID != f.ID {
		t.Fatalf("trash items = %+v", trash.Items)
	}
	var restored core.Item
	h.mustStatus(h.do("POST", "/api/items/"+f.ID+"/restore", nil, &restored), 200, "restore")
	if restored.File == nil || restored.File.Deleted || restored.File.Path != "notes.txt" {
		t.Fatalf("restored = %+v", restored)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "notes.txt")); err != nil || string(data) != "hello" {
		t.Fatalf("restored file = %q, %v", data, err)
	}
	h.mustStatus(h.do("DELETE", "/api/items/"+f.ID, nil, nil), 204, "delete")
	h.mustStatus(h.do("GET", "/api/files/"+f.ID, nil, nil), 404, "get deleted")
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file still on disk: %v", err)
	}
}

func TestMoveAndRenameRewriteLinks(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()
	f := h.uploadTo(space.ID, "", "east wall.jpg", []byte("jpeg bytes"))
	var note core.Note
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{
		SpaceID: space.ID, Title: "Survey", Content: "Photo ![](<east wall.jpg>) and [[east wall.jpg]].\n",
	}, &note), 201, "create note")
	var folder core.Folder
	h.mustStatus(h.do("POST", "/api/folders", core.FolderInput{SpaceID: space.ID, Name: "Photos"}, &folder), 201, "folder")

	var moved core.Item
	h.mustStatus(h.do("POST", "/api/items/"+f.ID+"/move", map[string]any{"parentId": folder.ID}, &moved), 200, "move")
	if moved.File == nil || moved.File.Path != "Photos/east wall.jpg" {
		t.Fatalf("moved = %+v", moved)
	}
	var got core.Note
	h.mustStatus(h.do("GET", "/api/notes/"+note.ID, nil, &got), 200, "read note")
	if !strings.Contains(got.Content, "![](<Photos/east wall.jpg>)") || !strings.Contains(got.Content, "[[east wall.jpg]]") {
		t.Fatalf("body after move = %q", got.Content)
	}

	h.mustStatus(h.do("PATCH", "/api/files/"+f.ID, map[string]string{"name": "west wall.jpg"}, nil), 200, "rename")
	h.mustStatus(h.do("GET", "/api/notes/"+note.ID, nil, &got), 200, "read note")
	if !strings.Contains(got.Content, "![](<Photos/west wall.jpg>)") || !strings.Contains(got.Content, "[[west wall.jpg]]") {
		t.Fatalf("body after rename = %q", got.Content)
	}
	var versions struct{ Items []*core.NoteVersion }
	h.do("GET", "/api/notes/"+note.ID+"/versions", nil, &versions)
	labelled := false
	for _, v := range versions.Items {
		labelled = labelled || v.Label == "Links updated"
	}
	if !labelled {
		t.Fatalf("no Links updated version among %d", len(versions.Items))
	}
	var backlinks struct{ Items []*core.Backlink }
	h.mustStatus(h.do("GET", "/api/files/"+f.ID+"/backlinks", nil, &backlinks), 200, "backlinks")
	if len(backlinks.Items) != 1 || backlinks.Items[0].FromNote.ID != note.ID {
		t.Fatalf("backlinks = %+v", backlinks.Items)
	}
}

// TestFolderNoteThroughAPI creates the folder note of an existing folder with
// POST /api/notes and folderNote.
func TestFolderNoteThroughAPI(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()
	var folder core.Folder
	h.mustStatus(h.do("POST", "/api/folders", core.FolderInput{SpaceID: space.ID, Name: "Wiki"}, &folder), 201, "folder")
	body := map[string]any{"spaceId": space.ID, "folderId": folder.ID, "folderNote": true, "title": "Wiki index", "content": "# Index\n"}
	var note core.Note
	h.mustStatus(h.do("POST", "/api/notes", body, &note), 201, "folder note")
	if note.Path != "Wiki/index.md" || note.ParentID != folder.ID || note.Title != "Wiki index" {
		t.Fatalf("folder note = %s parent %s title %q", note.Path, note.ParentID, note.Title)
	}
	data, err := os.ReadFile(filepath.Join(h.cfg.SpacesDir(), space.Slug, "Wiki", "index.md"))
	if err != nil || !strings.Contains(string(data), "# Index") {
		t.Fatalf("index.md on disk: %q, %v", data, err)
	}
	var view struct {
		Folder *core.Folder `json:"folder"`
	}
	h.mustStatus(h.do("GET", "/api/folders/"+folder.ID, nil, &view), 200, "get folder")
	if view.Folder == nil || view.Folder.IndexNoteID != note.ID {
		t.Fatalf("folder view = %+v, want index note %s", view.Folder, note.ID)
	}

	h.mustStatus(h.do("POST", "/api/notes", body, nil), 409, "second folder note")
	h.mustStatus(h.do("POST", "/api/notes", map[string]any{"spaceId": space.ID, "folderNote": true, "title": "X"}, nil), 400, "folderNote without folderId")
	h.mustStatus(h.do("POST", "/api/notes", map[string]any{"spaceId": space.ID, "folderId": folder.ID, "title": "X"}, nil), 400, "folderId without folderNote")
	h.mustStatus(h.do("POST", "/api/notes", map[string]any{"spaceId": space.ID, "folderId": note.ID, "folderNote": true, "title": "X"}, nil), 400, "folderId names a note")
	h.mustStatus(h.do("POST", "/api/notes", map[string]any{"spaceId": space.ID, "folderId": core.NewID(), "folderNote": true, "title": "X"}, nil), 404, "unknown folder")

	var plain core.Note
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{SpaceID: space.ID, ParentID: folder.ID, Title: "index"}, &plain), 201, "note titled index")
	if plain.Path != "Wiki/index-note.md" {
		t.Fatalf("ordinary note titled index = %s", plain.Path)
	}
}

func TestFoldersTreeResolveSyncAndDisk(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()
	var folder core.Folder
	h.mustStatus(h.do("POST", "/api/folders", core.FolderInput{SpaceID: space.ID, Name: "Projects"}, &folder), 201, "folder")
	h.mustStatus(h.do("POST", "/api/folders", core.FolderInput{SpaceID: space.ID, Name: "projects"}, nil), 409, "case-equal folder")
	var note core.Note
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{SpaceID: space.ID, ParentID: folder.ID, Title: "Harbour survey"}, &note), 201, "note in folder")
	f := h.uploadTo(space.ID, folder.ID, "doc.txt", []byte("text"))
	if note.Path != "Projects/harbour-survey.md" || f.Path != "Projects/doc.txt" {
		t.Fatalf("paths %q %q", note.Path, f.Path)
	}

	var view struct {
		Folder   *core.Folder     `json:"folder"`
		Children []*core.TreeItem `json:"children"`
	}
	h.mustStatus(h.do("GET", "/api/folders/"+folder.ID, nil, &view), 200, "get folder")
	if view.Folder == nil || view.Folder.ID != folder.ID || len(view.Children) != 2 {
		t.Fatalf("folder view = %+v with %d children", view.Folder, len(view.Children))
	}
	var tree struct{ Items []*core.TreeItem }
	h.mustStatus(h.do("GET", "/api/spaces/"+space.ID+"/tree", nil, &tree), 200, "tree")
	var top *core.TreeItem
	for _, it := range tree.Items {
		if it.Folder != nil && it.Folder.ID == folder.ID {
			top = it
		}
	}
	if top == nil || len(top.Children) != 2 {
		t.Fatalf("tree lacks the folder with its children: %+v", tree.Items)
	}

	var resolved struct{ Items []*core.ResolvedTarget }
	h.mustStatus(h.do("POST", "/api/resolve", core.ResolveRequest{
		SpaceID: space.ID, Targets: []string{"Harbour survey", "Projects/doc.txt", "missing"},
	}, &resolved), 200, "resolve")
	if len(resolved.Items) != 3 || resolved.Items[0].ID != note.ID || resolved.Items[1].ID != f.ID || resolved.Items[2].ID != "" {
		t.Fatalf("resolve = %+v", resolved.Items)
	}

	page, _ := h.pull(0, 0)
	hasFolder, hasFile := false, false
	for _, x := range page.Folders {
		hasFolder = hasFolder || x.ID == folder.ID
	}
	for _, x := range page.Files {
		hasFile = hasFile || x.ID == f.ID
	}
	if !hasFolder || !hasFile || !contains(page.LiveIDs, folder.ID) || !contains(page.LiveIDs, f.ID) || !contains(page.LiveIDs, note.ID) {
		t.Fatalf("sync folders=%v files=%v live=%v", hasFolder, hasFile, page.LiveIDs)
	}

	dir := filepath.Join(h.cfg.SpacesDir(), space.Slug)
	if info, err := os.Stat(filepath.Join(dir, "Projects")); err != nil || !info.IsDir() {
		t.Fatalf("Projects is not a directory: %v", err)
	}
	for _, rel := range []string{"Projects/doc.txt", "Projects/harbour-survey.md"} {
		if info, err := os.Stat(filepath.Join(dir, rel)); err != nil || info.IsDir() {
			t.Fatalf("%s missing: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "attachments")); !os.IsNotExist(err) {
		t.Fatal("space has an attachments directory")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "Projects/harbour-survey.md")); strings.Contains(string(data), "parent:") {
		t.Fatalf("note frontmatter has a parent key: %s", data)
	}
}
