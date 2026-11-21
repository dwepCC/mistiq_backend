package handler

import "tukifac/pkg/salecurrency"

// Los totales de ventas del dashboard se suman en SOLES: una venta en USD aporta total × TC de la venta (las ventas en soles,
// igual que siempre). Antes se sumaban los dólares como si fueran soles.
//
//   - salesTotalPEN: sobre la tabla `tenant_sales` (cabecera de venta).
//
// El recargo al consumo (cargo 46) se RESTA: está dentro de `total` porque el cliente lo paga, pero no es ingreso del negocio sino de los
// trabajadores, así que no debe inflar ventas, ticket promedio ni utilidad. Las líneas (`si.total`) nunca lo incluyen.
//   - saleLineTotalPEN: sobre una línea `si` de `tenant_sale_items` unida a su venta con alias `s`.
var (
	salesTotalPEN    = salecurrency.TotalPENSQL("tenant_sales", "(tenant_sales.total - COALESCE(tenant_sales.service_charge_amount, 0))")
	saleLineTotalPEN = salecurrency.TotalPENSQL("s", "si.total")
)

// sumSalesPENSelect devuelve la expresión Select de una suma de ventas en soles.
func sumSalesPENSelect() string { return "COALESCE(SUM(" + salesTotalPEN + "), 0)" }
