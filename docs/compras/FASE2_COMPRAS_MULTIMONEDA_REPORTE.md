# Fase 2 — Compras en múltiples monedas (PEN / USD): reporte (11-nov-2026)

Rama **`feature/orden-compra`** (sigue sin estar en `main`; sin push ni deploy). Modelo: el del sistema antiguo (ver `PROPUESTA_ORDEN_COMPRA_MULTIMONEDA.md` §3.2.1).

## Regla de fondo
**El documento conserva su moneda y su TC; el inventario y los costos se llevan en soles.** El pago y la cuenta por pagar quedan en la moneda del documento (igual que las ventas en USD hoy y que el sistema antiguo); los totales consolidados convierten con el TC de cada compra.

## Qué cambia
- **V181**: `tenant_purchases.exchange_rate` (TC venta, nullable). Compras existentes: PEN y TC NULL, sin backfill.
- **Compra** (`PurchaseService.Create`): moneda validada (PEN/USD, otra se rechaza); USD **exige TC > 0**; PEN descarta el TC. Antes aceptaba cualquier texto y lo contaba como soles.
- **Costo en soles**: el movimiento de stock (kardex) y `purchase_price` del producto se guardan como costo × TC; el ítem conserva el costo del documento. Anular la compra revierte con el costo en soles ya persistido.
- **Orden de compra → compra**: se quitó la guarda de la Fase 1; la compra **debe tener la misma moneda** que la orden (el TC puede diferir: el de la fecha del comprobante). Se valida dentro de la transacción de la compra.
- **Cuentas por pagar**: cada fila trae `currency` y `exchange_rate`; montos en la moneda del documento. Resumen: `total_due` consolidado en **soles** (USD × TC de cada compra) y `total_due_usd` aparte. El error de sobrepago usa `US$`/`S/` según la compra.
- **Contabilidad**: el registro de compras usa el TC guardado (antes avisaba "sin tipo de cambio: se usó 1.00"); el libro mayor convierte la CxP y sus pagos a soles con el TC.
- **Dashboard**: las compras del día/mes se suman en soles (USD × TC).
- **Helper** `salecurrency.TotalPENSQL` para expresar "importe en soles" en SQL.
- **Frontend**: lista de compras (`US$ 59.00` y debajo `≈ S/ 201.78 (TC 3.420)`), detalle (símbolos y equivalente), Cuentas por pagar (símbolo por fila, KPI en soles + KPI "en dólares", modal de pago), formulario de compra (selector de moneda, TC venta autocompletado por fecha y editable, costo de catálogo llevado a la moneda del documento, reconversión de costos al cambiar de moneda, totales con equivalente en soles, moneda bloqueada si viene de una orden).

## Pruebas
- Go: `go test ./...` completo en verde. Nuevos: compra USD sin TC/TC 0/EUR (rechazadas, sin rastro); costo de kardex y precio de compra en soles; PEN sin conversión; anular una compra USD; orden↔compra con moneda distinta (rechazada) y misma moneda con otro TC (acepta); CxP USD (filas y resumen en soles + USD); mensaje de sobrepago; `TotalPENSQL`. Migración V181 ejecutada con `migrate-fleet` en los 2 tenants locales.
- Front: `tsc` limpio, vitest 101 ✔.
- **En vivo local (tenant demo)**: orden USD (TC 3.40) → compra PEN sobre ella rechazada → compra USD sin TC rechazada → compra USD TC 3.42 desde la orden a crédito: precio de compra del producto 5 → **17.10** (5 × 3.42) → CxP USD 59.00 y resumen `total_due` 418.90 (con US$ 59 al 3.42) / `total_due_usd` 59 → pago parcial de US$ 20 → saldo `total_due_usd` 39; sobrepago rechazado con "US$". UI: lista con equivalente en soles, Cuentas por pagar con KPI en dólares, formulario con moneda/TC (3.421 autocompletado), producto agregado en USD con costo 4.9985 y totales `US$ 5.90` + `S/ 20.18`, y al volver a PEN se reconvierte.

## Pendiente / riesgos
- **Fase 3 — reportes**: reporte de compras (frontend), reporte de caja por sesión, `payment_method_report` y demás sumas de compras siguen sumando importes sin separar moneda (cuando haya compras USD mezclarán dólares y soles). Falta decidir el formato (totales por moneda, como el reporte de ventas).
- **Caja y bancos**: el pago de una compra USD resta "dólares" de la caja/cuenta sin separar por moneda (igual que el sistema antiguo y que las ventas USD). Falta la **advertencia al elegir método/cuenta** con moneda distinta a la del documento (hoy solo hay un texto informativo en el formulario).
- Sin diferencia de cambio (el antiguo tampoco la calcula).
- Retención/percepción desde una compra USD: no revisado.
- Tukichef no usa compras USD; no se tocó.
- **Despliegue (cuando se apruebe)**: backend con `migrate-fleet` V180 y V181 antes de reiniciar; luego frontend tenant. Una compra USD con el frontend viejo no puede crearse (la UI vieja no envía moneda), no rompe nada existente.
