package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// Corrección de trazabilidad: purchase_service.Create ahora exige y resuelve sesión de caja para
// CUALQUIER compra con pago inmediato (ResolveCashSessionForPurchase), sin importar el método —
// igual que ya exige ResolveCashSessionForSale para ventas. Antes, una compra no-efectivo nunca
// intentaba resolver sesión y quedaba con cash_session_id=NULL, dependiendo de un mecanismo de
// atribución por sucursal+fecha (ambiguo con varios cajeros concurrentes, ver auditoría previa).
// Estos tests prueban el vínculo DIRECTO y determinístico para cada método, el aislamiento entre
// cajeros, y que una venta y una compra de la misma sesión comparten el mismo cash_session_id.

func seedBankAccountForMethod(t *testing.T, db *gorm.DB, method string) *database.TenantBankAccount {
	t.Helper()
	acc := &database.TenantBankAccount{Name: "Cuenta " + method, PaymentMethod: method, Balance: 1000, Type: "bank", Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	return acc
}

func newPurchaseProduct(t *testing.T, db *gorm.DB, code string) uint {
	t.Helper()
	product := &database.TenantProduct{
		Code: code, Name: code, Type: "product", Unit: "NIU",
		SalePrice: 10, PurchasePrice: 5, TaxRate: 18, IgvAffectationType: "10",
		ManageStock: false, Active: true,
	}
	if err := db.Create(product).Error; err != nil {
		t.Fatal(err)
	}
	return product.ID
}

func createTestPurchase(t *testing.T, svc *PurchaseService, branchID, userID uint, method string, number string, pid uint) *database.TenantPurchase {
	t.Helper()
	purchase, err := svc.Create(CreatePurchaseInput{
		BranchID: branchID, UserID: userID, DocType: "FACTURA", Series: "F001", Number: number,
		IssueDate: time.Now(), PaymentMethod: method,
		Items: []PurchaseItemInput{{
			ProductID: &pid, Description: "item", Quantity: 1, UnitCost: 100,
			IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatalf("Create (%s): %v", method, err)
	}
	return purchase
}

// Yape: purchase.cash_session_id = sesión, bank_movement.cash_session_id = misma sesión,
// purchase_id correctamente relacionado, y NO genera ningún movimiento de efectivo físico.
func TestPurchaseCreate_Yape_vinculadaASesionSinGenerarEfectivo(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	session := newOpenCashSession(t, db, 1, 1)
	seedBankAccountForMethod(t, db, "yape")
	pid := newPurchaseProduct(t, db, "P-YAPE")

	purchase := createTestPurchase(t, svc, 1, 1, "yape", "500", pid)

	if purchase.CashSessionID == nil || *purchase.CashSessionID != session.ID {
		t.Fatalf("purchase.cash_session_id = %v, want %d", purchase.CashSessionID, session.ID)
	}
	var bankMov database.TenantBankMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != session.ID {
		t.Errorf("bank_movement.cash_session_id = %v, want %d", bankMov.CashSessionID, session.ID)
	}
	if bankMov.PurchaseID == nil || *bankMov.PurchaseID != purchase.ID {
		t.Errorf("bank_movement.purchase_id = %v, want %d", bankMov.PurchaseID, purchase.ID)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	if cashCount != 0 {
		t.Errorf("no debe generarse ningún TenantCashMovement para una compra por Yape, got %d", cashCount)
	}
}

// Transferencia: misma validación que Yape.
func TestPurchaseCreate_Transferencia_vinculadaASesionSinGenerarEfectivo(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	session := newOpenCashSession(t, db, 1, 1)
	seedBankAccountForMethod(t, db, "transferencia")
	pid := newPurchaseProduct(t, db, "P-TRANSF")

	purchase := createTestPurchase(t, svc, 1, 1, "transferencia", "501", pid)

	if purchase.CashSessionID == nil || *purchase.CashSessionID != session.ID {
		t.Fatalf("purchase.cash_session_id = %v, want %d", purchase.CashSessionID, session.ID)
	}
	var bankMov database.TenantBankMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != session.ID {
		t.Errorf("bank_movement.cash_session_id = %v, want %d", bankMov.CashSessionID, session.ID)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	if cashCount != 0 {
		t.Errorf("no debe generarse ningún TenantCashMovement para una compra por transferencia, got %d", cashCount)
	}
}

// Tarjeta: misma validación (el método existe en el sistema — normalizeReportMethod lo reconoce
// explícitamente).
func TestPurchaseCreate_Tarjeta_vinculadaASesionSinGenerarEfectivo(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	session := newOpenCashSession(t, db, 1, 1)
	seedBankAccountForMethod(t, db, "tarjeta")
	pid := newPurchaseProduct(t, db, "P-TARJETA")

	purchase := createTestPurchase(t, svc, 1, 1, "tarjeta", "502", pid)

	if purchase.CashSessionID == nil || *purchase.CashSessionID != session.ID {
		t.Fatalf("purchase.cash_session_id = %v, want %d", purchase.CashSessionID, session.ID)
	}
	var bankMov database.TenantBankMovement
	if err := db.Where("purchase_id = ?", purchase.ID).First(&bankMov).Error; err != nil {
		t.Fatal(err)
	}
	if bankMov.CashSessionID == nil || *bankMov.CashSessionID != session.ID {
		t.Errorf("bank_movement.cash_session_id = %v, want %d", bankMov.CashSessionID, session.ID)
	}
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	if cashCount != 0 {
		t.Errorf("no debe generarse ningún TenantCashMovement para una compra por tarjeta, got %d", cashCount)
	}
}

// Dos cajeros en la misma sucursal: sesión A (usuario 1) y sesión B (usuario 2) abiertas a la
// vez. Una compra del usuario 1 debe quedar con cash_session_id = sesión A, nunca la B — el
// aislamiento es determinístico (GetOpenSession por usuario), no por sucursal+fecha.
func TestPurchaseCreate_DosCajerosMismaSucursal_compraQuedaEnSuPropiaSesion(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	sessionA := newOpenCashSession(t, db, 1, 1)
	sessionB := newOpenCashSession(t, db, 1, 2)
	seedBankAccountForMethod(t, db, "yape")
	pid := newPurchaseProduct(t, db, "P-DOSCAJEROS")

	purchase := createTestPurchase(t, svc, 1, 1, "yape", "503", pid)

	if purchase.CashSessionID == nil || *purchase.CashSessionID != sessionA.ID {
		t.Fatalf("purchase.cash_session_id = %v, want sesión A (%d)", purchase.CashSessionID, sessionA.ID)
	}
	if *purchase.CashSessionID == sessionB.ID {
		t.Fatalf("la compra del usuario 1 quedó vinculada a la sesión B (%d) — aislamiento roto", sessionB.ID)
	}
}

// Venta + compra en la misma sesión: ambas deben compartir el mismo cash_session_id. La venta se
// construye directamente (TenantSale/TenantSalePayment) sin pasar por internal/sales/service para
// no crear un ciclo de imports (sales ya importa cashbank, y purchases también) — no se ejercita
// aquí la lógica de sale_service, solo se verifica el dato compartido.
func TestPurchaseCreate_VentaYCompraMismaSesion_comparteCashSessionID(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSalePayment{}); err != nil {
		t.Fatal(err)
	}
	svc := NewPurchaseService(db)
	session := newOpenCashSession(t, db, 1, 1)
	seedBankAccountForMethod(t, db, "yape")
	pid := newPurchaseProduct(t, db, "P-VENTACOMPRA")

	sale := &database.TenantSale{
		BranchID: 1, UserID: 1, CashSessionID: &session.ID, SeriesID: 1, DocType: "boleta",
		Series: "B001", Correlative: 1, Number: "B001-1", IssueDate: time.Now(),
		Total: 200, Status: "paid",
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}

	purchase := createTestPurchase(t, svc, 1, 1, "yape", "504", pid)

	if sale.CashSessionID == nil || purchase.CashSessionID == nil || *sale.CashSessionID != *purchase.CashSessionID {
		t.Fatalf("venta.cash_session_id=%v, compra.cash_session_id=%v — deben ser la misma sesión", sale.CashSessionID, purchase.CashSessionID)
	}
}
