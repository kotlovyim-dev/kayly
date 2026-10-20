package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time            { return c.t }
func (c *clock) advance(d time.Duration)   { c.t = c.t.Add(d) }
func limiter(c *clock, burst int) *Limiter { l := New(burst, time.Minute); l.now = c.now; return l }

func TestBurstThenWait(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	l := limiter(c, 3)
	for i := range 3 {
		if w := l.Wait("a"); w != 0 {
			t.Fatalf("attempt %d: wait %v, want 0", i+1, w)
		}
		l.Fail("a")
	}
	if w := l.Wait("a"); w != time.Minute {
		t.Fatalf("after the burst: wait %v, want 1m", w)
	}
	c.advance(20 * time.Second)
	if w := l.Wait("a"); w != 40*time.Second {
		t.Fatalf("20 s later: wait %v, want 40s", w)
	}
	c.advance(40 * time.Second)
	if w := l.Wait("a"); w != 0 {
		t.Fatalf("a minute later: wait %v, want 0", w)
	}
	l.Fail("a")
	if w := l.Wait("a"); w != time.Minute {
		t.Fatalf("one earned attempt, used: wait %v, want 1m", w)
	}
}

func TestKeysAreSeparate(t *testing.T) {
	c := &clock{t: time.Now()}
	l := limiter(c, 1)
	l.Fail("a")
	if l.Wait("a") == 0 {
		t.Fatal("a may try again at once")
	}
	if w := l.Wait("b"); w != 0 {
		t.Fatalf("b waits %v for a's failures", w)
	}
}

func TestEarnsBackNoMoreThanTheBurst(t *testing.T) {
	c := &clock{t: time.Now()}
	l := limiter(c, 2)
	l.Fail("a")
	c.advance(24 * time.Hour)
	l.Fail("a")
	l.Fail("a")
	if l.Wait("a") == 0 {
		t.Fatal("a day of quiet bought more than the burst")
	}
}

func TestForgetsKeysThatRecovered(t *testing.T) {
	c := &clock{t: time.Now()}
	l := limiter(c, 1)
	for i := range maxKeys {
		l.Fail(fmt.Sprint(i))
	}
	c.advance(time.Minute)
	l.Fail("new")
	if n := len(l.keys); n != 1 {
		t.Fatalf("keys kept = %d, want only the new one", n)
	}
}
