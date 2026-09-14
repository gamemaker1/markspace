package sdk

import (
	"testing"
	"time"
)

// TestReplayGuardOutOfOrder covers requests whose signature timestamps arrive
// out of order. An expired entry must never keep a newer entry from being
// pruned or evicted in its place, or a replay inside the window gets through.
func TestReplayGuardOutOfOrder(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	header := func(ts int64, delivery string) string {
		return Sign(secret, time.Unix(ts, 0), "POST", "/events", delivery, nil)
	}
	g := NewReplayGuard(2, DefaultWindow)
	// A newer request arrives before an older one that is still in the window.
	if err := g.Check("a", header(1000, "a"), time.Unix(1000, 0)); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := g.Check("b", header(800, "b"), time.Unix(1000, 0)); err != nil {
		t.Fatalf("b: %v", err)
	}
	// At 1150 entry b is expired and a is not. A third request must evict b.
	if err := g.Check("c", header(1150, "c"), time.Unix(1150, 0)); err != nil {
		t.Fatalf("c: %v", err)
	}
	if err := g.Check("a", header(1000, "a"), time.Unix(1150, 0)); sigCode(err) != CodeReplay {
		t.Fatalf("replay of a inside the window: got %v, want replay", err)
	}
	if err := g.Check("c", header(1150, "c"), time.Unix(1160, 0)); sigCode(err) != CodeReplay {
		t.Fatalf("replay of c: got %v, want replay", err)
	}

	// Many requests in shuffled timestamp order: every replay inside the
	// window is refused, and the guard stays within its capacity.
	big := NewReplayGuard(64, DefaultWindow)
	now := time.Unix(10_000, 0)
	order := []int64{9990, 9800, 9995, 9750, 9999, 9760, 9900, 9710}
	for i, ts := range order {
		id := string(rune('a' + i))
		if err := big.Check(id, header(ts, id), now); err != nil {
			t.Fatalf("%s at %d: %v", id, ts, err)
		}
	}
	for i, ts := range order {
		id := string(rune('a' + i))
		if err := big.Check(id, header(ts, id), now); sigCode(err) != CodeReplay {
			t.Fatalf("replay %s at %d: got %v, want replay", id, ts, err)
		}
	}
	if big.order.Len() > 64 {
		t.Fatalf("guard holds %d entries", big.order.Len())
	}
	prev := int64(-1 << 62)
	for e := big.order.Front(); e != nil; e = e.Next() {
		if ts := e.Value.(*replayEntry).t; ts < prev {
			t.Fatalf("entries out of timestamp order: %d after %d", ts, prev)
		} else {
			prev = ts
		}
	}
}
