package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

func TestMoveSessionTable_freeDestinationMoves(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	var floor database.TenantRestaurantFloor
	if err := db.First(&floor, table.FloorID).Error; err != nil {
		t.Fatal(err)
	}
	dest := &database.TenantRestaurantTable{
		BranchID: 1, FloorID: floor.ID, Name: "Mesa 6", Capacity: 4, Status: "libre", Active: true,
	}
	if err := db.Create(dest).Error; err != nil {
		t.Fatal(err)
	}

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.MoveSessionTable(sess.ID, dest.ID, 1); err != nil {
		t.Fatal(err)
	}

	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.TableID == nil || *after.TableID != dest.ID {
		t.Fatalf("esperaba table_id=%d, got %v", dest.ID, after.TableID)
	}

	var oldTbl, newTbl database.TenantRestaurantTable
	db.First(&oldTbl, table.ID)
	db.First(&newTbl, dest.ID)
	if oldTbl.Status != "libre" {
		t.Fatalf("mesa origen debe quedar libre, got %s", oldTbl.Status)
	}
	if newTbl.Status != "ocupada" {
		t.Fatalf("mesa destino debe quedar ocupada, got %s", newTbl.Status)
	}
	if countOpenSessions(t, db, table.ID) != 0 {
		t.Fatal("no debe quedar sesión open en la mesa origen")
	}
	if countOpenSessions(t, db, dest.ID) != 1 {
		t.Fatal("debe haber 1 sesión open en la mesa destino")
	}
}

func TestMoveSessionTable_occupiedDestinationRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	var floor database.TenantRestaurantFloor
	if err := db.First(&floor, table.FloorID).Error; err != nil {
		t.Fatal(err)
	}
	dest := &database.TenantRestaurantTable{
		BranchID: 1, FloorID: floor.ID, Name: "Mesa 6", Capacity: 4, Status: "libre", Active: true,
	}
	if err := db.Create(dest).Error; err != nil {
		t.Fatal(err)
	}

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	// La mesa destino ya tiene su propia sesión abierta.
	if _, err := svc.OpenTableExtended(openInput(dest.ID)); err != nil {
		t.Fatal(err)
	}

	err = svc.MoveSessionTable(sess.ID, dest.ID, 1)
	if err == nil {
		t.Fatal("esperaba error al mover a una mesa ocupada")
	}
	if !strings.Contains(err.Error(), "ocupada") {
		t.Fatalf("mensaje inesperado: %v", err)
	}

	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.TableID == nil || *after.TableID != table.ID {
		t.Fatalf("la sesión no debe haberse movido, table_id=%v", after.TableID)
	}
}

func TestMoveSessionTable_wrongBranchRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	var floor database.TenantRestaurantFloor
	if err := db.First(&floor, table.FloorID).Error; err != nil {
		t.Fatal(err)
	}
	dest := &database.TenantRestaurantTable{
		BranchID: 2, FloorID: floor.ID, Name: "Mesa Sucursal 2", Capacity: 4, Status: "libre", Active: true,
	}
	if err := db.Create(dest).Error; err != nil {
		t.Fatal(err)
	}

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}

	err = svc.MoveSessionTable(sess.ID, dest.ID, 1)
	if err == nil {
		t.Fatal("esperaba error al mover a una mesa de otra sucursal")
	}
	if !strings.Contains(err.Error(), "sucursal") {
		t.Fatalf("mensaje inesperado: %v", err)
	}
}

func TestMoveSessionTable_closedSessionRejected(t *testing.T) {
	db, table := setupTableSessionTestDB(t)
	svc := New(db)

	var floor database.TenantRestaurantFloor
	if err := db.First(&floor, table.FloorID).Error; err != nil {
		t.Fatal(err)
	}
	dest := &database.TenantRestaurantTable{
		BranchID: 1, FloorID: floor.ID, Name: "Mesa 6", Capacity: 4, Status: "libre", Active: true,
	}
	if err := db.Create(dest).Error; err != nil {
		t.Fatal(err)
	}

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CloseSessionOnly(sess.ID); err != nil {
		t.Fatal(err)
	}

	err = svc.MoveSessionTable(sess.ID, dest.ID, 1)
	if err == nil {
		t.Fatal("esperaba error al mover una sesión cerrada")
	}
	if !strings.Contains(err.Error(), "cerrado") {
		t.Fatalf("mensaje inesperado: %v", err)
	}
}
