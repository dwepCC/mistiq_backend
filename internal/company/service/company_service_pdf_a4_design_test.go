package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCompanyConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func strPtr(s string) *string { return &s }

// Un tenant sin elección usa 'modern'; elegir 'classic' se guarda; un valor inválido vuelve al
// diseño por defecto; un parche sin el campo no lo toca.
func TestCompanyService_PdfA4Design(t *testing.T) {
	db := setupCompanyConfigTestDB(t)
	if err := db.Create(&database.TenantCompanyConfig{BusinessName: "ACME", RUC: "20123456789"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewCompanyService(db)

	cfg, err := svc.GetConfig()
	if err != nil || cfg.PdfA4Design != "modern" {
		t.Fatalf("default: %v / %q", err, cfg.PdfA4Design)
	}

	if err := svc.ApplyConfigPatch(CompanyConfigPatch{PdfA4Design: strPtr(" Classic ")}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = svc.GetConfig(); cfg.PdfA4Design != "classic" {
		t.Fatalf("tras elegir classic: %q", cfg.PdfA4Design)
	}

	if err := svc.ApplyConfigPatch(CompanyConfigPatch{TradeName: strPtr("Otro nombre")}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = svc.GetConfig(); cfg.PdfA4Design != "classic" {
		t.Fatalf("un parche sin el campo lo cambió: %q", cfg.PdfA4Design)
	}

	if err := svc.ApplyConfigPatch(CompanyConfigPatch{PdfA4Design: strPtr("raro; drop")}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = svc.GetConfig(); cfg.PdfA4Design != "modern" {
		t.Fatalf("valor inválido debía volver a modern: %q", cfg.PdfA4Design)
	}
}
