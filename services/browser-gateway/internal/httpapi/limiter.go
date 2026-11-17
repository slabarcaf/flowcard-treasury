package httpapi

import (
	"sync"
	"time"
)

type attempt struct {
	count int
	until time.Time
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]attempt
}

// allow reserves both buckets atomically. Capacity exhaustion fails closed.
func (l *limiter) allow(username, peer string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = make(map[string]attempt)
	}
	for key, a := range l.entries {
		if !now.Before(a.until) {
			delete(l.entries, key)
		}
	}
	keys := []string{"u:" + username, "p:" + peer}
	limits := []int{5, 20}
	missing := 0
	for i, key := range keys {
		a, ok := l.entries[key]
		if !ok {
			missing++
		}
		if a.count >= limits[i] {
			return false
		}
	}
	if len(l.entries)+missing > 1000 {
		return false
	}
	for _, key := range keys {
		a, ok := l.entries[key]
		if !ok {
			a.until = now.Add(time.Minute)
		}
		a.count++
		l.entries[key] = a
	}
	return true
}
