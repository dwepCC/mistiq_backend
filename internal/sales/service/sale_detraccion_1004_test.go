package service

import (
	"strings"
	"testing"
	"time"

	detraccionsvc "tukifac/internal/detraccion"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// Fase 2 (1004): flujo completo UI→backend, ejercitando SaleService.Create de punta a punta —
// no solo el paquete internal/detraccion aislado. Reutiliza setupMaxMontoTestDB (de
// sale_max_monto_sin_ruc_test.go) y agrega tenant_sale_detraccion + la cuenta BN de la empresa.

func setup1004TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupMaxMontoTestDB(t)
	if err := db.AutoMigrate(&database.TenantSaleDetraccion{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.TenantCompanyConfig{}).Where("id = ?", 1).
		Update("detraction_bn_account", "0004-1234567890").Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// transporte1004Input: caso oficial de SUNAT (Lima-Casma, S/2,000 incluido IGV → detracción
// S/80), un solo ítem "Servicio de transporte de carga" gravado con IGV.
func transporte1004Input(contactID, seriesID uint) CreateSaleInput {
	return CreateSaleInput{
		BranchID: 1, UserID: 1, ContactID: &contactID, SeriesID: seriesID, DocType: "01",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		OperationTypeCode: "1004",
		Payments:          []PaymentInput{{Method: "cash", Amount: 1920}}, // 2000 - 80 detracción
		Items: []SaleItemInput{{
			Description: "Servicio de transporte de carga Lima-Casma", Unit: "ZZ", Quantity: 1,
			UnitPrice: 2000, IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
		Detraccion: &detraccionsvc.SaleInput{
			GoodCode: "027", ValorReferencialPen: 1232.28, MtcRegistro: "15X123CNG",
			ConfiguracionVehicular: "C3", PuntoOrigen: "AV. INDUSTRIAL 578 - LIMA",
			PuntoDestino: "JR. BUENAVISTA 234 - CASMA", CargaEfectivaTm: 12, CargaUtilTm: 15,
		},
	}
}

func seedContactConRUC(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	c := database.TenantContact{BusinessName: "TRANSPORTE SIEMPRE RAPIDO S.A.C.", DocType: "6", DocNumber: "20123456789"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func TestCreate1004_FlujoCompleto(t *testing.T) {
	db := setup1004TestDB(t)
	svc := NewSaleService(db)
	contactID := seedContactConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	sale, err := svc.Create(transporte1004Input(contactID, seriesID))
	if err != nil {
		t.Fatalf("Create 1004: %v", err)
	}
	if sale.OperationTypeCode != "1004" {
		t.Errorf("operation_type_code = %q, want 1004", sale.OperationTypeCode)
	}

	det, err := detraccionsvc.NewService(db).LoadBySaleID(sale.ID)
	if err != nil || det == nil {
		t.Fatalf("no se persistió tenant_sale_detraccion: %v", err)
	}
	if det.DetractionAmountPen != 80 {
		t.Errorf("detraction_amount_pen = %v, want 80", det.DetractionAmountPen)
	}
	if det.MtcRegistro != "15X123CNG" {
		t.Errorf("mtc_registro no persistido: %q", det.MtcRegistro)
	}
}

// Sin los campos de transporte, la venta completa debe fallar (no solo Evaluate/Persist
// aisladamente) — el usuario no debe poder registrar la venta y enterarse después.
func TestCreate1004_RechazaSinDatosDeTransporte(t *testing.T) {
	db := setup1004TestDB(t)
	svc := NewSaleService(db)
	contactID := seedContactConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	in := transporte1004Input(contactID, seriesID)
	in.Detraccion.PuntoDestino = ""
	if _, err := svc.Create(in); err == nil {
		t.Fatal("esperaba error: falta punto de destino")
	}
}

// El código 027 en operación 1001 sigue rechazado — no se coló ninguna excepción al extender los
// gates a IsDetraccion.
func TestCreate1001_SigueRechazandoCodigo027(t *testing.T) {
	db := setup1004TestDB(t)
	svc := NewSaleService(db)
	contactID := seedContactConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	in := transporte1004Input(contactID, seriesID)
	in.OperationTypeCode = "1001"
	in.Detraccion = &detraccionsvc.SaleInput{GoodCode: "027"}
	_, err := svc.Create(in)
	if err == nil {
		t.Fatal("esperaba error: 027 no es válido en 1001")
	}
	if !strings.Contains(err.Error(), "027") && !strings.Contains(err.Error(), "transporte") {
		t.Errorf("mensaje inesperado: %v", err)
	}
}

// Regresión: una venta 1001 normal (sin nada de transporte) sigue funcionando exactamente igual
// tras extender los 6 gates de sale_service.go a IsDetraccion.
func TestCreate1001_SigueFuncionandoSinCambios(t *testing.T) {
	db := setup1004TestDB(t)
	svc := NewSaleService(db)
	contactID := seedContactConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	in := CreateSaleInput{
		BranchID: 1, UserID: 1, ContactID: &contactID, SeriesID: seriesID, DocType: "01",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		OperationTypeCode: "1001",
		Payments:          []PaymentInput{{Method: "cash", Amount: 1132.8}},
		Items: []SaleItemInput{{
			Description: "Servicio administrativo", Unit: "ZZ", Quantity: 1, UnitPrice: 1180,
			IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
		Detraccion: &detraccionsvc.SaleInput{GoodCode: "014"},
	}
	sale, err := svc.Create(in)
	if err != nil {
		t.Fatalf("Create 1001: %v", err)
	}
	det, err := detraccionsvc.NewService(db).LoadBySaleID(sale.ID)
	if err != nil || det == nil {
		t.Fatalf("no se persistió detracción 1001: %v", err)
	}
	if det.DetractionAmountPen != 47.2 {
		t.Errorf("detraction_amount_pen = %v, want 47.20 (sin cambios respecto al comportamiento previo)", det.DetractionAmountPen)
	}
}
