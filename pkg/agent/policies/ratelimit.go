package policies

import (
	"context"
	"sync"
	"time"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
)

const (
	defaultMaxPerWindow = 120
	defaultWindow       = time.Hour
)

// RateLimit es un backstop de contención EN MEMORIA, POR PROCESO — no es
// exacto en despliegues con múltiples réplicas del backend (el tope
// efectivo se multiplica por instancia). Limita ejecuciones de acciones
// por conversación, no mensajes.
type RateLimit struct {
	max    int
	window time.Duration

	mu     sync.Mutex
	counts map[uint][]time.Time
}

var _ Policy = (*RateLimit)(nil)

// NewRateLimit: max<=0 o window<=0 usan los defaults (120/hora).
func NewRateLimit(max int, window time.Duration) *RateLimit {
	if max <= 0 {
		max = defaultMaxPerWindow
	}
	if window <= 0 {
		window = defaultWindow
	}
	return &RateLimit{max: max, window: window, counts: make(map[uint][]time.Time)}
}

func (r *RateLimit) Check(ctx context.Context, bc bizctx.Context, action actions.Action, args []byte) error {
	now := time.Now()
	cutoff := now.Add(-r.window)

	r.mu.Lock()
	defer r.mu.Unlock()

	times := r.counts[bc.ConversationID]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.max {
		r.counts[bc.ConversationID] = kept
		return Denied("rate_limit", "se alcanzó el límite de acciones por hora para esta conversación")
	}
	kept = append(kept, now)
	r.counts[bc.ConversationID] = kept
	return nil
}
