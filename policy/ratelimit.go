package policy

import (
	"container/list"
	"sync"
	"time"
)

// DefaultMaxRateKeys bounds the number of distinct keys a limiter tracks; the
// least-recently-used key is evicted beyond it. This caps memory under attack.
const DefaultMaxRateKeys = 100_000

// slidingLimiter is an approximate sliding-window-counter limiter: it keeps the
// current and previous fixed windows per key and weights the previous window by
// how much of it still overlaps the sliding window. O(1) memory per key.
type slidingLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	max    int
	m      map[string]*swEntry
	lru    *list.List // front = most recently used
}

type swEntry struct {
	key         string
	windowStart time.Time
	prev, cur   int
	elem        *list.Element
}

func newSlidingLimiter(limit, windowSec, max int) *slidingLimiter {
	if max <= 0 {
		max = DefaultMaxRateKeys
	}
	return &slidingLimiter{
		limit:  limit,
		window: time.Duration(windowSec) * time.Second,
		max:    max,
		m:      make(map[string]*swEntry),
		lru:    list.New(),
	}
}

// Allow reports whether a request on key is within the limit at time now. When
// denied it returns the suggested Retry-After.
func (l *slidingLimiter) Allow(key string, now time.Time) (ok bool, retryAfter time.Duration) {
	if l.limit <= 0 || l.window <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	e := l.m[key]
	if e == nil {
		e = &swEntry{key: key, windowStart: now}
		e.elem = l.lru.PushFront(e)
		l.m[key] = e
		l.evict()
	} else {
		l.lru.MoveToFront(e.elem)
	}

	l.roll(e, now)

	elapsed := now.Sub(e.windowStart)
	weight := float64(l.window-elapsed) / float64(l.window)
	if weight < 0 {
		weight = 0
	}
	estimate := float64(e.prev)*weight + float64(e.cur)
	if estimate+1 > float64(l.limit) {
		// Retry after the sliding window has moved enough; approximate as the
		// time left in the current window.
		return false, l.window - elapsed
	}
	e.cur++
	return true, 0
}

// roll advances the window(s) so that windowStart is within one window of now.
func (l *slidingLimiter) roll(e *swEntry, now time.Time) {
	elapsed := now.Sub(e.windowStart)
	switch {
	case elapsed < l.window:
		return
	case elapsed < 2*l.window:
		e.prev, e.cur = e.cur, 0
		e.windowStart = e.windowStart.Add(l.window)
	default:
		e.prev, e.cur = 0, 0
		e.windowStart = now
	}
}

func (l *slidingLimiter) evict() {
	for len(l.m) > l.max {
		back := l.lru.Back()
		if back == nil {
			return
		}
		ev := back.Value.(*swEntry)
		l.lru.Remove(back)
		delete(l.m, ev.key)
	}
}
