package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// La API pública de la tienda no requiere autenticación: no puede revelar el stock por sucursal, los nombres de las
// sucursales ni los números de serie, y con "Mostrar stock" apagado tampoco el stock por presentación.
func TestPublicProducts_DoesNotExposeBranchStockOrSerials(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	b1 := database.TenantBranch{Name: "Sucursal Secreta Norte", Active: true, IsMain: true}
	b2 := database.TenantBranch{Name: "Sucursal Secreta Sur", Active: true}
	db.Create(&b1)
	db.Create(&b2)
	p := database.TenantProduct{Code: "P1", Name: "Zapatilla", Type: "product", Unit: "NIU", Active: true, ShowInDigitalCatalog: true,
		ManageStock: true, ManageSeries: true, HasVariants: true, SalePrice: 100}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: b1.ID, Quantity: 3})
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: b2.ID, Quantity: 4})
	db.Create(&database.TenantProductSerial{ProductID: p.ID, BranchID: b1.ID, Serial: "SERIE-001", Status: "available"})
	pres := database.TenantProductPresentation{ProductID: p.ID, Name: "Talla 42", SalePrice: 100, Active: true}
	db.Create(&pres)
	db.Create(&database.TenantProductPresentationStock{PresentationID: pres.ID, BranchID: b1.ID, Quantity: 1})
	db.Create(&database.TenantProductPresentationStock{PresentationID: pres.ID, BranchID: b2.ID, Quantity: 2})

	svc := NewEcommerceService(db)
	for _, showStock := range []bool{true, false} {
		items, _, err := svc.PublicProducts("", 0, nil, nil, 1, 20, showStock)
		if err != nil || len(items) != 1 {
			t.Fatalf("showStock=%v: items=%d err=%v", showStock, len(items), err)
		}
		raw, _ := json.Marshal(items)
		body := string(raw)
		for _, leak := range []string{"Secreta", "SERIE-001"} {
			if strings.Contains(body, leak) {
				t.Errorf("showStock=%v: la respuesta pública filtra %q: %s", showStock, leak, body)
			}
		}
		var out []map[string]interface{}
		_ = json.Unmarshal(raw, &out)
		if v, ok := out[0]["stock_by_branch"]; ok && v != nil {
			t.Errorf("showStock=%v: stock_by_branch=%v, quiere ausente/null", showStock, v)
		}
		sbp, _ := out[0]["stock_by_presentation"].([]interface{})
		if showStock {
			// Se conserva la disponibilidad por presentación, SUMADA y sin identificar sucursales (1 + 2 = 3).
			if len(sbp) != 1 {
				t.Fatalf("stock_by_presentation: %v, quiere 1 fila agregada", sbp)
			}
			row := sbp[0].(map[string]interface{})
			if row["quantity"].(float64) != 3 {
				t.Errorf("cantidad agregada de la presentación: %v, quiere 3", row["quantity"])
			}
			if row["branch_id"].(float64) != 0 || row["branch_name"] != "" {
				t.Errorf("la fila agregada no debe identificar sucursal: %v", row)
			}
		} else if len(sbp) != 0 {
			t.Errorf("show_stock=false: stock_by_presentation=%v, quiere vacío", sbp)
		}
		if showStock && out[0]["stock_total"].(float64) != 3 {
			t.Errorf("stock_total con show_stock=true: %v, quiere 3 (suma de presentaciones; se conserva)", out[0]["stock_total"])
		}
		if !showStock && out[0]["stock_total"].(float64) != 0 {
			t.Errorf("stock_total con show_stock=false: %v, quiere 0", out[0]["stock_total"])
		}
	}
}
