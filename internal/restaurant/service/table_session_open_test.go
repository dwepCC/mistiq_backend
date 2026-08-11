package service

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTableSessionTestDB(t *testing.T) (*gorm.DB, *database.TenantRestaurantTable) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantRestaurantFloor{},
		&database.TenantRestaurantTable{},
		&database.TenantTableSession{},
		&database.TenantTableOrder{},
		&database.TenantComanda{},
		&database.TenantDocumentSeries{},
		&database.TenantSale{},
		&database.TenantSaleItem{},
		&database.TenantSalePayment{},
		&database.TenantRestaurantStaff{},
		&database.TenantUser{},
		&database.TenantCashSession{},
		&database.TenantPaymentMethod{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	floor := &database.TenantRestaurantFloor{BranchID: 1, Name: "Sala 1", Active: true}
	if err := db.Create(floor).Error; err != nil {
		t.Fatal(err)
	}
	table := &database.TenantRestaurantTable{
		BranchID: 1, FloorID: floor.ID, Name: "Mesa 5", Capacity: 4, Status: "libre", Active: true,
	}
	if err := db.Create(table).Error; err != nil {
		t.Fatal(err)
	}
	seedCashSessionForBilling(t, db)
	return db, table
}

// seedCashSessionForBilling abre caja para el usuario 1 en la sucursal 1 y registra los métodos
// de pago. BillTable pasa por ResolveCashSessionForSale, que rechaza toda venta sin caja abierta
// del propio usuario, así que sin este seed cualquier cobro falla.
func seedCashSessionForBilling(t *testing.T, db *gorm.DB) {
	t.Helper()
	methods := []database.TenantPaymentMethod{
		{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true},
		{Code: "card", Name: "Tarjeta", Active: true},
	}
	for i := range methods {
		if err := db.Create(&methods[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	session := &database.TenantCashSession{
		BranchID: 1, UserID: 1, OpenedBy: 1, OpeningBalance: 0,
		Status: "open", OpenedAt: time.Now(),
	}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
}

func openInput(tableID uint) OpenSessionInput {
	tid := tableID
	return OpenSessionInput{
		TableID:  &tid,
		UserID:   1,
		BranchID: 1,
		Guests:   2,
	}
}

func countOpenSessions(t *testing.T, db *gorm.DB, tableID uint) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&database.TenantTableSession{}).
		Where("table_id = ? AND status = ?", tableID, sessionStatusOpen).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenTableExtended_freeTableCreatesOneSession(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil || sess.ID == 0 {
		t.Fatal("sesión inválida")
	}
	if countOpenSessions(t, db, table.ID) != 1 {
		t.Fatalf("esperaba 1 sesión open, got %d", countOpenSessions(t, db, table.ID))
	}
	var tbl database.TenantRestaurantTable
	db.First(&tbl, table.ID)
	if tbl.Status != "ocupada" {
		t.Fatalf("mesa debe quedar ocupada, got %s", tbl.Status)
	}
}

func TestOpenTableExtended_reopenReturnsSameSession(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	first, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("esperaba misma sesión %d, got %d", first.ID, second.ID)
	}
	if countOpenSessions(t, db, table.ID) != 1 {
		t.Fatalf("esperaba 1 sesión open tras reapertura")
	}
}

func TestOpenTableExtended_concurrentOpenSingleSession(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	const workers = 8
	var wg sync.WaitGroup
	ids := make(chan uint, workers)
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 8; attempt++ {
				sess, err := svc.OpenTableExtended(openInput(table.ID))
				if err == nil {
					ids <- sess.ID
					return
				}
				if strings.Contains(strings.ToLower(err.Error()), "locked") ||
					strings.Contains(strings.ToLower(err.Error()), "deadlock") {
					time.Sleep(time.Millisecond * time.Duration(attempt+1))
					continue
				}
				errs <- err
				return
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first uint
	for id := range ids {
		if first == 0 {
			first = id
		} else if id != first {
			t.Fatalf("sesiones distintas en carrera: %d vs %d", first, id)
		}
	}
	if countOpenSessions(t, db, table.ID) != 1 {
		t.Fatalf("esperaba 1 sesión open tras carrera concurrente")
	}
}

func TestAddOrder_multipleOrdersSameSession(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	item := NewOrderItem{
		ProductCode: "P1", ProductName: "Ceviche", Quantity: 1, UnitPrice: 25,
	}
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{item}, "ronda 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{item}, "ronda 2"); err != nil {
		t.Fatal(err)
	}
	var orders int64
	db.Model(&database.TenantTableOrder{}).Where("session_id = ?", sess.ID).Count(&orders)
	if orders != 2 {
		t.Fatalf("esperaba 2 pedidos en la misma sesión, got %d", orders)
	}
	var stillOpen database.TenantTableSession
	db.First(&stillOpen, sess.ID)
	if stillOpen.Status != sessionStatusOpen {
		t.Fatalf("sesión debe seguir open, got %s", stillOpen.Status)
	}
}

func TestBillTable_partialPaymentKeepsSessionOpen(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	comanda := database.TenantComanda{
		SessionID: sess.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30,
		Status: "pendiente", IgvAffectationType: "10",
	}
	if err := db.Create(&comanda).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.BillTable(BillInput{
		SessionID:    sess.ID,
		UserID:       1,
		SeriesID:     series.ID,
		DocType:      "03",
		IssueDate:    time.Now(),
		CloseSession: false,
		Payments:     []PaymentInput{{Method: "card", Amount: 35.4}},
	}, tax.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.Status != sessionStatusOpen {
		t.Fatalf("cobro parcial debe dejar sesión open, got %s", after.Status)
	}
	var tbl database.TenantRestaurantTable
	db.First(&tbl, table.ID)
	if tbl.Status != "ocupada" {
		t.Fatalf("mesa debe seguir ocupada tras cobro parcial, got %s", tbl.Status)
	}
}

func TestCloseSessionOnly_closesSessionAndFreesTable(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CloseSessionOnly(sess.ID); err != nil {
		t.Fatal(err)
	}
	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.Status != "closed" {
		t.Fatalf("sesión debe quedar closed, got %s", after.Status)
	}
	if countOpenSessions(t, db, table.ID) != 0 {
		t.Fatal("no debe quedar sesión open")
	}
	var tbl database.TenantRestaurantTable
	db.First(&tbl, table.ID)
	if tbl.Status != "libre" {
		t.Fatalf("mesa debe quedar libre, got %s", tbl.Status)
	}
}

func TestBillTable_fullPaymentClosesSession(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	comanda := database.TenantComanda{
		SessionID: sess.ID, ProductCode: "P1", ProductName: "Arroz", Quantity: 1, UnitPrice: 20,
		Status: "pendiente", IgvAffectationType: "10",
	}
	if err := db.Create(&comanda).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.BillTable(BillInput{
		SessionID:    sess.ID,
		UserID:       1,
		SeriesID:     series.ID,
		DocType:      "03",
		IssueDate:    time.Now(),
		CloseSession: true,
		Payments:     []PaymentInput{{Method: "card", Amount: 23.6}},
	}, tax.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.Status != "billed" {
		t.Fatalf("cobro total debe facturar sesión, got %s", after.Status)
	}
	var tbl database.TenantRestaurantTable
	db.First(&tbl, table.ID)
	if tbl.Status != "libre" {
		t.Fatalf("mesa debe quedar libre tras cobro total, got %s", tbl.Status)
	}
}

func TestOpenTableExtended_newSessionAfterClose(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	first, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CloseSessionOnly(first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("tras cierre debe crearse una sesión nueva")
	}
	if countOpenSessions(t, db, table.ID) != 1 {
		t.Fatalf("debe haber exactamente 1 sesión open")
	}
}

func TestListTables_oneRowPerTableWithDuplicateOpenSessions(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	now := time.Now()
	old := database.TenantTableSession{
		TableID: &table.ID, UserID: 1, BranchID: 1, OpenedAt: now.Add(-2 * time.Hour),
		Status: sessionStatusOpen, OrderCode: "P-OLD-0001", OrderType: "dine_in",
	}
	newer := database.TenantTableSession{
		TableID: &table.ID, UserID: 1, BranchID: 1, OpenedAt: now,
		Status: sessionStatusOpen, OrderCode: "P-NEW-0001", OrderType: "dine_in",
	}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&newer).Error; err != nil {
		t.Fatal(err)
	}

	svc := New(db)
	rows, err := svc.ListTables(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var matches int
	var sessionID *uint
	for _, r := range rows {
		if r.ID == table.ID {
			matches++
			sessionID = r.SessionID
		}
	}
	if matches != 1 {
		t.Fatalf("ListTables debe devolver 1 fila por mesa, got %d", matches)
	}
	if sessionID == nil || *sessionID != newer.ID {
		t.Fatalf("debe asociar la sesión más reciente id=%d, got %v", newer.ID, sessionID)
	}
}

func TestOpenTableExtended_wrongBranchRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	in := openInput(table.ID)
	in.BranchID = 99
	_, err := svc.OpenTableExtended(in)
	if err == nil {
		t.Fatal("esperaba error al abrir mesa de otra sucursal")
	}
	if !strings.Contains(err.Error(), "sucursal") {
		t.Fatalf("mensaje inesperado: %v", err)
	}
	if countOpenSessions(t, db, table.ID) != 0 {
		t.Fatal("no debe crearse sesión con sucursal incorrecta")
	}
}

func TestAddOrder_afterSessionBilledRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	comanda := database.TenantComanda{
		SessionID: sess.ID, ProductCode: "P1", ProductName: "Arroz", Quantity: 1, UnitPrice: 20,
		Status: "pendiente", IgvAffectationType: "10",
	}
	if err := db.Create(&comanda).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.BillTable(BillInput{
		SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03",
		IssueDate: time.Now(), CloseSession: true,
		Payments:  []PaymentInput{{Method: "card", Amount: 23.6}},
	}, tax.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	item := NewOrderItem{ProductCode: "P2", ProductName: "Postre", Quantity: 1, UnitPrice: 10}
	_, err = svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{item}, "tras cobro")
	if err == nil {
		t.Fatal("esperaba error al agregar pedido en sesión facturada")
	}
	if !strings.Contains(err.Error(), "cerrada") {
		t.Fatalf("mensaje inesperado: %v", err)
	}
	var orders int64
	db.Model(&database.TenantTableOrder{}).Where("session_id = ?", sess.ID).Count(&orders)
	if orders != 0 {
		t.Fatalf("no debe crearse pedido tras facturar, got %d", orders)
	}
}

// TestBillTable_concurrentOnlyOneSucceeds exige que dos cobros simultáneos de la misma sesión
// dejen una sola venta. En MySQL/InnoDB eso lo garantizan el FOR UPDATE y el UPDATE guardado
// (RowsAffected == 0) de BillTable, pero sobre SQLite el test no sirve para demostrarlo:
//
//   - Con el DSN actual (mode=memory&cache=shared) falla ~1 de cada 14 corridas con
//     "all goroutines are asleep - deadlock!": los workers se quedan en el mutex de
//     unlock_notify de glebarez/go-sqlite, un camino que solo existe con cache=shared.
//   - Con un DSN de archivo (busy_timeout + WAL) el flake desaparece, pero el test pasa
//     igual aunque se borre el UPDATE guardado: el locking de archivo serializa tanto que
//     los perdedores reintentan desde cero y salen por el chequeo de status previo.
//
// O sea: sobre SQLite es flaky o es vacuo. Se salta hasta tenerlo como test de integración
// contra MySQL real, que es el único motor donde el guard se ejercita de verdad.
//
// Ojo: requiere -count=1. La BD in-memory de cache=shared sobrevive entre iteraciones y el
// seed de tenant_payment_methods choca con su índice UNIQUE.
func TestBillTable_concurrentOnlyOneSucceeds(t *testing.T) {
	t.Skip("requiere semántica de locking de MySQL/InnoDB; sobre SQLite es flaky (unlock_notify) o vacuo (DSN de archivo)")

	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	comanda := database.TenantComanda{
		SessionID: sess.ID, ProductCode: "P1", ProductName: "Pollo", Quantity: 1, UnitPrice: 20,
		Status: "pendiente", IgvAffectationType: "10",
	}
	if err := db.Create(&comanda).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}

	billInput := BillInput{
		SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03",
		IssueDate: time.Now(), CloseSession: true,
		Payments:  []PaymentInput{{Method: "card", Amount: 23.6}},
	}
	taxCfg := tax.DefaultConfig()

	const workers = 4
	var wg sync.WaitGroup
	okCh := make(chan bool, workers)
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 8; attempt++ {
				_, err := svc.BillTable(billInput, taxCfg)
				if err == nil {
					okCh <- true
					return
				}
				if strings.Contains(strings.ToLower(err.Error()), "cerrada") ||
					strings.Contains(strings.ToLower(err.Error()), "facturada") {
					errCh <- err
					return
				}
				if strings.Contains(strings.ToLower(err.Error()), "locked") ||
					strings.Contains(strings.ToLower(err.Error()), "deadlock") {
					time.Sleep(time.Millisecond * time.Duration(attempt+1))
					continue
				}
				errCh <- err
				return
			}
		}()
	}
	wg.Wait()
	close(okCh)
	close(errCh)

	var successes, failures int
	for range okCh {
		successes++
	}
	for range errCh {
		failures++
	}
	if successes != 1 {
		t.Fatalf("esperaba exactamente 1 cobro exitoso, got %d", successes)
	}
	if failures != workers-1 {
		t.Fatalf("esperaba %d cobros rechazados, got %d", workers-1, failures)
	}

	var sales int64
	db.Model(&database.TenantSale{}).Where("restaurant_session_id = ?", sess.ID).Count(&sales)
	if sales != 1 {
		t.Fatalf("debe existir una sola venta, got %d", sales)
	}
	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.Status != "billed" {
		t.Fatalf("sesión debe quedar billed, got %s", after.Status)
	}
	var tbl database.TenantRestaurantTable
	db.First(&tbl, table.ID)
	if tbl.Status != "libre" {
		t.Fatalf("mesa debe quedar libre, got %s", tbl.Status)
	}
}
