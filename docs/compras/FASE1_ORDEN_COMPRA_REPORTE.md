# Fase 1 — Orden de compra: reporte de implementación y pruebas (11-nov-2026)

Rama **`feature/orden-compra`** en `backend_principal` (9dc159f) y `frontend_tenant` (a508a09), partiendo de `main` (backend 4fd18f5, frontend ba29e1e). **No está en `main`, sin push, sin deploy.** Diseño: `PROPUESTA_ORDEN_COMPRA_MULTIMONEDA.md`.

## Qué incluye
- **V180**: `tenant_purchase_orders` + `tenant_purchase_order_items`, `tenant_purchases.purchase_order_id`, serie `OC##` por sucursal activa (chequeo tenant-wide de unicidad, `branchscope.System`). Idempotente.
- **Serie propia**: tipo `orden_compra` en el catálogo de series (Empresa ▸ Series), uso de serie (`series_usage_service`), siembra en tenants nuevos. Numeración `OC01-00000001`.
- **API** `/api/purchase-orders` (listar, ver, crear, editar `PATCH`, `send`, `cancel`), módulo `purchases`, permisos `purchases.view|create|delete`. Aislada por sucursal (`RecordReadable/Writable`, `ResolveWriteBranchID`; la tabla entra en `BranchScopedModels`).
- **Estados**: `draft → sent → received | cancelled`. Editable solo en draft/sent. 1 orden → 1 compra: `POST /api/purchases` acepta `purchase_order_id`; marca la orden `received` en la **misma transacción** (UPDATE condicional → una sola compra aunque haya concurrencia). Anular la compra devuelve la orden a `sent`.
- **Moneda**: PEN/USD; USD exige TC (>0); PEN descarta el TC. Frontend autocompleta el TC **venta** por fecha (editable). La orden no mueve stock, caja ni CxP.
- **Guarda temporal**: convertir una orden **USD** en compra se rechaza (backend y frontend) hasta la Fase 2, porque la compra todavía contabiliza todo como soles.
- **Frontend**: menú Compras ▸ Órdenes de compra, lista con filtros y detalle, formulario (proveedor, serie, fechas, moneda/TC, condición de pago de referencia, IGV incluido, productos), "Registrar compra" desde la orden (precarga proveedor, IGV, detalle, series/presentaciones por producto).

## Pruebas
| Prueba | Resultado |
|---|---|
| `go test ./...` completo | verde (único fallo: conteo de tipos de serie internos 4→5, test actualizado) |
| Unit servicio OC (numeración, totales, serie/sucursal, proveedor/ítems, USD/TC, update, enviar/anular, recibir una sola vez, liberar, USD bloqueado, listado) | 11 tests ✔ |
| Integración compra↔orden (vincula, 2.ª compra falla con rollback, anular compra libera) | ✔ |
| V180 (serie por sucursal activa, sin choque con `OC01` ya usada, idempotente, tablas/columna) | ✔ |
| Frontend `tsc` + vitest | limpio / 101 ✔ |
| **En vivo local** (tenant demo, MySQL local, migrate-fleet V180 en `demo` y `doriconta`): crear PEN, USD sin TC (400), USD con TC, sin proveedor (400), serie de cotización (400), editar, enviar, compra desde orden USD (rechazada), compra desde orden PEN (vincula, estado `received`), 2.ª compra (rechazada), editar/anular orden recibida (400), anular compra → orden `sent`, anular orden, 401 sin token, 404 inexistente | ✔ |
| **UI local**: menú y lista, formulario, USD autocompleta TC 3.421, "Registrar compra" precarga y registra; la orden queda "Recibida" | ✔ |

## No probado / riesgos pendientes
- ~~Aislamiento entre sucursales~~ → **hecho (1.1)**: `internal/purchaseorders/handler/branch_isolation_test.go` (S2 no lee/edita/envía/anula una orden de S1: 404; serie de otra sucursal: 400; admin con activa S2 lee pero no opera: 403).
- ~~PDF/impresión~~ → **hecho (1.1)**: `src/utils/purchaseOrderPdf.ts` (A4 con jsPDF, estilo del comprobante de transferencia; muestra moneda, TC y equivalente en soles), botones "Imprimir / PDF" y "Descargar" en el detalle. Verificado en pantalla con orden PEN y USD.
- **Envío por correo** de la orden (el antiguo lo tiene): pendiente (1.2); requiere decidir si se adjunta el PDF generado en el navegador, como en cotizaciones.
- Alta de la serie `OC` para tenants nuevos solo probada vía migración (el seed de provisión se agregó pero no se ejecutó).
- Tukichef (restaurante) no usa órdenes de compra; no se tocó.
- Orden de despliegue (cuando se apruebe): backend con `migrate-fleet` V180 primero, luego frontend tenant.

## Datos de prueba locales
Quedaron en la BD local del tenant `demo` (órdenes OC01-1/2, compras `OCTEST1` anulada y `UITEST1`). El tenant demo local se dejó de nuevo suspendido (`_tmp_probe down`).
