package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// eventBuffer is how many events one subscriber may fall behind by.
const eventBuffer = 64

// subscriber is one open event stream.
type subscriber struct {
	ch chan core.Event
	// spaces limits which events reach this subscriber. A nil map means every
	// space, which is what an instance admin gets.
	spaces  map[string]bool
	dropped int
}

// hub is a pub-sub fan-out for change events. A slow subscriber loses events
// rather than blocking the publisher.
type hub struct {
	mu     sync.Mutex
	subs   map[int]*subscriber
	nextID int
	closed bool
}

// newHub builds an empty hub.
func newHub() *hub { return &hub{subs: make(map[int]*subscriber)} }

// add registers a subscriber and returns its channel plus an unsubscribe
// function. A nil spaces map receives every event.
func (h *hub) add(spaces map[string]bool) (<-chan core.Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		ch := make(chan core.Event)
		close(ch)
		return ch, func() {}
	}
	h.nextID++
	id := h.nextID
	sub := &subscriber{ch: make(chan core.Event, eventBuffer), spaces: spaces}
	h.subs[id] = sub
	var once sync.Once
	return sub.ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if s, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(s.ch)
			}
		})
	}
}

// publish delivers an event to every subscriber allowed to see it. It drops
// the event for a subscriber whose buffer is full.
func (h *hub) publish(ev core.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		if sub.spaces != nil && ev.SpaceID != "" && !sub.spaces[ev.SpaceID] {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			sub.dropped++
		}
	}
}

// count returns the number of live subscribers.
func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// close drops every subscriber and closes their channels.
func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, sub := range h.subs {
		delete(h.subs, id)
		close(sub.ch)
	}
}

var (
	// eventFreshness is how old a subscriber's access snapshot may be before
	// the next event refreshes it. Tests set it to zero.
	eventFreshness = 2 * time.Second
	// eventRecheck is how often an idle subscription re-checks the account,
	// so a revoked stream closes even when no events flow.
	eventRecheck = 30 * time.Second
)

// errStreamRevoked ends a subscription whose account, session or token no
// longer grants access.
var errStreamRevoked = fmt.Errorf("event stream access revoked: %w", core.ErrUnauthorized)

// eventFilter decides which events one subscriber receives. It holds a
// snapshot of the subscriber's access and refreshes it as it ages.
type eventFilter struct {
	app       *App
	userID    string
	sessionID string
	tokenID   string
	// plugin and via describe a plugin actor. A plugin token sees its
	// granted spaces. An acting token sees what both sides may see.
	plugin *core.PluginIdentity
	via    string

	admin   bool
	full    map[string]bool // spaces the user can view in full
	partial map[string]bool // spaces the user reaches only through shares
	cache   *grantCache
	checked time.Time
}

// refresh reloads the account and the access snapshot. It returns
// errStreamRevoked when the user is gone or inactive, or when the session or
// API token behind the stream no longer exists or has expired.
func (f *eventFilter) refresh(ctx context.Context) error {
	if f.plugin != nil {
		r := f.app.pluginResolver()
		if r == nil {
			return errStreamRevoked
		}
		id, err := r.PluginIdentity(ctx, f.plugin.ID)
		if errors.Is(err, core.ErrNotFound) || (err == nil && (id == nil || !core.PluginStatusRuns(id.Status))) {
			return errStreamRevoked
		}
		if err != nil {
			return fmt.Errorf("reload plugin subscriber: %w", err)
		}
		f.plugin = id
	}
	u, err := f.app.db.GetUser(ctx, f.userID)
	if errors.Is(err, core.ErrNotFound) || (err == nil && (u == nil || !u.IsActive)) {
		return errStreamRevoked
	}
	if err != nil {
		return fmt.Errorf("reload event subscriber: %w", err)
	}
	now := f.app.now()
	if f.sessionID != "" {
		sessions, err := f.app.db.ListSessions(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("reload subscriber sessions: %w", err)
		}
		live := false
		for _, s := range sessions {
			if s.ID == f.sessionID && now.Before(s.ExpiresAt) {
				live = true
				break
			}
		}
		if !live {
			return errStreamRevoked
		}
	}
	if f.tokenID != "" {
		tokens, err := f.app.db.ListAPITokens(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("reload subscriber tokens: %w", err)
		}
		live := false
		for _, t := range tokens {
			if t.ID == f.tokenID && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt)) {
				live = true
				break
			}
		}
		if !live {
			return errStreamRevoked
		}
	}

	actor := &core.Actor{User: u, Plugin: f.plugin, Via: f.via}
	cache := f.app.grants(actor)
	full := map[string]bool{}
	partial := map[string]bool{}
	admin := actor.IsAdmin()
	if !admin {
		ids, err := f.app.accessibleSpaceIDs(ctx, actor)
		if err != nil {
			return fmt.Errorf("resolve accessible spaces: %w", err)
		}
		for _, id := range ids {
			g, err := cache.space(ctx, id)
			if err != nil {
				return err
			}
			if g.Access.AtLeast(core.AccessView) {
				full[id] = true
			} else {
				partial[id] = true
			}
		}
	}
	f.admin, f.full, f.partial, f.cache, f.checked = admin, full, partial, cache, time.Now()
	return nil
}

// allows reports whether the subscriber may see an event. In a space the user
// reaches only through shares, the event's item, of any kind, must be
// visible, so the stream never reveals the id or path of a hidden item. An
// event about a removed item passes only while its row still exists.
func (f *eventFilter) allows(ctx context.Context, ev core.Event) bool {
	if len(ev.Audience) > 0 && !slices.Contains(ev.Audience, f.userID) {
		return false
	}
	if strings.HasPrefix(ev.Type, "job.") {
		// Job progress reaches the person who started the job, and admins.
		return f.admin || (ev.ActorID != "" && ev.ActorID == f.userID)
	}
	if f.admin {
		return true
	}
	id := ev.ItemID
	if id == "" {
		id = ev.NoteID
	}
	if ev.SpaceID == "" {
		return id == "" && ev.Path == ""
	}
	if f.full[ev.SpaceID] {
		return true
	}
	if !f.partial[ev.SpaceID] || id == "" {
		return false
	}
	it, err := f.app.loadItem(ctx, id)
	if err != nil || it.SpaceID() != ev.SpaceID {
		return false
	}
	g, err := f.cache.item(ctx, refOf(it))
	return err == nil && g.Access.AtLeast(core.AccessView)
}

// Subscribe returns a channel of change events the actor may see. Each event
// passes a per-subscriber access check against a snapshot at most
// eventFreshness old. The stream closes when the account is deactivated or
// deleted, or when the session or API token it uses is revoked. The returned
// function unsubscribes and closes the channel.
func (a *App) Subscribe(ctx context.Context, actor *core.Actor) (<-chan core.Event, func(), error) {
	if err := requireUser(actor); err != nil {
		return nil, nil, err
	}
	f := &eventFilter{app: a, userID: actor.User.ID, plugin: actor.Plugin, via: actor.Via}
	if actor.Session != nil {
		f.sessionID = actor.Session.ID
	}
	if actor.Token != nil {
		f.tokenID = actor.Token.ID
	}
	dbctx, cancelDB := context.WithTimeout(context.Background(), 5*time.Second)
	err := f.refresh(dbctx)
	cancelDB()
	if err != nil {
		return nil, nil, err
	}

	// Copy the intervals so the goroutine never reads the package variables,
	// which tests change.
	freshness, recheck := eventFreshness, eventRecheck

	raw, unsubscribe := a.events.add(nil)
	out := make(chan core.Event, eventBuffer)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			unsubscribe()
		})
	}

	// refreshOrStop reloads the snapshot and reports whether the stream
	// should continue. A transient database error keeps the old snapshot.
	refreshOrStop := func() bool {
		dbctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.refresh(dbctx); err != nil {
			if errors.Is(err, errStreamRevoked) {
				a.log.Info("event stream closed, access revoked", "userId", f.userID)
				return false
			}
			a.log.Debug("event subscriber refresh failed", "userId", f.userID, "err", err)
		}
		return true
	}

	go func() {
		defer close(out)
		defer stop()
		ticker := time.NewTicker(recheck)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !refreshOrStop() {
					return
				}
			case ev, ok := <-raw:
				if !ok {
					return
				}
				if time.Since(f.checked) >= freshness && !refreshOrStop() {
					return
				}
				dbctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				allowed := f.allows(dbctx, ev)
				cancel()
				if !allowed {
					continue
				}
				select {
				case out <- ev:
				default:
					// A subscriber that falls behind loses events rather than
					// blocking the stream for everyone.
				}
			}
		}
	}()
	return out, stop, nil
}

// sharedFilter is an eventFilter safe for concurrent use that refreshes its
// snapshot as it ages.
type sharedFilter struct {
	mu        sync.Mutex
	f         *eventFilter
	freshness time.Duration
	revoked   bool
}

// NewEventFilter returns the access filter the event stream applies, for use
// outside the stream. The plugin host filters plugin event deliveries with
// it, so a plugin receives events only from the spaces it is granted. A
// revoked actor sees no events until a later refresh succeeds.
func (a *App) NewEventFilter(ctx context.Context, actor *core.Actor) (core.EventFilter, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	f := &eventFilter{app: a, userID: actor.User.ID, plugin: actor.Plugin, via: actor.Via}
	if err := f.refresh(ctx); err != nil {
		return nil, err
	}
	return &sharedFilter{f: f, freshness: eventFreshness}, nil
}

// Allows implements core.EventFilter.
func (s *sharedFilter) Allows(ctx context.Context, ev core.Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked || time.Since(s.f.checked) >= s.freshness {
		err := s.f.refresh(ctx)
		switch {
		case errors.Is(err, errStreamRevoked):
			s.revoked = true
			s.f.checked = time.Now()
		case err == nil:
			s.revoked = false
		}
	}
	if s.revoked {
		return false
	}
	return s.f.allows(ctx, ev)
}
