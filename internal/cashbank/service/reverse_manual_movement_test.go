package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupReverseManualMovementTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPaymentMethod{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Ingreso Efectivo +100 → Reversión Efectivo -100. Ambos en la misma sesión, original intacto.
func TestReverseManualMovement_cashIncome(t *testing.T) {
	db := setupReverseManualMovementTestDB(t)
	svc := NewCashBankService(db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)

	if err := svc.AddMovement(session.ID, 1, "income", "Aporte", "REF-1", "efectivo", 100, "", nil); err != nil {
		t.Fatal(err)
	}
	var original database.TenantCashMovement
	db.Where("cash_session_id = ?", session.ID).First(&original)

	if err := svc.ReverseManualMovement(db, "cash", original.ID, 1, "Error de digitación"); err != nil {
		t.Fatal(err)
	}

	var rev database.TenantCashMovement
	if err := db.Where("reversal_of_id = ?", original.ID).First(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.Type != "expense" || rev.Amount != 100 || rev.PaymentMethod != "efectivo" || rev.CashSessionID != session.ID {
		t.Fatalf("reversión inesperada: %+v", rev)
	}

	// El original sigue intacto.
	var stillThere database.TenantCashMovement
	db.First(&stillThere, original.ID)
	if stillThere.Type != "income" || stillThere.Amount != 100 {
		t.Fatalf("el movimiento original fue alterado: %+v", stillThere)
	}

	summary, err := svc.GetSessionBalanceSummary(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 0 {
		t.Errorf("total tras revertir = %v, want 0", summary.Total)
	}
}

// Ingreso Yape +100: desde el fix de la duplicación en AddMovement, un manual por un método con
// cuenta asociada vive EXCLUSIVAMENTE en tenant_bank_movements (nunca también en
// tenant_cash_movements) — se revierte con kind="bank", y el saldo de la cuenta Yape vuelve a su
// valor original.
func TestReverseManualMovement_yapeIncomeReversesBankMovement(t *testing.T) {
	db := setupReverseManualMovementTestDB(t)
	svc := NewCashBankService(db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)
	acc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", Balance: 500, PaymentMethod: "yape", Active: true}
	db.Create(acc)

	if err := svc.AddMovement(session.ID, 1, "income", "Aporte", "REF-YAPE", "yape", 100, "nota de prueba", nil); err != nil {
		t.Fatal(err)
	}
	var accAfterCreate database.TenantBankAccount
	db.First(&accAfterCreate, acc.ID)
	if accAfterCreate.Balance != 600 {
		t.Fatalf("balance tras crear: got %v want 600", accAfterCreate.Balance)
	}

	// No debe existir ningún TenantCashMovement — el manual Yape vive solo en bank_movements.
	var cashCount int64
	db.Model(&database.TenantCashMovement{}).Where("cash_session_id = ?", session.ID).Count(&cashCount)
	if cashCount != 0 {
		t.Fatalf("no debía crearse ningún tenant_cash_movement para un manual Yape: got %d", cashCount)
	}

	var original database.TenantBankMovement
	db.Where("bank_account_id = ?", acc.ID).First(&original)
	if original.Category != "Aporte" || original.Notes != "nota de prueba" {
		t.Fatalf("category/notes no se guardaron en el movimiento bancario: %+v", original)
	}

	if err := svc.ReverseManualMovement(db, "bank", original.ID, 1, ""); err != nil {
		t.Fatal(err)
	}

	var bankMovs []database.TenantBankMovement
	db.Where("bank_account_id = ?", acc.ID).Order("id ASC").Find(&bankMovs)
	if len(bankMovs) != 2 {
		t.Fatalf("bank movements: got %d want 2 (original + reversión)", len(bankMovs))
	}
	if bankMovs[1].ReversalOfID == nil || *bankMovs[1].ReversalOfID != bankMovs[0].ID {
		t.Fatalf("la reversión bancaria no referencia al original: %+v", bankMovs[1])
	}
	if bankMovs[1].Type != "debit" {
		t.Fatalf("la reversión de un ingreso debe ser debit: %+v", bankMovs[1])
	}

	var accAfterReverse database.TenantBankAccount
	db.First(&accAfterReverse, acc.ID)
	if accAfterReverse.Balance != 500 {
		t.Fatalf("balance tras revertir: got %v want 500 (vuelve al original)", accAfterReverse.Balance)
	}

	summary, err := svc.GetSessionBalanceSummary(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 0 {
		t.Errorf("total tras revertir = %v, want 0", summary.Total)
	}
}

// No se puede revertir un movimiento que pertenece a una venta/compra por esta vía.
func TestReverseManualMovement_rejectsSaleLinkedMovement(t *testing.T) {
	db := setupReverseManualMovementTestDB(t)
	svc := NewCashBankService(db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)
	saleID := uint(9)
	mov := &database.TenantCashMovement{CashSessionID: session.ID, Type: "income", Amount: 50, PaymentMethod: "efectivo", Category: "Venta", SaleID: &saleID, UserID: 1}
	db.Create(mov)

	if err := svc.ReverseManualMovement(db, "cash", mov.ID, 1, ""); err == nil {
		t.Fatal("esperaba error: no se debe poder revertir un movimiento de venta por este endpoint")
	}
}

// Mismo rechazo que TestReverseManualMovement_rejectsSaleLinkedMovement, para un movimiento
// bancario ligado a una compra (kind="bank").
func TestReverseManualMovement_rejectsPurchaseLinkedBankMovement(t *testing.T) {
	db := setupReverseManualMovementTestDB(t)
	svc := NewCashBankService(db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)
	acc := &database.TenantBankAccount{Name: "Transferencias", Type: "bank", Balance: 500, PaymentMethod: "transferencia", Active: true}
	db.Create(acc)
	purchaseID := uint(7)
	mov := &database.TenantBankMovement{BankAccountID: acc.ID, Type: "debit", Amount: 50, PurchaseID: &purchaseID, CashSessionID: &session.ID, UserID: 1}
	db.Create(mov)

	if err := svc.ReverseManualMovement(db, "bank", mov.ID, 1, ""); err == nil {
		t.Fatal("esperaba error: no se debe poder revertir un movimiento de compra por este endpoint")
	}
}

// No se puede revertir dos veces el mismo movimiento.
func TestReverseManualMovement_rejectsDoubleReversal(t *testing.T) {
	db := setupReverseManualMovementTestDB(t)
	svc := NewCashBankService(db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)

	if err := svc.AddMovement(session.ID, 1, "expense", "Retiro", "", "efectivo", 40, "", nil); err != nil {
		t.Fatal(err)
	}
	var original database.TenantCashMovement
	db.Where("cash_session_id = ?", session.ID).First(&original)

	if err := svc.ReverseManualMovement(db, "cash", original.ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReverseManualMovement(db, "cash", original.ID, 1, ""); err == nil {
		t.Fatal("esperaba error: el movimiento ya fue revertido")
	}
}

// No se puede revertir un movimiento de una sesión ya cerrada.
func TestReverseManualMovement_rejectsClosedSession(t *testing.T) {
	db := setupReverseManualMovementTestDB(t)
	svc := NewCashBankService(db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)

	if err := svc.AddMovement(session.ID, 1, "income", "Aporte", "", "efectivo", 20, "", nil); err != nil {
		t.Fatal(err)
	}
	var original database.TenantCashMovement
	db.Where("cash_session_id = ?", session.ID).First(&original)

	db.Model(&database.TenantCashSession{}).Where("id = ?", session.ID).Update("status", "closed")

	if err := svc.ReverseManualMovement(db, "cash", original.ID, 1, ""); err == nil {
		t.Fatal("esperaba error: sesión ya cerrada")
	}
}

// kind inválido (ni "cash" ni "bank") se rechaza en vez de adivinar.
func TestReverseManualMovement_rejectsInvalidKind(t *testing.T) {
	db := setupReverseManualMovementTestDB(t)
	svc := NewCashBankService(db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)

	if err := svc.AddMovement(session.ID, 1, "income", "Aporte", "", "efectivo", 20, "", nil); err != nil {
		t.Fatal(err)
	}
	var original database.TenantCashMovement
	db.Where("cash_session_id = ?", session.ID).First(&original)

	if err := svc.ReverseManualMovement(db, "", original.ID, 1, ""); err == nil {
		t.Fatal("esperaba error: kind vacío no debe asumirse como cash")
	}
	if err := svc.ReverseManualMovement(db, "otro", original.ID, 1, ""); err == nil {
		t.Fatal("esperaba error: kind desconocido")
	}
}
