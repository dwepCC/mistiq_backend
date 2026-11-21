package billingqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"tukifac/pkg/branchscope"

	"tukifac/config"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/metrics"

	"github.com/redis/go-redis/v9"
)

const (
	QueueKey           = "tukifac:billing:queue"
	ProcessingListKey  = "tukifac:billing:processing"
	DeadLetterKey      = "tukifac:billing:dead"
	ClaimKeyPrefix     = "tukifac:billing:claim:"
	ProcessingLockPref = "tukifac:billing:lock:"
)

// JobStatus estados del job de emisión.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusSent       = "sent"
	StatusFailed     = "failed"
	StatusRetrying   = "retrying"
	StatusDeadLetter = "dead_letter"
)

// Job payload en cola Redis.
type Job struct {
	TenantDB       string `json:"tenant_db"`
	TenantID       uint   `json:"tenant_id"`
	TenantSlug     string `json:"tenant_slug"`
	SaleID         uint   `json:"sale_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Attempt        int    `json:"attempt"`
}

// Processor procesa un job (registrado por internal/billing/worker).
type Processor func(job Job) error

var (
	rdb       *redis.Client
	processor Processor
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	workers   int
)

// Start registra el procesador e inicia workers.
func Start(cfg *config.Config, client *redis.Client, proc Processor) {
	rdb = client
	processor = proc
	if !cfg.BillingAsyncEnabled || rdb == nil || processor == nil {
		return
	}
	recoverStuckProcessing()
	workers = cfg.BillingQueueWorkers
	if workers < 1 {
		workers = 1
	}
	stopCh = make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go workerLoop(i)
	}
}

// Stop detiene workers.
func Stop() {
	if stopCh == nil {
		return
	}
	stopOnce.Do(func() {
		close(stopCh)
	})
	wg.Wait()
}

// TryClaimEnqueue evita doble encolado concurrente (SETNX distribuido).
func TryClaimEnqueue(idempotencyKey string) (bool, error) {
	if rdb == nil {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := ClaimKeyPrefix + idempotencyKey
	ok, err := rdb.SetNX(ctx, key, "1", 15*time.Minute).Result()
	return ok, err
}

// ReleaseClaim libera el lock de encolado si no se completó el enqueue.
func ReleaseClaim(idempotencyKey string) {
	if rdb == nil || idempotencyKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = rdb.Del(ctx, ClaimKeyPrefix+idempotencyKey).Err()
}

// Enqueue encola emisión SUNAT.
func Enqueue(job Job) error {
	if rdb == nil {
		return errors.New("billing queue: redis not available")
	}
	if job.IdempotencyKey == "" {
		job.IdempotencyKey = fmt.Sprintf("%s:%d", job.TenantDB, job.SaleID)
	}
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.LPush(ctx, QueueKey, b).Err(); err != nil {
		return err
	}
	metrics.BillingQueueEnqueued.Add(1)
	return nil
}

// Enabled indica si la cola async está activa.
func Enabled() bool {
	return rdb != nil && config.AppConfig != nil && config.AppConfig.BillingAsyncEnabled
}

// QueueDepth tamaño aproximado de la cola (métricas).
func QueueDepth() int64 {
	if rdb == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n, err := rdb.LLen(ctx, QueueKey).Result()
	if err != nil {
		return 0
	}
	return n
}

func recoverStuckProcessing() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		payload, err := rdb.RPop(ctx, ProcessingListKey).Result()
		if err == redis.Nil {
			break
		}
		if err != nil {
			logger.L.Warn("billing_recover_processing_failed", slog.Any("error", err))
			break
		}
		if payload == "" {
			continue
		}
		_ = rdb.LPush(ctx, QueueKey, payload).Err()
		logger.L.Info("billing_job_recovered_from_processing")
	}
}

func workerLoop(id int) {
	defer wg.Done()
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		payload, err := rdb.BRPopLPush(ctx, QueueKey, ProcessingListKey, 2*time.Second).Result()
		cancel()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			time.Sleep(time.Second)
			continue
		}
		if payload == "" {
			continue
		}

		var job Job
		if err := json.Unmarshal([]byte(payload), &job); err != nil {
			logger.L.Error("billing_job_unmarshal", slog.Any("error", err))
			pushInvalidPayload(payload, err)
			ackProcessing(payload)
			continue
		}
		processOne(job, payload)
	}
}

func processOne(job Job, rawPayload string) {
	if processor == nil {
		ackProcessing(rawPayload)
		return
	}

	lockKey := ProcessingLockPref + job.IdempotencyKey
	ctx, cancel := context.WithTimeout(context.Background(), config.AppConfig.DBBillingTimeout+15*time.Second)
	defer cancel()

	ok, err := rdb.SetNX(ctx, lockKey, "1", config.AppConfig.DBBillingTimeout).Result()
	if err != nil || !ok {
		// Otro worker procesando el mismo idempotency: devolver a cola principal.
		ackProcessing(rawPayload)
		requeuePayload(rawPayload)
		return
	}
	defer func() { _ = rdb.Del(context.Background(), lockKey).Err() }()

	err = processor(job)
	if err != nil {
		job.Attempt++
		if job.Attempt >= config.AppConfig.BillingMaxRetries {
			markJobDeadLetter(job, err)
			pushDeadLetter(job, err)
			metrics.BillingQueueDeadLetter.Add(1)
			ackProcessing(rawPayload)
			metrics.BillingQueueFailed.Add(1)
			return
		}
		scheduleRetry(job, rawPayload)
		metrics.BillingQueueFailed.Add(1)
		return
	}
	ackProcessing(rawPayload)
	metrics.BillingQueueProcessed.Add(1)
}

func ackProcessing(rawPayload string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = rdb.LRem(ctx, ProcessingListKey, 1, rawPayload).Err()
}

func requeuePayload(rawPayload string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = rdb.LPush(ctx, QueueKey, rawPayload).Err()
}

func requeue(job Job) {
	b, _ := json.Marshal(job)
	requeuePayload(string(b))
}

func scheduleRetry(job Job, rawPayload string) {
	ackProcessing(rawPayload)
	delay := config.AppConfig.BillingRetryBaseDelay << job.Attempt
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	time.AfterFunc(delay, func() { requeue(job) })
}

func pushDeadLetter(job Job, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, _ := json.Marshal(map[string]interface{}{
		"job":   job,
		"error": err.Error(),
		"at":    time.Now().Unix(),
	})
	_ = rdb.LPush(ctx, DeadLetterKey, b).Err()
}

func pushInvalidPayload(raw string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, _ := json.Marshal(map[string]interface{}{
		"payload": raw,
		"error":   err.Error(),
		"at":      time.Now().Unix(),
	})
	_ = rdb.LPush(ctx, DeadLetterKey, b).Err()
}

func markJobDeadLetter(job Job, err error) {
	db, e := database.GetTenantDB(job.TenantDB)
	if e != nil {
		return
	}
	defer database.ReleaseTenantDB(job.TenantDB)
	db = branchscope.System(db, "billingqueue: marcar job como dead-letter")
	_ = db.Model(&database.TenantInvoice{}).Where("sale_id = ?", job.SaleID).Updates(map[string]interface{}{
		"job_status":      StatusDeadLetter,
		"pipeline_status": billingstate.DEAD_LETTER,
		"job_last_error":  err.Error(),
	}).Error
}
