package service

import "testing"

import "tukifac/pkg/database"

// La lista de cuentas por pagar muestra al proveedor por su razón social; solo si no la tiene usa el nombre comercial. (Antes un nombre
// comercial vacío —'' y no NULL— se mostraba como «—», y uno con valor le ganaba a la razón social.)
func TestList_ProveedorSeMuestraPorRazonSocial(t *testing.T) {
	db := setupPayableServiceTestDB(t)
	mk := func(business, trade string) uint {
		c := database.TenantContact{Type: "supplier", DocType: "6", DocNumber: "20" + business + trade, BusinessName: business, TradeName: trade, Active: true}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	cases := map[string]struct{ business, trade, want string }{
		"sin comercial":           {"SUNAT RAZON SOCIAL", "", "SUNAT RAZON SOCIAL"},
		"con comercial":           {"TOTOCAYO CAYLLAHUA ANGEL", "angel", "TOTOCAYO CAYLLAHUA ANGEL"},
		"sin razón, con comercial": {"", "BODEGA DON PEPE", "BODEGA DON PEPE"},
	}
	byPurchase := map[uint]string{}
	n := 0
	for name, c := range cases {
		n++
		purchase := seedCreditPurchase(t, db, 1, 20, 100)
		cid := mk(c.business, c.trade)
		db.Model(purchase).Updates(map[string]interface{}{"contact_id": cid, "number": name})
		byPurchase[purchase.ID] = c.want
	}
	rows, _, err := NewPayableService(db).List(ListFilter{Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != n {
		t.Fatalf("filas=%d, quiere %d", len(rows), n)
	}
	for _, r := range rows {
		if r.ContactName != byPurchase[r.PurchaseID] {
			t.Errorf("compra %d: proveedor %q, quiere %q", r.PurchaseID, r.ContactName, byPurchase[r.PurchaseID])
		}
	}
}
