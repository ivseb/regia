package auth

import (
	"sync"
	"time"
)

// limiter conta i tentativi falliti per chiave (email|ip) in una finestra fissa.
// È in memoria: con più istanze il limite vale per istanza, che basta contro
// gli attacchi a forza bruta su un singolo account.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*bucket
	last   time.Time
}

type bucket struct {
	n     int
	until time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string]*bucket{}}
}

// allow registra un tentativo e dice se è ammesso.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.last) > l.window {
		for k, b := range l.hits { // pulizia periodica, niente crescita illimitata
			if now.After(b.until) {
				delete(l.hits, k)
			}
		}
		l.last = now
	}
	b := l.hits[key]
	if b == nil || now.After(b.until) {
		b = &bucket{until: now.Add(l.window)}
		l.hits[key] = b
	}
	b.n++
	return b.n <= l.max
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
