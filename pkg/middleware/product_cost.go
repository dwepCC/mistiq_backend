package middleware

import (
	"bytes"
	"encoding/json"
	"strings"

	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

// PermProductsViewCost permite ver (y editar) el costo de compra de los productos. Sin él, el
// servidor omite el costo de las respuestas (StripProductCost) y no deja que una edición lo
// sobrescriba (ver internal/products/handler).
const PermProductsViewCost = "products.view_cost"

// Permisos de la VISTA Punto de venta: uno por botón de información rápida de las tarjetas de
// producto. Son independientes de los de las vistas de Productos/Inventario (products.view_cost,
// inventory.*): quien administra productos puede no querer que el POS muestre el costo, y al revés.
const (
	PermPOSViewStock  = "pos.view_stock"  // «Ver stock por sucursal»
	PermPOSViewCost   = "pos.view_cost"   // «Costo» (se sirve por GET /products/:id/cost, no por la lista)
	PermPOSViewPrices = "pos.view_prices" // «Precios»
)

// CanViewProductCost indica si el usuario puede ver/editar el costo de los productos: permiso
// products.view_cost (que products.manage implica), o gestión de catálogo del restaurante
// (Tukichef, ver restaurantperm.ProductsManage; el Administrador la tiene siempre).
func CanViewProductCost(c fiber.Ctx) bool {
	claims, ok := c.Locals("tenant_claims").(*TenantClaims)
	if !ok || claims == nil {
		return false
	}
	if tenantHasPermission(claims.Permissions, PermProductsViewCost) {
		return true
	}
	ensureRestaurantPermissions(c)
	return HasRestaurantPerm(c, restaurantperm.ProductsManage)
}

// costKeys son las claves JSON que llevan el costo del producto (o su copia en la línea de venta).
var costKeys = map[string]struct{}{"purchase_price": {}}

// costStripExemptPaths son los endpoints cuyo propósito ES mostrar el costo (reporte de
// utilidades): los gobierna su propio permiso, no products.view_cost.
var costStripExemptPaths = []string{"/api/sales/profit-detail"}

// StripProductCost pone en 0 el costo (purchase_price) de cualquier respuesta JSON del grupo para
// quien no puede verlo. Sin esto el permiso sería cosmético: el costo viaja en la lista de
// productos, el detalle, el selector de ventas y las líneas de venta. Solo reescribe respuestas
// que realmente contienen la clave, así que cuesta casi nada para el resto.
func StripProductCost() fiber.Handler {
	return func(c fiber.Ctx) error {
		if err := c.Next(); err != nil {
			return err
		}
		status := c.Response().StatusCode()
		if status < 200 || status >= 300 {
			return nil
		}
		if !strings.Contains(strings.ToLower(string(c.Response().Header.ContentType())), "json") {
			return nil
		}
		body := c.Response().Body()
		if !bytes.Contains(body, []byte(`"purchase_price"`)) {
			return nil
		}
		path := c.Path()
		for _, exempt := range costStripExemptPaths {
			if strings.HasPrefix(path, exempt) {
				return nil
			}
		}
		if CanViewProductCost(c) {
			return nil
		}
		out, ok := stripCostFromJSON(body)
		if !ok {
			return nil
		}
		c.Response().SetBodyRaw(out)
		return nil
	}
}

// stripCostFromJSON devuelve el JSON con las claves de costo en 0. ok=false si no se pudo
// procesar (se deja la respuesta intacta en vez de romperla).
func stripCostFromJSON(body []byte) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // no alterar ids ni decimales del resto del documento
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, false
	}
	zeroCostKeys(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	return out, true
}

func zeroCostKeys(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if _, isCost := costKeys[k]; isCost {
				if val != nil {
					t[k] = 0
				}
				continue
			}
			zeroCostKeys(val)
		}
	case []any:
		for _, item := range t {
			zeroCostKeys(item)
		}
	}
}
