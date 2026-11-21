package exchangerate

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func init() {
	if logger.L == nil {
		logger.L = slog.Default()
	}
}

type mockProvider struct {
	mu      sync.Mutex
	calls   int
	venta   float64
	err     error
	failMsg string
}

func (m *mockProvider) Fetch(_ context.Context, fecha string) (*ProviderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	if m.failMsg != "" {
		return &ProviderResult{OK: false, Fecha: fecha, Error: m.failMsg}, nil
	}
	return &ProviderResult{
		OK:     true,
		Fecha:  fecha,
		Moneda: "USD",
		Venta:  m.venta,
		Compra: m.venta - 0.01,
	}, nil
}

func (m *mockProvider) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func setupExchangeRateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SaasExchangeRate{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCacheService_confirmedSingleProviderCall(t *testing.T) {
	db := setupExchangeRateTestDB(t)
	prov := &mockProvider{venta: 3.75}
	svc := NewCacheService(db, prov)
	date := "2026-06-20"

	res1, err := svc.GetExchangeRate(date)
	if err != nil || !res1.Success || res1.Venta != 3.75 {
		t.Fatalf("first: %+v err=%v", res1, err)
	}
	res2, err := svc.GetExchangeRate(date)
	if err != nil || !res2.Success {
		t.Fatalf("second: %+v err=%v", res2, err)
	}
	if prov.callCount() != 1 {
		t.Fatalf("expected 1 provider call, got %d", prov.callCount())
	}
}

func TestCacheService_fallbackUsesPreviousDay(t *testing.T) {
	db := setupExchangeRateTestDB(t)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")

	if err := db.Create(&database.SaasExchangeRate{
		RateDate: yesterday, SaleRate: 3.70, BuyRate: 3.69,
		Status: StatusConfirmed, Source: SourceApiPeru, EffectiveDate: yesterday,
	}).Error; err != nil {
		t.Fatal(err)
	}

	prov := &mockProvider{failMsg: "Has alcanzado el límite de consultas de tu plan (100/100)."}
	svc := NewCacheService(db, prov)

	res, err := svc.GetExchangeRate(today)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || !res.EsFallback || res.Venta != 3.70 {
		t.Fatalf("expected fallback 3.70, got %+v", res)
	}
	if res.Status != StatusFallback && res.Status != StatusPending {
		t.Fatalf("status: %s", res.Status)
	}
}

func TestCacheService_cooldownSkipsProvider(t *testing.T) {
	db := setupExchangeRateTestDB(t)
	today := time.Now().Format("2006-01-02")
	next := time.Now().Add(2 * time.Hour)
	if err := db.Create(&database.SaasExchangeRate{
		RateDate: today, SaleRate: 3.72, BuyRate: 3.71,
		Status: StatusPending, Source: SourceFallbackPrevious,
		EffectiveDate: time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
		NextRetryAt:   &next,
		AttemptCount:  1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	prov := &mockProvider{venta: 9.99}
	svc := NewCacheService(db, prov)
	res, err := svc.GetExchangeRate(today)
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if prov.callCount() != 0 {
		t.Fatalf("expected 0 calls during cooldown, got %d", prov.callCount())
	}
}

// Todo día debe tener su TC guardado: EnsureRecentDays completa los días sin fila (hoy y los últimos N) y no vuelve a
// consultar los que ya tienen TC.
func TestCacheService_EnsureRecentDaysFillsEveryDay(t *testing.T) {
	db := setupExchangeRateTestDB(t)
	prov := &mockProvider{venta: 3.45}
	svc := NewCacheService(db, prov)

	filled := svc.EnsureRecentDays(5) // hoy + 5 días anteriores = 6 días
	if filled != 6 {
		t.Fatalf("completó %d días, quiere 6", filled)
	}
	var n int64
	db.Model(&database.SaasExchangeRate{}).Where("sale_rate > 0").Count(&n)
	if n != 6 {
		t.Errorf("filas con TC guardado = %d, quiere 6 (uno por día)", n)
	}
	calls := prov.callCount()
	if again := svc.EnsureRecentDays(5); again != 0 {
		t.Errorf("segunda pasada no debe completar nada, completó %d", again)
	}
	if prov.callCount() != calls {
		t.Errorf("no debe volver a consultar al proveedor por días ya guardados")
	}
}

// Con el proveedor caído, los días faltantes se completan con el último TC confirmado (nunca queda un día sin TC).
func TestCacheService_EnsureRecentDaysFallsBackWhenProviderDown(t *testing.T) {
	db := setupExchangeRateTestDB(t)
	yesterday := time.Now().AddDate(0, 0, -4).Format("2006-01-02")
	db.Create(&database.SaasExchangeRate{RateDate: yesterday, SaleRate: 3.4, BuyRate: 3.39, Status: StatusConfirmed, Source: SourceApiPeru, EffectiveDate: yesterday})
	svc := NewCacheService(db, &mockProvider{err: fmt.Errorf("sin servicio")})

	svc.EnsureRecentDays(3)
	var rows []database.SaasExchangeRate
	db.Where("rate_date > ?", yesterday).Find(&rows)
	if len(rows) < 3 {
		t.Fatalf("faltan días: %d", len(rows))
	}
	for _, r := range rows {
		if r.SaleRate != 3.4 {
			t.Errorf("%s: TC %v, quiere el último confirmado 3.4", r.RateDate, r.SaleRate)
		}
	}
}
