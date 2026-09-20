package agent

import (
	"sync"
	"time"
)

// sessionRateLimiter es el mismo tipo de backstop en memoria/por proceso
// que pkg/agent/policies.RateLimit, pero a nivel de CANAL (mensajes por
// sesión de chat web), no de acción. El canal WhatsApp usa su propio
// limitador respaldado en Redis (Fase 4, pkg/agent/queue) — el chat web
// público nunca pasa por esa cola (ver
// docs/CHATBOT-AGENT-ARCHITECTURE.md §2.9), así que necesita el suyo.
type sessionRateLimiter struct {
	max    int
	window time.Duration

	mu     sync.Mutex
	events map[string][]time.Time
}

func newSessionRateLimiter(max int, window time.Duration) *sessionRateLimiter {
	return &sessionRateLimiter{max: max, window: window, events: make(map[string][]time.Time)}
}

func (l *sessionRateLimiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	times := l.events[key]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.events[key] = kept
		return false
	}
	kept = append(kept, now)
	l.events[key] = kept
	return true
}
