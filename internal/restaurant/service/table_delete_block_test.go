package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Una mesa libre cuya sesión ya fue facturada (billed) DEBE poder eliminarse, aunque su
// pedido siga marcado 'active'. Este era el bug: la regla usaba 'billed' (terminada) en vez
// de 'billing' (en curso), y con los pedidos huérfanos ninguna mesa cobrada se podía borrar.
func TestTableDeleteBlock_billedSessionDoesNotBlock(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess := &database.TenantTableSession{
		TableID: &table.ID, BranchID: 1, UserID: 1,
		Status: sessionStatusBilled, OpenedAt: time.Now(),
	}
	if err := db.Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	// Pedido huérfano: quedó 'active' pese a estar la sesión facturada.
	if err := db.Create(&database.TenantTableOrder{
		SessionID: sess.ID, UserID: 1, OrderNumber: 1, Status: tableOrderActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if reason := svc.tableDeleteBlockReason(table); reason != "" {
		t.Fatalf("una mesa con sesión facturada no debe bloquearse: %q", reason)
	}
	if err := svc.DeleteTable(table.ID); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
}

// Una sesión realmente abierta con un pedido activo SÍ bloquea: es una operación en curso.
func TestTableDeleteBlock_openSessionWithActiveOrderBlocks(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess := &database.TenantTableSession{
		TableID: &table.ID, BranchID: 1, UserID: 1,
		Status: sessionStatusOpen, OpenedAt: time.Now(),
	}
	if err := db.Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantTableOrder{
		SessionID: sess.ID, UserID: 1, OrderNumber: 1, Status: tableOrderActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// La mesa ocupada por una sesión open: el estado real la marca ocupada.
	db.Model(table).Update("status", "ocupada")

	if reason := svc.tableDeleteBlockReason(table); reason == "" {
		t.Fatal("una mesa con sesión abierta y pedido activo debe bloquearse")
	}
}

// Una sesión en 'billing' (cobro en proceso) también bloquea: la operación no terminó.
func TestTableDeleteBlock_billingSessionBlocks(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	sess := &database.TenantTableSession{
		TableID: &table.ID, BranchID: 1, UserID: 1,
		Status: sessionStatusBilling, OpenedAt: time.Now(),
	}
	if err := db.Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantTableOrder{
		SessionID: sess.ID, UserID: 1, OrderNumber: 1, Status: tableOrderActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if reason := svc.tableDeleteBlockReason(table); reason == "" {
		t.Fatal("una mesa con cobro en proceso debe bloquearse")
	}
}

// La limpieza cancela ventas rápidas abandonadas (open, sin mesa, sin venta, de días
// anteriores) y sus comandas, sin tocar las mesas ocupadas del día.
func TestCleanupAbandonedQuickSales(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)
	ayer := time.Now().AddDate(0, 0, -2)
	hoy := time.Now()

	// Abandonada: open, sin mesa, sin venta, de anteayer → se cancela.
	aband := &database.TenantTableSession{
		BranchID: 1, UserID: 1, Status: "open", OrderType: "quick_sale", OpenedAt: ayer,
	}
	db.Create(aband)
	db.Create(&database.TenantComanda{OrderID: 1, SessionID: aband.ID, ProductName: "X", Status: "lista"})

	// Del día (open, sin mesa, sin venta pero de HOY) → NO se toca.
	hoySess := &database.TenantTableSession{
		BranchID: 1, UserID: 1, Status: "open", OrderType: "quick_sale", OpenedAt: hoy,
	}
	db.Create(hoySess)
	db.Create(&database.TenantComanda{OrderID: 2, SessionID: hoySess.ID, ProductName: "Y", Status: "pendiente"})

	// Mesa ocupada (con table_id) → NO se toca aunque sea vieja.
	mesaSess := &database.TenantTableSession{
		TableID: &table.ID, BranchID: 1, UserID: 1, Status: "open", OrderType: "dine_in", OpenedAt: ayer,
	}
	db.Create(mesaSess)

	n, err := svc.CleanupAbandonedQuickSales()
	if err != nil {
		t.Fatalf("CleanupAbandonedQuickSales: %v", err)
	}
	if n != 1 {
		t.Fatalf("sesiones canceladas = %d want 1", n)
	}

	var abandStatus, hoyStatus, mesaStatus string
	db.Model(&database.TenantTableSession{}).Where("id=?", aband.ID).Pluck("status", &abandStatus)
	db.Model(&database.TenantTableSession{}).Where("id=?", hoySess.ID).Pluck("status", &hoyStatus)
	db.Model(&database.TenantTableSession{}).Where("id=?", mesaSess.ID).Pluck("status", &mesaStatus)
	if abandStatus != "cancelled" {
		t.Errorf("la abandonada debe quedar cancelled: %s", abandStatus)
	}
	if hoyStatus != "open" {
		t.Errorf("la del día NO debe tocarse: %s", hoyStatus)
	}
	if mesaStatus != "open" {
		t.Errorf("la mesa ocupada NO debe tocarse: %s", mesaStatus)
	}

	var comandasVivas int64
	db.Model(&database.TenantComanda{}).Where("session_id=? AND cancelled_at IS NULL", aband.ID).Count(&comandasVivas)
	if comandasVivas != 0 {
		t.Errorf("la comanda de la abandonada debe quedar cancelada: %d vivas", comandasVivas)
	}
}
