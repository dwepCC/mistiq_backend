package service

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupOrderOptionsDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{}, &database.TenantProductPresentation{}, &database.TenantProductPresentationStock{},
		&database.TenantProductSaleUnit{}, &database.TenantModifierGroup{}, &database.TenantModifierOption{},
		&database.TenantProductModifierGroup{}, &database.TenantEcommerceOrder{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCreateOrder_PresentationPriceResolvedByServer(t *testing.T) {
	db := setupOrderOptionsDB(t)
	p := database.TenantProduct{Code: "P1", Name: "Polo", Unit: "NIU", SalePrice: 0, Active: true, ShowInDigitalCatalog: true, HasVariants: true}
	db.Create(&p)
	m := database.TenantProductPresentation{ProductID: p.ID, Name: "Talla M", SalePrice: 40, Active: true}
	db.Create(&m)
	svc := NewEcommerceService(db)

	if _, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1, UnitPrice: 1}}}); err == nil || !strings.Contains(err.Error(), "presentación") {
		t.Fatalf("sin presentación debe pedirla, got %v", err)
	}
	// El precio que manda el cliente (1) se ignora: manda el de la presentación.
	o, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 2, UnitPrice: 1, PresentationID: &m.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if o.Total != 80 || !strings.Contains(o.ItemsJSON, "Polo - Talla M") || !strings.Contains(o.ItemsJSON, "presentation_id") {
		t.Fatalf("pedido: %+v", o)
	}
}

func TestCreateOrder_ExtrasAndRequiredGroup(t *testing.T) {
	db := setupOrderOptionsDB(t)
	p := database.TenantProduct{Code: "P2", Name: "Hamburguesa", Unit: "NIU", SalePrice: 10, Active: true, ShowInDigitalCatalog: true, HasModifiers: true}
	db.Create(&p)
	g := database.TenantModifierGroup{Name: "Extras", Required: true, Active: true}
	db.Create(&g)
	o1 := database.TenantModifierOption{GroupID: g.ID, Name: "Queso", ExtraPrice: 2, Active: true}
	db.Create(&o1)
	db.Create(&database.TenantProductModifierGroup{ProductID: p.ID, GroupID: g.ID})
	svc := NewEcommerceService(db)

	if _, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1}}}); err == nil {
		t.Fatal("grupo obligatorio sin elegir debe fallar")
	}
	o, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1, ModifierOptionIDs: []uint{o1.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if o.Total != 12 {
		t.Fatalf("total: %v", o.Total)
	}
}

func TestCreateOrder_SaleUnitPrice(t *testing.T) {
	db := setupOrderOptionsDB(t)
	p := database.TenantProduct{Code: "P3", Name: "Arroz", Unit: "KGM", SalePrice: 5, Active: true, ShowInDigitalCatalog: true}
	db.Create(&p)
	u := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Saco 50 KG", ConversionFactor: 50, Price1: 220, Active: true}
	db.Create(&u)
	svc := NewEcommerceService(db)
	o, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 1, SaleUnitID: &u.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if o.Total != 220 {
		t.Fatalf("total: %v", o.Total)
	}
}

func TestCreateOrder_RejectsOverStockButDoesNotDiscount(t *testing.T) {
	db := setupOrderOptionsDB(t)
	if err := db.AutoMigrate(&database.TenantProductStock{}); err != nil {
		t.Fatal(err)
	}
	p := database.TenantProduct{Code: "P4", Name: "Arroz", Unit: "KGM", SalePrice: 5, Active: true, ShowInDigitalCatalog: true, ManageStock: true}
	db.Create(&p)
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 3})
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 2, Quantity: 2})
	svc := NewEcommerceService(db)

	if _, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 6}}}); err == nil || !strings.Contains(err.Error(), "stock") {
		t.Fatalf("debía rechazar por stock, got %v", err)
	}
	// Dos líneas del mismo producto suman contra el mismo stock.
	if _, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 3}, {ProductID: p.ID, Quantity: 3}}}); err == nil {
		t.Fatal("la suma de líneas debía rechazarse")
	}
	if _, err := svc.CreateOrder(CreateOrderInput{CustomerName: "Ana", CustomerPhone: "999", Items: []OrderItemInput{{ProductID: p.ID, Quantity: 5}}}); err != nil {
		t.Fatalf("5 de 5 debía pasar: %v", err)
	}
	var total float64
	db.Model(&database.TenantProductStock{}).Where("product_id = ?", p.ID).Select("SUM(quantity)").Scan(&total)
	if total != 5 {
		t.Fatalf("crear el pedido no debe descontar stock, quedó %v", total)
	}
}
