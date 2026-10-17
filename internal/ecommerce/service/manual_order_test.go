package service

import (
	"testing"

	"tukifac/pkg/database"
)

func TestPedidoManual_AdmiteProductoNoPublicadoYMarcaOrigen(t *testing.T) {
	db := setupShippingTestDB(t)
	if err := db.AutoMigrate(&database.TenantContact{}); err != nil {
		t.Fatal(err)
	}
	svc := &EcommerceService{db: db}
	hidden := database.TenantProduct{Code: "H1", Name: "Solo mostrador", Type: "product", Unit: "NIU", SalePrice: 20, Active: true, ShowInDigitalCatalog: false}
	if err := db.Create(&hidden).Error; err != nil {
		t.Fatal(err)
	}
	in := CreateOrderInput{
		CustomerName: "Rosa", CustomerPhone: "999222333", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: hidden.ID, Quantity: 2}},
	}
	if _, _, err := svc.CreateOrder(in); err == nil {
		t.Fatal("la tienda no debe poder pedir un producto no publicado")
	}
	in.Manual, in.UserID = true, 7
	o, _, err := svc.CreateOrder(in)
	if err != nil {
		t.Fatalf("el pedido manual debe aceptarlo: %v", err)
	}
	if o.Source != "MANUAL" || o.Total != 40 {
		t.Errorf("source=%q total=%v", o.Source, o.Total)
	}
	var hist database.TenantEcommerceOrderStatusHistory
	db.Where("order_id = ?", o.ID).First(&hist)
	if hist.UserID == nil || *hist.UserID != 7 {
		t.Errorf("el historial debe registrar quién lo creó: %+v", hist)
	}
	var notes int64
	db.Model(&database.TenantNotification{}).Count(&notes)
	if notes != 0 {
		t.Errorf("un pedido manual no genera aviso de «nuevo pedido online» (hay %d)", notes)
	}
	web := newPickupOrder(t, svc, seedSimpleProduct(t, db, "W1", "Web", 5).ID, 1)
	if web.Source != "WEB" {
		t.Errorf("el pedido de la tienda debe ser WEB, es %q", web.Source)
	}
}

func TestPedidoManual_AgenciaOcultaEnTiendaYClienteInexistente(t *testing.T) {
	db := setupShippingTestDB(t)
	if err := db.AutoMigrate(&database.TenantContact{}); err != nil {
		t.Fatal(err)
	}
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "A1", "Prod", 10)
	car := database.TenantCarrier{Name: "Interna", Kind: "AGENCIA", Active: true, ShowInStore: false}
	if err := db.Create(&car).Error; err != nil {
		t.Fatal(err)
	}
	// ShowInStore=false con default:true puede haberse ignorado por GORM (zero value): se fuerza.
	db.Model(&car).Update("show_in_store", false)
	in := CreateOrderInput{
		CustomerName: "Rosa", CustomerPhone: "999222333", DeliveryMethod: DeliveryMethodAgency,
		CarrierID: &car.ID, DestinationUbigeo: "150101", DestinationAgency: "Lima Centro",
		RecipientDocType: "1", RecipientDocNumber: "40123456",
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	}
	if _, _, err := svc.CreateOrder(in); err == nil {
		t.Fatal("la tienda no debe poder usar una empresa oculta")
	}
	in.Manual = true
	if _, _, err := svc.CreateOrder(in); err != nil {
		t.Fatalf("el pedido manual debe poder usarla: %v", err)
	}
	ghost := uint(9999)
	in.ContactID = &ghost
	if _, _, err := svc.CreateOrder(in); err == nil {
		t.Fatal("un cliente inexistente debe rechazarse")
	}
}
