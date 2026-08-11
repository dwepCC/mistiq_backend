package exchangerate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	ajustesvc "tukifac/internal/ajustes/service"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/saas"

	"gorm.io/gorm"
)

// ExternalFetcher abstrae el proveedor apiperu.
type ExternalFetcher interface {
	Fetch(ctx context.Context, fecha string) (*ProviderResult, error)
}

// CacheService orquesta Redis, BD central, lock y proveedor externo.
type CacheService struct {
	repo     *repository
	cache    *redisCache
	lock     *distributedLock
	provider ExternalFetcher
}

var (
	defaultService     *CacheService
	defaultServiceOnce sync.Once
)

// DefaultService singleton con dependencias del proyecto.
func DefaultService() *CacheService {
	defaultServiceOnce.Do(func() {
		defaultService = NewCacheService(database.CentralDB, NewApiPeruProvider(ajustesvc.NewAjusteService()))
	})
	return defaultService
}

// NewCacheService permite inyectar dependencias en tests.
func NewCacheService(db *gorm.DB, provider ExternalFetcher) *CacheService {
	return &CacheService{
		repo:     newRepository(db),
		cache:    newRedisCache(),
		lock:     newDistributedLock(),
		provider: provider,
	}
}

// GetExchangeRate obtiene TC para fecha yyyy-mm-dd (zona Lima para "hoy").
func (s *CacheService) GetExchangeRate(fecha string) (*QueryResult, error) {
	return s.get(context.Background(), fecha, false)
}

// ForceRefresh ignora cooldown y reconsulta al proveedor (panel central / cron).
func (s *CacheService) ForceRefresh(fecha string) (*QueryResult, error) {
	fecha = strings.TrimSpace(fecha)
	if fecha == "" {
		fecha = saas.NowLima().Format("2006-01-02")
	}
	return s.get(context.Background(), fecha, true)
}

func (s *CacheService) get(ctx context.Context, fecha string, force bool) (*QueryResult, error) {
	fecha = strings.TrimSpace(fecha)
	if fecha == "" {
		return nil, fmt.Errorf("fecha requerida (formato yyyy-mm-dd)")
	}
	if _, err := time.Parse("2006-01-02", fecha); err != nil {
		return nil, fmt.Errorf("fecha inválida: use yyyy-mm-dd")
	}

	today := saas.NowLima().Format("2006-01-02")

	if res, ok := s.cache.get(ctx, fecha); ok && res != nil && res.Success {
		logger.L.Debug("exchange_rate_redis_hit", slog.String("fecha", fecha))
		return res, nil
	}

	if row, err := s.repo.findByDate(fecha); err == nil && row != nil {
		if row.Status == StatusConfirmed && row.EffectiveDate == fecha {
			out := rowToQueryResult(fecha, row)
			s.cache.set(ctx, fecha, out, cacheTTL(fecha, today))
			return out, nil
		}
		if !force && row.Status == StatusPending && row.NextRetryAt != nil && time.Now().Before(*row.NextRetryAt) {
			if out := s.buildFallbackResponse(fecha, row); out != nil {
				s.cache.set(ctx, fecha, out, 5*time.Minute)
				return out, nil
			}
		}
	}

	if s.shouldFetchExternal(fecha, today, force) {
		if out, err := s.fetchAndPersist(ctx, fecha, today, force); err != nil {
			return nil, err
		} else if out != nil {
			return out, nil
		}
	}

	if row, err := s.repo.findByDate(fecha); err == nil {
		if out := rowToQueryResult(fecha, row); out != nil && out.Success {
			s.cache.set(ctx, fecha, out, cacheTTL(fecha, today))
			return out, nil
		}
	}

	if out := s.buildFallbackFromPrevious(fecha, nil); out != nil {
		s.cache.set(ctx, fecha, out, 5*time.Minute)
		return out, nil
	}

	return &QueryResult{
		Success:      false,
		Fecha:        fecha,
		Status:       StatusUnavailable,
		ErrorMessage: "tipo de cambio no disponible",
	}, nil
}

func (s *CacheService) shouldFetchExternal(fecha, today string, force bool) bool {
	if force {
		return true
	}
	row, err := s.repo.findByDate(fecha)
	if err == nil && row != nil {
		if row.Status == StatusConfirmed && row.EffectiveDate == fecha {
			return false
		}
		if row.NextRetryAt != nil && time.Now().Before(*row.NextRetryAt) {
			return false
		}
	}
	// Histórico: solo si nunca se obtuvo confirmed.
	if fecha < today {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true
		}
		if row != nil && row.Status == StatusConfirmed {
			return false
		}
		return row == nil || row.Status != StatusConfirmed
	}
	return true
}

func (s *CacheService) fetchAndPersist(ctx context.Context, fecha, today string, force bool) (*QueryResult, error) {
	release, acquired := s.lock.tryAcquire(ctx, fecha)
	if !acquired {
		if waited := s.lock.waitForPeer(ctx, fecha, s.cache); waited != nil {
			return waited, nil
		}
		if out := s.buildFallbackFromPrevious(fecha, nil); out != nil {
			return out, nil
		}
		return nil, fmt.Errorf("tipo de cambio en proceso de actualización; intente en unos segundos")
	}
	defer release()

	// Releer cache tras lock por si otro nodo completó.
	if res, ok := s.cache.get(ctx, fecha); ok && res != nil && res.Success && !force {
		return res, nil
	}

	now := time.Now()
	attemptCount := 1
	if prev, err := s.repo.findByDate(fecha); err == nil && prev != nil {
		attemptCount = prev.AttemptCount + 1
	}

	logger.L.Info("exchange_rate_provider_fetch", slog.String("fecha", fecha), slog.Bool("force", force))

	prov, err := s.provider.Fetch(ctx, fecha)
	if err != nil {
		next := now.Add(cooldownDuration)
		_ = s.persistFailure(fecha, now, next, attemptCount, err.Error())
		if out := s.buildFallbackFromPrevious(fecha, nil); out != nil {
			return out, nil
		}
		return nil, err
	}

	if prov.OK && prov.Venta > 0 {
		effective := strings.TrimSpace(prov.Fecha)
		if effective == "" {
			effective = fecha
		}
		if err := s.repo.upsertConfirmed(fecha, prov.Venta, prov.Compra, effective, now); err != nil {
			return nil, err
		}
		out := &QueryResult{
			Success:       true,
			Fecha:         fecha,
			FechaEfectiva: effective,
			Moneda:        prov.Moneda,
			Venta:         prov.Venta,
			Compra:        prov.Compra,
			Fuente:        SourceApiPeru,
			Status:        StatusConfirmed,
			EsFallback:    effective != fecha,
		}
		if out.EsFallback {
			out.Mensaje = fmt.Sprintf(
				"SUNAT devolvió TC del %s para la solicitud del %s.",
				formatDateDMY(effective), formatDateDMY(fecha),
			)
		}
		s.cache.set(ctx, fecha, out, cacheTTL(fecha, today))
		logger.L.Info("exchange_rate_confirmed", slog.String("fecha", fecha), slog.Float64("venta", prov.Venta))
		return out, nil
	}

	errMsg := "tipo de cambio no disponible para la fecha indicada"
	if prov != nil && prov.Error != "" {
		errMsg = prov.Error
	}
	next := now.Add(cooldownDuration)
	_ = s.persistFailure(fecha, now, next, attemptCount, errMsg)
	if out := s.buildFallbackFromPrevious(fecha, nil); out != nil {
		return out, nil
	}
	return &QueryResult{
		Success:      false,
		Fecha:        fecha,
		Status:       StatusUnavailable,
		ErrorMessage: errMsg,
	}, nil
}

func (s *CacheService) persistFailure(fecha string, attemptAt, nextRetry time.Time, attemptCount int, errMsg string) error {
	prev, _ := s.repo.findLastConfirmedBefore(fecha)
	in := pendingFallbackInput{
		RateDate:      fecha,
		Status:        StatusPending,
		Source:        SourceApiPeru,
		EffectiveDate: fecha,
		AttemptAt:     attemptAt,
		NextRetryAt:   &nextRetry,
		AttemptCount:  attemptCount,
		ErrorMessage:  errMsg,
	}
	if prev != nil {
		in.SaleRate = prev.SaleRate
		in.BuyRate = prev.BuyRate
		in.EffectiveDate = prev.EffectiveDate
		in.Status = StatusFallback
		in.Source = SourceFallbackPrevious
	} else {
		in.Status = StatusUnavailable
	}
	return s.repo.upsertPendingFallback(in)
}

func (s *CacheService) buildFallbackResponse(fecha string, row *database.SaasExchangeRate) *QueryResult {
	if row != nil && row.SaleRate > 0 {
		return rowToQueryResult(fecha, row)
	}
	return s.buildFallbackFromPrevious(fecha, row)
}

func (s *CacheService) buildFallbackFromPrevious(fecha string, current *database.SaasExchangeRate) *QueryResult {
	if current != nil && current.SaleRate > 0 {
		out := rowToQueryResult(fecha, current)
		if out != nil && out.Success {
			return out
		}
	}
	prev, err := s.repo.findLastConfirmedBefore(fecha)
	if err != nil || prev == nil || prev.SaleRate <= 0 {
		return nil
	}
	now := time.Now()
	next := now.Add(cooldownDuration)
	attemptCount := 1
	if current != nil {
		attemptCount = current.AttemptCount
	}
	_ = s.repo.upsertPendingFallback(pendingFallbackInput{
		RateDate:      fecha,
		SaleRate:      prev.SaleRate,
		BuyRate:       prev.BuyRate,
		Status:        StatusFallback,
		Source:        SourceFallbackPrevious,
		EffectiveDate: prev.RateDate,
		AttemptAt:     now,
		NextRetryAt:   &next,
		AttemptCount:  attemptCount,
		ErrorMessage:  "",
	})
	out := &QueryResult{
		Success:       true,
		Fecha:         fecha,
		FechaEfectiva: prev.RateDate,
		Moneda:        "USD",
		Venta:         prev.SaleRate,
		Compra:        prev.BuyRate,
		Fuente:        SourceCacheCentral,
		Status:        StatusFallback,
		EsFallback:    true,
		Mensaje: fmt.Sprintf(
			"Se utiliza el tipo de cambio del %s porque SUNAT aún no publica el correspondiente al %s.",
			formatDateDMY(prev.RateDate), formatDateDMY(fecha),
		),
	}
	retryAtStr := next.Format(time.RFC3339)
	out.ProximoReintento = &retryAtStr
	return out
}

// GetTodayStatus para panel central.
func (s *CacheService) GetTodayStatus() (*QueryResult, *database.SaasExchangeRate, error) {
	today := saas.NowLima().Format("2006-01-02")
	res, err := s.GetExchangeRate(today)
	row, _ := s.repo.findByDate(today)
	return res, row, err
}
