package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/gamemaker1/markspace/internal/config"
)

// loadConfig loads the configuration as serve does. The data directory and
// listen flags override the file and the environment. Setting the environment
// before Load lets finalize derive DBPath from the overridden data dir.
func loadConfig(path, dataDir, listen string) (*config.Config, error) {
	if dataDir != "" {
		os.Setenv("MARKSPACE_DATA_DIR", dataDir)
	}
	if listen != "" {
		os.Setenv("MARKSPACE_LISTEN", listen)
	}
	return config.Load(path)
}

// configCmd serves "markspace config".
func (c *cli) configCmd(args []string) error {
	return c.group("config", args, map[string]func([]string) error{
		"example": c.configExample,
		"check":   c.configCheck,
	})
}

// configExample serves "markspace config example".
func (c *cli) configExample(args []string) error {
	fs := c.flags("config example")
	if err := c.parse(fs, "config example", args); err != nil {
		return err
	}
	_, err := fmt.Fprint(c.stdout, exampleConfig)
	return err
}

// configCheck serves "markspace config check".
func (c *cli) configCheck(args []string) error {
	flags := c.flags("config check")
	f := addServeFlags(flags)
	if err := c.parse(flags, "config check", args); err != nil {
		return err
	}
	cfg, err := loadConfig(*f.config, *f.data, *f.listen)
	if err != nil {
		return err
	}
	out, err := yaml.Marshal(redacted(cfg))
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	abs, _ := filepath.Abs(*f.config)
	if _, err := os.Stat(*f.config); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(c.stdout, "# %s does not exist. The defaults and the environment apply.\n", abs)
	} else {
		fmt.Fprintf(c.stdout, "# Loaded from %s and the environment.\n", abs)
	}
	_, err = c.stdout.Write(out)
	return err
}

// redactedValue replaces a secret in the output of config check.
const redactedValue = "[redacted]"

// redacted returns a copy of cfg with the API keys and header values of the
// AI providers replaced.
func redacted(cfg *config.Config) *config.Config {
	cp := *cfg
	cp.AI.Providers = slices.Clone(cfg.AI.Providers)
	for i := range cp.AI.Providers {
		p := &cp.AI.Providers[i]
		if p.APIKey != "" {
			p.APIKey = redactedValue
		}
		if len(p.Headers) > 0 {
			h := make(map[string]string, len(p.Headers))
			for k := range p.Headers {
				h[k] = redactedValue
			}
			p.Headers = h
		}
	}
	return &cp
}
