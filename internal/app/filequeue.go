package app

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/extract"
	"github.com/gamemaker1/markspace/internal/ports"
)

const (
	// fileQueueInterval is how often the background queues look for work
	// when nothing kicks them.
	fileQueueInterval = 30 * time.Second
	// fileQueueBatch is how many files one queue pass takes.
	fileQueueBatch = 50
	// maxTextFileBytes is the largest file whose text is extracted.
	maxTextFileBytes = 20 << 20
	// maxTextRunes caps the stored text of one file.
	maxTextRunes = 200000
	// textTimeout caps the extraction of one file.
	textTimeout = time.Minute
)

// kickQueue wakes the background queues without blocking.
func (a *App) kickQueue() {
	select {
	case a.queueKick <- struct{}{}:
	default:
	}
}

// fileQueueLoop fills missing hashes and extracted text in the background.
func (a *App) fileQueueLoop(ctx context.Context) {
	ticker := time.NewTicker(fileQueueInterval)
	defer ticker.Stop()
	for {
		a.drainFileQueues(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.queueKick:
		}
	}
}

// drainFileQueues runs both queues until neither finds new work. A file that
// fails is tried again on a later drain.
func (a *App) drainFileQueues(ctx context.Context) {
	tried := map[string]bool{}
	for ctx.Err() == nil {
		n := a.hashPending(ctx, tried) + a.extractPending(ctx)
		if n == 0 {
			return
		}
	}
}

// queueSpace loads a space for a queue pass, reusing earlier lookups.
func (a *App) queueSpace(ctx context.Context, cache map[string]*core.Space, id string) *core.Space {
	if sp, ok := cache[id]; ok {
		return sp
	}
	sp, err := a.space(ctx, id)
	if err != nil {
		sp = nil
	}
	cache[id] = sp
	return sp
}

// hashPending hashes one batch of files that have no stored hash. It returns
// how many files it tried that it had not tried before.
func (a *App) hashPending(ctx context.Context, tried map[string]bool) int {
	list, err := a.db.ListFilesNeedingHash(ctx, fileQueueBatch)
	if err != nil {
		a.log.Warn("list files needing a hash failed", "err", err)
		return 0
	}
	spaces := map[string]*core.Space{}
	n := 0
	for _, f := range list {
		if tried[f.ID] || ctx.Err() != nil {
			continue
		}
		tried[f.ID] = true
		n++
		space := a.queueSpace(ctx, spaces, f.SpaceID)
		if space == nil {
			continue
		}
		info, err := a.files.StatEntry(space.Slug, f.Path)
		if err != nil {
			continue
		}
		sha, err := a.files.HashFile(space.Slug, f.Path)
		if err != nil {
			a.log.Debug("hash file failed", "fileId", f.ID, "err", err)
			continue
		}
		if err := a.db.SetFileHash(ctx, f.ID, sha, info.Size(), info.ModTime().UTC()); err != nil {
			a.log.Debug("store file hash failed", "fileId", f.ID, "err", err)
		}
	}
	return n
}

// extractPending extracts the text of one batch of files whose text state is
// pending. Every file leaves the pending state.
func (a *App) extractPending(ctx context.Context) int {
	list, err := a.db.ListFilesNeedingText(ctx, fileQueueBatch)
	if err != nil {
		a.log.Warn("list files needing text failed", "err", err)
		return 0
	}
	spaces := map[string]*core.Space{}
	n := 0
	for _, f := range list {
		if ctx.Err() != nil {
			break
		}
		state, text := ports.TextFailed, ""
		if space := a.queueSpace(ctx, spaces, f.SpaceID); space != nil {
			state, text = a.extractText(ctx, space, f)
		}
		if err := a.db.SetFileText(ctx, f.ID, text, state); err != nil {
			a.log.Warn("store file text failed", "fileId", f.ID, "err", err)
			continue
		}
		n++
	}
	return n
}

// isPDFFile reports whether a file is a PDF by type or name.
func isPDFFile(f *core.File) bool {
	return strings.EqualFold(f.MIME, "application/pdf") || strings.EqualFold(path.Ext(f.Name), ".pdf")
}

// extractText reads the text of a PDF or a text-like file up to
// maxTextFileBytes and keeps the first maxTextRunes characters.
func (a *App) extractText(ctx context.Context, space *core.Space, f *core.File) (ports.FileTextState, string) {
	pdf := isPDFFile(f)
	if !pdf && !extract.IsText(f.MIME, f.Name) {
		return ports.TextSkipped, ""
	}
	if f.Size > maxTextFileBytes {
		return ports.TextSkipped, ""
	}
	rc, _, err := a.files.OpenFile(space.Slug, f.Path)
	if err != nil {
		return ports.TextFailed, ""
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxTextFileBytes+1))
	rc.Close()
	if err != nil {
		return ports.TextFailed, ""
	}
	if len(data) > maxTextFileBytes {
		return ports.TextSkipped, ""
	}
	var text string
	if pdf {
		tctx, cancel := context.WithTimeout(ctx, textTimeout)
		text, _, err = extract.PDFText(tctx, data, 0, maxTextRunes*utf8.UTFMax)
		cancel()
	} else {
		text, err = extract.DecodeText(data)
	}
	if err != nil {
		a.log.Debug("extract text failed", "fileId", f.ID, "err", fmt.Sprint(err))
		return ports.TextFailed, ""
	}
	return ports.TextDone, truncateRunes(text, maxTextRunes)
}
