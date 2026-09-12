// Package cache is the MCP server's in-process, time-bounded LRU: it holds a
// fetched document long enough for the caller to page through it with offset
// without refetching. It never touches disk, keeps no errors, and is disabled
// entirely when the TTL is zero (RFP §3 cache contract).
package cache

import (
	"container/list"
	"sync"
	"time"
)

// Cache maps string keys to values of one type.
type Cache[V any] struct {
	ttl time.Duration
	max int
	now func() time.Time

	mu    sync.Mutex
	items map[string]*list.Element
	order *list.List // front = most recently used
}

type entry[V any] struct {
	key   string
	value V
	at    time.Time
}

// New builds a cache. ttl <= 0 disables it: Put is a no-op and Get always
// misses. max is the entry cap; the least recently used entry is evicted past
// it.
func New[V any](ttl time.Duration, max int) *Cache[V] {
	return NewWithClock[V](ttl, max, time.Now)
}

// NewWithClock is New with an injected clock, for tests.
func NewWithClock[V any](ttl time.Duration, max int, now func() time.Time) *Cache[V] {
	if max < 1 {
		max = 1
	}
	return &Cache[V]{ttl: ttl, max: max, now: now, items: map[string]*list.Element{}, order: list.New()}
}

// Enabled reports whether the cache keeps anything.
func (c *Cache[V]) Enabled() bool { return c.ttl > 0 }

// Get returns the value for key if it is present and not expired, and marks
// it most recently used. An expired entry is removed on the way.
func (c *Cache[V]) Get(key string) (V, time.Time, bool) {
	var zero V
	if !c.Enabled() {
		return zero, time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return zero, time.Time{}, false
	}
	e := el.Value.(*entry[V])
	if c.now().Sub(e.at) >= c.ttl {
		c.order.Remove(el)
		delete(c.items, key)
		return zero, time.Time{}, false
	}
	c.order.MoveToFront(el)
	return e.value, e.at, true
}

// Put stores value under key, stamped now, evicting the least recently used
// entry when the cap is passed.
func (c *Cache[V]) Put(key string, value V) {
	if !c.Enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value = &entry[V]{key: key, value: value, at: c.now()}
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&entry[V]{key: key, value: value, at: c.now()})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*entry[V]).key)
	}
}

// Delete forgets key (the `fresh` argument).
func (c *Cache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.Remove(el)
		delete(c.items, key)
	}
}

// Len is the number of entries held, expired ones included until touched.
func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
