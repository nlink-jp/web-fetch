package cache

import (
	"testing"
	"time"
)

func clock(start time.Time) (func() time.Time, func(time.Duration)) {
	now := start
	return func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }
}

func TestHitMissAndExpiry(t *testing.T) {
	now, advance := clock(time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	c := NewWithClock[string](10*time.Minute, 4, now)
	if _, _, ok := c.Get("a"); ok {
		t.Fatal("empty cache hit")
	}
	c.Put("a", "A")
	v, at, ok := c.Get("a")
	if !ok || v != "A" || !at.Equal(now()) {
		t.Errorf("hit: %q %v %v", v, at, ok)
	}
	advance(9 * time.Minute)
	if _, at2, ok := c.Get("a"); !ok || !at2.Equal(at) {
		t.Error("a hit reports the original fetch time")
	}
	advance(time.Minute)
	if _, _, ok := c.Get("a"); ok {
		t.Error("entry must expire at the TTL")
	}
	if c.Len() != 0 {
		t.Error("an expired entry is removed when touched")
	}
}

func TestLRUEvictionAndReplace(t *testing.T) {
	now, _ := clock(time.Now())
	c := NewWithClock[int](time.Hour, 2, now)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Get("a") // a is now the most recent
	c.Put("c", 3)
	if _, _, ok := c.Get("b"); ok {
		t.Error("b was least recently used and must be gone")
	}
	if _, _, ok := c.Get("a"); !ok {
		t.Error("a was touched and must survive")
	}
	c.Put("a", 10)
	if v, _, _ := c.Get("a"); v != 10 || c.Len() != 2 {
		t.Errorf("replace: %d len=%d", v, c.Len())
	}
	c.Delete("a")
	if _, _, ok := c.Get("a"); ok || c.Len() != 1 {
		t.Error("delete")
	}
	c.Delete("missing")
}

func TestZeroTTLDisables(t *testing.T) {
	c := New[string](0, 32)
	if c.Enabled() {
		t.Fatal("ttl 0 must disable")
	}
	c.Put("a", "A")
	if _, _, ok := c.Get("a"); ok || c.Len() != 0 {
		t.Error("a disabled cache keeps nothing")
	}
	c.Delete("a")
	if c2 := New[string](time.Minute, 0); c2.max != 1 {
		t.Error("max below 1 is clamped to 1")
	}
}
