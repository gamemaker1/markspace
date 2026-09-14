package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
	"github.com/gamemaker1/markspace/sdk"
)

// bucket is a token bucket of host API requests.
type bucket struct {
	tokens float64
	last   time.Time
}

// Allow implements plugins.Host with a token bucket per plugin on the real
// clock.
func (h *Host) Allow(pluginID string) (bool, time.Duration) {
	now := time.Now()
	rate, burst := h.opts.RateLimit, float64(h.opts.Burst)
	h.mu.Lock()
	defer h.mu.Unlock()
	b := h.buckets[pluginID]
	if b == nil {
		b = &bucket{tokens: burst, last: now}
		h.buckets[pluginID] = b
	}
	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / rate * float64(time.Second))
}

// BeginIdempotent implements plugins.Host.
func (h *Host) BeginIdempotent(ctx context.Context, pluginID, key, method, path string) (*plugins.StoredResponse, error) {
	resp, err := h.store.BeginIdempotent(ctx, pluginID, key, method, path, h.now())
	if err != nil || resp == nil {
		return nil, err
	}
	return &plugins.StoredResponse{Status: resp.Status, Body: resp.Body}, nil
}

// CompleteIdempotent implements plugins.Host.
func (h *Host) CompleteIdempotent(ctx context.Context, pluginID, key string, status int, body []byte) error {
	return h.store.CompleteIdempotent(ctx, pluginID, key, status, body)
}

// ReleaseIdempotent implements plugins.Host.
func (h *Host) ReleaseIdempotent(ctx context.Context, pluginID, key string) error {
	return h.store.ReleaseIdempotent(ctx, pluginID, key)
}

func checkKey(key string) error {
	if !sdk.ValidStorageKey(key) {
		return core.Invalid("key", "invalid storage key")
	}
	return nil
}

// KVGet implements plugins.Host.
func (h *Host) KVGet(ctx context.Context, pluginID, key string) (*plugins.KVEntry, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	e, err := h.store.GetKV(ctx, pluginID, key)
	if err != nil {
		return nil, err
	}
	return &plugins.KVEntry{Key: e.Key, Value: json.RawMessage(e.Value), Size: e.Size, UpdatedAt: e.UpdatedAt}, nil
}

// KVPut implements plugins.Host. The value is stored as JSON.
func (h *Host) KVPut(ctx context.Context, pluginID, key string, value json.RawMessage) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if len(value) == 0 || !json.Valid(value) {
		return core.Invalid("value", "must be JSON")
	}
	return h.store.PutKV(ctx, pluginID, key, value, h.now())
}

// KVDelete implements plugins.Host.
func (h *Host) KVDelete(ctx context.Context, pluginID, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	return h.store.DeleteKV(ctx, pluginID, key)
}

// KVList implements plugins.Host.
func (h *Host) KVList(ctx context.Context, pluginID, prefix, cursor string, limit int) ([]plugins.KVEntry, string, error) {
	rows, next, err := h.store.ListKV(ctx, pluginID, prefix, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]plugins.KVEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, plugins.KVEntry{Key: r.Key, Size: r.Size, UpdatedAt: r.UpdatedAt})
	}
	return out, next, nil
}

// blobPath is the file of a blob. Keys may hold slashes, so the file name is
// the SHA-256 of the key.
func (h *Host) blobPath(pluginID, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(h.blobDir(pluginID), hex.EncodeToString(sum[:]))
}

// BlobPut implements plugins.Host. It streams the body to a temporary file,
// checks the size and quota, then renames it into place and stores the
// metadata. size is -1 when unknown.
func (h *Host) BlobPut(ctx context.Context, pluginID, key, contentType string, r io.Reader, size int64) (*plugins.BlobInfo, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if size > index.MaxBlobSize {
		return nil, fmt.Errorf("blob of %d bytes exceeds %d: %w", size, index.MaxBlobSize, core.ErrTooLarge)
	}
	if size >= 0 {
		if err := h.store.CheckBlobQuota(ctx, pluginID, key, size); err != nil {
			return nil, err
		}
	}
	dir := h.blobDir(pluginID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create blob directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("create blob file: %w", err)
	}
	defer os.Remove(tmp.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(r, index.MaxBlobSize+1))
	if err != nil {
		tmp.Close()
		return nil, fmt.Errorf("read blob: %w", err)
	}
	if n > index.MaxBlobSize {
		tmp.Close()
		return nil, fmt.Errorf("blob exceeds %d bytes: %w", index.MaxBlobSize, core.ErrTooLarge)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := h.store.CheckBlobQuota(ctx, pluginID, key, n); err != nil {
		return nil, err
	}
	path := h.blobPath(pluginID, key)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil, fmt.Errorf("store blob: %w", err)
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" || len(ct) > 200 {
		ct = "application/octet-stream"
	}
	_ = os.WriteFile(path+".type", []byte(ct), 0o600)
	meta := index.BlobMeta{PluginID: pluginID, Key: key, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil)), UpdatedAt: h.now()}
	if err := h.store.PutBlobMeta(ctx, meta); err != nil {
		os.Remove(path)
		os.Remove(path + ".type")
		return nil, err
	}
	return &plugins.BlobInfo{Key: key, Size: n, SHA256: meta.SHA256, ContentType: ct, UpdatedAt: meta.UpdatedAt}, nil
}

// BlobOpen implements plugins.Host.
func (h *Host) BlobOpen(ctx context.Context, pluginID, key string) (*plugins.BlobInfo, io.ReadCloser, error) {
	if err := checkKey(key); err != nil {
		return nil, nil, err
	}
	meta, err := h.store.GetBlobMeta(ctx, pluginID, key)
	if err != nil {
		return nil, nil, err
	}
	path := h.blobPath(pluginID, key)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("blob %s: %w", key, core.ErrNotFound)
	}
	if err != nil {
		return nil, nil, err
	}
	ct := "application/octet-stream"
	if b, err := os.ReadFile(path + ".type"); err == nil && len(b) > 0 {
		ct = string(b)
	}
	return &plugins.BlobInfo{Key: meta.Key, Size: meta.Size, SHA256: meta.SHA256, ContentType: ct, UpdatedAt: meta.UpdatedAt}, f, nil
}

// BlobDelete implements plugins.Host.
func (h *Host) BlobDelete(ctx context.Context, pluginID, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if err := h.store.DeleteBlobMeta(ctx, pluginID, key); err != nil {
		return err
	}
	path := h.blobPath(pluginID, key)
	os.Remove(path)
	os.Remove(path + ".type")
	return nil
}

// Secret implements plugins.Host. Only secrets the approved manifest declares
// can be read.
func (h *Host) Secret(ctx context.Context, pluginID, name string) (string, error) {
	_, man, _, ok := h.snapshot(pluginID)
	if !ok || !slices.ContainsFunc(man.Secrets, func(s sdk.ManifestSecret) bool { return s.Name == name }) {
		return "", fmt.Errorf("secret %s: %w", name, core.ErrNotFound)
	}
	return h.store.GetPluginSecret(ctx, pluginID, name)
}

// Settings implements plugins.Host.
func (h *Host) Settings(ctx context.Context, pluginID string) (json.RawMessage, error) {
	row, err := h.store.GetPlugin(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	if len(row.Settings) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return row.Settings, nil
}
