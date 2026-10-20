// Package ratelimit slows down guessing: a key (a client address) that fails too often has to
// wait before it may try again.
package ratelimit

import (
	"sync"
	"time"
)

// maxKeys bounds memory: past it, keys that have earned back every attempt are forgotten.
const maxKeys = 10_000

// Limiter allows each key a burst of failures, then one more for every interval that passes.
// The zero value is not usable; call New.
type Limiter struct {
	burst float64
	every time.Duration
	now   func() time.Time

	mu   sync.Mutex
	keys map[string]*bucket
}

type bucket struct {
	left float64 // attempts left
	at   time.Time
}

// New returns a Limiter that lets a key fail burst times in a row and earns back one attempt
// every interval.
func New(burst int, every time.Duration) *Limiter {
	return &Limiter{burst: float64(burst), every: every, now: time.Now, keys: map[string]*bucket{}}
}

// refill brings b up to date and reports whether it is full. Callers hold l.mu.
func (l *Limiter) refill(b *bucket, now time.Time) bool {
	b.left = min(l.burst, b.left+float64(now.Sub(b.at))/float64(l.every))
	b.at = now
	return b.left >= l.burst
}

// Wait returns how long key has to wait before its next attempt; zero means it may try now.
func (l *Limiter) Wait(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.keys[key]
	if !ok {
		return 0
	}
	l.refill(b, l.now())
	if b.left >= 1 {
		return 0
	}
	return time.Duration((1 - b.left) * float64(l.every))
}

// Fail records a failed attempt by key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.keys[key]
	if !ok {
		if len(l.keys) >= maxKeys {
			l.forgetFull(now)
		}
		b = &bucket{left: l.burst, at: now}
		l.keys[key] = b
	}
	l.refill(b, now)
	b.left = max(0, b.left-1)
}

// forgetFull drops the keys that may fail a whole burst again. Callers hold l.mu.
func (l *Limiter) forgetFull(now time.Time) {
	for k, b := range l.keys {
		if l.refill(b, now) {
			delete(l.keys, k)
		}
	}
}
