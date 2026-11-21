package service

import (
	"testing"
	"time"

	posvc "tukifac/internal/purchaseorders/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// 1 orden de compra → 1 compra: la compra marca la orden como recibida en la misma transacción,
// una segunda compra sobre la misma orden falla sin dejar rastro, y anular la compra libera la orden.
func TestPurchaseFromOrder_LinksBlocksSecondAndReleasesOnVoid(t *testing.T) {
	db := setupPurchaseServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantPurchaseOrder{}, &database.TenantPurchaseOrderItem{}); err != nil {
		t.Fatal(err)
	}
	newOpenCashSession(t, db, 1, 1)
	pid := newPurchaseProduct(t, db, "P-OC")
	order := &database.TenantPurchaseOrder{BranchID: 1, UserID: 1, SeriesID: 1, Series: "OC01", Correlative: 1, Number: "OC01-00000001",
		IssueDate: time.Now(), Currency: "PEN", Total: 118, Status: posvc.StatusSent}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewPurchaseService(db)
	mk := func(number string) (*database.TenantPurchase, error) {
		return svc.Create(CreatePurchaseInput{
			BranchID: 1, UserID: 1, DocType: "FACTURA", Series: "F001", Number: number, IssueDate: time.Now(), PaymentMethod: "",
			Items:     []PurchaseItemInput{{ProductID: &pid, Description: "item", Quantity: 1, UnitCost: 100, IgvAffectationType: "10"}},
			TaxConfig: tax.Config{TaxRate: 18}, PurchaseOrderID: &order.ID,
		})
	}
	p1, err := mk("1")
	if err != nil {
		t.Fatal(err)
	}
	if p1.PurchaseOrderID == nil || *p1.PurchaseOrderID != order.ID {
		t.Errorf("la compra debe guardar purchase_order_id: %+v", p1.PurchaseOrderID)
	}
	var got database.TenantPurchaseOrder
	db.First(&got, order.ID)
	if got.Status != posvc.StatusReceived || got.PurchaseID == nil || *got.PurchaseID != p1.ID {
		t.Fatalf("orden tras la compra: %+v", got)
	}

	if _, err := mk("2"); err == nil {
		t.Fatal("una segunda compra sobre la misma orden debe fallar")
	}
	var n int64
	db.Model(&database.TenantPurchase{}).Where("number = ?", "2").Count(&n)
	if n != 0 {
		t.Errorf("la compra rechazada no debe persistir (rollback), hay %d", n)
	}

	if err := svc.Void(p1.ID, 1); err != nil {
		t.Fatal(err)
	}
	db.First(&got, order.ID)
	if got.Status != posvc.StatusSent || got.PurchaseID != nil {
		t.Errorf("anular la compra libera la orden: %+v", got)
	}
}
