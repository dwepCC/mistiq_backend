package service

import (
	"strings"
	"testing"

	"tukifac/config"
	"tukifac/pkg/database"
)

func TestOrderPDFToken_BoundToTenantAndOrder(t *testing.T) {
	config.AppConfig = &config.Config{JWTSecret: "test-secret"}
	tok := OrderPDFToken(1, 10)
	if !ValidOrderPDFToken(1, 10, tok) {
		t.Fatal("token propio debe validar")
	}
	if ValidOrderPDFToken(1, 11, tok) || ValidOrderPDFToken(2, 10, tok) || ValidOrderPDFToken(1, 10, "") {
		t.Fatal("token no debe servir para otro pedido, otro tenant ni vacío")
	}
}

func TestBuildOrderPDF_ProducesRealPDF(t *testing.T) {
	order := &database.TenantEcommerceOrder{
		ID: 7, CustomerName: "Ñandú Pérez", CustomerPhone: "999111222", Total: 25.5,
		ItemsJSON: `[{"product_id":1,"name":"Café molido","detail":"250 g","quantity":2,"unit_price":12.75}]`,
	}
	out, err := BuildOrderPDF(order, "Tienda Ñ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "%PDF-") || len(out) < 500 {
		t.Fatalf("no es un PDF válido (%d bytes)", len(out))
	}
}
