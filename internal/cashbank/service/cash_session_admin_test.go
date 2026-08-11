package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSessionAdminDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{}, &database.TenantSale{},
		&database.TenantUser{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func newSession(t *testing.T, db *gorm.DB, opening float64) *database.TenantCashSession {
	t.Helper()
	st := &database.TenantCashSession{
		BranchID: 1, UserID: 7, OpenedBy: 7,
		OpeningBalance: opening, Status: "open", OpenedAt: time.Now(),
	}
	if err := db.Create(st).Error; err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDeleteEmptySession_BorraLaQueNoRegistroNada(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	st := newSession(t, db, 100)

	if err := svc.DeleteEmptySession(st.ID); err != nil {
		t.Fatalf("DeleteEmptySession: %v", err)
	}

	// Borrado físico: no debe quedar ni como registro con deleted_at.
	var n int64
	db.Unscoped().Model(&database.TenantCashSession{}).Where("id = ?", st.ID).Count(&n)
	if n != 0 {
		t.Fatalf("la sesión sigue en la base (%d filas)", n)
	}
}

func TestDeleteEmptySession_RechazaConMovimientos(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	st := newSession(t, db, 100)
	db.Create(&database.TenantCashMovement{CashSessionID: st.ID, Type: "income", Amount: 50})

	if err := svc.DeleteEmptySession(st.ID); err == nil {
		t.Fatal("una caja con movimientos no debe poder eliminarse")
	}
	var n int64
	db.Model(&database.TenantCashSession{}).Where("id = ?", st.ID).Count(&n)
	if n != 1 {
		t.Fatal("la sesión no debía tocarse")
	}
}

// Una caja puede tener ventas cobradas sin movimiento propio (p. ej. cobros a cuenta):
// tampoco se borra.
func TestDeleteEmptySession_RechazaConVentas(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	st := newSession(t, db, 0)
	sid := st.ID
	db.Create(&database.TenantSale{BranchID: 1, UserID: 7, CashSessionID: &sid, Number: "NV001-1"})

	if err := svc.DeleteEmptySession(st.ID); err == nil {
		t.Fatal("una caja con ventas no debe poder eliminarse")
	}
}

func TestUpdateOpeningBalance_CajaAbierta(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	st := newSession(t, db, 1000) // se tipeó 1000 en vez de 100

	updated, err := svc.UpdateOpeningBalance(st.ID, 100)
	if err != nil {
		t.Fatalf("UpdateOpeningBalance: %v", err)
	}
	if updated.OpeningBalance != 100 {
		t.Fatalf("opening_balance = %v, se esperaba 100", updated.OpeningBalance)
	}
}

// En una caja cerrada el esperado y la diferencia salen del monto de apertura: si no se
// recalculan, el arqueo guardado deja de cuadrar con sus propios números.
func TestUpdateOpeningBalance_CajaCerradaRecalculaDiferencia(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	st := newSession(t, db, 1000)

	expected := 1200.0 // 1000 apertura + 200 de ingresos
	closing := 300.0   // lo realmente contado
	diff := closing - expected
	db.Model(st).Updates(map[string]interface{}{
		"status":           "closed",
		"expected_balance": expected,
		"closing_balance":  closing,
		"difference":       diff,
	})

	updated, err := svc.UpdateOpeningBalance(st.ID, 100)
	if err != nil {
		t.Fatalf("UpdateOpeningBalance: %v", err)
	}
	if updated.ExpectedBalance == nil || *updated.ExpectedBalance != 300 {
		t.Fatalf("expected_balance = %v, se esperaba 300 (100 + 200)", updated.ExpectedBalance)
	}
	if updated.Difference == nil || *updated.Difference != 0 {
		t.Fatalf("difference = %v, se esperaba 0: lo contado ahora cuadra", updated.Difference)
	}
}

func TestUpdateOpeningBalance_RechazaNegativo(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	st := newSession(t, db, 100)

	if _, err := svc.UpdateOpeningBalance(st.ID, -1); err == nil {
		t.Fatal("un monto de apertura negativo no debe aceptarse")
	}
}

func TestListSessionsEnriched_MarcaLasVacias(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	vacia := newSession(t, db, 0)
	conMov := newSession(t, db, 0)
	db.Create(&database.TenantCashMovement{CashSessionID: conMov.ID, Type: "income", Amount: 10})

	items, err := svc.ListSessionsEnriched(1)
	if err != nil {
		t.Fatalf("ListSessionsEnriched: %v", err)
	}
	byID := map[uint]bool{}
	for _, it := range items {
		byID[it.ID] = it.Empty
	}
	if !byID[vacia.ID] {
		t.Fatal("la sesión sin nada debía marcarse como vacía")
	}
	if byID[conMov.ID] {
		t.Fatal("la sesión con movimientos no debía marcarse como vacía")
	}
}
