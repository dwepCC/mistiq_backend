package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

func seedSplitBillSeries(t *testing.T, db *gorm.DB) *database.TenantDocumentSeries {
	t.Helper()
	series := &database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(series).Error; err != nil {
		t.Fatal(err)
	}
	return series
}

func TestBillTable_splitByComandaIDs_partialKeepsSessionOpen(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	c1 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30, Status: "pendiente", IgvAffectationType: "10"}
	c2 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P2", ProductName: "Chicha", Quantity: 1, UnitPrice: 8, Status: "pendiente", IgvAffectationType: "10"}
	if err := db.Create(&c1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&c2).Error; err != nil {
		t.Fatal(err)
	}
	series := seedSplitBillSeries(t, db)

	_, err = svc.BillTable(BillInput{
		SessionID:  sess.ID,
		UserID:     1,
		SeriesID:   series.ID,
		DocType:    "03",
		IssueDate:  time.Now(),
		ComandaIDs: []uint{c1.ID},
		Payments:   []PaymentInput{{Method: "card", Amount: 35.4}},
	}, tax.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.Status != sessionStatusOpen {
		t.Fatalf("cobro parcial por selección debe dejar sesión open, got %s", after.Status)
	}
	var tbl database.TenantRestaurantTable
	db.First(&tbl, table.ID)
	if tbl.Status != "ocupada" {
		t.Fatalf("mesa debe seguir ocupada, got %s", tbl.Status)
	}

	var billedC1, pendingC2 database.TenantComanda
	db.First(&billedC1, c1.ID)
	db.First(&pendingC2, c2.ID)
	if billedC1.BilledAt == nil {
		t.Fatal("c1 debe quedar marcada como facturada (billed_at)")
	}
	if pendingC2.BilledAt != nil {
		t.Fatal("c2 no debe quedar facturada: no estaba en la selección")
	}
	// El status de cocina no se toca por facturar — sigue "pendiente".
	if billedC1.Status != "pendiente" {
		t.Fatalf("facturar no debe tocar el status de cocina, got %s", billedC1.Status)
	}
}

func TestBillTable_splitByComandaIDs_coveringAllPendingClosesSession(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	c1 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30, Status: "pendiente", IgvAffectationType: "10"}
	if err := db.Create(&c1).Error; err != nil {
		t.Fatal(err)
	}
	series := seedSplitBillSeries(t, db)

	// CloseSession=false explícito: el servidor debe ignorarlo porque la selección cubre TODO
	// lo pendiente de la sesión.
	_, err = svc.BillTable(BillInput{
		SessionID:    sess.ID,
		UserID:       1,
		SeriesID:     series.ID,
		DocType:      "03",
		IssueDate:    time.Now(),
		CloseSession: false,
		ComandaIDs:   []uint{c1.ID},
		Payments:     []PaymentInput{{Method: "card", Amount: 35.4}},
	}, tax.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.Status != "billed" {
		t.Fatalf("selección que cubre todo lo pendiente debe cerrar la sesión igual, got %s", after.Status)
	}
	var tbl database.TenantRestaurantTable
	db.First(&tbl, table.ID)
	if tbl.Status != "libre" {
		t.Fatalf("mesa debe quedar libre, got %s", tbl.Status)
	}
}

func TestBillTable_splitByComandaIDs_partialComboRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	// Dos componentes del mismo combo (mismo ComboParentKey) + un ítem suelto.
	combo1 := database.TenantComanda{
		SessionID: sess.ID, ProductCode: "POLLO", ProductName: "Pollo (combo)", Quantity: 1, UnitPrice: 0,
		Status: "pendiente", IgvAffectationType: "10", ComboParentKey: "combo-abc",
	}
	combo2 := database.TenantComanda{
		SessionID: sess.ID, ProductCode: "GASEOSA", ProductName: "Gaseosa (combo)", Quantity: 1, UnitPrice: 0,
		Status: "pendiente", IgvAffectationType: "10", ComboParentKey: "combo-abc",
	}
	suelto := database.TenantComanda{
		SessionID: sess.ID, ProductCode: "P1", ProductName: "Postre", Quantity: 1, UnitPrice: 10,
		Status: "pendiente", IgvAffectationType: "10",
	}
	for _, c := range []*database.TenantComanda{&combo1, &combo2, &suelto} {
		if err := db.Create(c).Error; err != nil {
			t.Fatal(err)
		}
	}
	series := seedSplitBillSeries(t, db)

	// Selecciona solo un componente del combo (combo1) + el ítem suelto: debe rechazar.
	_, err = svc.BillTable(BillInput{
		SessionID:  sess.ID,
		UserID:     1,
		SeriesID:   series.ID,
		DocType:    "03",
		IssueDate:  time.Now(),
		ComandaIDs: []uint{combo1.ID, suelto.ID},
		Payments:   []PaymentInput{{Method: "card", Amount: 100}},
	}, tax.DefaultConfig())
	if err == nil {
		t.Fatal("esperaba error al partir un combo entre selecciones")
	}
	if !strings.Contains(err.Error(), "combo") {
		t.Fatalf("mensaje inesperado: %v", err)
	}

	// Ninguna comanda debe haber quedado marcada como facturada tras el rechazo.
	var c1, c2, s1 database.TenantComanda
	db.First(&c1, combo1.ID)
	db.First(&c2, combo2.ID)
	db.First(&s1, suelto.ID)
	if c1.BilledAt != nil || c2.BilledAt != nil || s1.BilledAt != nil {
		t.Fatal("nada debe quedar facturado tras un rechazo por combo partido")
	}
}

func TestBillTable_splitByComandaIDs_alreadyBilledRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	c1 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30, Status: "pendiente", IgvAffectationType: "10"}
	c2 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P2", ProductName: "Chicha", Quantity: 1, UnitPrice: 8, Status: "pendiente", IgvAffectationType: "10"}
	if err := db.Create(&c1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&c2).Error; err != nil {
		t.Fatal(err)
	}
	series := seedSplitBillSeries(t, db)

	if _, err := svc.BillTable(BillInput{
		SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03", IssueDate: time.Now(),
		ComandaIDs: []uint{c1.ID}, Payments: []PaymentInput{{Method: "card", Amount: 35.4}},
	}, tax.DefaultConfig()); err != nil {
		t.Fatal(err)
	}

	// Reintentar cobrar c1 (ya facturada) junto con c2: debe rechazar por completo.
	_, err = svc.BillTable(BillInput{
		SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03", IssueDate: time.Now(),
		ComandaIDs: []uint{c1.ID, c2.ID}, Payments: []PaymentInput{{Method: "card", Amount: 45}},
	}, tax.DefaultConfig())
	if err == nil {
		t.Fatal("esperaba error al reintentar cobrar una comanda ya facturada")
	}

	var pendingC2 database.TenantComanda
	db.First(&pendingC2, c2.ID)
	if pendingC2.BilledAt != nil {
		t.Fatal("c2 no debe quedar facturada tras el rechazo (todo o nada)")
	}
}

func TestCancelComanda_alreadyBilledRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	// Dos comandas: solo c1 se cobra, así la sesión sigue open y c1 sobrevive como facturada
	// (si la selección cubriera todo lo pendiente, la mesa se cerraría y las comandas se borran).
	c1 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P1", ProductName: "Lomo", Quantity: 1, UnitPrice: 30, Status: "pendiente", IgvAffectationType: "10"}
	c2 := database.TenantComanda{SessionID: sess.ID, ProductCode: "P2", ProductName: "Chicha", Quantity: 1, UnitPrice: 8, Status: "pendiente", IgvAffectationType: "10"}
	if err := db.Create(&c1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&c2).Error; err != nil {
		t.Fatal(err)
	}
	series := seedSplitBillSeries(t, db)

	if _, err := svc.BillTable(BillInput{
		SessionID: sess.ID, UserID: 1, SeriesID: series.ID, DocType: "03", IssueDate: time.Now(),
		ComandaIDs: []uint{c1.ID}, Payments: []PaymentInput{{Method: "card", Amount: 35.4}},
	}, tax.DefaultConfig()); err != nil {
		t.Fatal(err)
	}

	err = svc.CancelComanda(c1.ID, "cliente se arrepintió", 1)
	if err == nil {
		t.Fatal("esperaba error al anular una comanda ya facturada")
	}
	if !strings.Contains(err.Error(), "facturada") {
		t.Fatalf("mensaje inesperado: %v", err)
	}
}
