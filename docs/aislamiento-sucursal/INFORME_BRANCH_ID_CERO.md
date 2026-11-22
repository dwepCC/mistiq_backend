# Informe: registros con `branch_id = 0` en producción

Fecha: 10-nov-2026. Método: consultas **solo de lectura** (usuario `claude_ro`) sobre los 787 tenants (`saas_tenant_*`) de producción,
contando filas con `branch_id = 0 OR branch_id IS NULL` en las 15 tablas con sucursal propia
(ventas, cotizaciones, compras, guías, sesiones de caja, stock, stock por presentación, movimientos de stock, documentos de inventario,
series, series de producto, pisos, mesas, sesiones de mesa, membresías). **No se modificó nada.**

## Resultado
| Tenant | Tabla | Filas | Detalle |
|---|---|---|---|
| `saas_tenant_doriconta` | `tenant_sales` | 1 | id 1015, `doc_type = RETENCION`, R001-1, 19-ago-2026 |
| `saas_tenant_industrialrafaz` | `tenant_sales` | 3 | ids 1559, 1813, 2513, `RETENCION`, R001-1/2/3, 16-sep a 15-oct-2026 |
| `saas_tenant_col` | `tenant_sales` | 1 | id 1, `RETENCION`, R001-1, 22-sep-2026 |

Total: **5 filas en 3 de 787 tenants, todas en `tenant_sales`**; ninguna otra tabla tiene filas con sucursal 0/NULL.

## Origen
Las 5 filas son **comprobantes de retención** (`doc_type = RETENCION`), no ventas de usuarios: `user_id = 0`, `cash_session_id = NULL`,
`status = paid`, `billing_status = error`. El servicio de emisión de retenciones/percepciones crea la fila "venta" (`retSale`/`perSale`) **sin `branch_id`**
(ya señalado en la auditoría de 3A, punto 4 de lo no protegido). No es un defecto de datos históricos previos al multi-sucursal ni de la migración.

## Impacto actual
- Los callbacks del scope y `RecordReadable` tratan `branch_id = 0` como "del tenant": se leen desde cualquier sucursal, no se pierden.
- Se listan en consultas sin filtro de sucursal, pero **desaparecen** de listados filtrados por `branch_id = X`.
- Efecto práctico mínimo hoy (5 filas), pero **seguirá creciendo** con cada retención/percepción emitida mientras no se corrija el servicio.

## Hallazgos adicionales
- `saas_tenant_doriconta` tiene **dos sucursales marcadas `is_main = 1`** (ids 1 y 3). `GetMainBranchID` toma la primera principal activa; conviene corregirlo con el cliente.
- Las 3 retenciones de `industrialrafaz` y la de `col` y `doriconta` están con `billing_status = error`: no tiene relación con sucursales, pero merece revisión aparte.

## Propuesta (NO ejecutada; requiere tu aprobación)
1. **Corrección hacia adelante (código):** que el servicio de emisión de retenciones/percepciones fije `branch_id` desde la sucursal activa del contexto (o la de la compra/venta origen). Sin migración.
2. **Backfill de las 5 filas:** asignar cada una a la sucursal de su documento origen (la compra o venta que la originó) cuando exista, y consultarlo con el cliente cuando no. Se haría con un script de solo esas 5 filas, con backup y verificación previa, **no** con una migración automática.
3. Mientras tanto no se endurece nada que dependa de `branch_id != 0` en esas entidades.

---

## Decisión del dueño (10-nov-2026) y su implementación

1. **Un tenant tiene exactamente UNA sucursal principal.** Si hay varias, se conserva la primera (menor id, preferiblemente activa); si no hay ninguna, la primera sucursal activa pasa a serlo.
2. **Todo registro huérfano (`branch_id = 0`/NULL) se vincula a la sucursal principal.**

Implementado en **V179** (`SingleMainBranchAndOrphans`, aditiva e idempotente) y en el código:
- Causa raíz de las dos principales de `doriconta`: `CompanyService.CreateBranch` quitaba la marca anterior con un `UPDATE` sin `WHERE` (GORM lo rechaza con "WHERE conditions required") y no revisaba el error.
  Ahora crear/editar una sucursal es transaccional: marcar una como principal desmarca las demás; la primera sucursal del negocio es principal; quitarle la marca a la única principal se rechaza (`ErrMainBranchRequired`: hay que marcar otra).
- Las retenciones y percepciones ya no se crean sin sucursal (`docBranchID`: la pedida, la de su serie o la principal). Era el origen de los 5 huérfanos.
- V179 vincula los huérfanos existentes a la principal en las 15 entidades con sucursal propia. Una fila que choque con un índice único se omite (queda en el log `V179: registros sin sucursal…`) en vez de fallar o borrar datos.
- **Efecto esperado en producción:** `doriconta` queda con la sucursal 1 como única principal (la 3 deja de serlo); las 5 retenciones (doriconta 1, industrialrafaz 3, col 1) pasan a la principal de su tenant. No se ejecutó nada en producción: se aplica con `migrate-fleet`.
