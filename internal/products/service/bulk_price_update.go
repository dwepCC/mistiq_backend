package service

import (
	"strings"

	"tukifac/pkg/database"
)

// BulkPriceUpdateRow una fila del Excel de "Actualizar precio": código de barras + precio(s)
// nuevos. nil = esa columna venía vacía en el Excel, no se toca ese campo.
type BulkPriceUpdateRow struct {
	RowNumber     int
	Code          string
	SalePrice     *float64
	PurchasePrice *float64
}

type BulkPriceUpdateRowResult struct {
	RowNumber   int    `json:"row_number"`
	Code        string `json:"code"`
	ProductID   uint   `json:"product_id,omitempty"`
	ProductName string `json:"product_name,omitempty"`
	Status      string `json:"status"` // "updated" | "error"
	Error       string `json:"error,omitempty"`
}

type BulkPriceUpdateResult struct {
	Rows    []BulkPriceUpdateRowResult `json:"rows"`
	Updated int                        `json:"updated"`
	Errors  int                        `json:"errors"`
}

// BulkUpdatePrices actualiza sale_price/purchase_price en masa matcheando por código de barras
// (tenant_products.code). Usada por "Actualizar precio" en Productos: exporta código + precios de
// la sucursal activa, el usuario edita el Excel, lo vuelve a subir y acá se aplica fila por fila.
func (s *ProductService) BulkUpdatePrices(rows []BulkPriceUpdateRow) (BulkPriceUpdateResult, error) {
	result := BulkPriceUpdateResult{Rows: make([]BulkPriceUpdateRowResult, 0, len(rows))}

	codes := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		code := strings.TrimSpace(r.Code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}

	// tenant_products.code no es único en BD (índice normal, no uniqueIndex): un código que
	// matchea más de un producto queda marcado "ambiguo" en vez de actualizar el primero que
	// aparezca al azar (mismo criterio que loadProductsByCodes en inventory_import_adjustment.go).
	byCode := make(map[string]database.TenantProduct, len(codes))
	ambiguous := make(map[string]bool)
	if len(codes) > 0 {
		var products []database.TenantProduct
		if err := s.db.Where("code IN ?", codes).Find(&products).Error; err != nil {
			return result, err
		}
		for _, p := range products {
			code := strings.TrimSpace(p.Code)
			if code == "" {
				continue
			}
			if _, exists := byCode[code]; exists {
				ambiguous[code] = true
				continue
			}
			byCode[code] = p
		}
	}

	for _, r := range rows {
		rr := BulkPriceUpdateRowResult{RowNumber: r.RowNumber, Code: r.Code}
		code := strings.TrimSpace(r.Code)
		switch {
		case code == "":
			rr.Status, rr.Error = "error", "Código vacío"
		case ambiguous[code]:
			rr.Status, rr.Error = "error", "Hay más de un producto con este código; corríjalo manualmente"
		case r.SalePrice == nil && r.PurchasePrice == nil:
			rr.Status, rr.Error = "error", "Sin precio de venta ni de compra para actualizar"
		case r.SalePrice != nil && *r.SalePrice < 0:
			rr.Status, rr.Error = "error", "Precio de venta inválido"
		case r.PurchasePrice != nil && *r.PurchasePrice < 0:
			rr.Status, rr.Error = "error", "Precio de compra inválido"
		default:
			p, ok := byCode[code]
			if !ok {
				rr.Status, rr.Error = "error", "No se encontró ningún producto con este código"
				break
			}
			upd := map[string]interface{}{}
			if r.SalePrice != nil {
				upd["sale_price"] = *r.SalePrice
			}
			if r.PurchasePrice != nil {
				upd["purchase_price"] = *r.PurchasePrice
			}
			if err := s.db.Model(&database.TenantProduct{}).Where("id = ?", p.ID).Updates(upd).Error; err != nil {
				rr.Status, rr.Error = "error", "Error al guardar: "+err.Error()
				break
			}
			rr.ProductID = p.ID
			rr.ProductName = p.Name
			rr.Status = "updated"
		}
		if rr.Status == "updated" {
			result.Updated++
		} else {
			result.Errors++
		}
		result.Rows = append(result.Rows, rr)
	}

	return result, nil
}
