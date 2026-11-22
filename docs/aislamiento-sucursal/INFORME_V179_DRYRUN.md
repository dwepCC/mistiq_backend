# Dry-run de V179 sobre producción (solo lectura)

Fecha: 10-nov-2026. **No se modificó ningún dato, no se ejecutó ninguna migración.** Consultas `SELECT`/`information_schema` con el usuario de solo lectura sobre **788 tenants** (`saas_tenant_*`); todos tienen `tenant_branches`.

## 1. Resumen global
| Concepto | Tenants |
|---|---|
| Exactamente una sucursal principal (V179 no cambia nada) | **785** |
| Varias sucursales principales | **1** (`doriconta`) |
| Ninguna sucursal principal | **2** (`bernapom`, `naturales`) |
| Una única principal pero inactiva / principal eliminada / sin sucursales | 0 / 0 / 0 |
| Con huérfanos (`branch_id` 0 o NULL) | **3** (`doriconta`, `industrialrafaz`, `col`) |
| Huérfanos en total | **5**, todos en `tenant_sales` (comprobantes de retención) |
| Conflictos con índices únicos | **0** |

Tenants cuyo estado cambiaría: 5 (783 no cambian).

## 2. Detalle por tenant afectado
- **doriconta — dos principales.** Sucursales: 1 *Principal* (1 841 ventas, 13 usuarios, principal), 2 *RESTAURANTE AV. 28* (59, 6), 3 *sucursal 01* (creada el 10-oct, 6 ventas, 1 usuario, también principal). V179 conserva la **1** (primera principal activa de menor id) y desmarca la 3. La retención huérfana (venta 1015) va a la 1.
  Causa: `CompanyService.CreateBranch` quitaba la marca anterior con un `UPDATE` sin `WHERE` (GORM lo rechaza) e ignoraba el error. ⚠ La sucursal 3 pudo marcarse principal intencionalmente: confirmar con el cliente (ver `REPORTE_CONFIRMACION_PRINCIPALES.md`).
- **bernapom — sin principal.** 1 *SANTA ANITA* (27 ventas, 2 usuarios), 2 *BERNA POM* (0, 0). V179 marca la 1.
- **naturales — sin principal.** 1 *Santas Hierbas - San Borja* (4 ventas, 2 usuarios), 2 *Santas Hierbas - Surco* (0 ventas, 2 usuarios). V179 marca la 1. Confirmar casa matriz con el cliente.
- **Huérfanos (todos `tenant_sales`, doc RETENCION, branch_id 0):** doriconta 1015 (R001-1) → Principal; industrialrafaz 1559, 1813, 2513 (R001-1..3) → FABRICA (id 1); col 1 (R001-1) → Principal (única sucursal).

## 3. Conflictos con índices únicos
El único índice único con `branch_id` en las 16 entidades es `ux_branch_daily_comanda_counter (branch_id, business_date)`, sin huérfanos en producción. `tenant_sales`: `uk_tenant_sales_series_correlative (series_id, correlative)` y `uk_tenant_sales_user_idempotency (user_id, idempotency_key)` no incluyen `branch_id` → no pueden chocar; ningún huérfano comparte serie y correlativo con otra fila. **Conflictos esperados: 0.**

## 4. ¿Dónde "menor id" podría no ser lo correcto?
1. doriconta (único caso de riesgo real: marca reciente de la sucursal 3). 2. naturales (Surco tiene usuarios y aún no vende). 3. bernapom: correcto.
Ningún tenant tiene hoy una principal inactiva, así que "preferir la activa" no cambia ningún caso real.

## 5. Retenciones
Las 5 retenciones de toda la plataforma están en `error` (0 percepciones): ver `docs/fiscal/REGULARIZACION_RETENCIONES.md`.
