package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/control"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/system"
)

// healthTimeout bounds "markspace health", which a container health check
// runs.
const healthTimeout = 2 * time.Second

// callTimeout bounds the short management calls. Reindex and backup have no
// bound.
const callTimeout = 30 * time.Second

// remoteFlags locate a running server and its token.
type remoteFlags struct {
	config, data, url, tokenFile *string
}

// addRemoteFlags adds the connection flags. withToken adds -token-file.
func addRemoteFlags(fs *flag.FlagSet, withToken bool) *remoteFlags {
	f := &remoteFlags{
		config: fs.String("config", envOr("MARKSPACE_CONFIG", "markspace.yaml"), "path to the configuration file"),
		data:   fs.String("data", "", "data directory, overrides the configuration file"),
		url:    fs.String("url", "", "server URL, instead of the listen address of the configuration"),
	}
	if withToken {
		f.tokenFile = fs.String("token-file", "", "token file, default <dataDir>/cli.token, unused when MARKSPACE_TOKEN is set")
	}
	return f
}

// client builds the API client. The URL is -url, or the listen address of
// the configuration on 127.0.0.1. The token is MARKSPACE_TOKEN, then
// -token-file, then <dataDir>/cli.token. The configuration is loaded only
// when one of them needs it.
func (f *remoteFlags) client(withToken bool) (*control.Client, error) {
	var cfg *config.Config
	load := func() (*config.Config, error) {
		if cfg != nil {
			return cfg, nil
		}
		var err error
		cfg, err = loadConfig(*f.config, *f.data, "")
		return cfg, err
	}
	base := *f.url
	if base == "" {
		c, err := load()
		if err != nil {
			return nil, err
		}
		base = control.LocalURL(c.Listen)
	}
	token := ""
	if withToken {
		token = os.Getenv("MARKSPACE_TOKEN")
		if token == "" {
			path := *f.tokenFile
			if path == "" {
				c, err := load()
				if err != nil {
					return nil, err
				}
				path = filepath.Join(c.DataDir, control.TokenFile)
			}
			var err error
			token, err = control.ReadToken(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return nil, fmt.Errorf("no token: %s does not exist, start the server once or set MARKSPACE_TOKEN", path)
			case err != nil:
				return nil, fmt.Errorf("read token: %w", err)
			case token == "":
				return nil, fmt.Errorf("no token: %s is empty", path)
			}
		}
	}
	return control.New(base, token), nil
}

// signalContext returns a context that ends on SIGINT or SIGTERM, and after
// timeout when it is positive.
func signalContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if timeout <= 0 {
		return ctx, stop
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	return tctx, func() { cancel(); stop() }
}

// health serves "markspace health".
func (c *cli) health(args []string) error {
	fs := c.flags("health")
	rf := addRemoteFlags(fs, false)
	if err := c.parse(fs, "health", args); err != nil {
		return err
	}
	client, err := rf.client(false)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(healthTimeout)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("the server at %s did not answer within %s", client.BaseURL, healthTimeout)
		}
		return err
	}
	fmt.Fprintf(c.stdout, "the server at %s is healthy\n", client.BaseURL)
	return nil
}

// status serves "markspace status".
func (c *cli) status(args []string) error {
	fs := c.flags("status")
	rf := addRemoteFlags(fs, true)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := c.parse(fs, "status", args); err != nil {
		return err
	}
	client, err := rf.client(true)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(callTimeout)
	defer cancel()
	st, err := client.Status(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(c.stdout, st)
	}
	return printStatus(c.stdout, st)
}

// printStatus writes the status as aligned plain text.
func printStatus(w io.Writer, st *system.Status) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "version\t%s\n", st.Version)
	fmt.Fprintf(tw, "uptime\t%s\n", time.Duration(st.UptimeSeconds)*time.Second)
	fmt.Fprintf(tw, "listen\t%s\n", st.Listen)
	fmt.Fprintf(tw, "data dir\t%s\n", st.DataDir)
	fmt.Fprintf(tw, "users\t%d\n", st.Users)
	fmt.Fprintf(tw, "spaces\t%d\n", st.Spaces)
	fmt.Fprintf(tw, "notes\t%d\n", st.Notes)
	fmt.Fprintf(tw, "files\t%d\n", st.Files)
	for _, s := range module.JobStatuses {
		fmt.Fprintf(tw, "jobs %s\t%d\n", s, st.Jobs[string(s)])
	}
	if len(st.Plugins) == 0 {
		fmt.Fprintf(tw, "plugins\tnone\n")
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(st.Plugins) == 0 {
		return nil
	}
	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PLUGIN\tVERSION\tHEALTH\tFAILURES\tLAST CHECK\tERROR")
	for _, p := range st.Plugins {
		last := "never"
		if !p.LastHealthAt.IsZero() {
			last = p.LastHealthAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", p.ID, p.Version, p.Status, p.HealthFailures, last, p.LastError)
	}
	return tw.Flush()
}

// reindex serves "markspace reindex".
func (c *cli) reindex(args []string) error {
	fs := c.flags("reindex")
	rf := addRemoteFlags(fs, true)
	if err := c.parse(fs, "reindex", args); err != nil {
		return err
	}
	client, err := rf.client(true)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(0)
	defer cancel()
	n, err := client.Reindex(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "reindexed %d files\n", n)
	return nil
}

// backup serves "markspace backup".
func (c *cli) backup(args []string) error {
	fs := c.flags("backup")
	rf := addRemoteFlags(fs, true)
	out := fs.String("out", "", "write the archive to this file instead of standard output")
	if err := c.parse(fs, "backup", args); err != nil {
		return err
	}
	if *out == "" {
		if f, ok := c.stdout.(*os.File); ok {
			if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
				return usageError{cmd: "backup", msg: "standard output is a terminal, use -out or redirect it to a file"}
			}
		}
	}
	client, err := rf.client(true)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(0)
	defer cancel()
	if *out == "" {
		_, err := client.Backup(ctx, c.stdout)
		return err
	}
	// The archive goes to a temporary file that replaces -out only when it
	// is complete.
	tmp, err := os.CreateTemp(filepath.Dir(*out), ".markspace-backup-*")
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	n, err := client.Backup(ctx, tmp)
	if cerr := tmp.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("close backup file: %w", cerr)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), *out)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	fmt.Fprintf(c.stdout, "wrote %s, %d bytes\n", *out, n)
	return nil
}

// writeJSON writes v as indented JSON.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
