# Fase 3 — Reportes de compras por moneda: reporte (11-nov-2026)

Rama `feature/orden-compra` (sin push/deploy). Criterio: el mismo ya decidido para ventas — **no mezclar dólares con soles**; se muestra cada moneda y un consolidado en soles con el TC de cada compra.

## Qué se hizo
- **Reporte de compras** (`/reports/purchases`): `GET /api/purchases` devuelve además `totals_by_currency` (por moneda: comprobantes, subtotal, IGV, total y `total_pen`; **excluye anuladas**, respeta los mismos filtros y la sucursal). La pantalla muestra una tarjeta por moneda y, si hay más de una, el consolidado en soles. La tabla y las exportaciones PDF/Excel traen columnas **Moneda**, **TC**, importes con su símbolo y **Total en soles** (antes todo salía con "S/" fijo).
- **Retención desde una compra USD**: el prefill usa el **TC guardado de la compra** (`tipo_cambio` USD→PEN, fecha del comprobante) en vez de consultar uno por fecha, que podía diferir del real del documento. Compras en soles no cambian.
- Ya hechos en la Fase 2: dashboard, contabilidad (registro de compras, libro mayor/CxP) y Cuentas por pagar.

## Pruebas
- Go: `go test ./...` completo en verde. Nuevos: totales por moneda (PEN/USD separados, anulada excluida, otra sucursal vacía) y prefill de retención (USD con TC / PEN sin TC).
- Front: `tsc` limpio, vitest 101 ✔. En vivo local: tarjetas "Compras en soles 118.00", "Compras en dólares US$ 59.00 ≈ S/ 201.78" y consolidado S/ 319.78 (118 + 59 × 3.42).

## Prueba visual de punta a punta en el navegador (11-nov-2026, tenant demo local)
Flujo real por la interfaz, sin atajos por API:
1. **Orden de compra** nueva en **dólares**: proveedor, moneda USD → el TC venta se autocompleta (3.421) → producto agregado → totales `US$` y equivalente en soles → "Crear orden" (OC01-00000004, Borrador).
2. **Detalle de la orden**: estado, moneda/TC, líneas, totales y botones (Imprimir/PDF, Descargar, Marcar como enviada, Anular, Registrar compra) → "Marcar como enviada".
3. **Registrar compra** desde la orden: la compra nace con el proveedor, el IGV y el detalle de la orden y **moneda USD bloqueada** con su TC → se completa serie y número del comprobante → registrada a crédito.
4. **Lista de compras**: `US$ 5.90 ≈ S/ 20.18 (TC 3.421)`; **detalle** con equivalente en soles.
5. **Cuentas por pagar**: la compra figura en US$ (saldo 5.90) → pago parcial de US$ 2.00 por el modal → saldo US$ 3.90; los KPI muestran el saldo consolidado en soles y, aparte, el saldo en dólares.
6. **Reporte de compras**: tarjetas "en soles: 1 comprobante S/ 118.00", "en dólares: 2 comprobantes US$ 64.90 (≈ S/ 221.96)" y consolidado S/ 339.96; columnas Moneda/TC/Total en soles.
7. **Anular la compra** → la orden OC01-00000004 vuelve a **Enviada** (se puede volver a convertir).

**Hallazgos de la prueba visual (corregidos):**
- En el formulario de la **orden** en USD, el producto entraba con el costo de catálogo en soles (17.10) como si fueran dólares. Ahora se convierte con el TC (US$ 4.9985) y al cambiar de moneda se reconvierten los costos, igual que en la compra.
- Cierra el pendiente de la advertencia de moneda: el **modal de pago de CxP avisa** (sin bloquear) cuando la cuenta bancaria del método elegido tiene otra moneda que la compra ("La cuenta "Billetera Yape" es en soles y esta compra es en dólares…") y cuando se paga una compra en dólares con efectivo (caja única sin moneda). Verificado con Efectivo y con Yape.

Una **orden de compra no es una compra**: no mueve stock, caja ni CxP, tiene sus propias series (`OC01`), estados y moneda/TC; solo al "Registrar compra" se crea el documento real, que además valida la misma moneda que la orden.

## Decisión pendiente (no implementado): caja y movimientos de caja/bancos
Los reportes de **caja por sesión** y de **movimientos** (`cashbank`) suman importes de movimientos que **no tienen moneda** (ni en el sistema antiguo ni en las ventas en USD de Tukifac): una compra en USD pagada en efectivo resta "59" de la caja única. Con el modelo elegido (caja única, pago en la moneda del documento) es una limitación conocida, no un bug nuevo; arreglarla de verdad exige **moneda por movimiento y arqueo por moneda** (campo `currency` en `tenant_cash_movements`/`tenant_bank_movements`, totales de sesión y PDF/Excel por moneda), que es una iniciativa aparte y afecta también a las ventas en USD. Recomendación: no mezclarla aquí; mientras tanto, **pagar las compras en USD con cuentas bancarias en dólares** (o por CxP) y registrar en caja solo soles. Si se aprueba, se propone como "Caja multimoneda" con su propio diseño.

## Pendiente (menor)
- ~~Advertencia de cuenta con moneda distinta~~ → hecha en el modal de pago de CxP (ver prueba visual); el formulario de compra solo muestra texto informativo.
- Correo de la orden de compra (Fase 1.2).
- Otros resúmenes que sumen compras no revisados exhaustivamente (p. ej. reportes de utilidad usan costos del kardex en soles: sin cambio).
