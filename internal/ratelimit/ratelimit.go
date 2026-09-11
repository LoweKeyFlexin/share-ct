// Package ratelimit is a fixed-window counter per key, in memory. Share CT is one
// process, so a process-local map is the whole story; a restart forgets the counts,
// which is fine for limits that exist to slow abuse, not to meter.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows at most limit events per key in any one window.
type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	counts  map[string]*bucket
	sweptAt time.Time
}

type bucket struct {
	start time.Time
	n     int
}

// New returns a limiter of limit events per window. now is injectable for tests.
func New(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: limit, window: window, now: now, counts: map[string]*bucket{}}
}

// Allow counts one event for key. It returns true when the event is within the limit,
// otherwise false and how long until the window opens again.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	b := l.counts[key]
	if b == nil || !now.Before(b.start.Add(l.window)) {
		l.counts[key] = &bucket{start: now, n: 1}
		return true, 0
	}
	if b.n < l.limit {
		b.n++
		return true, 0
	}
	return false, b.start.Add(l.window).Sub(now)
}

// sweep drops expired windows, at most once per window length, so idle keys do not
// accumulate for the life of the process.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.sweptAt) < l.window {
		return
	}
	l.sweptAt = now
	for k, b := range l.counts {
		if !now.Before(b.start.Add(l.window)) {
			delete(l.counts, k)
		}
	}
}

// Len is the number of keys currently tracked (for tests).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.counts)
}
