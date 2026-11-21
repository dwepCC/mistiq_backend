package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"tukifac/config"
	"tukifac/pkg/database"

	"github.com/go-pdf/fpdf"
)

// OrderPDFToken firma (tenant, pedido) para el enlace público del PDF del pedido. El id del pedido
// es secuencial, así que sin esta firma cualquiera podría leer los pedidos (nombre y celular del
// cliente) de otros. El token no caduca: el enlace viaja dentro del mensaje de WhatsApp.
func OrderPDFToken(tenantID, orderID uint) string {
	mac := hmac.New(sha256.New, []byte(config.AppConfig.JWTSecret))
	fmt.Fprintf(mac, "ecommerce-order-pdf:%d:%d", tenantID, orderID)
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func ValidOrderPDFToken(tenantID, orderID uint, token string) bool {
	return hmac.Equal([]byte(OrderPDFToken(tenantID, orderID)), []byte(strings.TrimSpace(token)))
}

// BuildOrderPDF arma el comprobante del pedido web (no es un documento fiscal).
func BuildOrderPDF(order *database.TenantEcommerceOrder, storeName string) ([]byte, error) {
	var items []OrderItemInput
	if err := json.Unmarshal([]byte(order.ItemsJSON), &items); err != nil {
		return nil, err
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(0, 9, tr(strings.TrimSpace(storeName)), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 7, tr(fmt.Sprintf("Pedido N° %d", order.ID)), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(0, 5, tr("Fecha: "+order.CreatedAt.Format("02/01/2006 15:04")), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 5, tr("Cliente: "+order.CustomerName), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 5, tr("Celular: "+order.CustomerPhone), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	const wDesc, wQty, wPrice, wTotal = 100.0, 20.0, 30.0, 30.0
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(235, 235, 235)
	pdf.CellFormat(wDesc, 7, "Producto", "1", 0, "L", true, 0, "")
	pdf.CellFormat(wQty, 7, "Cant.", "1", 0, "C", true, 0, "")
	pdf.CellFormat(wPrice, 7, "P. unit.", "1", 0, "R", true, 0, "")
	pdf.CellFormat(wTotal, 7, "Importe", "1", 1, "R", true, 0, "")

	pdf.SetFont("Helvetica", "", 10)
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		if d := strings.TrimSpace(it.Detail); d != "" {
			name += " (" + d + ")"
		}
		x, y := pdf.GetX(), pdf.GetY()
		pdf.MultiCell(wDesc, 6, tr(name), "1", "L", false)
		h := pdf.GetY() - y
		pdf.SetXY(x+wDesc, y)
		pdf.CellFormat(wQty, h, trimQty(it.Quantity), "1", 0, "C", false, 0, "")
		pdf.CellFormat(wPrice, h, fmt.Sprintf("S/ %.2f", it.UnitPrice), "1", 0, "R", false, 0, "")
		pdf.CellFormat(wTotal, h, fmt.Sprintf("S/ %.2f", it.Quantity*it.UnitPrice), "1", 1, "R", false, 0, "")
	}

	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(wDesc+wQty+wPrice, 8, "Total", "", 0, "R", false, 0, "")
	pdf.CellFormat(wTotal, 8, fmt.Sprintf("S/ %.2f", order.Total), "", 1, "R", false, 0, "")

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.MultiCell(0, 4, tr("Detalle del pedido web. No es un comprobante de pago; el negocio confirmará disponibilidad y entrega."), "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func trimQty(q float64) string {
	if q == float64(int64(q)) {
		return fmt.Sprintf("%d", int64(q))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", q), "0"), ".")
}
