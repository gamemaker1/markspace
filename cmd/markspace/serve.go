package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/app"
	"github.com/gamemaker1/markspace/internal/control"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
	"github.com/gamemaker1/markspace/internal/modules/system"
	"github.com/gamemaker1/markspace/internal/store"
	"github.com/gamemaker1/markspace/internal/web"
	"github.com/gamemaker1/markspace/internal/wiring"
)

// serveFlags are the flags of serve.
type serveFlags struct {
	config, listen, data *string
}

func addServeFlags(fs *flag.FlagSet) serveFlags {
	return serveFlags{
		config: fs.String("config", envOr("MARKSPACE_CONFIG", "markspace.yaml"), "path to the configuration file"),
		listen: fs.String("listen", "", "bind address, overrides the configuration file"),
		data:   fs.String("data", "", "data directory, overrides the configuration file"),
	}
}

// serve serves "markspace serve".
func (c *cli) serve(args []string) error {
	fs := c.flags("serve")
	f := addServeFlags(fs)
	if err := c.parse(fs, "serve", args); err != nil {
		return err
	}
	return serve(*f.config, *f.listen, *f.data)
}

// legacy serves "markspace" with no command. It accepts the serve flags and
// the older -version and -write-config flags.
func (c *cli) legacy(args []string) error {
	fs := c.flags("")
	f := addServeFlags(fs)
	writeConfig := fs.Bool("write-config", false, "write an example configuration file to -config and exit")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := c.parse(fs, "", args); err != nil {
		return err
	}
	switch {
	case *showVersion:
		fmt.Fprintln(c.stdout, "markspace "+version())
		return nil
	case *writeConfig:
		return writeExampleConfig(*f.config)
	}
	return serve(*f.config, *f.listen, *f.data)
}

// serve runs the server until SIGINT or SIGTERM.
func serve(configPath, listen, dataDir string) error {
	started := time.Now()
	cfg, err := loadConfig(configPath, dataDir, listen)
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	files, err := store.New(cfg.DataDir, logger.With("component", "store"))
	if err != nil {
		return fmt.Errorf("open file store: %w", err)
	}

	db, err := index.Open(cfg.DBPath, logger.With("component", "index"))
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer db.Close()

	renderer := markdown.New(logger.With("component", "markdown"))
	registry := ai.NewRegistry(logger.With("component", "ai"), cfg.AI.ContextChunks)

	// The module engine: registry, event bus, job engine and scheduler.
	engine := wiring.NewEngine(cfg, db, logger)

	backend, err := app.New(app.Deps{
		Files:    files,
		DB:       db,
		Renderer: renderer,
		AI:       registry,
		// The public price catalogs download once a day when
		// ai.fetchPublicPrices is on.
		PriceFetcher: ai.NewPublicFetcher(),
		Hasher:       hasher{},
		Config:       cfg,
		Logger:       logger.With("component", "app"),
		Suggestions:  db.Suggestions(),
		Bus:          engine.Bus,
		Jobs:         engine.Jobs,
		Scheduler:    engine.Scheduler,
		EventSink:    func(ev core.Event) { engine.Bus.Publish(wiring.ModuleEvent(ev)) },
	})
	if err != nil {
		return fmt.Errorf("build backend: %w", err)
	}
	engine.Bridge(backend)
	auth := &api.Authenticator{
		Backend:    backend,
		Logger:     logger.With("component", "auth"),
		Secure:     cfg.SecureCookies,
		TrustProxy: cfg.TrustProxy,
	}
	pages := &wiring.PageRenderer{}
	if err := engine.AddBuiltins(wiring.NewEnv(cfg, backend, auth, logger), pages); err != nil {
		return fmt.Errorf("register modules: %w", err)
	}
	host, err := engine.AddPlugins(cfg, db, backend, pages, logger)
	if err != nil {
		return fmt.Errorf("register plugin host: %w", err)
	}
	if err := engine.AddUI(backend, host, host, logger); err != nil {
		return fmt.Errorf("register ui module: %w", err)
	}
	// The status and backup routes of the management commands.
	if err := engine.Registry.Add(system.New(system.Options{
		Backend: backend,
		Jobs:    index.NewJobStore(db),
		Plugins: host,
		DB:      db.SQL(),
		Listen:  cfg.Listen,
		DataDir: cfg.DataDir,
		DBPath:  cfg.DBPath,
		Skip:    []string{control.TokenFile},
		Version: version(),
		Started: started,
		Logger:  logger.With("module", "system"),
	})); err != nil {
		return fmt.Errorf("register system module: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := backend.Start(ctx); err != nil {
		return fmt.Errorf("start backend: %w", err)
	}
	defer backend.Close()

	// The management commands authenticate with this token.
	tokenPath := filepath.Join(cfg.DataDir, control.TokenFile)
	if err := control.EnsureToken(ctx, backend, tokenPath, logger.With("component", "control")); err != nil {
		return err
	}

	apiServer, err := api.NewServer(api.Options{
		Logger: logger.With("component", "api"),
		Auth:   auth,
		Mount:  engine.Mount,
	})
	if err != nil {
		return fmt.Errorf("build api: %w", err)
	}

	webServer, err := web.NewServer(web.Options{
		Backend:      backend,
		Logger:       logger.With("component", "web"),
		Auth:         auth,
		SessionTTL:   cfg.SessionTTL,
		BaseURL:      cfg.BaseURL,
		InstanceName: cfg.InstanceName,
		MaxUpload:    cfg.MaxUploadBytes(),
		DevMode:      cfg.DevMode,
		MarkdownCSS:  renderer.CSS(),
		Mount:        engine.Mount,
	})
	if err != nil {
		return fmt.Errorf("build web: %w", err)
	}
	pages.Set(webServer)

	mux := http.NewServeMux()
	mux.Handle("/api/", apiServer.Handler())
	mux.Handle("/", webServer.Handler())

	handler := api.Chain(mux,
		api.Recover(logger),
		api.Logging(logger),
	)
	if cfg.SecureCookies {
		// Sec-Fetch-Site based cross-origin protection, added in Go 1.25.
		cop := http.NewCrossOriginProtection()
		if cfg.BaseURL != "" {
			if err := cop.AddTrustedOrigin(cfg.BaseURL); err != nil {
				return fmt.Errorf("baseUrl: %w", err)
			}
		}
		handler = cop.Handler(handler)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		// A backup download ends when the signal arrives.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	// Shutdown waits for active requests. Event and AI streams end only when
	// their client leaves, so they are told to close as shutdown begins.
	srv.RegisterOnShutdown(api.BeginShutdown)

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "baseUrl", cfg.BaseURL, "dataDir", cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
