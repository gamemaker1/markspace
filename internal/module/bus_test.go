package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestMatchEventType(t *testing.T) {
	cases := []struct {
		pattern, typ string
		want         bool
	}{
		{"*", "note.created", true},
		{"*", "", true},
		{"note.*", "note.created", true},
		{"note.*", "note.a.b", true},
		{"note.*", "note", false},
		{"note.*", "notes.created", false},
		{"note.*", "file.created", false},
		{"note.created", "note.created", true},
		{"note.created", "note.created.x", false},
		{"note.created", "note.update", false},
		{"plugin.bookmark-wiki.*", "plugin.bookmark-wiki.synced", true},
		{"plugin.*", "plugin.bookmark-wiki.synced", true},
		{"*.created", "folder.created", true},
		{"*.created", "folder.created.x", false},
		{"a*b*c", "aXbYbZc", true},
		{"a*b*c", "aXbYc.d", false},
		{"job.updated", "job.updated", true},
	}
	var doc struct {
		IDs struct {
			EventMatches []struct {
				Pattern string `json:"pattern"`
				Type    string `json:"type"`
				Match   bool   `json:"match"`
			} `json:"eventMatches"`
		} `json:"ids"`
	}
	loadProtocolFile(t, &doc)
	if len(doc.IDs.EventMatches) < 10 {
		t.Fatalf("only %d shared event match cases", len(doc.IDs.EventMatches))
	}
	for _, m := range doc.IDs.EventMatches {
		cases = append(cases, struct {
			pattern, typ string
			want         bool
		}{m.Pattern, m.Type, m.Match})
	}
	for _, c := range cases {
		if got := MatchEventType(c.pattern, c.typ); got != c.want {
			t.Errorf("MatchEventType(%q, %q) = %v, want %v", c.pattern, c.typ, got, c.want)
		}
	}
	for p, want := range map[string]bool{
		"*": true, "note.*": true, "note.created": true, "plugin.bookmark-wiki.synced": true,
		"*.created": true, "note.**": true, strings.Repeat("a", 128): true,
		"": false, ".*": false, "-note": false, "Note.created": false,
		"note created": false, "note/created": false, strings.Repeat("a", 129): false,
	} {
		if got := ValidEventPattern(p); got != want {
			t.Errorf("ValidEventPattern(%q) = %v, want %v", p, got, want)
		}
	}
}

func seqEvent(typ string, n int) Event {
	return Event{Type: typ, Data: json.RawMessage(fmt.Sprint(n))}
}

func eventSeq(e Event) int {
	var n int
	_ = json.Unmarshal(e.Data, &n)
	return n
}

func TestBusOrderingPerHandler(t *testing.T) {
	b := NewBus(BusOptions{Logger: quietLogger()})
	var mu sync.Mutex
	got := map[string][]int{}
	record := func(name string) EventHandler {
		return func(_ context.Context, e Event) error {
			mu.Lock()
			got[name] = append(got[name], eventSeq(e))
			mu.Unlock()
			return nil
		}
	}
	b.Handle("a", "*", record("all"))
	b.Handle("b", "note.*", record("notes"))
	b.Handle("c", "file.created", record("files"))
	types := []string{"note.created", "file.created", "note.updated", "job.updated"}
	var wg sync.WaitGroup
	const perPublisher = 250
	for p := range 4 {
		wg.Go(func() {
			for i := range perPublisher {
				b.Publish(seqEvent(types[(p+i)%len(types)], p*perPublisher+i))
			}
		})
	}
	wg.Wait()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got["all"]) != 4*perPublisher {
		t.Fatalf("catch-all handler got %d events, want %d", len(got["all"]), 4*perPublisher)
	}
	pos := map[int]int{}
	for i, n := range got["all"] {
		pos[n] = i
	}
	for name, seq := range got {
		for i := 1; i < len(seq); i++ {
			if pos[seq[i-1]] > pos[seq[i]] {
				t.Fatalf("handler %s saw %d before %d, but the catch-all saw the reverse", name, seq[i-1], seq[i])
			}
		}
		for p := range 4 {
			last := -1
			for _, n := range seq {
				if n/perPublisher == p {
					if n <= last {
						t.Fatalf("handler %s: publisher %d events out of order", name, p)
					}
					last = n
				}
			}
		}
	}
	if len(got["notes"]) != 2*perPublisher || len(got["files"]) != perPublisher {
		t.Fatalf("filtered handlers got %d and %d events", len(got["notes"]), len(got["files"]))
	}
}

func TestBusDropOldest(t *testing.T) {
	b := NewBus(BusOptions{QueueSize: 4, Logger: quietLogger()})
	started := make(chan struct{})
	release := make(chan struct{})
	var got []int
	b.Handle("slow", "*", func(_ context.Context, e Event) error {
		if eventSeq(e) == 0 {
			close(started)
			<-release
		}
		got = append(got, eventSeq(e))
		return nil
	})
	b.Publish(seqEvent("note.created", 0))
	<-started
	for i := 1; i <= 10; i++ {
		b.Publish(seqEvent("note.created", i))
	}
	st := b.Stats().Handlers[0]
	if st.Dropped != 6 || st.Queued != 4 {
		t.Fatalf("stats %+v, want 6 dropped and 4 queued", st)
	}
	close(release)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []int{0, 7, 8, 9, 10}) {
		t.Fatalf("delivered %v, want [0 7 8 9 10]", got)
	}
	st = b.Stats().Handlers[0]
	if st.Delivered != 5 || st.Dropped != 6 {
		t.Fatalf("final stats %+v", st)
	}
}

func TestBusPanicIsolation(t *testing.T) {
	b := NewBus(BusOptions{Logger: quietLogger()})
	var got, other []string
	b.Handle("fragile", "*", func(_ context.Context, e Event) error {
		switch e.Type {
		case "note.boom":
			panic("handler bug")
		case "note.fail":
			return errors.New("handler error")
		}
		got = append(got, e.Type)
		return nil
	})
	b.Handle("steady", "*", func(_ context.Context, e Event) error {
		other = append(other, e.Type)
		return nil
	})
	for _, typ := range []string{"note.one", "note.boom", "note.fail", "note.two"} {
		b.Publish(Event{Type: typ})
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"note.one", "note.two"}) || len(other) != 4 {
		t.Fatalf("fragile got %v, steady got %v", got, other)
	}
	st := b.Stats().Handlers[0]
	if st.Panics != 1 || st.Errors != 1 || st.Delivered != 3 {
		t.Fatalf("stats %+v, want 1 panic, 1 error, 3 delivered", st)
	}
}

func TestBusCloseDrains(t *testing.T) {
	b := NewBus(BusOptions{Logger: quietLogger()})
	var mu sync.Mutex
	n := 0
	b.Handle("slow", "*", func(context.Context, Event) error {
		time.Sleep(200 * time.Microsecond)
		mu.Lock()
		n++
		mu.Unlock()
		return nil
	})
	for i := range 100 {
		b.Publish(seqEvent("note.created", i))
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if n != 100 {
		t.Fatalf("drained %d events, want 100", n)
	}
	b.Publish(seqEvent("note.created", 101))
	if err := b.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestBusCloseTimeoutCancelsHandlers(t *testing.T) {
	b := NewBus(BusOptions{DrainTimeout: 30 * time.Millisecond, Logger: quietLogger()})
	canceled := make(chan struct{})
	b.Handle("stuck", "*", func(ctx context.Context, e Event) error {
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	b.Publish(Event{Type: "note.created"})
	err := b.Close()
	if err == nil || !strings.Contains(err.Error(), "1 handlers") {
		t.Fatalf("close error %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatalf("handler context not canceled")
	}
}

func TestBusSubscribe(t *testing.T) {
	b := NewBus(BusOptions{Logger: quietLogger()})
	ch, cancel := b.Subscribe("job.*", 2)
	all, cancelAll := b.Subscribe("*", 0)
	defer cancelAll()
	for i := range 5 {
		b.Publish(seqEvent("job.updated", i))
	}
	b.Publish(seqEvent("note.created", 99))
	st := b.Stats().Subscribers
	if len(st) != 2 || st[0].Dropped != 3 || st[0].Queued != 2 || st[1].Dropped != 0 {
		t.Fatalf("subscriber stats %+v", st)
	}
	if a, c := eventSeq(<-ch), eventSeq(<-ch); a != 3 || c != 4 {
		t.Fatalf("subscriber got %d, %d, want 3, 4", a, c)
	}
	cancel()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatalf("channel open after cancel")
	}
	if len(all) != 6 {
		t.Fatalf("catch-all subscriber holds %d events, want 6", len(all))
	}
	e := <-all
	if !core.ValidID(e.ID) || e.At.IsZero() {
		t.Fatalf("publish did not fill id and time: %+v", e)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for range all {
	}
	late, _ := b.Subscribe("*", 1)
	if _, ok := <-late; ok {
		t.Fatalf("subscription after close is open")
	}
}

func TestBusSetHandlers(t *testing.T) {
	r := NewRegistry()
	b := NewBus(BusOptions{Logger: quietLogger()})
	var mu sync.Mutex
	got := map[string]int{}
	ctxDone := make(chan struct{})
	r.Watch(func() { b.SetHandlers(r.EventHandlers()) })
	mustAdd(t, r, plugin("p1", func(r *Registry) {
		r.On("note.*", func(ctx context.Context, e Event) error {
			mu.Lock()
			got["p1"]++
			mu.Unlock()
			go func() {
				<-ctx.Done()
				select {
				case <-ctxDone:
				default:
					close(ctxDone)
				}
			}()
			return nil
		})
	}))
	mustAdd(t, r, plugin("p2", func(r *Registry) {
		r.On("*", func(context.Context, Event) error {
			mu.Lock()
			got["p2"]++
			mu.Unlock()
			return nil
		})
	}))
	b.Publish(Event{Type: "note.created"})
	waitFor(t, "both handlers", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got["p1"] == 1 && got["p2"] == 1
	})
	if err := r.Unregister("p1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctxDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("removed handler context not canceled")
	}
	if n := len(b.Stats().Handlers); n != 1 {
		t.Fatalf("%d handlers after unregister, want 1", n)
	}
	b.Publish(Event{Type: "note.updated"})
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if got["p1"] != 1 || got["p2"] != 2 {
		t.Fatalf("deliveries %v", got)
	}
}
