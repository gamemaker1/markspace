package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/auth"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/sdk"
)

// callResult is the outcome of one request to a plugin.
type callResult struct {
	Status int
	Body   []byte
}

// errorText returns the error message of a plugin error body, or the status.
func (c callResult) errorText() string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(c.Body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return fmt.Sprintf("status %d", c.Status)
}

// call sends one request to a plugin. Every request except GET is signed with
// the plugin's signing secret and a fresh timestamp.
func (h *Host) call(ctx context.Context, row *index.Plugin, method, path, deliveryID string, body any, timeout time.Duration) (callResult, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return callResult{}, err
		}
	}
	return h.callRaw(ctx, row, method, path, deliveryID, raw, timeout)
}

func (h *Host) callRaw(ctx context.Context, row *index.Plugin, method, path, deliveryID string, raw []byte, timeout time.Duration) (callResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if raw != nil {
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(row.BaseURL, "/")+path, rd)
	if err != nil {
		return callResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		secret, err := sdk.DecodeSecret(row.SigningSecret)
		if err != nil {
			return callResult{}, fmt.Errorf("plugin %s has no signing secret: %w", row.ID, core.ErrUnavailable)
		}
		if deliveryID == "" {
			deliveryID = core.NewID()
		}
		sdk.SignRequest(req, secret, row.ID, deliveryID, raw, time.Now())
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return callResult{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return callResult{Status: resp.StatusCode}, err
	}
	if len(b) > maxResponseBytes {
		return callResult{Status: resp.StatusCode}, fmt.Errorf("plugin response is larger than %d bytes", maxResponseBytes)
	}
	return callResult{Status: resp.StatusCode, Body: b}, nil
}

// unavailable wraps a failed plugin interaction for the client.
func unavailable(pluginID, what string, err error) error {
	return fmt.Errorf("plugin %s %s: %v: %w", pluginID, what, err, core.ErrUnavailable)
}

// runnable returns a plugin that may take a user interaction now.
func (h *Host) runnable(id string) (*index.Plugin, sdk.Manifest, error) {
	row, man, _, ok := h.snapshot(id)
	switch {
	case !ok:
		return nil, man, fmt.Errorf("plugin %s: %w", id, core.ErrNotFound)
	case row.Status == core.PluginStatusDown:
		return nil, man, fmt.Errorf("plugin %s is not responding: %w", id, core.ErrUnavailable)
	case !core.PluginStatusRuns(row.Status):
		return nil, man, fmt.Errorf("plugin %s is %s: %w", id, row.Status, core.ErrUnavailable)
	}
	return row, man, nil
}

// actingToken issues an acting token for one interaction of a user.
func (h *Host) actingToken(ctx context.Context, pluginID, userID, interaction string) (string, error) {
	plain := core.ActingTokenPrefix + core.NewToken(25)
	tok := &index.ActingToken{TokenHash: auth.HashToken(plain), PluginID: pluginID, UserID: userID, Interaction: interaction}
	if err := h.store.CreateActingToken(ctx, tok, h.now()); err != nil {
		return "", fmt.Errorf("issue acting token: %w", err)
	}
	return plain, nil
}

// interactionBody is the body of a command, panel or action call.
type interactionBody struct {
	Context     module.CommandContext `json:"context"`
	Values      map[string]any        `json:"values,omitempty"`
	Payload     map[string]any        `json:"payload,omitempty"`
	ActingToken string                `json:"actingToken"`
	User        module.Actor          `json:"user"`
}

// interact issues an acting token and posts an interaction to the plugin.
func (h *Host) interact(ctx context.Context, pluginID, kind, local string, req module.CommandRequest, payload map[string]any, timeout time.Duration) (callResult, error) {
	row, _, err := h.runnable(pluginID)
	if err != nil {
		return callResult{}, err
	}
	tok, err := h.actingToken(ctx, pluginID, req.User.ID, kind+":"+local)
	if err != nil {
		return callResult{}, err
	}
	path := map[string]string{"command": "/commands/", "panel": "/panels/", "action": "/actions/"}[kind] + local
	res, err := h.call(ctx, row, http.MethodPost, path, "", interactionBody{
		Context: req.Context, Values: req.Values, Payload: payload, ActingToken: tok, User: req.User,
	}, timeout)
	if err != nil {
		return res, unavailable(pluginID, kind+" "+local, err)
	}
	if res.Status != http.StatusOK {
		return res, unavailable(pluginID, kind+" "+local, errors.New(res.errorText()))
	}
	return res, nil
}

// parseResponse validates a command response from a plugin.
func parseResponse(pluginID, what string, res callResult) (module.CommandResponse, error) {
	resp, err := module.ParseCommandResponse(res.Body)
	if err != nil {
		return module.CommandResponse{}, unavailable(pluginID, what+" returned an invalid response", err)
	}
	return resp, nil
}

// commandProxy runs a plugin command.
func (h *Host) commandProxy(pluginID, local string) module.CommandHandler {
	return func(ctx context.Context, req module.CommandRequest) (module.CommandResponse, error) {
		res, err := h.interact(ctx, pluginID, "command", local, req, nil, h.opts.CallTimeout)
		if err != nil {
			return module.CommandResponse{}, err
		}
		return parseResponse(pluginID, "command "+local, res)
	}
}

// panelProxy renders a plugin panel. The response is {ui}.
func (h *Host) panelProxy(pluginID, local string) module.PanelHandler {
	return func(ctx context.Context, req module.CommandRequest) (module.Node, error) {
		res, err := h.interact(ctx, pluginID, "panel", local, req, nil, h.opts.CallTimeout)
		if err != nil {
			return module.Node{}, err
		}
		var top map[string]json.RawMessage
		if json.Unmarshal(res.Body, &top) != nil || len(top["ui"]) == 0 {
			return module.Node{}, unavailable(pluginID, "panel "+local, errors.New("the response has no ui tree"))
		}
		node, err := module.ValidateUI(top["ui"])
		if err != nil {
			return module.Node{}, unavailable(pluginID, "panel "+local+" returned an invalid tree", err)
		}
		return node, nil
	}
}

// RunAction runs a panel action of a plugin module. It implements
// ui.ActionRunner. An action may return {job} to run long work as a job, and
// its call may take up to the action timeout.
func (h *Host) RunAction(ctx context.Context, moduleID, actionID string, req module.CommandRequest, payload map[string]any) (module.CommandResponse, error) {
	id, ok := strings.CutPrefix(moduleID, "plugin.")
	if !ok || !sdk.ValidLocalID(actionID) {
		return module.CommandResponse{}, fmt.Errorf("action: %w", core.ErrNotFound)
	}
	_, man, err := h.runnable(id)
	if err != nil {
		return module.CommandResponse{}, err
	}
	if !slices.Contains(man.Permissions, "ui") {
		return module.CommandResponse{}, fmt.Errorf("action: %w", core.ErrNotFound)
	}
	res, err := h.interact(ctx, id, "action", actionID, req, payload, h.opts.ActionTimeout)
	if err != nil {
		return module.CommandResponse{}, err
	}
	return parseResponse(id, "action "+actionID, res)
}

// importProxy starts a plugin import. The response is {job} or a command
// response.
func (h *Host) importProxy(pluginID, local string) module.ImportHandler {
	return func(ctx context.Context, req module.ImportRequest) (module.CommandResponse, error) {
		row, _, err := h.runnable(pluginID)
		if err != nil {
			return module.CommandResponse{}, err
		}
		tok, err := h.actingToken(ctx, pluginID, req.User.ID, "import:"+local)
		if err != nil {
			return module.CommandResponse{}, err
		}
		res, err := h.call(ctx, row, http.MethodPost, "/import/"+local, "", map[string]any{
			"fileId": req.FileID, "spaceId": req.SpaceID, "actingToken": tok, "user": req.User,
		}, h.opts.CallTimeout)
		if err != nil {
			return module.CommandResponse{}, unavailable(pluginID, "import "+local, err)
		}
		if res.Status != http.StatusOK {
			return module.CommandResponse{}, unavailable(pluginID, "import "+local, errors.New(res.errorText()))
		}
		return parseResponse(pluginID, "import "+local, res)
	}
}

// ModuleAvailable hides a plugin's contributions in spaces it is not granted.
// It implements ui.Availability.
func (h *Host) ModuleAvailable(ctx context.Context, moduleID, spaceID string) bool {
	id, ok := strings.CutPrefix(moduleID, "plugin.")
	if !ok {
		return true
	}
	row, _, ident, loaded := h.snapshot(id)
	if !loaded || !core.PluginStatusRuns(row.Status) {
		return false
	}
	return spaceID == "" || ident.SpaceAccess(spaceID) >= core.AccessView
}
