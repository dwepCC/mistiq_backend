package service

import (
	"testing"

	"tukifac/pkg/database"
)

func boolPtr(b bool) *bool          { return &b }
func floatPtr(f float64) *float64   { return &f }

// El recargo al consumo viene apagado; el tenant lo activa y fija el % (tope legal 13 %); un % inválido se rechaza sin tocar nada.
func TestCompanyService_ServiceCharge(t *testing.T) {
	db := setupCompanyConfigTestDB(t)
	if err := db.Create(&database.TenantCompanyConfig{BusinessName: "ACME", RUC: "20123456789"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewCompanyService(db)

	cfg, _ := svc.GetConfig()
	if cfg.ServiceChargeEnabled || cfg.ServiceChargeRate != 10 {
		t.Fatalf("por defecto apagado y 10%%: %+v", cfg)
	}
	if err := svc.ApplyConfigPatch(CompanyConfigPatch{ServiceChargeEnabled: boolPtr(true), ServiceChargeRate: floatPtr(12.5)}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = svc.GetConfig(); !cfg.ServiceChargeEnabled || cfg.ServiceChargeRate != 12.5 {
		t.Fatalf("tras activar: %+v", cfg)
	}
	for _, bad := range []float64{0, -1, 13.01, 20} {
		if err := svc.ApplyConfigPatch(CompanyConfigPatch{ServiceChargeEnabled: boolPtr(false), ServiceChargeRate: floatPtr(bad)}); err == nil {
			t.Errorf("tasa %v debió rechazarse", bad)
		}
	}
	if cfg, _ = svc.GetConfig(); !cfg.ServiceChargeEnabled || cfg.ServiceChargeRate != 12.5 {
		t.Fatalf("un parche inválido no debe cambiar nada: %+v", cfg)
	}
	// Desactivar sin tocar el %.
	if err := svc.ApplyConfigPatch(CompanyConfigPatch{ServiceChargeEnabled: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = svc.GetConfig(); cfg.ServiceChargeEnabled || cfg.ServiceChargeRate != 12.5 {
		t.Fatalf("tras desactivar: %+v", cfg)
	}
}
