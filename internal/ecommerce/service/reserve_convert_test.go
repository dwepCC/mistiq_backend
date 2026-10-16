package service

import (
	"strings"
	"testing"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// TestReserva_VentaDeMostradorRespetaLoReservado: un pedido confirmado aparta su mercadería; el
// mostrador solo puede vender lo disponible, y al convertir el propio pedido su reserva no cuenta
// contra sí misma — la venta descuenta el stock de verdad y la reserva desaparece.
func TestReserva_VentaDeMostradorRespetaLoReservado(t *testing.T) {
	db := setupConvertTestDB(t)
	series := seedNotaVentaSeries(t, db)
	p := database.TenantProduct{
		Code: "RS1", Name: "Producto reservable", Type: "product", Unit: "NIU", SalePrice: 10,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true, ShowInDigitalCatalog: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 4}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	branch := uint(1)
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado, BranchID: &branch}); err != nil {
		t.Fatal(err)
	}

	counterSale := func(qty float64) error {
		_, err := salessvc.NewSaleService(db).Create(salessvc.CreateSaleInput{
			BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00",
			IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
			Payments: []salessvc.PaymentInput{{Method: "cash", Amount: 10 * qty}},
			Items:    []salessvc.SaleItemInput{{ProductID: &p.ID, Quantity: qty, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true, Description: p.Name, UnitPrice: 10}},
		})
		return err
	}
	// Hay 4 en almacén pero 3 reservados: el mostrador solo puede vender 1.
	err = counterSale(2)
	if err == nil || !strings.Contains(err.Error(), "reservados") {
		t.Fatalf("vender 2 con 3 de 4 reservados debe rechazarse por reserva, err=%v", err)
	}
	if err := counterSale(1); err != nil {
		t.Fatalf("vender 1 (lo disponible) debe poder: %v", err)
	}

	// Ahora el almacén tiene 3 y el pedido reservaba 3: al convertir, su propia reserva no estorba.
	convSvc, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := convSvc.ConvertToSale(order.ID, input)
	if err != nil {
		t.Fatalf("convertir el pedido reservado debe poder: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 0 {
		t.Errorf("stock tras vender 1 + convertir 3 = %v, quería 0", stock.Quantity)
	}
	after, _ := svc.GetOrder(order.ID)
	if after.ConvertedSaleID == nil || *after.ConvertedSaleID != sale.ID {
		t.Fatalf("el pedido debe quedar convertido: %v", after.ConvertedSaleID)
	}
}
