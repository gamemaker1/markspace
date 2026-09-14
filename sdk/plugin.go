package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/sdk/ui"
)

// Body limits for requests from Markspace.
const (
	MaxInstallBody = 1 << 20
	MaxRequestBody = 16 << 20
)

// Plugin serves the plugin side of the protocol. Set the exported fields
// before the first call to Handler.
type Plugin struct {
	// DataDir holds install.json. The default is $PLUGIN_DATA or /data.
	DataDir string
	// InstallWindow is how long after startup an unsigned first install is
	// accepted. The default is 10 minutes.
	InstallWindow time.Duration
	// JobWait is how long a job call waits for the handler before it answers
	// 202. The default is 25 s.
	JobWait time.Duration
	// HeartbeatEvery is the interval of lease heartbeats for jobs that run
	// after a 202. The default is 20 s.
	HeartbeatEvery time.Duration
	// Now replaces the clock for signature checks and the install window.
	Now func() time.Time
	// Getenv replaces os.Getenv.
	Getenv     func(string) string
	Logger     *slog.Logger
	HTTPClient *http.Client

	manifest  Manifest
	events    []eventReg
	jobs      map[string]JobHandler
	commands  map[string]CommandHandler
	panels    map[string]PanelHandler
	actions   map[string]ActionHandler
	importers map[string]ImportHandler

	once      sync.Once
	handler   http.Handler
	start     time.Time
	envToken  bool
	guard     *ReplayGuard
	installMu sync.Mutex
	credMu    sync.RWMutex
	creds     Credentials
	runMu     sync.Mutex
	running   map[string]bool
	wg        sync.WaitGroup
}

type eventReg struct {
	pattern string
	h       EventHandler
}

// New returns a plugin for a validated manifest.
func New(m Manifest) (*Plugin, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &Plugin{
		manifest:  m,
		jobs:      map[string]JobHandler{},
		commands:  map[string]CommandHandler{},
		panels:    map[string]PanelHandler{},
		actions:   map[string]ActionHandler{},
		importers: map[string]ImportHandler{},
		running:   map[string]bool{},
	}, nil
}

// Manifest returns the plugin manifest.
func (p *Plugin) Manifest() Manifest { return p.manifest }

func (p *Plugin) register(what, id string, declared bool, taken bool) {
	if !ValidLocalID(id) {
		panic(fmt.Sprintf("sdk: invalid %s id %q", what, id))
	}
	if !declared {
		panic(fmt.Sprintf("sdk: %s %q is not declared in the manifest", what, id))
	}
	if taken {
		panic(fmt.Sprintf("sdk: duplicate %s handler %q", what, id))
	}
}

// On registers an event handler for a pattern such as note.updated, file.*
// or *. It panics on an invalid pattern.
func (p *Plugin) On(pattern string, h EventHandler) {
	if !ValidEventPattern(pattern) {
		panic(fmt.Sprintf("sdk: invalid event pattern %q", pattern))
	}
	p.events = append(p.events, eventReg{pattern: pattern, h: h})
}

// Job registers the handler of a job kind declared in the manifest.
func (p *Plugin) Job(kind string, h JobHandler) {
	declared := false
	for _, j := range p.manifest.Jobs {
		declared = declared || j.Kind == kind
	}
	_, taken := p.jobs[kind]
	p.register("job", kind, declared, taken)
	p.jobs[kind] = h
}

// Command registers the handler of a command declared in the manifest.
func (p *Plugin) Command(id string, h CommandHandler) {
	declared := false
	for _, c := range p.manifest.Commands {
		declared = declared || c.ID == id
	}
	_, taken := p.commands[id]
	p.register("command", id, declared, taken)
	p.commands[id] = h
}

// Panel registers the handler of a panel declared in the manifest.
func (p *Plugin) Panel(id string, h PanelHandler) {
	declared := false
	for _, c := range p.manifest.Panels {
		declared = declared || c.ID == id
	}
	_, taken := p.panels[id]
	p.register("panel", id, declared, taken)
	p.panels[id] = h
}

// Action registers an action handler. Actions are not declared in the
// manifest.
func (p *Plugin) Action(id string, h ActionHandler) {
	_, taken := p.actions[id]
	p.register("action", id, true, taken)
	p.actions[id] = h
}

// Importer registers the handler of an importer declared in the manifest.
func (p *Plugin) Importer(id string, h ImportHandler) {
	declared := false
	for _, c := range p.manifest.Importers {
		declared = declared || c.ID == id
	}
	_, taken := p.importers[id]
	p.register("importer", id, declared, taken)
	p.importers[id] = h
}

func (p *Plugin) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Plugin) init() {
	if p.Getenv == nil {
		p.Getenv = os.Getenv
	}
	if p.DataDir == "" {
		p.DataDir = DefaultDataDir(p.Getenv)
	}
	if p.InstallWindow <= 0 {
		p.InstallWindow = 10 * time.Minute
	}
	if p.JobWait <= 0 {
		p.JobWait = 25 * time.Second
	}
	if p.HeartbeatEvery <= 0 {
		p.HeartbeatEvery = 20 * time.Second
	}
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
	p.start = p.now()
	p.envToken = p.Getenv(EnvToken) != ""
	p.guard = NewReplayGuard(DefaultReplayCapacity, DefaultWindow)
	if err := p.reload(); err != nil {
		p.Logger.Error("plugin credentials", "err", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, p.manifest)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": p.manifest.Version})
	})
	mux.HandleFunc("POST /install", p.serveInstall)
	mux.HandleFunc("POST /events", p.signed(p.serveEvents))
	mux.HandleFunc("POST /jobs/{kind}", p.signed(p.serveJob))
	mux.HandleFunc("POST /commands/{id}", p.signed(p.serveCommand))
	mux.HandleFunc("POST /panels/{id}", p.signed(p.servePanel))
	mux.HandleFunc("POST /actions/{id}", p.signed(p.serveAction))
	mux.HandleFunc("POST /import/{importer}", p.signed(p.serveImport))
	p.handler = mux
}

// Handler returns the HTTP handler for the plugin routes. The first call
// reads the environment and install.json and starts the install window.
func (p *Plugin) Handler() http.Handler {
	p.once.Do(p.init)
	return p.handler
}

// ListenAndServe serves the plugin on addr, such as :8090.
func (p *Plugin) ListenAndServe(addr string) error {
	srv := &http.Server{Addr: addr, Handler: p.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

func (p *Plugin) reload() error {
	c, err := ResolveCredentials(p.DataDir, p.Getenv)
	p.credMu.Lock()
	p.creds = c
	p.credMu.Unlock()
	return err
}

// Credentials returns the effective credentials.
func (p *Plugin) Credentials() Credentials {
	p.credMu.RLock()
	defer p.credMu.RUnlock()
	return p.creds
}

// Host returns a host client with the plugin token.
func (p *Plugin) Host() *Host {
	return p.hostFor(p.Credentials(), "")
}

func (p *Plugin) hostFor(c Credentials, token string) *Host {
	if token == "" {
		token = c.Token
	}
	h := NewHost(c.MarkspaceURL, token, p.manifest.ID)
	h.Client = p.HTTPClient
	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := ui.Encode(v)
	if err != nil {
		http.Error(w, `{"error":"cannot encode response"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func writeError(w http.ResponseWriter, status int, msg, code, field string) {
	body := map[string]string{"error": msg}
	if code != "" {
		body["code"] = code
	}
	if field != "" {
		body["field"] = field
	}
	writeJSON(w, status, body)
}

func writeValidation(w http.ResponseWriter, status int, err error) {
	var ve *ui.Error
	if errors.As(err, &ve) {
		writeError(w, status, ve.Message, ve.Code, ve.Path)
		return
	}
	writeError(w, status, err.Error(), "", "")
}

func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large", "", "")
		} else {
			writeError(w, http.StatusBadRequest, "cannot read body", "", "")
		}
		return nil, false
	}
	return b, true
}

func (p *Plugin) serveInstall(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r, MaxInstallBody)
	if !ok {
		return
	}
	p.installMu.Lock()
	defer p.installMu.Unlock()
	c := p.Credentials()
	now := p.now()
	if c.Secret != nil {
		if err := VerifySignature(c.Secret, r, body, now, p.guard); err != nil {
			writeSigError(w, err)
			return
		}
	} else {
		if p.envToken {
			writeError(w, http.StatusForbidden, "install is configured from the environment", "install_closed", "")
			return
		}
		if now.Sub(p.start) > p.InstallWindow {
			writeError(w, http.StatusForbidden, "install window closed", "install_closed", "")
			return
		}
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON", "", "")
		return
	}
	if err := ValidateInstall(v, p.manifest.ID); err != nil {
		writeValidation(w, http.StatusBadRequest, err)
		return
	}
	if err := SaveInstall(p.DataDir, body); err != nil {
		p.Logger.Error("save install", "err", err)
		writeError(w, http.StatusInternalServerError, "cannot store install", "", "")
		return
	}
	if err := p.reload(); err != nil {
		p.Logger.Error("reload credentials", "err", err)
		writeError(w, http.StatusInternalServerError, "cannot load credentials", "", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeSigError(w http.ResponseWriter, err error) {
	code := ""
	var se *SignatureError
	if errors.As(err, &se) {
		code = se.Code
	}
	writeError(w, http.StatusUnauthorized, "invalid signature", code, "")
}

type signedFunc func(w http.ResponseWriter, r *http.Request, body []byte, c Credentials)

func (p *Plugin) signed(next signedFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := p.Credentials()
		if c.Secret == nil {
			writeError(w, http.StatusServiceUnavailable, "not installed", "not_installed", "")
			return
		}
		if pid := r.Header.Get(HeaderPlugin); pid != "" && pid != p.manifest.ID {
			writeError(w, http.StatusUnauthorized, "request is for another plugin", CodeWrongPlugin, "")
			return
		}
		body, ok := readBody(w, r, MaxRequestBody)
		if !ok {
			return
		}
		if err := VerifySignature(c.Secret, r, body, p.now(), p.guard); err != nil {
			writeSigError(w, err)
			return
		}
		next(w, r, body, c)
	}
}

// protect runs f and turns a panic into an error.
func protect(f func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v\n%s", v, debug.Stack())
		}
	}()
	return f()
}

func decodeJSON(w http.ResponseWriter, body []byte, v any) bool {
	if err := json.Unmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "", "")
		return false
	}
	return true
}

func (p *Plugin) serveEvents(w http.ResponseWriter, r *http.Request, body []byte, c Credentials) {
	var in struct {
		Events []Event `json:"events"`
	}
	if !decodeJSON(w, body, &in) {
		return
	}
	host := p.hostFor(c, "")
	delivery := r.Header.Get(HeaderDelivery)
	for _, reg := range p.events {
		var matched []Event
		for _, e := range in.Events {
			if MatchEvent(reg.pattern, e.Type) {
				matched = append(matched, e)
			}
		}
		if len(matched) == 0 {
			continue
		}
		batch := &EventBatch{Events: matched, DeliveryID: delivery, Host: host}
		if err := protect(func() error { return reg.h(r.Context(), batch) }); err != nil {
			p.Logger.Error("event handler", "pattern", reg.pattern, "err", err)
			writeError(w, http.StatusInternalServerError, "event handler failed", "", "")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type jobOutcome struct {
	result any
	err    error
}

func (p *Plugin) serveJob(w http.ResponseWriter, r *http.Request, body []byte, c Credentials) {
	kind := r.PathValue("kind")
	h, ok := p.jobs[kind]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown job kind", "", "")
		return
	}
	var in struct {
		Job         JobInfo `json:"job"`
		ActingToken string  `json:"actingToken"`
		User        *User   `json:"user"`
	}
	if !decodeJSON(w, body, &in) {
		return
	}
	if !ValidObjectID(in.Job.ID) {
		writeError(w, http.StatusBadRequest, "invalid job id", ui.CodeInvalidID, "$.job.id")
		return
	}
	id := in.Job.ID
	p.runMu.Lock()
	if p.running[id] {
		p.runMu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
		return
	}
	p.running[id] = true
	p.runMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	jc := &JobContext{Job: in.Job, DeliveryID: r.Header.Get(HeaderDelivery), User: in.User, Host: p.hostFor(c, in.ActingToken), cancel: cancel}
	done := make(chan jobOutcome, 1)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		var o jobOutcome
		o.err = protect(func() error {
			res, err := h(ctx, jc)
			o.result = res
			return err
		})
		done <- o
	}()
	timer := time.NewTimer(p.JobWait)
	defer timer.Stop()
	select {
	case o := <-done:
		cancel()
		p.finishRunning(id)
		switch {
		case o.err == nil:
			res := o.result
			if res == nil {
				res = map[string]any{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"result": res})
		case errors.Is(o.err, ErrJobCanceled):
			writeError(w, http.StatusConflict, "job canceled", "canceled", "")
		default:
			p.Logger.Error("job failed", "kind", kind, "job", id, "err", o.err)
			writeError(w, http.StatusInternalServerError, o.err.Error(), "", "")
		}
	case <-timer.C:
		p.background(jc, done, cancel)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
	case <-r.Context().Done():
		p.background(jc, done, cancel)
	}
}

func (p *Plugin) finishRunning(id string) {
	p.runMu.Lock()
	delete(p.running, id)
	p.runMu.Unlock()
}

// background waits for a job that outlived JobWait. It sends heartbeats and
// reports the outcome through PATCH /jobs/{id}.
func (p *Plugin) background(jc *JobContext, done <-chan jobOutcome, cancel context.CancelFunc) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer p.finishRunning(jc.Job.ID)
		defer cancel()
		tick := time.NewTicker(p.HeartbeatEvery)
		defer tick.Stop()
		for {
			select {
			case o := <-done:
				p.reportOutcome(jc, o)
				return
			case <-tick.C:
				d, t, at := jc.lastReport()
				if time.Since(at) < p.HeartbeatEvery {
					continue
				}
				ctx, stop := context.WithTimeout(context.Background(), p.HeartbeatEvery)
				if err := jc.patch(ctx, map[string]any{"progress": d, "total": t}); err != nil {
					p.Logger.Warn("job heartbeat", "job", jc.Job.ID, "err", err)
				}
				stop()
			}
		}
	}()
}

func (p *Plugin) reportOutcome(jc *JobContext, o jobOutcome) {
	if errors.Is(o.err, ErrJobCanceled) || (o.err != nil && jc.Canceled()) {
		return
	}
	body := map[string]any{"status": "done"}
	if o.err != nil {
		body = map[string]any{"status": "failed", "message": o.err.Error()}
	} else if o.result != nil {
		body["result"] = o.result
	} else {
		body["result"] = map[string]any{}
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
	defer stop()
	if err := jc.Host.Do(ctx, "PATCH", "/jobs/"+jc.Job.ID, body, nil); err != nil {
		p.Logger.Error("report job outcome", "job", jc.Job.ID, "err", err)
	}
}

type interaction struct {
	Context     map[string]any `json:"context"`
	Values      map[string]any `json:"values"`
	Payload     map[string]any `json:"payload"`
	ActingToken string         `json:"actingToken"`
	User        User           `json:"user"`
	FileID      string         `json:"fileId"`
	SpaceID     string         `json:"spaceId"`
}

func (p *Plugin) decodeInteraction(w http.ResponseWriter, body []byte) (*interaction, bool) {
	var in interaction
	if !decodeJSON(w, body, &in) {
		return nil, false
	}
	if in.ActingToken == "" {
		writeError(w, http.StatusBadRequest, "missing actingToken", ui.CodeMissingField, "$.actingToken")
		return nil, false
	}
	if in.Context == nil {
		in.Context = map[string]any{}
	}
	return &in, true
}

func (p *Plugin) commandContext(r *http.Request, c Credentials, in *interaction, id string) CommandContext {
	return CommandContext{
		ID:         id,
		Context:    in.Context,
		User:       in.User,
		DeliveryID: r.Header.Get(HeaderDelivery),
		Host:       p.hostFor(c, in.ActingToken),
	}
}

func (p *Plugin) writeResponse(w http.ResponseWriter, what, id string, resp Response, err error) {
	if err != nil {
		p.Logger.Error(what+" failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error(), "", "")
		return
	}
	if verr := ValidateCommandResponse(resp); verr != nil {
		p.Logger.Error(what+" returned an invalid response", "id", id, "err", verr)
		writeValidation(w, http.StatusInternalServerError, verr)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (p *Plugin) serveCommand(w http.ResponseWriter, r *http.Request, body []byte, c Credentials) {
	id := r.PathValue("id")
	h, ok := p.commands[id]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown command", "", "")
		return
	}
	in, ok := p.decodeInteraction(w, body)
	if !ok {
		return
	}
	cc := p.commandContext(r, c, in, id)
	var resp Response
	err := protect(func() (err error) { resp, err = h(r.Context(), &cc); return err })
	p.writeResponse(w, "command", id, resp, err)
}

func (p *Plugin) servePanel(w http.ResponseWriter, r *http.Request, body []byte, c Credentials) {
	id := r.PathValue("id")
	h, ok := p.panels[id]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown panel", "", "")
		return
	}
	in, ok := p.decodeInteraction(w, body)
	if !ok {
		return
	}
	cc := p.commandContext(r, c, in, id)
	var node ui.Node
	err := protect(func() (err error) { node, err = h(r.Context(), &cc); return err })
	if err != nil {
		p.Logger.Error("panel failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error(), "", "")
		return
	}
	if verr := ui.Validate(node); verr != nil {
		p.Logger.Error("panel returned an invalid tree", "id", id, "err", verr)
		writeValidation(w, http.StatusInternalServerError, verr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ui": node})
}

func (p *Plugin) serveAction(w http.ResponseWriter, r *http.Request, body []byte, c Credentials) {
	id := r.PathValue("id")
	h, ok := p.actions[id]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown action", "", "")
		return
	}
	in, ok := p.decodeInteraction(w, body)
	if !ok {
		return
	}
	ac := ActionContext{CommandContext: p.commandContext(r, c, in, id), Values: in.Values, Payload: in.Payload}
	if ac.Values == nil {
		ac.Values = map[string]any{}
	}
	var resp Response
	err := protect(func() (err error) { resp, err = h(r.Context(), &ac); return err })
	p.writeResponse(w, "action", id, resp, err)
}

func (p *Plugin) serveImport(w http.ResponseWriter, r *http.Request, body []byte, c Credentials) {
	id := r.PathValue("importer")
	h, ok := p.importers[id]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown importer", "", "")
		return
	}
	in, ok := p.decodeInteraction(w, body)
	if !ok {
		return
	}
	if !ValidObjectID(in.FileID) {
		writeError(w, http.StatusBadRequest, "invalid fileId", ui.CodeInvalidID, "$.fileId")
		return
	}
	ic := ImportContext{
		ID: id, FileID: in.FileID, SpaceID: in.SpaceID, User: in.User,
		DeliveryID: r.Header.Get(HeaderDelivery), Host: p.hostFor(c, in.ActingToken),
	}
	var resp Response
	err := protect(func() (err error) { resp, err = h(r.Context(), &ic); return err })
	p.writeResponse(w, "importer", id, resp, err)
}
