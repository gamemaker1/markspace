package module

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Event is a change notification. The wiring step converts core events into
// this type.
type Event struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	SpaceID string          `json:"spaceId,omitempty"`
	ItemID  string          `json:"itemId,omitempty"`
	Kind    core.ItemKind   `json:"kind,omitempty"`
	ActorID string          `json:"actorId,omitempty"`
	Path    string          `json:"path,omitempty"`
	OldPath string          `json:"oldPath,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	At      time.Time       `json:"at"`
}

// EventJobUpdated is the type of the event the job engine publishes.
const EventJobUpdated = "job.updated"

// EventHandler handles one event. The context is canceled when the handler
// is removed or when Bus.Close gives up waiting. Handlers share Event.Data and
// must not modify it.
type EventHandler func(ctx context.Context, e Event) error

// Publisher publishes events. *Bus implements it.
type Publisher interface {
	Publish(e Event)
}

// Bus defaults.
const (
	DefaultQueueSize    = 1024
	DefaultDrainTimeout = 5 * time.Second
)

var (
	eventTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)*$`)
	eventGlobPattern = regexp.MustCompile(`^[a-z0-9*][a-z0-9._*-]{0,127}$`)
)

// ValidEventType reports whether t is a valid event type such as
// "note.created" or "plugin.bookmark-wiki.synced".
func ValidEventType(t string) bool {
	return len(t) <= 128 && eventTypePattern.MatchString(t)
}

// ValidEventPattern reports whether p is a valid event pattern as the plugin
// protocol defines it: 1 to 128 characters from a-z, 0-9, dot, underscore,
// hyphen and star, starting with a letter, digit or star.
func ValidEventPattern(p string) bool {
	return eventGlobPattern.MatchString(p)
}

// MatchEventType reports whether an event type matches a pattern. A star
// matches any run of characters, dots and the empty run included. Every other
// character matches itself. So "*" matches every type, "note.*" matches
// "note.created" but not "note", and "*.created" matches "folder.created".
func MatchEventType(pattern, typ string) bool {
	p, t := 0, 0
	star, mark := -1, 0
	for t < len(typ) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, t
			p++
		case p < len(pattern) && pattern[p] == typ[t]:
			p++
			t++
		case star >= 0:
			mark++
			p, t = star+1, mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// BusOptions configures a Bus. Zero values select the defaults.
type BusOptions struct {
	// QueueSize is the per-handler queue length. The default is 1024.
	QueueSize int
	// DrainTimeout bounds how long Close waits for handlers. The default is 5 s.
	DrainTimeout time.Duration
	Logger       *slog.Logger
	Now          func() time.Time
}

// Bus delivers events to in-process handlers and streaming subscribers. Each
// handler has its own goroutine and a bounded queue. A full queue drops its
// oldest event. Publish never blocks on a handler.
type Bus struct {
	queueSize int
	drain     time.Duration
	log       *slog.Logger
	now       func() time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	managed map[uint64]*handlerQueue
	queues  []*handlerQueue
	subs    []*subscription
}

type handlerQueue struct {
	module  string
	pattern string
	handler EventHandler
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	done    chan struct{}

	mu        sync.Mutex
	items     []Event
	stopping  bool
	removed   bool
	delivered uint64
	dropped   uint64
	errors    uint64
	panics    uint64
}

type subscription struct {
	pattern string
	ch      chan Event
	dropped uint64
}

// HandlerStats reports the counters of one handler.
type HandlerStats struct {
	Module    string `json:"module"`
	Pattern   string `json:"pattern"`
	Queued    int    `json:"queued"`
	Delivered uint64 `json:"delivered"`
	Dropped   uint64 `json:"dropped"`
	Errors    uint64 `json:"errors"`
	Panics    uint64 `json:"panics"`
}

// SubscriberStats reports the counters of one streaming subscriber.
type SubscriberStats struct {
	Pattern string `json:"pattern"`
	Queued  int    `json:"queued"`
	Dropped uint64 `json:"dropped"`
}

// BusStats holds the counters the admin page shows.
type BusStats struct {
	Handlers    []HandlerStats    `json:"handlers"`
	Subscribers []SubscriberStats `json:"subscribers"`
}

// NewBus returns a running bus.
func NewBus(opts BusOptions) *Bus {
	b := &Bus{
		queueSize: opts.QueueSize,
		drain:     opts.DrainTimeout,
		log:       opts.Logger,
		now:       opts.Now,
		managed:   map[uint64]*handlerQueue{},
	}
	if b.queueSize <= 0 {
		b.queueSize = DefaultQueueSize
	}
	if b.drain <= 0 {
		b.drain = DefaultDrainTimeout
	}
	if b.log == nil {
		b.log = slog.Default()
	}
	if b.now == nil {
		b.now = utcNow
	}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	return b
}

// Publish queues an event for every matching handler and subscriber. It fills
// an empty ID with core.NewID and a zero At with the bus clock. Events
// published after Close are discarded.
func (b *Bus) Publish(e Event) {
	if e.ID == "" {
		e.ID = core.NewID()
	}
	if e.At.IsZero() {
		e.At = b.now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, q := range b.queues {
		if MatchEventType(q.pattern, e.Type) {
			q.push(e, b.queueSize)
		}
	}
	for _, s := range b.subs {
		if MatchEventType(s.pattern, e.Type) {
			s.send(e)
		}
	}
}

// SetHandlers makes the bus run exactly the given registrations. Handlers
// whose Seq is new start, and handlers whose Seq is gone stop and lose their
// queued events. The wiring step calls it with Registry.EventHandlers from a
// Registry.Watch callback.
func (b *Bus) SetHandlers(regs []EventRegistration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	want := make(map[uint64]bool, len(regs))
	for _, r := range regs {
		want[r.Seq] = true
	}
	for seq, q := range b.managed {
		if !want[seq] {
			q.remove()
			delete(b.managed, seq)
			b.queues = slices.DeleteFunc(b.queues, func(x *handlerQueue) bool { return x == q })
		}
	}
	for _, r := range regs {
		if _, ok := b.managed[r.Seq]; !ok {
			b.managed[r.Seq] = b.startLocked(r.Module, r.Pattern, r.Handler)
		}
	}
}

// Handle adds one handler outside the registry and returns a function that
// removes it.
func (b *Bus) Handle(module, pattern string, h EventHandler) (remove func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return func() {}
	}
	q := b.startLocked(module, pattern, h)
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			q.remove()
			b.queues = slices.DeleteFunc(b.queues, func(x *handlerQueue) bool { return x == q })
		})
	}
}

func (b *Bus) startLocked(module, pattern string, h EventHandler) *handlerQueue {
	ctx, cancel := context.WithCancel(b.ctx)
	q := &handlerQueue{
		module:  module,
		pattern: pattern,
		handler: h,
		ctx:     ctx,
		cancel:  cancel,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	b.queues = append(b.queues, q)
	b.wg.Add(1)
	go b.run(q)
	return q
}

func (q *handlerQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *handlerQueue) push(e Event, size int) {
	q.mu.Lock()
	if q.removed || q.stopping {
		q.mu.Unlock()
		return
	}
	if len(q.items) >= size {
		q.items[0] = Event{}
		q.items = q.items[1:]
		q.dropped++
	}
	q.items = append(q.items, e)
	q.mu.Unlock()
	q.signal()
}

// next returns the next event, or reports that the worker should exit.
func (q *handlerQueue) next() (e Event, ok, exit bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.removed {
		return Event{}, false, true
	}
	if len(q.items) == 0 {
		q.items = nil
		return Event{}, false, q.stopping
	}
	e = q.items[0]
	q.items[0] = Event{}
	q.items = q.items[1:]
	return e, true, false
}

func (q *handlerQueue) remove() {
	q.mu.Lock()
	q.removed = true
	q.items = nil
	q.mu.Unlock()
	q.cancel()
	q.signal()
}

func (q *handlerQueue) stop() {
	q.mu.Lock()
	q.stopping = true
	q.mu.Unlock()
	q.signal()
}

func (b *Bus) run(q *handlerQueue) {
	defer b.wg.Done()
	defer close(q.done)
	for {
		e, ok, exit := q.next()
		if ok {
			b.deliver(q, e)
			continue
		}
		if exit {
			return
		}
		<-q.wake
	}
}

func (b *Bus) deliver(q *handlerQueue, e Event) {
	defer func() {
		if v := recover(); v != nil {
			q.mu.Lock()
			q.panics++
			q.mu.Unlock()
			b.log.Error("event handler panicked",
				"module", q.module, "pattern", q.pattern, "event", e.Type, "eventId", e.ID,
				"panic", fmt.Sprint(v), "stack", string(debug.Stack()))
		}
	}()
	err := q.handler(q.ctx, e)
	q.mu.Lock()
	q.delivered++
	if err != nil {
		q.errors++
	}
	q.mu.Unlock()
	if err != nil {
		b.log.Warn("event handler failed",
			"module", q.module, "pattern", q.pattern, "event", e.Type, "eventId", e.ID, "err", err)
	}
}

func (s *subscription) send(e Event) {
	select {
	case s.ch <- e:
		return
	default:
	}
	select {
	case <-s.ch:
		s.dropped++
	default:
	}
	select {
	case s.ch <- e:
	default:
		s.dropped++
	}
}

// Subscribe returns a channel of events whose type matches filter, with the
// given buffer (DefaultQueueSize when buffer <= 0). A full buffer drops its
// oldest event. cancel removes the subscription and closes the channel. Close
// also closes it.
func (b *Bus) Subscribe(filter string, buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = DefaultQueueSize
	}
	s := &subscription{pattern: filter, ch: make(chan Event, buffer)}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(s.ch)
		return s.ch, func() {}
	}
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if i := slices.Index(b.subs, s); i >= 0 {
				b.subs = slices.Delete(b.subs, i, i+1)
				close(s.ch)
			}
		})
	}
}

// Stats returns the handler and subscriber counters.
func (b *Bus) Stats() BusStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := BusStats{Handlers: []HandlerStats{}, Subscribers: []SubscriberStats{}}
	for _, q := range b.queues {
		q.mu.Lock()
		st.Handlers = append(st.Handlers, HandlerStats{
			Module: q.module, Pattern: q.pattern, Queued: len(q.items),
			Delivered: q.delivered, Dropped: q.dropped, Errors: q.errors, Panics: q.panics,
		})
		q.mu.Unlock()
	}
	for _, s := range b.subs {
		st.Subscribers = append(st.Subscribers, SubscriberStats{Pattern: s.pattern, Queued: len(s.ch), Dropped: s.dropped})
	}
	return st
}

// Close stops accepting events, closes subscriber channels, and waits up to
// the drain timeout for handlers to finish their queued events. When the
// timeout passes it cancels the handler contexts and returns an error.
func (b *Bus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	queues := slices.Clone(b.queues)
	for _, q := range queues {
		q.stop()
	}
	for _, s := range b.subs {
		close(s.ch)
	}
	b.subs = nil
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(b.drain)
	defer timer.Stop()
	select {
	case <-done:
		b.cancel()
		return nil
	case <-timer.C:
	}
	b.cancel()
	running := 0
	for _, q := range queues {
		select {
		case <-q.done:
		default:
			running++
		}
	}
	return fmt.Errorf("event bus: %d handlers did not drain within %s", running, b.drain)
}
