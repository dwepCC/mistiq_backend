package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"

	"gorm.io/gorm"
)

// seedClienteConRUC: cliente peruano normal con RUC de 11 dígitos.
func seedClienteConRUC(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	c := database.TenantContact{BusinessName: "Empresa SAC", DocType: "6", DocNumber: "20123456789"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// Fase 1: 0401 (ventas no domiciliados) exime a la factura de la exigencia de RUC. Reutiliza el
// mismo fixture de sale_max_monto_sin_ruc_test.go (setupMaxMontoTestDB, seedClienteSinRUC,
// seedSeries, maxMontoSaleInput).

func facturaOperationTypeInput(contactID, seriesID uint, opCode string, total float64) CreateSaleInput {
	in := maxMontoSaleInput(contactID, seriesID, "01", total)
	in.OperationTypeCode = opCode
	return in
}

// TestFactura0101SinRUC_SigueRechazada: comportamiento de siempre, sin cambios — venta interna
// en factura sigue exigiendo RUC.
func TestFactura0101SinRUC_SigueRechazada(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteSinRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	_, err := svc.Create(facturaOperationTypeInput(contactID, seriesID, salecurrency.OpVentaInterna, 100))
	if err == nil {
		t.Fatal("venta interna (0101) en factura a cliente sin RUC debe seguir rechazada")
	}
	if !strings.Contains(err.Error(), "RUC") {
		t.Errorf("esperaba el mensaje de RUC, got %v", err)
	}
}

// TestFactura0401SinRUC_SePermite: el caso nuevo — cliente extranjero/no domiciliado sin RUC
// peruano, factura con tipo de operación 0401.
func TestFactura0401SinRUC_SePermite(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteSinRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	sale, err := svc.Create(facturaOperationTypeInput(contactID, seriesID, salecurrency.OpVentasNoDomiciliados, 100))
	if err != nil {
		t.Fatalf("factura 0401 a cliente sin RUC debe permitirse: %v", err)
	}
	if sale == nil || sale.OperationTypeCode != salecurrency.OpVentasNoDomiciliados {
		t.Fatalf("esperaba operation_type_code=0401 persistido, got %+v", sale)
	}
}

// TestFactura0401ConRUC_TambienSePermite: 0401 no excluye clientes con RUC (mismo criterio que
// el sistema de referencia: la lista de doc. tipo permitida para 0401 incluye RUC).
func TestFactura0401ConRUC_TambienSePermite(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")
	contactID := seedClienteConRUC(t, db)

	if _, err := svc.Create(facturaOperationTypeInput(contactID, seriesID, salecurrency.OpVentasNoDomiciliados, 100)); err != nil {
		t.Fatalf("factura 0401 a cliente con RUC debe permitirse: %v", err)
	}
}

// TestBoleta0401_SeRechaza: 0401 solo tiene sentido (y solo se validó) en factura — igual que
// detracción (1001), que ya exige sunat_code=01.
func TestBoleta0401_SeRechaza(t *testing.T) {
	db := setupMaxMontoTestDB(t)
	svc := NewSaleService(db)
	contactID := seedClienteSinRUC(t, db)
	seriesID := seedSeries(t, db, "Boleta", "03", "B001")

	in := maxMontoSaleInput(contactID, seriesID, "03", 100)
	in.OperationTypeCode = salecurrency.OpVentasNoDomiciliados
	_, err := svc.Create(in)
	if err == nil {
		t.Fatal("0401 en boleta debe rechazarse")
	}
	if !strings.Contains(err.Error(), "0401") {
		t.Errorf("esperaba mensaje mencionando 0401, got %v", err)
	}
}

