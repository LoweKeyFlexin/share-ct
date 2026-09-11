package ratelimit

import (
	"testing"
	"time"
)

func TestFixedWindow(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := New(3, time.Minute, func() time.Time { return now })

	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("event %d within the limit was refused", i+1)
		}
	}
	now = now.Add(20 * time.Second)
	ok, retry := l.Allow("a")
	if ok || retry != 40*time.Second {
		t.Fatalf("4th event: ok=%v retry=%v, want refused with 40s", ok, retry)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys must not share a window")
	}
	now = now.Add(40 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("the window must reopen once it has elapsed")
	}
}

func TestSweepDropsIdleKeys(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := New(1, time.Minute, func() time.Time { return now })
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	if l.Len() != 3 {
		t.Fatalf("tracking %d keys, want 3", l.Len())
	}
	now = now.Add(2 * time.Minute)
	l.Allow("d")
	if l.Len() != 1 {
		t.Fatalf("after a sweep %d keys remain, want only the live one", l.Len())
	}
}
