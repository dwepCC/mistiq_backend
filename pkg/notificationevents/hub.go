// Package notificationevents entrega en vivo (SSE) de TenantNotification al panel — Contrato
// ecommerce v2 §1.7, Fase 6. Mismo patrón exacto que pkg/billingevents (hub in-memory + Redis
// pub/sub opcional por tenant), pero infraestructura INDEPENDIENTE a propósito: billing y
// notificaciones son dominios de eventos distintos y no deben compartir canal/hub — confirmado en
// el diseño de Fase 6 ("no mezclar billing y ecommerce en el mismo hub").
package notificationevents

import (
	"context"
	"sync"

	"github.com/redis/go-redis/v9"
)

var globalHub *Hub

// Hub fan-out in-memory por instancia; una suscripción Redis por tenant activo.
type Hub struct {
	rdb *redis.Client

	mu      sync.Mutex
	tenants map[uint]*tenantFanout
}

type tenantFanout struct {
	refCount int
	clients  map[uint64]chan []byte
	nextID   uint64
	cancel   context.CancelFunc
}

// Init configura hub global (opcional si Redis no está).
func Init(rdb *redis.Client) {
	if rdb == nil {
		globalHub = &Hub{tenants: map[uint]*tenantFanout{}}
		return
	}
	globalHub = &Hub{rdb: rdb, tenants: map[uint]*tenantFanout{}}
}

// Shutdown cancela suscripciones Redis activas.
func Shutdown() {
	if globalHub == nil {
		return
	}
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()
	for _, tf := range globalHub.tenants {
		if tf.cancel != nil {
			tf.cancel()
		}
		for _, ch := range tf.clients {
			close(ch)
		}
	}
	globalHub.tenants = map[uint]*tenantFanout{}
}

// Subscribe registra cliente SSE local; retorna canal y función cleanup.
func Subscribe(tenantID uint) (<-chan []byte, func()) {
	if globalHub == nil {
		ch := make(chan []byte)
		close(ch)
		return ch, func() {}
	}
	return globalHub.subscribe(tenantID)
}

func (h *Hub) subscribe(tenantID uint) (<-chan []byte, func()) {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	tf, ok := h.tenants[tenantID]
	if !ok {
		tf = &tenantFanout{clients: map[uint64]chan []byte{}}
		h.tenants[tenantID] = tf
	}
	id := tf.nextID
	tf.nextID++
	tf.clients[id] = ch
	tf.refCount++
	startSub := tf.refCount == 1 && h.rdb != nil
	if startSub {
		ctx, cancel := context.WithCancel(context.Background())
		tf.cancel = cancel
		go h.redisLoop(ctx, tenantID, tf)
	}
	h.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			tf, ok := h.tenants[tenantID]
			if !ok {
				return
			}
			delete(tf.clients, id)
			close(ch)
			tf.refCount--
			if tf.refCount <= 0 {
				if tf.cancel != nil {
					tf.cancel()
				}
				delete(h.tenants, tenantID)
			}
		})
	}
	return ch, unsub
}

func (h *Hub) redisLoop(ctx context.Context, tenantID uint, tf *tenantFanout) {
	sub := h.rdb.Subscribe(ctx, tenantChannel(tenantID))
	defer sub.Close()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			payload := []byte(msg.Payload)
			h.mu.Lock()
			for _, clientCh := range tf.clients {
				select {
				case clientCh <- payload:
				default:
					// cliente lento: drop no bloqueante
				}
			}
			h.mu.Unlock()
		}
	}
}

// ActiveSubscriptions métrica debug (tests).
func ActiveSubscriptions() int {
	if globalHub == nil {
		return 0
	}
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()
	n := 0
	for _, tf := range globalHub.tenants {
		n += tf.refCount
	}
	return n
}
