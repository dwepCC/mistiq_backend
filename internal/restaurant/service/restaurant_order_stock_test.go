package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"
)

// Al AGREGAR un pedido se valida el stock (por presentación si el producto las tiene). Antes solo
// se validaba al cobrar: se podía pedir 5 teniendo 4, la comanda salía a cocina y recién al cobrar
// fallaba con un genérico "stock insuficiente".

func variantJSON(presentationID uint, name string, price float64) string {
	b, _ := json.Marshal([]map[string]interface{}{{
		"group_id": 0, "group_name": "Presentación", "type": "variant", "group_type": "variant",
		"option_id": presentationID, "option_name": name, "extra_price": price, "snapshot": true,
	}})
	return string(b)
}

func TestAddOrder_presentacionSinStockSuficiente_seRechazaAlPedir(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	svc := New(db)

	pizza := database.TenantProduct{
		Code: "PIZZA", Name: "Pizza QA", Type: "product", Unit: "NIU", SalePrice: 20,
		IgvAffectationType: "10", PriceIncludesIgv: true, IsRestaurant: true, BranchID: 1, Active: true,
		ManageStock: true, HasVariants: true,
	}
	if err := db.Create(&pizza).Error; err != nil {
		t.Fatal(err)
	}
	personal := database.TenantProductPresentation{ProductID: pizza.ID, Name: "Personal", SalePrice: 20, Active: true}
	familiar := database.TenantProductPresentation{ProductID: pizza.ID, Name: "Familiar", SalePrice: 40, Active: true}
	db.Create(&personal)
	db.Create(&familiar)
	db.Create(&database.TenantProductPresentationStock{PresentationID: personal.ID, BranchID: 1, Quantity: 10})
	db.Create(&database.TenantProductPresentationStock{PresentationID: familiar.ID, BranchID: 1, Quantity: 4})

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}
	pid := pizza.ID
	order := func(pres database.TenantProductPresentation, qty float64) error {
		_, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
			ProductID: &pid, Quantity: qty, UnitPrice: pres.SalePrice,
			ModifiersJSON: variantJSON(pres.ID, pres.Name, pres.SalePrice),
		}}, "")
		return err
	}

	// 5 Familiar con 4 en stock: rechazado, y el mensaje dice producto, presentación y cantidades.
	err = order(familiar, 5)
	if err == nil {
		t.Fatal("esperaba rechazo: se piden 5 Familiar y hay 4")
	}
	for _, want := range []string{"Pizza QA", "Familiar", "pides 5", "disponible 4"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("el mensaje %q debe contener %q", err.Error(), want)
		}
	}
	var n int64
	db.Model(&database.TenantComanda{}).Where("session_id = ?", sess.ID).Count(&n)
	if n != 0 {
		t.Errorf("un pedido rechazado no debe dejar comandas, hay %d", n)
	}

	// El stock de OTRA presentación no cubre: 5 Personal sí alcanzan (hay 10).
	if err := order(personal, 5); err != nil {
		t.Fatalf("5 Personal con 10 en stock debe pasar: %v", err)
	}

	// 3 Familiar pasan (hay 4) ...
	if err := order(familiar, 3); err != nil {
		t.Fatalf("3 Familiar con 4 en stock debe pasar: %v", err)
	}
	// ... pero otros 2 en la MISMA mesa ya no (3 pedidas + 2 > 4).
	err = order(familiar, 2)
	if err == nil {
		t.Fatal("esperaba rechazo: 3 ya pedidas + 2 supera el stock de 4")
	}
	if !strings.Contains(err.Error(), "ya pedidas en la mesa: 3") {
		t.Errorf("el mensaje debe indicar lo ya pedido en la mesa: %q", err.Error())
	}
	// Y exactamente 1 más sí.
	if err := order(familiar, 1); err != nil {
		t.Fatalf("la 4.ª unidad debe pasar: %v", err)
	}
}

func TestAddOrder_productoSinPresentaciones_validaStockDelProducto(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	svc := New(db)
	p := database.TenantProduct{
		Code: "CAFE", Name: "Café", Type: "product", Unit: "NIU", SalePrice: 5,
		IgvAffectationType: "10", PriceIncludesIgv: true, IsRestaurant: true, BranchID: 1, Active: true, ManageStock: true,
	}
	db.Create(&p)
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 2})
	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	pid := p.ID

	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{ProductID: &pid, Quantity: 3, UnitPrice: 5}}, ""); err == nil {
		t.Fatal("esperaba rechazo: 3 cafés con 2 en stock")
	} else if !strings.Contains(err.Error(), fmt.Sprintf("«%s»", p.Name)) {
		t.Errorf("el mensaje debe nombrar el producto: %q", err.Error())
	}
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{ProductID: &pid, Quantity: 2, UnitPrice: 5}}, ""); err != nil {
		t.Fatalf("2 cafés con 2 en stock debe pasar: %v", err)
	}
}

func TestAddOrder_productoSinControlDeStock_noSeValida(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	svc := New(db)
	p := database.TenantProduct{
		Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 2,
		IgvAffectationType: "10", PriceIncludesIgv: true, IsRestaurant: true, BranchID: 1, Active: true, ManageStock: false,
	}
	db.Create(&p)
	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	pid := p.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{ProductID: &pid, Quantity: 50, UnitPrice: 2}}, ""); err != nil {
		t.Fatalf("sin control de stock no debe validarse: %v", err)
	}
}

func TestNameWithPresentation(t *testing.T) {
	v := variantJSON(6, "Familiar", 40)
	cases := []struct{ name, mods, want string }{
		{"Pizza QA", v, "Pizza QA - Familiar"},
		{"Pizza QA", "", "Pizza QA"},
		{"Pizza QA", "no es json", "Pizza QA"},
		{"Pizza familiar", v, "Pizza familiar"}, // ya la incluye
		{"Café", `[{"type":"modifier","option_name":"Extra shot"}]`, "Café"},
	}
	for _, c := range cases {
		if got := nameWithPresentation(c.name, c.mods); got != c.want {
			t.Errorf("nameWithPresentation(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
