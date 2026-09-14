package service

import (
	"testing"
	"time"

	cashbanksvc "tukifac/internal/cashbank/service"
	"tukifac/pkg/database"
)

// TEST 12 (Fase 2 — CxP): anular una compra a crédito que ya recibió un pago a proveedor NO
// EFECTIVO debe revertir ese pago — Void() ya no exige PaymentMethod != "" para intentar la
// reversión (ver comentario en Void), así que reutiliza el MISMO mecanismo existente
// (ReverseBankMovementsByReference, por referencia = docNumber) sin ningún camino nuevo. Verifica
// cash_session_id conservado, reversal_of_id correcto, y protección contra doble reversión.
func TestPurchaseVoid_ReversesPayablePayment_PreservesSessionAndReversalLink(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	session := newOpenCashSession(t, db, 1, 1)
	seedBankAccountForMethod(t, db, "yape")
	pid := newPurchaseProduct(t, db, "P-CXP-VOID")

	// Compra a crédito (PaymentMethod vacío) — Create() ya genera el TenantPurchasePayable.
	purchase := createTestPurchase(t, svc, 1, 1, "", "900", pid)

	// Pago a proveedor por Yape, en la misma sesión (misma referencia docNumber que ya usa el
	// pago inmediato — así Void lo encuentra con la consulta existente).
	docNumber := purchase.Series + "-" + purchase.Number
	if err := db.Create(&database.TenantPurchasePayment{
		PurchaseID: purchase.ID, Method: "yape", Amount: 50, CashSessionID: &session.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	cbSvc := cashbanksvc.NewCashBankService(db)
	if err := cbSvc.RecordExpensePayment(db, "yape", 50, &session.ID, docNumber, "Pago proveedor "+docNumber, &purchase.ID, 1); err != nil {
		t.Fatal(err)
	}

	var original database.TenantBankMovement
	if err := db.Where("purchase_id = ? AND type = ?", purchase.ID, "debit").First(&original).Error; err != nil {
		t.Fatal(err)
	}
	if original.CashSessionID == nil || *original.CashSessionID != session.ID {
		t.Fatalf("fixture inconsistente: pago original sin sesión %d: %+v", session.ID, original)
	}

	if err := svc.Void(purchase.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}

	var rev database.TenantBankMovement
	if err := db.Where("reversal_of_id = ?", original.ID).First(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.Type != "credit" {
		t.Fatalf("tipo de reversión: %s, want credit", rev.Type)
	}
	if rev.CashSessionID == nil || *rev.CashSessionID != session.ID {
		t.Fatalf("reversal.CashSessionID = %v, want %d (conservado del pago original)", rev.CashSessionID, session.ID)
	}

	// Protección contra doble reversión: anular de nuevo debe rechazarse desde el propio Void
	// (la compra ya está cancelada), sin generar una segunda fila de reversión.
	if err := svc.Void(purchase.ID, 1); err == nil {
		t.Fatal("se esperaba error: la compra ya está anulada")
	}
	var revCount int64
	db.Model(&database.TenantBankMovement{}).Where("reversal_of_id = ?", original.ID).Count(&revCount)
	if revCount != 1 {
		t.Fatalf("reversiones = %d, want 1 (no debe duplicarse)", revCount)
	}
}

// Decisión A — revisión explícita de Void(): una compra a crédito SIN ningún pago de CxP debe
// anularse igual que antes de esta fase (las consultas de reversión no encuentran nada, no pasa
// nada) — no debe generarse ningún movimiento espurio.
func TestPurchaseVoid_CreditoSinPagos_seAnulaSinGenerarMovimientos(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	newOpenCashSession(t, db, 1, 1)
	pid := newPurchaseProduct(t, db, "P-CXP-VOID-NOPAGO")

	purchase := createTestPurchase(t, svc, 1, 1, "", "901", pid)

	if err := svc.Void(purchase.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}
	var reloaded database.TenantPurchase
	db.First(&reloaded, purchase.ID)
	if reloaded.Status != StatusCancelled {
		t.Fatalf("status = %q, want %q", reloaded.Status, StatusCancelled)
	}
	var cashCount, bankCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	db.Model(&database.TenantBankMovement{}).Where("purchase_id = ?", purchase.ID).Count(&bankCount)
	if cashCount != 0 || bankCount != 0 {
		t.Errorf("no debe generarse ningún movimiento al anular una compra a crédito sin pagos: cash=%d bank=%d", cashCount, bankCount)
	}
}

// Decisión A — revisión explícita de Void(): una compra a crédito TOTALMENTE PAGADA (una cuota
// en efectivo + otra por Yape, en sesiones distintas a la de registro) debe revertir AMBOS
// pagos correctamente al anularse, sin duplicar ninguno y sin permitir una segunda reversión.
func TestPurchaseVoid_CompraTotalmentePagada_reviertAmbosPagos(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	svc := NewPurchaseService(db)
	session30 := newOpenCashSession(t, db, 1, 2)
	session35 := newOpenCashSession(t, db, 1, 3)
	seedCashPaymentMethod(t, db)
	seedBankAccountForMethod(t, db, "yape")
	pid := newPurchaseProduct(t, db, "P-CXP-VOID-FULL")
	newOpenCashSession(t, db, 1, 1) // sesión para registrar la compra

	purchase := createTestPurchase(t, svc, 1, 1, "", "902", pid)
	docNumber := purchase.Series + "-" + purchase.Number
	cbSvc := cashbanksvc.NewCashBankService(db)

	// Cuota 1: efectivo, Caja 30 (usuario 2).
	if err := db.Create(&database.TenantPurchasePayment{
		PurchaseID: purchase.ID, Method: "cash", Amount: 70, CashSessionID: &session30.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := cbSvc.RecordExpensePayment(db, "cash", 70, &session30.ID, docNumber, "Pago proveedor "+docNumber, &purchase.ID, 2); err != nil {
		t.Fatal(err)
	}
	// Cuota 2: Yape, Caja 35 (usuario 3) — completa el pago total (100 + 18% IGV = 118, ver
	// newPurchaseProduct/createTestPurchase: unit_cost=100, igv 18%).
	if err := db.Create(&database.TenantPurchasePayment{
		PurchaseID: purchase.ID, Method: "yape", Amount: 48, CashSessionID: &session35.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := cbSvc.RecordExpensePayment(db, "yape", 48, &session35.ID, docNumber, "Pago proveedor "+docNumber, &purchase.ID, 3); err != nil {
		t.Fatal(err)
	}

	var origCash database.TenantCashMovement
	db.Where("purchase_id = ? AND type = ?", purchase.ID, "expense").First(&origCash)
	var origBank database.TenantBankMovement
	db.Where("purchase_id = ? AND type = ?", purchase.ID, "debit").First(&origBank)

	if err := svc.Void(purchase.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}

	var revCash database.TenantCashMovement
	if err := db.Where("reversal_of_id = ?", origCash.ID).First(&revCash).Error; err != nil {
		t.Fatalf("no se encontró la reversión del pago en efectivo: %v", err)
	}
	if revCash.CashSessionID != session30.ID {
		t.Errorf("reversión de efectivo: CashSessionID = %v, want %d", revCash.CashSessionID, session30.ID)
	}
	var revBank database.TenantBankMovement
	if err := db.Where("reversal_of_id = ?", origBank.ID).First(&revBank).Error; err != nil {
		t.Fatalf("no se encontró la reversión del pago por Yape: %v", err)
	}
	if revBank.CashSessionID == nil || *revBank.CashSessionID != session35.ID {
		t.Errorf("reversión de Yape: CashSessionID = %v, want %d", revBank.CashSessionID, session35.ID)
	}

	// Sin duplicados: exactamente 2 movimientos de efectivo (original + reversión) y 2 bancarios.
	var cashCount, bankCount int64
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	db.Model(&database.TenantBankMovement{}).Where("purchase_id = ?", purchase.ID).Count(&bankCount)
	if cashCount != 2 || bankCount != 2 {
		t.Fatalf("movimientos tras Void: cash=%d (want 2) bank=%d (want 2)", cashCount, bankCount)
	}

	// Doble anulación rechazada, sin generar una tercera reversión de ningún tipo.
	if err := svc.Void(purchase.ID, 1); err == nil {
		t.Fatal("se esperaba error: la compra ya está anulada")
	}
	db.Model(&database.TenantCashMovement{}).Where("purchase_id = ?", purchase.ID).Count(&cashCount)
	db.Model(&database.TenantBankMovement{}).Where("purchase_id = ?", purchase.ID).Count(&bankCount)
	if cashCount != 2 || bankCount != 2 {
		t.Fatalf("tras segundo intento de Void, movimientos cambiaron: cash=%d bank=%d (no debían)", cashCount, bankCount)
	}
}
