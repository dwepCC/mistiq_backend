# V182 — Snapshot de moneda en cada movimiento de caja y de banco (11-nov-2026)

Rama `feature/orden-compra` (junto con V180 órdenes de compra y V181 TC de compras; sin push ni deploy). Cierra la fase «snapshot» de `PROPUESTA_MULTIMONEDA_SISTEMA.md` §4.3.

## Qué guarda
`tenant_cash_movements` y `tenant_bank_movements` ganan: **`currency`** (PEN por defecto), **`exchange_rate`** (TC con el que se valuó; solo USD) y **`amount_pen`** (equivalente en soles; NULL = no se pudo valuar). `amount` sigue siendo el importe en la **moneda del documento** y no cambia.

## Cómo se llena
- **Al crear** (hook `BeforeCreate` de ambos modelos, `pkg/database/movement_snapshot.go`): si el movimiento viene de una venta o compra, toma su moneda y TC (o el TC guardado del día de emisión si el documento no lo tenía); los manuales o de documentos en soles son PEN con `amount_pen = amount`. Cubre de una vez todos los lugares que crean movimientos (cobros, pagos a proveedor, anulaciones, reversiones, devoluciones) sin tocar cada uno.
- **Migración V182** (idempotente): agrega las columnas y rellena los existentes: soles/sin documento en una sentencia por tabla (`amount_pen = amount`), dólares uno por uno con el TC del documento o el del día; sin ningún TC queda NULL (los lectores derivan como antes). No modifica `amount` ni otros datos.

## Quién lo usa
- **Esperado del arqueo, cierre, resumen por método y reporte de sesión** (`penAmounts`): usan `amount_pen` cuando existe; solo derivan del documento los movimientos sin snapshot. Con esto el saldo de caja **no cambia** si después se edita el TC del documento.
- Los reportes de movimientos y el resto siguen derivando de la venta/compra (mismo resultado mientras el documento no cambie); se pueden pasar al snapshot sin cambio de aspecto.

## Pruebas
- Hook: un cobro de venta en USD (TC 4) guarda `USD / 4 / 100`; uno en soles guarda `PEN / nil / 40`; al cambiar luego el TC del documento a 9.9 el esperado de la sesión sigue en 140.
- Migración: movimientos antiguos (manual, soles, USD con TC, USD sin TC con TC del día, USD sin ningún TC) quedan `PEN/PEN/USD 105/USD 144/USD NULL`; segunda pasada idempotente; `amount` intacto.
- `go test ./...` completo en verde. Local: `migrate-fleet` aplicó V180–V182 a los 2 tenants locales sin errores; el balance de la sesión 12 pasó a usar snapshot (`rate_estimated_count = 0`) y una venta nueva en USD (TC 3.9, US$ 10) sumó S/ 39.00 al esperado.

## Despliegue (cuando se apruebe la rama)
`migrate-fleet` aplica V180 → V181 → V182 antes de reiniciar. V182 hace un UPDATE por tabla (las tablas de movimientos de los tenants grandes pueden tener muchas filas): conviene correrlo en horario de baja carga y verificar el tiempo en 1–2 tenants antes de toda la flota.
