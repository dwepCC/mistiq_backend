package service

import (
	"testing"

	"tukifac/pkg/database"
)

// TestCancelComanda_comboCancelledOneByOne_totalReturnsToZero reproduce el bug reportado: se
// agrega un combo (su precio fijo se suma UNA vez a total_amount), y se anula cada componente
// por separado (como hace el botón "Anular" de a uno). Antes de este fix, cada anulación
// descontaba el UnitPrice propio del componente (0), así que el precio del combo quedaba
// pegado en la mesa para siempre aunque no quedara ninguna comanda activa.
func TestCancelComanda_comboCancelledOneByOne_totalReturnsToZero(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}

	comboID := f.Combo.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
		ProductID: &comboID, Quantity: 1,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
	}}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var afterAdd database.TenantTableSession
	db.First(&afterAdd, sess.ID)
	if afterAdd.TotalAmount != 18 {
		t.Fatalf("total tras agregar el combo = %.2f, want 18.00", afterAdd.TotalAmount)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Order("id ASC").Find(&comandas)
	if len(comandas) != 2 {
		t.Fatalf("esperaba 2 comandas (pollo + agua), got %d", len(comandas))
	}

	// Anular el primer componente: el combo sigue teniendo un componente activo, así que
	// todavía NO debe descontar nada (el precio del combo no es de este componente).
	if err := svc.CancelComanda(comandas[0].ID, "no le gustó", 1); err != nil {
		t.Fatalf("CancelComanda (1er componente): %v", err)
	}
	var afterFirst database.TenantTableSession
	db.First(&afterFirst, sess.ID)
	if afterFirst.TotalAmount != 18 {
		t.Fatalf("total tras anular solo 1 de 2 componentes = %.2f, want 18.00 (aún no se anula el combo entero)", afterFirst.TotalAmount)
	}

	// Anular el último componente activo: ahí sí se descuenta el precio del combo, una sola vez.
	if err := svc.CancelComanda(comandas[1].ID, "no le gustó", 1); err != nil {
		t.Fatalf("CancelComanda (2do componente): %v", err)
	}
	var afterSecond database.TenantTableSession
	db.First(&afterSecond, sess.ID)
	if afterSecond.TotalAmount != 0 {
		t.Fatalf("total tras anular todo el combo = %.2f, want 0.00 (antes del fix quedaba pegado en 18.00)", afterSecond.TotalAmount)
	}
}

// TestCancelAllComandas_comboCancelled_totalReturnsToZero: mismo escenario pero vía "Anular
// todo" (CancelAllComandas), el camino de anulación masiva.
func TestCancelAllComandas_comboCancelled_totalReturnsToZero(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	if err := db.AutoMigrate(&database.TenantRestaurantSetting{}); err != nil {
		t.Fatal(err)
	}
	const testPin = "1234"
	if err := svc.SaveRestaurantSettings(testPin); err != nil {
		t.Fatal(err)
	}

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	comboID := f.Combo.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
		ProductID: &comboID, Quantity: 1,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
	}}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	if _, err := svc.CancelAllComandas(sess.ID, nil, testPin, "cliente se retiró", 1); err != nil {
		t.Fatalf("CancelAllComandas: %v", err)
	}

	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.TotalAmount != 0 {
		t.Fatalf("total tras anular todo (bulk) = %.2f, want 0.00", after.TotalAmount)
	}
}
