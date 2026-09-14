// Package control connects the markspace command line to a running server.
// The server keeps a system API token named "Markspace CLI" and writes its
// plaintext to <dataDir>/cli.token. The commands read that file and call the
// JSON API with the typed client in this package.
package control

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// TokenName is the name of the system API token of the command line.
const TokenName = "Markspace CLI"

// TokenFile is the name of the token file inside the data directory.
const TokenFile = "cli.token"

// Issuer keeps a system API token. app.App implements it.
type Issuer interface {
	EnsureSystemToken(ctx context.Context, name, current string) (string, *core.APIToken, error)
}

// EnsureToken makes sure path holds the plaintext of the stored Markspace CLI
// token. A missing or empty file, or a file whose token was revoked, gets a
// new token. The file is created with mode 0600 and replaced by a rename, so
// a reader never sees a partial token. An existing file that others can read
// is narrowed to 0600.
func EnsureToken(ctx context.Context, issuer Issuer, path string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	current, err := ReadToken(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	plain, token, err := issuer.EnsureSystemToken(ctx, TokenName, current)
	if err != nil {
		return fmt.Errorf("cli token: %w", err)
	}
	if plain == "" {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("cli token: %w", err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(path, 0o600); err != nil {
				return fmt.Errorf("cli token: %w", err)
			}
		}
		return nil
	}
	if err := writeToken(path, plain); err != nil {
		return err
	}
	logger.Info("cli token issued", "tokenId", token.ID, "prefix", token.Prefix, "file", path)
	return nil
}

// ReadToken returns the token in a token file with surrounding white space
// removed.
func ReadToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// writeToken writes the token to a new file with O_EXCL and mode 0600 next to
// path, then renames it over path.
func writeToken(path, plain string) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-"+core.NewToken(8))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("cli token: create %s: %w", tmp, err)
	}
	_, werr := f.WriteString(plain + "\n")
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp)
		return fmt.Errorf("cli token: write %s: %w", tmp, werr)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cli token: rename to %s: %w", path, err)
	}
	return nil
}
