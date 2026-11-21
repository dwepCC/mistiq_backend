package service

import (
	"encoding/xml"
	"os/exec"
	"strings"
	"testing"

	"tukifac/pkg/facturador"
)

// ublCustomerParty refleja solo lo necesario para verificar si <cac:RegistrationAddress> del
// cliente está presente o no en el XML UBL 2.1 generado.
type ublCustomerParty struct {
	Party struct {
		PartyLegalEntity struct {
			RegistrationAddress *struct {
				ID          string `xml:"ID"`
				AddressLine struct {
					Line string `xml:"Line"`
				} `xml:"AddressLine"`
			} `xml:"RegistrationAddress"`
		} `xml:"PartyLegalEntity"`
	} `xml:"Party"`
}

type ublInvoiceParties struct {
	AccountingCustomerParty ublCustomerParty `xml:"AccountingCustomerParty"`
	AccountingSupplierParty ublCustomerParty `xml:"AccountingSupplierParty"`
}

func minimalInvoicePayload(clientAddr *facturador.InvoiceAddress) facturador.InvoicePayload {
	return facturador.InvoicePayload{
		UBLVersion:   "2.1",
		TipoDoc:      "03",
		Serie:        "B001",
		Correlativo:  "1",
		FechaEmision: "2026-09-28T12:00:00-05:00",
		TipoMoneda:   "PEN",
		Details: []facturador.InvoiceDetail{
			{
				Unidad: "NIU", Cantidad: 1, CodProducto: "P01", Descripcion: "Producto demo",
				MtoValorUnitario: 100, MtoValorVenta: 100, TipAfeIgv: "10",
				MtoBaseIgv: 100, PorcentajeIgv: 18, Igv: 18, TotalImpuestos: 18,
				MtoPrecioUnitario: 118,
			},
		},
		Company: facturador.InvoiceCompany{
			RUC: "10726187938", RazonSocial: "DEMO S.A.C.", NombreComercial: "DEMO",
			// El emisor SÍ está obligado a tener dirección real — no forma parte de este fix.
			Address: facturador.InvoiceAddress{Ubigueo: "040101", CodigoPais: "PE", Direccion: "Av. Emisor 100"},
		},
		Client: facturador.InvoiceClient{
			TipoDoc: "0", NumDoc: "99999999999", RznSocial: "Clientes Varios",
			Address: clientAddr,
		},
		MtoOperGravadas: 100,
		MtoIGV:          18,
		TotalImpuestos:  18,
		ValorVenta:      100,
		SubTotal:        118,
		MtoImpVenta:     118,
	}
}

// Regresión directa (2026-09-28): un cliente sin dirección/ubigeo real NO debe generar un XML con
// <cac:RegistrationAddress> del cliente vacío — el nodo entero debe estar ausente, que es lo que
// SUNAT/Greenter soportan de forma nativa (Client::$address nullable, el propio template UBL 2.1
// de Greenter omite el nodo cuando es null). El domicilio del EMISOR, en cambio, sigue obligatorio
// y SIEMPRE debe aparecer.
func TestGreenterXML_ClientWithoutAddress_OmitsRegistrationAddressNode(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not in PATH; skip Greenter XML integration test")
	}

	payload := minimalInvoicePayload(nil)
	xmlBytes, err := renderGreenterInvoiceXML(t, payload)
	if err != nil {
		t.Fatalf("renderGreenterInvoiceXML: %v\n%s", err, string(xmlBytes))
	}

	var doc ublInvoiceParties
	if err := xml.Unmarshal(xmlBytes, &doc); err != nil {
		t.Fatalf("parse xml: %v\n%s", err, string(xmlBytes))
	}

	if doc.AccountingCustomerParty.Party.PartyLegalEntity.RegistrationAddress != nil {
		t.Fatalf(
			"el cliente sin dirección terminó con <cac:RegistrationAddress> en el XML (debería estar ausente): %s",
			string(xmlBytes),
		)
	}
	if doc.AccountingSupplierParty.Party.PartyLegalEntity.RegistrationAddress == nil {
		t.Fatalf("el EMISOR debe seguir teniendo <cac:RegistrationAddress> siempre — no es parte de este fix: %s", string(xmlBytes))
	}
	if doc.AccountingSupplierParty.Party.PartyLegalEntity.RegistrationAddress.AddressLine.Line != "Av. Emisor 100" {
		t.Fatalf("dirección del emisor incorrecta: %+v", doc.AccountingSupplierParty)
	}
}

// Caso contrario: un cliente CON dirección real sí debe traer el nodo completo, con sus datos.
func TestGreenterXML_ClientWithAddress_IncludesRegistrationAddressNode(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not in PATH; skip Greenter XML integration test")
	}

	payload := minimalInvoicePayload(&facturador.InvoiceAddress{
		Ubigueo: "150101", CodigoPais: "PE", Direccion: "Av. Real 123",
	})
	xmlBytes, err := renderGreenterInvoiceXML(t, payload)
	if err != nil {
		t.Fatalf("renderGreenterInvoiceXML: %v\n%s", err, string(xmlBytes))
	}

	var doc ublInvoiceParties
	if err := xml.Unmarshal(xmlBytes, &doc); err != nil {
		t.Fatalf("parse xml: %v\n%s", err, string(xmlBytes))
	}

	addr := doc.AccountingCustomerParty.Party.PartyLegalEntity.RegistrationAddress
	if addr == nil {
		t.Fatalf("el cliente CON dirección real debe traer <cac:RegistrationAddress>: %s", string(xmlBytes))
	}
	if !strings.Contains(addr.AddressLine.Line, "Av. Real 123") {
		t.Errorf("AddressLine.Line = %q, want contener \"Av. Real 123\"", addr.AddressLine.Line)
	}
	if addr.ID != "150101" {
		t.Errorf("ID (ubigueo) = %q, want \"150101\"", addr.ID)
	}
}
