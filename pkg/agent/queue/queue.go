// Package queue encola mensajes entrantes de WhatsApp en Redis para no
// bloquear el webhook, con dedup/lock/rate-limit — TODOS no-ops si no hay
// Redis (no una versión reducida: sin Redis, cada mensaje se procesa
// inline en una goroutine sin NINGUNA de estas tres protecciones). El
// canal de chat web público NUNCA pasa por esta cola (ver
// internal/agent/public_chat_handler.go) — tiene su propio rate-limit por
// sesión, más simple.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/cronlock"
	"tukifac/pkg/logger"

	"github.com/redis/go-redis/v9"
)

const (
	queueKey      = "assistant:inbound:queue"
	processingKey = "assistant:inbound:processing"
	dedupKeyPfx   = "assistant:dedup:"
	rateKeyPfx    = "assistant:rate:"

	defaultWorkers         = 2
	defaultRatePerMin      = 15
	maxInboundChars        = 4000 // distinto del límite del chat web (2000, ver public_chat_handler.go)
	dedupTTL               = 24 * time.Hour
	convLockTTL            = 100 * time.Second
	blockTimeout           = 5 * time.Second
	processTimeout         = 90 * time.Second
	requeueSleepOnLockBusy = 200 * time.Millisecond
)

// Processor procesa un mensaje ya desencolado (llama al orquestador y
// envía la respuesta por el canal correspondiente). Cableado en
// internal/agent/wiring.go.
type Processor func(ctx context.Context, in agentpkg.Inbound)

type Config struct {
	Workers    int
	RatePerMin int
}

type Queue struct {
	rdb       *redis.Client
	cfg       Config
	processor Processor

	stop   chan struct{}
	wg     sync.WaitGroup
	closed bool
	mu     sync.Mutex
}

// New crea la cola. rdb puede ser nil: en ese caso Enqueue procesa inline,
// sin ninguna de las protecciones (ver comentario del paquete).
func New(rdb *redis.Client, cfg Config, processor Processor) *Queue {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.RatePerMin <= 0 {
		cfg.RatePerMin = defaultRatePerMin
	}
	return &Queue{rdb: rdb, cfg: cfg, processor: processor, stop: make(chan struct{})}
}

// Start lanza los workers. No-op si no hay Redis (Enqueue ya procesa
// inline en ese caso, no hay nada que consumir).
func (q *Queue) Start() {
	if q.rdb == nil {
		return
	}
	for i := 0; i < q.cfg.Workers; i++ {
		q.wg.Add(1)
		go q.workerLoop()
	}
	logger.L.Info("assistant_queue_started", "workers", q.cfg.Workers)
}

func (q *Queue) Stop() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	close(q.stop)
	q.mu.Unlock()
	q.wg.Wait()
}

// Enqueue recibe un mensaje ya parseado del webhook. Trunca el texto a
// maxInboundChars EN SILENCIO (mismo comportamiento que el original: no
// se avisa al remitente). Devuelve accepted=false si el mensaje se
// descartó por rate-limit o por ser un duplicado ya visto (Meta reintenta
// webhooks) — el llamador responde 200 a Meta de todas formas.
func (q *Queue) Enqueue(ctx context.Context, in agentpkg.Inbound) (accepted bool, err error) {
	in.Text = truncateRunes(in.Text, maxInboundChars)

	if q.rdb == nil {
		// Sin Redis: inline, SIN dedup, SIN lock, SIN rate-limit.
		go q.processor(context.Background(), in)
		return true, nil
	}

	if !q.allow(ctx, in.From) {
		return false, nil
	}
	if in.ChannelMsgID != "" {
		key := dedupKeyPfx + in.ChannelMsgID
		ok, err := q.rdb.SetNX(ctx, key, "1", dedupTTL).Result()
		if err == nil && !ok {
			return false, nil // ya se vio este ChannelMsgID (reintento de Meta)
		}
	}

	raw, err := json.Marshal(in)
	if err != nil {
		return false, fmt.Errorf("queue: serializar mensaje: %w", err)
	}
	if err := q.rdb.RPush(ctx, queueKey, raw).Err(); err != nil {
		return false, fmt.Errorf("queue: encolar: %w", err)
	}
	return true, nil
}

// allow: rate-limit por remitente, best-effort — un error de Redis PERMITE
// el mensaje (no bloquea por un problema de infraestructura ajeno).
func (q *Queue) allow(ctx context.Context, from string) bool {
	key := rateKeyPfx + from
	count, err := q.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true
	}
	if count == 1 {
		q.rdb.Expire(ctx, key, time.Minute)
	}
	return int(count) <= q.cfg.RatePerMin
}

func (q *Queue) workerLoop() {
	defer q.wg.Done()
	ctx := context.Background()
	for {
		select {
		case <-q.stop:
			return
		default:
		}

		blockCtx, cancel := context.WithTimeout(ctx, blockTimeout)
		result, err := q.rdb.BRPopLPush(blockCtx, queueKey, processingKey, blockTimeout).Result()
		cancel()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			select {
			case <-q.stop:
				return
			case <-time.After(time.Second):
			}
			continue
		}

		var in agentpkg.Inbound
		if err := json.Unmarshal([]byte(result), &in); err != nil {
			logger.L.Warn("assistant_queue_decode_failed", "error", err.Error())
			q.rdb.LRem(ctx, processingKey, 1, result)
			continue
		}

		lockKey := fmt.Sprintf("assistant:conv:%d:%s:%s", in.AssistantID, in.Channel, in.From)
		release, acquired := cronlock.TryAcquire(lockKey, convLockTTL)
		if !acquired {
			// Otro worker/instancia ya está atendiendo a este contacto:
			// reencolar tras una pausa corta en vez de bloquear este
			// worker reintentando — puede reordenar mensajes bajo
			// contención, es un trade-off aceptado (igual que el original).
			time.Sleep(requeueSleepOnLockBusy)
			q.rdb.RPush(ctx, queueKey, result)
			q.rdb.LRem(ctx, processingKey, 1, result)
			continue
		}

		func() {
			defer release()
			procCtx, cancel := context.WithTimeout(ctx, processTimeout)
			defer cancel()
			q.processor(procCtx, in)
		}()

		q.rdb.LRem(ctx, processingKey, 1, result)
	}
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
