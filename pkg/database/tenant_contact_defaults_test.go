package database

import "testing"

// Regresión directa (2026-09-28): hasta esta fecha, un cliente sin dirección/ubigeo quedaba con
// el fijo "Arequipa"/040101 sin importar en qué departamento estuviera el tenant real — falso
// para el cliente que recibía el comprobante. SUNAT no exige dirección del cliente en factura ni
// boleta (Greenter\Model\Client\Client::$address es nullable, su propio template UBL 2.1 omite
// el nodo completo cuando es null). Esta prueba falla si alguien vuelve a fabricar un default acá.
func TestNormalizeTenantContactAddressUbigeo_NoLongerFabricatesDefaults(t *testing.T) {
	addr, ubi := NormalizeTenantContactAddressUbigeo("", "")
	if addr != "" {
		t.Errorf("address = %q, want \"\" (no debe fabricar ningún valor)", addr)
	}
	if ubi != "" {
		t.Errorf("ubigeo = %q, want \"\" (no debe fabricar ningún valor)", ubi)
	}
}

func TestNormalizeTenantContactAddressUbigeo_TrimsRealValues(t *testing.T) {
	addr, ubi := NormalizeTenantContactAddressUbigeo("  Av. Los Incas 123  ", "  150101  ")
	if addr != "Av. Los Incas 123" {
		t.Errorf("address = %q, want trimmed real value", addr)
	}
	if ubi != "150101" {
		t.Errorf("ubigeo = %q, want trimmed real value", ubi)
	}
}

func TestNormalizeTenantContactAddressUbigeo_PartialInputStaysPartial(t *testing.T) {
	// Antes, tener SOLO dirección (sin ubigeo) o viceversa rellenaba el campo faltante con el
	// default — ahora cada campo se trata de forma independiente, sin inventar el que falta.
	addr, ubi := NormalizeTenantContactAddressUbigeo("Av. Real 456", "")
	if addr != "Av. Real 456" {
		t.Errorf("address = %q, want \"Av. Real 456\"", addr)
	}
	if ubi != "" {
		t.Errorf("ubigeo = %q, want \"\" (no debe inventar ubigeo aunque falte)", ubi)
	}
}
