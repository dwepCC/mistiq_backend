# Fase 3C — Scope global de sucursal con callbacks GORM

Fecha: 10-nov-2026. Código: `pkg/branchscope`, `pkg/database/branch_scope_models.go`, `pkg/middleware/branch_context.go`.

## Qué hace
- El middleware de sucursal reemplaza el `tenantDB` de la solicitud por una sesión GORM con el **Scope** del usuario
  (`branchscope.Attach`): sucursales permitidas, activa, `ViewAll` y `ReadOnly` (modo ALL_BRANCHES).
- Callbacks de GORM (`Query`, `Row`, `Update`, `Delete`, `Create`) sobre las entidades registradas:
  - **Lecturas / updates / deletes:** agregan `branch_id IN (permitidas) OR branch_id IS NULL OR branch_id = 0`
    (los registros sin sucursal son del tenant). `ViewAll` no filtra. Sin ninguna sucursal permitida no devuelve nada.
  - **Altas:** si `branch_id` viene en 0 se fija con la sucursal activa del contexto; si trae una sucursal no permitida, error `ErrForbidden`.
  - **Modo ALL_BRANCHES:** cualquier alta, update o delete falla con `ErrReadOnly` (además del 403 del middleware).
- El scope sobrevive a `WithContext(ctxConTimeout)`, `Session{}` y `Session{NewDB:true}` (se guarda en el contexto **y** en los Settings
  del `*gorm.DB`). Hallazgo en la prueba en vivo: `SaleService.List` hace `WithContext(timeout)` y perdía el scope.

## Entidades con scope (`database.BranchScopedModels`)
Series de comprobantes, series/lotes de producto, stock y stock por presentación, movimientos de stock, documentos de inventario,
ventas, cotizaciones, guías, compras, sesiones de caja, pisos, mesas, sesiones de mesa, contador de comandas, membresías.

## Sin scope por diseño
Globales del tenant (contactos, categorías, marcas, métodos de pago, bancos, configuración, roles); `TenantProduct` (catálogo compartido con
visibilidad por `tenant_product_branches`); precios por sucursal y el puente de visibilidad; transferencias (dos sucursales); usuarios y asignaciones;
entidades que heredan la sucursal de otra (ítems y pagos de venta, comprobantes, comandas, movimientos de caja…), protegidas por su padre
en los handlers (`internal/sales/branchguard`).

## Sin contexto: auditoría, modo estricto y acceso interno
- Una consulta sobre una entidad registrada **sin Scope ni marca System** se registra en el log una vez por tabla y origen
  (`[branchscope] AUDITORÍA: …`). Con `BRANCH_SCOPE_STRICT=1` **falla** con `ErrScopeMissing` en vez de mostrar todas las sucursales.
  Recomendado: operar un tiempo en auditoría, resolver lo que aparezca y luego activar el modo estricto en producción.
- Procesos internos identificados con `branchscope.System(db, motivo)` (log la primera vez): worker de facturación (jobs, reenvío de pendientes,
  reconciliación, notificación), cola de facturación (dead-letter), worker fiscal, barrido de resúmenes diarios, webhook de estado del facturador,
  limpieza de ventas rápidas abandonadas (superadmin). **Nunca** deben usarse con datos de un usuario de solicitud.
- `BRANCH_SCOPE_DISABLED=1` apaga los callbacks (válvula de emergencia; requiere reiniciar).

## Excepciones: lo que los callbacks NO pueden proteger
1. **`Raw()` / `Exec()` con SQL manual** (no pasan por los callbacks de Query/Update). Hay que revisarlos a mano.
2. **JOIN hacia una tabla registrada** desde otra tabla: solo se filtra la tabla principal de la consulta.
3. **`Session{NewDB:true}` combinado con `WithContext(otro)`** pierde ambos canales (contexto y Settings).
4. **Entidades sin `branch_id` propio:** dependen de la guarda del padre en el handler.
5. **Subconsultas construidas con otra conexión** (`database.GetTenantDB` directo dentro de una solicitud) no tienen scope: aparecerían en la auditoría.
6. **Goroutines lanzadas desde una solicitud** reutilizan el scope del usuario (quedan acotadas); si deben ver otras sucursales, usar `System` con motivo.
7. **ViewAll en modo SINGLE_BRANCH** no se filtra en los callbacks: la restricción de escritura a la sucursal activa sigue en los handlers
   (`RecordWritable`), porque algunos flujos legítimos (reversar transferencias) escriben en dos sucursales.

## Pruebas
`pkg/branchscope/branchscope_test.go` (SQLite): lecturas por sucursal asignada / multi-sucursal / ver todo; fail-closed sin sucursales; updates y deletes
cruzados sin efecto; altas con contexto, sucursal ajena y lotes; modo ALL solo lectura; transacciones y `Table()`; auditoría y modo estricto con `System`;
supervivencia del scope a `WithContext`/`Session`; válvula `BRANCH_SCOPE_DISABLED`. Suite completa del backend en verde y verificación en vivo
(MySQL, tenant demo): vendedor ve solo su sucursal, admin consolidado con `X-Branch-Mode: all`, sin accesos sin contexto en el tráfico de la solicitud.
