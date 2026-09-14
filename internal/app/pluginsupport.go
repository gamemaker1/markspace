package app

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// Embedding limits for plugin requests.
const (
	maxEmbedTexts = 256
	maxEmbedRunes = 1 << 20
)

// OutboundLinks lists the stored outbound links of a note. A target the
// actor cannot view, or a trashed target, comes back with only its text.
func (a *App) OutboundLinks(ctx context.Context, actor *core.Actor, noteID string) ([]*core.ResolvedTarget, error) {
	note, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView)
	if err != nil {
		return nil, err
	}
	space, err := a.space(ctx, note.SpaceID)
	if err != nil {
		return nil, err
	}
	refs, err := a.db.OutboundLinks(ctx, note.ID)
	if err != nil {
		return nil, fmt.Errorf("outbound links: %w", err)
	}
	cache := a.grants(actor)
	out := make([]*core.ResolvedTarget, 0, len(refs))
	for _, l := range refs {
		res := &core.ResolvedTarget{Target: l.Target}
		out = append(out, res)
		if l.TargetID == "" {
			continue
		}
		it, err := a.loadItem(ctx, l.TargetID)
		if err != nil || itemDeleted(it) || it.SpaceID() != space.ID {
			continue
		}
		if g, err := cache.item(ctx, refOf(it)); err != nil || !g.Access.AtLeast(core.AccessView) {
			continue
		}
		res.Kind, res.ID, res.Path = it.Kind, it.ID(), it.Path()
		res.URL = itemURL(space.Slug, it.Kind, it.Path())
	}
	return out, nil
}

// fileTexts is the index port that reads extracted file text.
type fileTexts interface {
	FileText(ctx context.Context, fileID string) (string, error)
}

// FileText returns the extracted text of a file the actor may view. The text
// queue keeps the first maxTextRunes characters, so a text of that length
// reports truncated.
func (a *App) FileText(ctx context.Context, actor *core.Actor, fileID string) (string, bool, error) {
	it, _, err := a.requireItemAny(ctx, actor, fileID, core.AccessView)
	if err != nil {
		return "", false, err
	}
	if it.File == nil {
		return "", false, core.Invalid("id", "the item is not a file")
	}
	reader, ok := a.db.(fileTexts)
	if !ok {
		return "", false, fmt.Errorf("file text: %w", core.ErrUnsupported)
	}
	text, err := reader.FileText(ctx, it.File.ID)
	if errors.Is(err, core.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("file text: %w", err)
	}
	return text, utf8.RuneCountInString(text) >= maxTextRunes, nil
}

// EmbedTexts embeds texts with the instance embeddings route. The call counts
// against the actor's and the plugin's budgets, and the usage row names the
// plugin.
func (a *App) EmbedTexts(ctx context.Context, actor *core.Actor, texts []string, pluginID string) (*core.AIEmbedResult, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if len(texts) == 0 || len(texts) > maxEmbedTexts {
		return nil, core.Invalid("texts", fmt.Sprintf("send 1 to %d texts", maxEmbedTexts))
	}
	runes := 0
	for _, t := range texts {
		runes += utf8.RuneCountInString(t)
	}
	if runes > maxEmbedRunes {
		return nil, fmt.Errorf("texts hold more than %d characters: %w", maxEmbedRunes, core.ErrTooLarge)
	}
	settings, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.AIEnabled {
		return nil, errAIDisabled
	}
	target, err := a.embeddingTarget(ctx)
	if err != nil {
		return nil, err
	}
	if _, stop, err := a.checkBudgets(ctx, settings, actor.UserID(), pluginID); err == nil && stop != nil {
		return nil, quotaError(stop)
	}
	start := time.Now()
	vectors, err := target.provider.Embed(ctx, texts, target.model)
	rec := core.AIUsageRecord{
		UserID: actor.UserID(), ProviderID: target.cfg.ID, Model: target.model, Feature: "plugin",
		Task: core.RouteEmbeddings, PluginID: cutLabel(pluginID, maxPluginIDBytes),
		Status: usageStatus(ctx, err), Error: errorText(err), CreatedAt: a.now(),
	}
	if err != nil {
		a.noteCallError(ctx, target.cfg.ID, err)
	}
	fillUsage(&rec, target.price, runes, 0, 0, "", start)
	a.recordUsage(ctx, rec)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	return &core.AIEmbedResult{Provider: target.cfg.ID, Model: target.model, Vectors: vectors}, nil
}
