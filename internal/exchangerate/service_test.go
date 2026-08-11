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
