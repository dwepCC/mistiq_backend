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
		&database.TenantUser{}, &database.TenantBankMovement{}, &database.TenantBankAccount{},
		&database.TenantPaymentMethod{},
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

	items, _, err := svc.ListSessionsEnriched(SessionListParams{BranchID: 1})
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

// Total (historial) debe reflejar TODOS los métodos de pago, no solo efectivo — a diferencia de
// TotalIncome/TotalExpense (cashOnlyMovementTotals, pensados para el arqueo).
func TestListSessionsEnriched_TotalIncluyeTodosLosMetodos(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	session := newSession(t, db, 100)
	db.Create(&database.TenantCashMovement{CashSessionID: session.ID, Type: "income", Amount: 20})
	acc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", PaymentMethod: "yape", Active: true}
	db.Create(acc)
	db.Create(&database.TenantBankMovement{BankAccountID: acc.ID, Type: "credit", Amount: 50, CashSessionID: &session.ID, Date: time.Now(), UserID: 1})

	items, _, err := svc.ListSessionsEnriched(SessionListParams{BranchID: 1})
	if err != nil {
		t.Fatalf("ListSessionsEnriched: %v", err)
	}
	var got *CashSessionListItem
	for i := range items {
		if items[i].ID == session.ID {
			got = &items[i]
		}
	}
	if got == nil {
		t.Fatal("sesión no encontrada en el listado")
	}
	if got.TotalIncome != 20 {
		t.Errorf("TotalIncome (solo efectivo) = %v, want 20", got.TotalIncome)
	}
	if got.Total != 170 { // 100 apertura + 20 efectivo + 50 yape
		t.Errorf("Total (todos los métodos) = %v, want 170", got.Total)
	}
}

// Paginación (historial de Caja): Page/PerPage traen la página pedida + el total real de filas
// (no de la página), con las más recientes primero.
func TestListSessions_Paginacion(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	// 5 sesiones, cada una con opened_at distinto y creciente para que el orden DESC sea
	// determinístico (created_at por defecto podría empatar en el mismo instante de test).
	base := time.Now()
	ids := make([]uint, 5)
	for i := 0; i < 5; i++ {
		st := &database.TenantCashSession{
			BranchID: 1, UserID: 7, OpenedBy: 7, Status: "open",
			OpenedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := db.Create(st).Error; err != nil {
			t.Fatal(err)
		}
		ids[i] = st.ID
	}

	page1, total, err := svc.ListSessionsEnriched(SessionListParams{BranchID: 1, Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("página 1: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %v, want 5", total)
	}
	if len(page1) != 2 {
		t.Fatalf("página 1: got %d filas, want 2", len(page1))
	}
	// Más reciente primero: la sesión #4 (i=4, la última creada) debe ser la primera fila.
	if page1[0].ID != ids[4] || page1[1].ID != ids[3] {
		t.Fatalf("orden de página 1 inesperado: %+v", []uint{page1[0].ID, page1[1].ID})
	}

	page3, total2, err := svc.ListSessionsEnriched(SessionListParams{BranchID: 1, Page: 3, PerPage: 2})
	if err != nil {
		t.Fatalf("página 3: %v", err)
	}
	if total2 != 5 {
		t.Fatalf("total (página 3) = %v, want 5", total2)
	}
	if len(page3) != 1 {
		t.Fatalf("página 3 (última, incompleta): got %d filas, want 1", len(page3))
	}
	if page3[0].ID != ids[0] {
		t.Fatalf("página 3 debía traer la sesión más antigua: got id=%d", page3[0].ID)
	}
}

// OpenedBy filtra en la propia consulta SQL (no después, como antes hacía
// filterSessionsForCaller): quien no administra cualquier caja no debe ver ni contar en el total
// las sesiones de otros usuarios.
func TestListSessions_OpenedByFiltraTotalYFilas(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	mine := newSession(t, db, 0) // OpenedBy: 7, por newSession
	other := &database.TenantCashSession{BranchID: 1, UserID: 9, OpenedBy: 9, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(other).Error; err != nil {
		t.Fatal(err)
	}

	// PerPage > 0 para ejercitar la rama que calcula total (la variante sin paginar, usada por
	// ejemplo desde CashReportsPage.tsx, no lo necesita y por eso no lo calcula — ver
	// ListSessions).
	items, total, err := svc.ListSessionsEnriched(SessionListParams{BranchID: 1, OpenedBy: 7, Page: 1, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total filtrado por OpenedBy = %v, want 1", total)
	}
	if len(items) != 1 || items[0].ID != mine.ID {
		t.Fatalf("esperaba solo la sesión propia (%d): %+v", mine.ID, items)
	}
	_ = other
}
