package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// InstallFile is the name of the stored install payload inside the data
// directory.
const InstallFile = "install.json"

// Environment variables that override the stored install payload.
const (
	EnvMarkspaceURL  = "MARKSPACE_URL"
	EnvToken         = "MARKSPACE_TOKEN"
	EnvSigningSecret = "MARKSPACE_SIGNING_SECRET"
	EnvPluginData    = "PLUGIN_DATA"
	EnvInstallWindow = "PLUGIN_INSTALL_WINDOW"
)

// Grant is one space the plugin may use.
type Grant struct {
	SpaceID string `json:"spaceId"`
	Role    string `json:"role"`
}

// Install is the payload Markspace sends to POST /install.
type Install struct {
	PluginID      string   `json:"pluginId"`
	MarkspaceURL  string   `json:"markspaceUrl"`
	Token         string   `json:"token"`
	SigningSecret string   `json:"signingSecret"`
	Grants        []Grant  `json:"grants,omitempty"`
	Permissions   []string `json:"permissions,omitempty"`
}

// DefaultDataDir returns $PLUGIN_DATA, or /data when it is empty.
func DefaultDataDir(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if d := getenv(EnvPluginData); d != "" {
		return d
	}
	return "/data"
}

// LoadInstall reads the stored install payload. It returns nil and no error
// when no install exists.
func LoadInstall(dataDir string) (*Install, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, InstallFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var in Install
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("%s: %w", InstallFile, err)
	}
	return &in, nil
}

// SaveInstall writes raw to install.json atomically with mode 0600. It
// writes a temporary file in the same directory, syncs it, renames it over
// the old file and syncs the directory.
func SaveInstall(dataDir string, raw []byte) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir, InstallFile+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dataDir, InstallFile)); err != nil {
		return err
	}
	if d, err := os.Open(dataDir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// Credentials are the effective connection settings of a plugin.
type Credentials struct {
	MarkspaceURL string
	Token        string
	Secret       []byte
	Install      *Install
}

// ResolveCredentials combines install.json with the environment. Each of
// MARKSPACE_URL, MARKSPACE_TOKEN and MARKSPACE_SIGNING_SECRET, when set,
// overrides the stored value on its own.
func ResolveCredentials(dataDir string, getenv func(string) string) (Credentials, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	var c Credentials
	in, err := LoadInstall(dataDir)
	if err != nil {
		return c, err
	}
	secret := ""
	if in != nil {
		c.Install = in
		c.MarkspaceURL = in.MarkspaceURL
		c.Token = in.Token
		secret = in.SigningSecret
	}
	if v := getenv(EnvMarkspaceURL); v != "" {
		c.MarkspaceURL = v
	}
	if v := getenv(EnvToken); v != "" {
		c.Token = v
	}
	if v := getenv(EnvSigningSecret); v != "" {
		secret = v
	}
	if secret != "" {
		b, err := DecodeSecret(secret)
		if err != nil {
			return c, err
		}
		c.Secret = b
	}
	return c, nil
}
