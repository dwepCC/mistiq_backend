package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupMaxMontoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{},
		&database.TenantDocumentSeries{},
		&database.TenantContact{},
		&database.TenantSale{},
		&database.TenantSaleItem{},
		&database.TenantSalePayment{},
		&database.TenantCashSession{},
		&database.TenantPaymentMethod{},
		&database.TenantProduct{},
		&database.TenantBranch{},
		&database.TenantInventoryOperationType{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: true, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
	}
	// Registrar una venta exige método de pago y caja abierta del usuario.
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashSession{
		BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// seedClienteSinRUC crea el típico «Clientes Varios»: doc. tipo 0 (sin RUC).
func seedClienteSinRUC(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	c := database.TenantContact{BusinessName: "Clientes Varios", DocType: "0", DocNumber: "99999999"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func seedSeries(t *testing.T, db *gorm.DB, docType, sunatCode, series string) uint {
	t.Helper()
	s := database.TenantDocumentSeries{
		BranchID: 1, DocType: docType, SunatCode: sunatCode, Series: series, Correlative: 1, Active: true,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func maxMontoSaleInput(contactID, seriesID uint, docType string, total float64) CreateSaleInput {
	return CreateSaleInput{
		BranchID:  1,
		UserID:    1,
		ContactID: &contactID,
		SeriesID:  seriesID,
		DocType:   docType,
		IssueDate: time.Now(),
		Currency:  "PEN",
		TaxConfig: tax.DefaultConfig(),
		Payments:  []PaymentInput{{Method: "cash", Amount: total}},
		Items: []SaleItemInput{{
			Description:        "Servicio",
			Unit:               "NIU",
			Quantity:           1,
			UnitPrice:          total,
			IgvAffectationType: "10",
			PriceIncludesIgv:   true,
		}},
	}
}

// TestNotaVentaSinRUC_NoAplicaTopeDe700: el tope de S/ 700 con cliente sin RUC es una regla
// SUNAT para la boleta. La nota de venta (00) es un documento interno que no se declara,
// así que no debe heredarlo.
func TestNotaVentaSinRUC_NoAplicaTopeDe700(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteSinRUC(t, db)
	seriesID := seedSeries(t, db, "Nota de Venta", "00", "NV01")

	sale, err := svc.Create(maxMontoSaleInput(contactID, seriesID, "00", 1500))
	if err != nil {
		t.Fatalf("una nota de venta de S/ 1500 a Clientes Varios debe permitirse: %v", err)
	}
	if sale == nil || sale.ID == 0 {
		t.Fatal("esperaba la venta creada")
	}
}

// TestBoletaSinRUC_SigueConTopeDe700: la boleta sí se declara, así que conserva el tope.
func TestBoletaSinRUC_SigueConTopeDe700(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteSinRUC(t, db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")

	_, err := svc.Create(maxMontoSaleInput(contactID, seriesID, "03", 1500))
	if err == nil {
		t.Fatal("una boleta de S/ 1500 a un cliente sin RUC debe rechazarse (regla SUNAT)")
	}
	if !strings.Contains(err.Error(), "700") {
		t.Errorf("esperaba el error del tope de 700, got %v", err)
	}
	// El mensaje ya no debe mencionar la nota de venta: la regla no le aplica.
	if strings.Contains(strings.ToLower(err.Error()), "nota de venta") {
		t.Errorf("el mensaje no debe mencionar la nota de venta: %v", err)
	}
}

// TestBoletaSinRUC_BajoElTopePasa: la regla es un tope, no un bloqueo por tipo de cliente.
func TestBoletaSinRUC_BajoElTopePasa(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteSinRUC(t, db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")

	if _, err := svc.Create(maxMontoSaleInput(contactID, seriesID, "03", 650)); err != nil {
		t.Fatalf("una boleta de S/ 650 a Clientes Varios debe permitirse: %v", err)
	}
}
