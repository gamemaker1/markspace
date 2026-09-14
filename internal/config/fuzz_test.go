package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad checks that Load of an arbitrary YAML file never panics, and that a
// file it accepts yields providers that each have an id, a known kind and a
// base URL.
func FuzzLoad(f *testing.F) {
	for _, seed := range []string{
		"",
		"listen: ':9000'\n",
		"ai:\n  providers:\n    - name: Local\n      baseUrl: http://localhost:11434\n      kind: ollama\n",
		"ai:\n  providers:\n    - id: a\n      baseUrl: /\n",
		"ai:\n  providers:\n    - id: a\n      baseUrl: '${MARKSPACE_FUZZ_UNSET}'\n",
		"ai:\n  providers:\n    - baseUrl: http://x\n    - baseUrl: http://y\n",
		"ai:\n  providers:\n    - id: a\n      kind: nope\n      baseUrl: http://x\n",
		"ai:\n  providers: [null, 3]\n",
		"ai:\n  providers:\n    - {id: '', name: '', baseUrl: 'http://x', enabled: false}\n",
		"sessionTTL: 5m\nmaxUploadMB: -1\n",
		"unknownKey: 1\n",
		"a: &a [*a]\n",
		"---\n---\n",
		"ai: [1, 2]\n",
		"ai:\n  providers:\n    - id: a\n      baseUrl: http://x\n      headers: {X: y}\n",
		"ai:\n  providers:\n    - preset: openrouter\n",
		"ai:\n  routes:\n    edit: [{providerId: a, model: m}]\n",
		"ai:\n  routes:\n    plugin.x.y: []\n",
	} {
		f.Add([]byte(seed))
	}
	for _, key := range []string{
		"MARKSPACE_LISTEN", "MARKSPACE_BASE_URL", "MARKSPACE_DATA_DIR", "MARKSPACE_DB_PATH",
		"MARKSPACE_AI_FETCH_PUBLIC_PRICES", "MARKSPACE_AI_ENABLED",
	} {
		os.Unsetenv(key)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "markspace.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			return
		}
		if cfg == nil {
			t.Fatal("Load returned a nil config and no error")
		}
		for i, p := range cfg.AI.Providers {
			if p.ID == "" {
				t.Fatalf("provider %d has an empty id\ninput: %q", i, data)
			}
			if !validKind(p.Kind) {
				t.Fatalf("provider %s has kind %q\ninput: %q", p.ID, p.Kind, data)
			}
			if p.BaseURL == "" {
				t.Fatalf("provider %s has no base URL\ninput: %q", p.ID, data)
			}
		}
	})
}
