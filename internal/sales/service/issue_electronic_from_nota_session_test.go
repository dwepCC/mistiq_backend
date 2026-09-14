package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Caso especial "reemisión de venta" (auditado, decisión A confirmada con evidencia): emitir un
// comprobante electrónico (boleta/factura) a partir de una nota de venta ya pagada NO representa
// un nuevo movimiento de dinero — el pago real ya se registró, con su propia sesión, cuando se
// creó la nota (por eso SkipPaymentDistribution=true, y ningún RecordPayment se vuelve a llamar
// para el comprobante). Pero el comprobante SÍ debe heredar la MISMA cash_session_id que la nota,
// para que "en qué turno ocurrió esta venta" sea consultable desde el documento fiscal final.
//
// La herencia se hace por UPDATE directo (no vía CreateSaleInput.CashSessionID) precisamente para
// NO disparar ValidateCashSessionForUser como si fuera un cobro en vivo: entre la nota y su
// emisión electrónica pueden pasar horas o días, la sesión original bien puede estar cerrada, y
// quien emite el comprobante puede no ser el mismo usuario que atendió la venta original. Estos
// tests prueban exactamente eso: la herencia funciona SIN exigir sesión abierta ni mismo usuario.

func setupIssueElectronicFromNotaSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{},
		&database.TenantDocumentSeries{},
		&database.TenantSale{},
		&database.TenantSaleItem{},
		&database.TenantSalePayment{},
		&database.TenantCashSession{},
		&database.TenantCashMovement{},
		&database.TenantBankMovement{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: true, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// seedNotaVentaConSesion crea una nota de venta PEN (sin complicación de tipo de cambio) y,
// opcionalmente, una sesión de caja a la que queda vinculada.
func seedNotaVentaConSesion(t *testing.T, db *gorm.DB, session *database.TenantCashSession) (notaID, boletaSeriesID uint) {
	t.Helper()
	nvSeries := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NV002", Correlative: 1, Active: true,
	}
	boletaSeries := database.TenantDocumentSeries{
		BranchID: 1, DocType: "BOLETA", SunatCode: "03", Category: "venta",
		Series: "B002", Correlative: 1, Active: true,
	}
	if err := db.Create(&nvSeries).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&boletaSeries).Error; err != nil {
		t.Fatal(err)
	}
	nota := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: nvSeries.ID, DocType: "NOTA DE VENTA",
		Series: "NV002", Correlative: 1, Number: "NV002-00000001",
		IssueDate: time.Now(), Subtotal: 100, TaxAmount: 18, Total: 118,
		Currency: "PEN", Status: "paid",
	}
	if session != nil {
		nota.CashSessionID = &session.ID
	}
	if err := db.Create(&nota).Error; err != nil {
		t.Fatal(err)
	}
	item := database.TenantSaleItem{
		SaleID: nota.ID, Code: "P1", Description: "Producto", Unit: "NIU",
		Quantity: 1, UnitPrice: 100, Subtotal: 100, TaxAmount: 18, Total: 118,
		IgvAffectationType: "10",
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	pay := database.TenantSalePayment{SaleID: nota.ID, Method: "cash", Amount: 118}
	if err := db.Create(&pay).Error; err != nil {
		t.Fatal(err)
	}
	if session != nil {
		if err := db.Create(&database.TenantCashMovement{
			CashSessionID: session.ID, Type: "income", Amount: 118, PaymentMethod: "cash",
			Category: "Venta", SaleID: &nota.ID, UserID: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return nota.ID, boletaSeries.ID
}

// 1. La nota se pagó en una sesión que YA ESTÁ CERRADA (el caso realista: la emisión electrónica
// suele pasar horas o días después del cobro real). El comprobante debe heredar igual esa sesión,
// SIN error — la herencia es documental, no un cobro nuevo que exija sesión abierta.
func TestIssueElectronicFromNota_inheritsClosedSessionFromNota(t *testing.T) {
	db := setupIssueElectronicFromNotaSessionTestDB(t)
	closedAt := time.Now()
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "closed", ClosedAt: &closedAt}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	notaID, boletaSeriesID := seedNotaVentaConSesion(t, db, session)

	svc := NewSaleService(db)
	child, err := svc.IssueElectronicFromNota(notaID, boletaSeriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatalf("no se esperaba error emitiendo desde una nota con sesión cerrada: %v", err)
	}
	if child.CashSessionID == nil || *child.CashSessionID != session.ID {
		t.Fatalf("child.CashSessionID = %v, want %d (heredado de la nota)", child.CashSessionID, session.ID)
	}
}

// 2. Quien emite el comprobante es un usuario DISTINTO al dueño de la sesión original (p. ej.
// contabilidad formalizando después). Debe heredar igual, sin exigir que el emisor sea el dueño.
func TestIssueElectronicFromNota_inheritsSessionFromDifferentUser(t *testing.T) {
	db := setupIssueElectronicFromNotaSessionTestDB(t)
	session := &database.TenantCashSession{BranchID: 1, UserID: 7, OpenedBy: 7, Status: "open"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	notaID, boletaSeriesID := seedNotaVentaConSesion(t, db, session)

	svc := NewSaleService(db)
	emisorID := uint(99) // no es el usuario 7 dueño de la sesión
	child, err := svc.IssueElectronicFromNota(notaID, boletaSeriesID, emisorID, "", 0, nil)
	if err != nil {
		t.Fatalf("no se esperaba error emitiendo con un usuario distinto al dueño de la sesión: %v", err)
	}
	if child.CashSessionID == nil || *child.CashSessionID != session.ID {
		t.Fatalf("child.CashSessionID = %v, want %d", child.CashSessionID, session.ID)
	}
}

// 3. Nota sin sesión (dato histórico anterior a esta columna, o nota emitida sin caja): el
// comprobante se emite igual, y su CashSessionID se mantiene nil — sin inventar ninguna.
func TestIssueElectronicFromNota_notaSinSesion_quedaNil(t *testing.T) {
	db := setupIssueElectronicFromNotaSessionTestDB(t)
	notaID, boletaSeriesID := seedNotaVentaConSesion(t, db, nil)

	svc := NewSaleService(db)
	child, err := svc.IssueElectronicFromNota(notaID, boletaSeriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if child.CashSessionID != nil {
		t.Fatalf("child.CashSessionID = %v, want nil", child.CashSessionID)
	}
}

// 4. No se duplica dinero: la emisión electrónica no debe crear ningún TenantCashMovement ni
// TenantBankMovement nuevo — el único movimiento de la venta sigue siendo el que ya existía desde
// la nota.
func TestIssueElectronicFromNota_noDuplicaMovimientoDeDinero(t *testing.T) {
	db := setupIssueElectronicFromNotaSessionTestDB(t)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	notaID, boletaSeriesID := seedNotaVentaConSesion(t, db, session)

	var cashMovBefore int64
	db.Model(&database.TenantCashMovement{}).Count(&cashMovBefore)

	svc := NewSaleService(db)
	if _, err := svc.IssueElectronicFromNota(notaID, boletaSeriesID, 1, "", 0, nil); err != nil {
		t.Fatal(err)
	}

	var cashMovAfter int64
	db.Model(&database.TenantCashMovement{}).Count(&cashMovAfter)
	if cashMovAfter != cashMovBefore {
		t.Errorf("tenant_cash_movements: antes=%d después=%d — la emisión electrónica no debe crear un movimiento nuevo", cashMovBefore, cashMovAfter)
	}
	var bankMovCount int64
	db.Model(&database.TenantBankMovement{}).Count(&bankMovCount)
	if bankMovCount != 0 {
		t.Errorf("tenant_bank_movements = %d, want 0 (esta venta se pagó en efectivo, no debe generar movimiento bancario)", bankMovCount)
	}
}
