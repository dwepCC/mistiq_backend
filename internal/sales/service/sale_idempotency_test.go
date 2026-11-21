package service

import (
	"errors"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

func idemInput(seriesID uint, userID uint, key string) CreateSaleInput {
	return CreateSaleInput{
		BranchID: 1, UserID: userID, SeriesID: seriesID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments:       []PaymentInput{{Method: "cash", Amount: 10}},
		Items:          []SaleItemInput{{Description: "Servicio", Code: "SRV", Unit: "NIU", Quantity: 1, UnitPrice: 10, IgvAffectationType: "10", PriceIncludesIgv: true}},
		IdempotencyKey: key,
		// El ítem manual no existe en catálogo: se salta el control de precio autorizado.
		UserCanOverridePrice: true,
	}
}

func setupIdempotencyDB(t *testing.T) (*gorm.DB, uint) {
	t.Helper()
	db := setupSaleCombosDB(t)
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	// Caja abierta también para el usuario 2.
	if err := db.Create(&database.TenantCashSession{
		BranchID: 1, UserID: 2, OpenedBy: 2, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db, series.ID
}

func countSales(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&database.TenantSale{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// Un reintento con la misma clave devuelve la venta original, no crea otra ni consume correlativo.
func TestSaleService_Create_IdempotentRetryReturnsOriginal(t *testing.T) {
	db, seriesID := setupIdempotencyDB(t)
	svc := NewSaleService(db)

	first, err := svc.Create(idemInput(seriesID, 1, "key-aaa"))
	if err != nil {
		t.Fatalf("primer cobro: %v", err)
	}
	second, err := svc.Create(idemInput(seriesID, 1, "key-aaa"))
	if !errors.Is(err, ErrIdempotentReplay) {
		t.Fatalf("el reintento debe devolver ErrIdempotentReplay, obtuve: %v", err)
	}
	if second == nil || second.ID != first.ID || second.Number != first.Number {
		t.Fatalf("el reintento debe devolver la venta original %v, obtuve %+v", first.Number, second)
	}
	if n := countSales(t, db); n != 1 {
		t.Fatalf("debe existir 1 venta, hay %d", n)
	}
	var ser database.TenantDocumentSeries
	db.First(&ser, seriesID)
	if ser.Correlative != 2 {
		t.Fatalf("el reintento no debe consumir correlativo: serie en %d, want 2", ser.Correlative)
	}
}

// Claves distintas son ventas distintas; sin clave no hay protección (comportamiento previo).
func TestSaleService_Create_DifferentOrMissingKeysCreateSeparateSales(t *testing.T) {
	db, seriesID := setupIdempotencyDB(t)
	svc := NewSaleService(db)

	for _, key := range []string{"key-1", "key-2", "", ""} {
		if _, err := svc.Create(idemInput(seriesID, 1, key)); err != nil {
			t.Fatalf("Create(key=%q): %v", key, err)
		}
	}
	if n := countSales(t, db); n != 4 {
		t.Fatalf("deben existir 4 ventas, hay %d", n)
	}
}

// La clave se acota por usuario: otro cajero con la misma clave no recibe la venta ajena.
func TestSaleService_Create_KeyIsScopedPerUser(t *testing.T) {
	db, seriesID := setupIdempotencyDB(t)
	svc := NewSaleService(db)

	a, err := svc.Create(idemInput(seriesID, 1, "shared-key"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(idemInput(seriesID, 2, "shared-key"))
	if err != nil {
		t.Fatalf("otro usuario con la misma clave debe poder vender: %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("ventas de usuarios distintos no deben colapsar")
	}
}

// Carrera: el índice único rechaza el segundo insert y se resuelve devolviendo la ganadora.
func TestReplayIfIdempotentConflict(t *testing.T) {
	db, seriesID := setupIdempotencyDB(t)
	svc := NewSaleService(db)

	winner, err := svc.Create(idemInput(seriesID, 1, "race-key"))
	if err != nil {
		t.Fatal(err)
	}

	// Inserción directa que viola el índice único (lo que vería la petición perdedora de la carrera).
	key := "race-key"
	dup := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: seriesID, DocType: "00", Series: "NV01", Correlative: 99,
		Number: "NV01-00000099", IssueDate: time.Now(), Total: 10, IdempotencyKey: &key,
	}
	insertErr := db.Create(&dup).Error
	if insertErr == nil {
		t.Fatal("el índice único debe rechazar la segunda venta con la misma clave")
	}
	if !IsDuplicateIdempotencyError(insertErr) {
		t.Fatalf("el error debe reconocerse como clave duplicada: %v", insertErr)
	}

	got, replayErr := ReplayIfIdempotentConflict(db, 1, "race-key", insertErr)
	if !errors.Is(replayErr, ErrIdempotentReplay) || got == nil || got.ID != winner.ID {
		t.Fatalf("debe devolver la ganadora (%d), obtuve %+v / %v", winner.ID, got, replayErr)
	}

	// Un error ajeno a la clave se propaga intacto.
	other := errors.New("boom")
	if g, e := ReplayIfIdempotentConflict(db, 1, "race-key", other); g != nil || e != other {
		t.Fatalf("un error ajeno debe propagarse: %+v / %v", g, e)
	}
}

func TestNormalizeIdempotencyKey(t *testing.T) {
	long := make([]byte, MaxIdempotencyKeyLen+1)
	for i := range long {
		long[i] = 'a'
	}
	cases := map[string]string{
		"":              "",
		"   ":           "",
		"  abc  ":       "abc",
		string(long):    "",
		string(long[1:]): string(long[1:]),
	}
	for in, want := range cases {
		if got := NormalizeIdempotencyKey(in); got != want {
			t.Errorf("Normalize(%d chars) = %q, want %q", len(in), got, want)
		}
	}
}

// OnCreatedTx corre dentro de la transacción de la venta: si falla, rollback total (venta,
// ítems, pagos y correlativo). Es lo que hace atómica la conversión de cotizaciones.
func TestSaleService_Create_OnCreatedTxFailureRollsBackEverything(t *testing.T) {
	db, seriesID := setupIdempotencyDB(t)
	svc := NewSaleService(db)

	boom := errors.New("otra conversión ganó la carrera")
	in := idemInput(seriesID, 1, "")
	in.OnCreatedTx = func(tx *gorm.DB, sale *database.TenantSale) error {
		if sale.ID == 0 {
			t.Error("el hook debe ejecutarse con la venta ya persistida")
		}
		return boom
	}
	if _, err := svc.Create(in); !errors.Is(err, boom) {
		t.Fatalf("debe propagar el error del hook, obtuve %v", err)
	}
	if n := countSales(t, db); n != 0 {
		t.Fatalf("no debe quedar ninguna venta, hay %d", n)
	}
	var items, pays int64
	db.Model(&database.TenantSaleItem{}).Count(&items)
	db.Model(&database.TenantSalePayment{}).Count(&pays)
	if items != 0 || pays != 0 {
		t.Fatalf("no deben quedar ítems ni pagos: %d / %d", items, pays)
	}
	var ser database.TenantDocumentSeries
	db.First(&ser, seriesID)
	if ser.Correlative != 1 {
		t.Fatalf("el correlativo no debe consumirse: %d", ser.Correlative)
	}

	// Y con un hook que no falla, la venta se crea normalmente.
	ok := idemInput(seriesID, 1, "")
	called := false
	ok.OnCreatedTx = func(tx *gorm.DB, sale *database.TenantSale) error { called = true; return nil }
	if _, err := svc.Create(ok); err != nil || !called {
		t.Fatalf("con hook exitoso la venta debe crearse: err=%v called=%v", err, called)
	}
}
