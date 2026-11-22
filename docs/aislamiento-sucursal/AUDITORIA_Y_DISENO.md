# Aislamiento de datos por sucursal — Auditoría (Fase 1) y diseño propuesto (Fase 2)

Fecha: 10-nov-2026. Alcance: `backend_principal` (Go), `frontend_tenant` (Tukifac) y `frontend_restaurant_tenant` (Tukichef).
Método: lectura estática de código por 5 auditorías en paralelo + verificación directa de los hallazgos más graves
(`GET /sales/:id`, `GET /purchases`, `POST /inventory/transfer`). **Nada se ejecutó contra una app en marcha y no se
cambió código.** Lo que no se pudo verificar está en la sección 9.

---

## 1. Resumen ejecutivo

1. **No existe un `BranchContext` ni un filtro global.** La sucursal activa viaja en el JWT (`active_branch_id`); el
   middleware `BranchContextMiddleware` deja 3 valores sueltos en `Locals` y **cada handler decide a mano** si filtra
   (5 helpers en `pkg/branch` + 152 `Where("branch_id …")` repetidos). Los listados casi siempre filtran bien.
2. **Los endpoints por ID casi nunca validan la sucursal (IDOR entre sucursales).** Un usuario de la sucursal 1 que
   conozca el ID de un registro de la sucursal 2 puede leerlo, anularlo, emitirlo o cobrarlo. Solo las cotizaciones
   lo hacen bien (`canAccessQuotation`) y son el modelo a replicar.
3. **"Ver todas las sucursales" no es un permiso:** es el booleano `is_branch_admin` = rol llamado literalmente
   `"Administrador"` **o** `can_switch_branch`. No existe `branches.*` en el RBAC. Un usuario con 2+ sucursales
   asignadas recibe `can_switch_branch=true` y se vuelve "administrador" de **todas**, no solo de las suyas.
4. **Frontend:** cambiar de sucursal no remonta ni recarga nada (`<Outlet />` sin `key`); solo ~3 pantallas en
   Tukifac y ~12 archivos en Tukichef reaccionan. Es la causa directa del "hay que presionar F5". Además no hay
   cancelación de peticiones (respuestas viejas pisan datos nuevos), el carrito del POS de Tukifac **no se vacía** y
   la mesa abierta de Tukichef **conserva la cuenta de la sucursal anterior**.
5. **Nota de arquitectura:** cada tenant tiene **su propia base de datos**; el aislamiento entre tenants es por
   conexión y no hace falta `tenant_id` en las consultas. El trabajo es **solo por sucursal dentro del tenant**
   (lo que tu texto describe como `WHERE tenant_id=? AND branch_id=?` queda, en la práctica, `WHERE branch_id IN (...)`).

---

## 2. Arquitectura actual (hechos verificados)

| Tema | Hoy |
|---|---|
| Identidad | JWT con `UserID`, `RoleName`, `Permissions[]`, `ActiveBranchID`, `BranchSessionVersion`, `AuthMethod` (password / pin / master_access). Se emite en 4 sitios con lógica duplicada (`LoginAPI`, `BuildTenantSession`, `PinLogin`, `SwitchBranch`). |
| Sucursal del usuario | `TenantUser.BranchID` (legacy), `HomeBranchID`, `CanSwitchBranch`, tabla N:N `tenant_user_branches`. Cuatro fuentes de verdad que se sincronizan a mano. |
| Cambio de sucursal | `POST /api/session/switch-branch`: valida pertenencia (`UserHasBranchAccess`) y reemite JWT de 24 h. El token viejo no se revoca. El refresh **resetea** a la sucursal base. |
| Middleware | `BranchContextMiddleware` (`pkg/middleware/branch_context.go`): carga el usuario, compara `branch_session_version` (409 `SESSION_UPDATED`), fija `active_branch_id`, `is_branch_admin`. No consulta `tenant_user_branches`; no comprueba usuario inactivo ni sucursal activa. |
| Helpers | `ResolveWriteBranchID`, `ResolveReadBranchFilter`, `ResolveReportBranchFilter`, `ActiveBranchID`, `IsBranchAdmin`. Para un "admin" aceptan **cualquier** `branch_id` sin validar que exista, esté activa ni esté asignada. Con `active=0` los de lectura devuelven 0 = "todas" (fallan abiertos). |
| RBAC | `tenant_permissions` / `tenant_role_permissions`; permisos en el JWT; `{modulo}.manage` implica todo el módulo. Puente `restaurantperm` para el POS de PIN (su JWT no lleva `Permissions`). |
| Filtro global | **No existe.** `db.Scopes(...)` se usa solo para otros fines (`salescope`, `branchScope` local del dashboard). |
| Contexto a servicios | Los servicios reciben `*gorm.DB` + `branchID uint` a mano; casi no se usa `context.Context`. |

---

## 3. Inventario de entidades y su alcance recomendado

| Alcance | Entidades |
|---|---|
| **Exclusivas de sucursal** (`branch_id` propio) | Ventas `TenantSale`, cotizaciones, compras, guías `TenantDespatch`, sesiones de caja, membresías, stock (`ProductStock`, `PresentationStock`, `StockMovement`, `ProductSerial`), documentos de inventario, series de comprobantes, pisos/mesas/sesiones de mesa y contador de comandas (Tukichef) |
| **Exclusivas por herencia** (sin `branch_id`, dependen de otra) | ítems/pagos de venta, comprobantes `TenantInvoice`, retenciones/percepciones, cuentas por cobrar/pagar, movimientos de caja, **comandas y pedidos de mesa** (por `session_id`) |
| **Dos sucursales** | Transferencias de stock (`from_branch_id`, `to_branch_id`) |
| **Compartidas con visibilidad por sucursal** | Productos (puente `tenant_product_branches`; sin filas = visible en todas) + precios por sucursal (override) |
| **Globales del tenant** | Contactos, categorías, marcas, unidades, grupos de modificadores, combos, métodos de pago, cuentas bancarias, configuración de empresa/SUNAT, transportistas/conductores/vehículos, roles |
| **Mixtas / por decidir** | `TenantProduct.BranchID` (dueña legacy de platos Tukichef) vs puente; repartidores y empresas de delivery (hoy globales); resúmenes/bajas/reversiones SUNAT (por RUC, tenant-wide por naturaleza); tienda pública (sin sucursal) |

Riesgos de modelado: `default:1` en pisos/mesas (supone que la sucursal 1 existe); dos mecanismos de visibilidad de
productos que no se componen; bancos y movimientos bancarios revelan operaciones de todas las sucursales.

---

## 4. Hallazgos del backend

### 4.1 Núcleo (severidad alta)
1. Helpers aceptan cualquier `branch_id` para un "admin" (sin validar existencia/estado/asignación) — `pkg/branch/context.go`.
2. Usuario con 2+ sucursales asignadas = "admin" de todas (`SetUserAssignedBranches` fija `can_switch_branch=len>1`, el middleware lo trata como admin).
3. Sin permisos `branches.*`; "Administrador" se detecta por **nombre de rol** en 11+ archivos; `roles.manage` puede renombrar un rol a "Administrador"; `users.create/edit` puede asignarlo (sin anti-escalada).
4. Discrepancia `CanSwitchBranch` (middleware) vs `UserCanSwitchBranch` (login/switch): un usuario con 2 sucursales y flag en false recibe un token que el middleware luego rechaza (403).
5. IDOR por ID generalizado (sección 4.2).
6. Medias: el middleware confía en `claims.ActiveBranchID` sin revalidar contra `tenant_user_branches`; no comprueba `user.Active` (token válido 24 h tras desactivar); cambiar rol/permisos no invalida el JWT; modo *legacy* sin versión de sesión; usuario sin sucursal cae en silencio en la principal; PIN con rol "Administrador" hereda poder cross-branch; el refresh pierde la sucursal activa.

### 4.2 Endpoints vulnerables o incompletos (resumen; detalle completo en las auditorías de origen)

| Módulo | Endpoints (sev. alta salvo indicación) |
|---|---|
| **Ventas** | `GET /sales/:id` · `POST /sales/:id/cancel` · `POST /sales/:id/void-rejected` · `POST /sales/:id/issue-electronic` · `POST /sales/pending-refunds/apply` y `/apply-note` (egresos en cualquier caja) · `POST /sales` con `from_quotation_id` ajena (media) · `POST /restaurant/sales/:id/payments` |
| **Facturación** | `POST /billing/send|resend/:saleId` · `GET /billing/invoice/:saleId/document/:kind` (XML/CDR/PDF) · `POST /billing/void-with-credit-note/:saleId` · `POST /billing/debit-notes/:saleId` · `POST /billing/notes/independent` (`branch_id` del body) · `POST /billing/voided` · `GET|POST /billing/despatches` (lista sin filtro; `branch_id` y serie del body) · `POST /billing/retentions|perceptions` · (media) status/job/summaries/reversions/notification-counts/SSE |
| **Caja** | `POST /cashbank/sessions/:id/arqueo` (sin guard) · `GET /cashbank/sessions/open/list` (`branch_id` del cliente) · (media) `canAccessCashSession` ignora sucursal: managers operan cajas de otras sucursales; movimientos bancarios sin filtro |
| **Compras** | `GET /purchases` (lista de todas las sucursales) · `GET /purchases/:id` · `POST /purchases/:id/void` (revierte stock/caja/banco ajenos; colisión por `reference`) |
| **Cobranzas** | `POST /receivables/:saleId/confirm-bn` (sin ninguna protección) · (media) `collect`/`pay` solo protegidos indirectamente por la caja |
| **Membresías** | `GET|POST /memberships` (`branch_id` crudo) · `PUT /memberships/:id` (puede **mover** la sucursal) · `PATCH status`, `DELETE`, `generate-sale` |
| **Inventario** | `POST /inventory/transfer` (origen/destino del body) · confirm/cancel/reverse de transferencias · `GET /inventory/transfers` (todas) · documentos `GET|PUT /:id`, `confirm`, `void` · (media) `stock-summary` y `stock/:productId` con `branch_id` crudo |
| **Productos** | `GET /products` sin filtro por defecto (devuelve `stock_by_branch` de todas) · `PATCH /products/bulk-update-prices` (valida la sucursal y **actualiza el precio global**) · `PATCH /products/bulk-branches` y `branch_ids` en POST/PUT sin límite · CRUD de precios por sucursal para cualquier `:branchId` |
| **Restaurante (Tukichef)** | Por ID de sesión, mesa, piso o comanda: `GET/PATCH /sessions/:id`, `/tables/:id/session`, `orders`, `close`, `cancel`, `cancel-comandas`, `precuenta`, `PUT|DELETE /floors|tables/:id`, comandas `notes/status/print/DELETE` · **`POST /sessions/:id/bill` y `/pos/checkout` con `session_id` ajeno** |
| **Ecommerce** | `POST /ecommerce/orders/:id/convert` (`branch_id` del body) · tienda pública sin sucursal y con `stock_by_branch` de todas, sin autenticación |
| **Usuarios / Empresa** | `POST|PUT /users` (anti-escalada ausente) · `/company/series` GET/POST/PUT/DELETE con `branch_id` crudo · `/company/branches` mutaciones solo con `company.edit` |
| **Dashboard** | `/dashboard/stats` sin filtro de sucursal (KPIs de todo el tenant); `sales_by_branch` y vencimientos sin `branchScope` |

Lo que **sí** está bien: listados de ventas/cotizaciones/CxC/CxP/kardex/sesiones de caja, cotizaciones completas,
`MoveSessionTable`, series de comprobante (`docseries.ValidateForBranch`), `usePosInfiniteProducts` y
`BranchCheckoutSeriesContext` en el frontend.

---

## 5. Hallazgos del frontend

### 5.1 Tukifac (`frontend_tenant`)
- **Causa raíz del F5:** `MainLayout` renderiza `<Outlet />` sin `key`; `switchBranch` no recarga ni remonta. Solo `SalesRegisterPage`, `BranchCheckoutSeriesContext` y `branchIdentity` usan `resetEpoch`.
- **No reaccionan al cambio:** Dashboard, Ventas (lista), Compras, Kardex, Transferencias (crear e historial), Facturación, SunatDocs, Guías, Cuentas por cobrar/pagar, los 6 reportes, Informes de caja, Bancos, Membresías, Cotización (registro), Compra (registro), Header (conteos), `ConvertOrderModal` y varias más (filtros locales de sucursal que quedan anclados).
- **POS:** el carrito **no se vacía** al cambiar de sucursal y luego se cobra con el `activeBranchId` nuevo (riesgo de cobrar con precios/stock de otra sucursal).
- `BRANCH_REQUIRED` / `BRANCH_FORBIDDEN` no se manejan; `SESSION_UPDATED` cierra sesión (una petición en vuelo con token viejo podría expulsar al usuario en pleno cambio).
- `switchBranch` no actualiza `refresh_token` ni los claims en `AuthContext`; no está serializado (dos cambios seguidos: gana el último en responder).
- Carreras sin guarda: `CashPage`, `QuotationsPage`, `DashboardPage`, `PurchasesPage`, `loadMoreProducts`, `SalesRegisterPage`.
- Sin caché de peticiones (no hay React Query/SWR); sí cachés de módulo (`branchIdentity` no se limpia en logout en Tauri/Android, `saleUnitNames`, tipo de cambio y carrito ecommerce en `localStorage` sin tenant).
- El frontend manda `branch_id` por params/body en decenas de llamadas (el backend no debe confiar en él).

### 5.2 Tukichef (`frontend_restaurant_tenant`)
- Mismo patrón: 12 archivos reaccionan; **MesasPage, VentasPage, DashboardPage, Reportes, Repartidores, Modificadores** no.
- **`MesaPage`**: tras cambiar de sucursal sigue mostrando la mesa y el carrito de la sucursal vieja y las acciones viajan con el token nuevo contra un `sessionId` ajeno.
- **`VentasPage`**: caché `searchCache` de 45 s cuya clave no incluye la sucursal.
- `AuthContext.state.token` no se actualiza al cambiar de sucursal (la caja y el SSE de facturación quedan con el token viejo); el SSE `/api/billing/events` no se reconecta y **manda el token en la URL**.
- `BranchProvider` no limpia su estado en logout; logo y configuración de impresoras persisten entre tenants; `getContext()` se pide una sola vez; la clave de idempotencia no incluye sucursal.
- El staff por PIN no elige sucursal (la fija el backend); si tiene 2+ asignadas puede cambiar. El cambio alarga su token de 12 h a 24 h.
- Duplicación entre ambos frontends: `BranchContext`, `session.service` y `api.ts` son copias que ya divergieron.

---

## 6. Diseño propuesto

### 6.1 Principios
1. **El backend decide; el frontend solo refleja.** `branch_id` del cliente deja de ser una fuente de verdad.
2. **Fallar cerrado:** sin contexto de sucursal, una consulta sobre una entidad con alcance de sucursal **falla**, no devuelve "todas".
3. **Un solo punto** construye el contexto y un solo punto lo aplica; los handlers dejan de elegir filtros.
4. **No todo se filtra:** el alcance sale del inventario de la sección 3 (registro explícito por entidad).
5. **Reutilizar el RBAC existente** (permisos `módulo.acción` en el JWT y `{modulo}.manage`).

### 6.2 Backend
**a) `BranchContext` (nuevo paquete `pkg/branchctx`)** construido en el middleware a partir de BD (no del JWT solo):
`{UserID, ActiveBranchID, AllowedBranchIDs, CanViewAll, CanSwitch, Mode: SINGLE_BRANCH | ALL_BRANCHES}`.
- `AllowedBranchIDs`: `tenant_user_branches` (o todas las activas si `branches.view_all`). Se resuelve **por request** (ya existe una consulta de usuario por request; se suma la de asignaciones con caché corta por versión de sesión).
- Valida: usuario activo, sucursal activa/no eliminada, `ActiveBranchID ∈ AllowedBranchIDs`. Un `branch_id` pedido por el cliente solo se acepta si está en `AllowedBranchIDs` (y, para escribir, si el modo lo permite).
- `ALL_BRANCHES` solo existe si el usuario tiene `branches.view_all` **y** lo pidió explícitamente (cabecera/endpoint de contexto); nunca por omisión, `0` o `null`.
- Se guarda en `Locals` **y** en `context.Context` de la solicitud para que lo lean servicios y repositorios.

**b) Permisos RBAC nuevos** (sin segundo mecanismo): `branches.view_all`, `branches.switch`, `branches.manage` y
la asignación (`tenant_user_branches`) como equivalente de `view_assigned`. Migración tenant **V176**: crea los permisos;
`Administrador` los recibe; backfill para roles con usuarios con `can_switch_branch=true` (a confirmar). Se elimina la
comparación por nombre `"Administrador"` (flag de sistema inmutable). Anti-escalada: asignar un rol con
`branches.*` o `can_switch` solo lo hace quien ya lo tiene.

**c) Global Scope equivalente en GORM.**
- Registro declarativo de entidades con alcance: `{Modelo → columna directa | cadena de JOIN al dueño}` (p. ej. `TenantSale: branch_id`, `TenantCashMovement: via cash_session`, `TenantComanda: via table_session`).
- Un `*gorm.DB` **ligado a la solicitud** (`db(c)` pasa a devolver `base.WithContext(ctx)`), con **callbacks** de GORM (`Query`/`Update`/`Delete` agregan `branch_id IN (allowed)` y `Create` fija/valida `branch_id` desde el contexto). Así `First(&sale, id)` de otra sucursal devuelve *no encontrado* sin tocar cada handler (cierra de raíz el IDOR).
- Solo actúa si hay `BranchContext` en el `context`; **cron, workers y jobs del facturador** usan la conexión base (sin contexto) y siguen viendo todo el tenant, de forma explícita y auditable (`branchctx.System(db)`).
- **Fail-closed**: una entidad registrada consultada con contexto de solicitud pero sin `BranchContext` → error.
- Límites conocidos: `db.Raw/Exec` y joins manuales no pasan por callbacks → se auditan (inventario de 152 `Where` + SQL crudo) y se cubren con un test que falla si aparece SQL crudo sobre tablas registradas.

**d) Guardas explícitas inmediatas** (no esperan al scope): helper `branchctx.AuthorizeRecord(c, recordBranchID)` → 404 (como cotizaciones) para los endpoints de la sección 4.2; reglas específicas: transferencias (`from ∈ allowed` y `to` activo/existente; confirmar exige `to = activa`; cancelar exige `from = activa`), cobro de mesa/`pos/checkout` (la sesión debe ser de la sucursal activa), `apply`/`apply-note` (sesión destino de la misma sucursal), `bulk-update-prices` (precio global solo con permiso de catálogo; por sucursal en `ProductBranchPrice`).

**e) Rutas por entidad:** compartidas y globales **no** se tocan (contactos, categorías, métodos de pago…). Productos: listado por sucursal activa por defecto + `IsAvailable` también en lectura/edición por ID; stock siempre por `ResolveRead…` (nunca "todas" por omisión).

### 6.3 Frontend (ambas apps, una sola solución)
1. **`BranchProvider` unificado** (copiar la versión de Tukifac, que ya limpia estado en logout y tiene `refreshContext`, a Tukichef; a mediano plazo paquete compartido). Expone `activeBranch`, `allowedBranches`, `mode`, `canViewAll`, `switching`, `switchBranch`.
2. **`switchBranch` serializado**: bloquea el selector mientras cambia; actualiza `localStorage`, `AuthContext.token/permissions/modules` y `refresh_token`; luego `bump` de época.
3. **Remontaje por sucursal:** `<Outlet key={`${branchId}:${epoch}`} />` en ambos layouts → todas las pantallas reinician `useState`, filtros, paginación y vuelven a pedir datos **sin migrar pantalla por pantalla**. Las pantallas con trabajo pendiente (carrito del POS, mesa abierta, documento en edición) **avisan antes** de cambiar (y se bloquea con caja abierta cuando corresponda).
4. **Cancelación por época en el interceptor de axios:** cada petición lleva `signal` del `AbortController` de la época actual; al cambiar se aborta todo y se ignoran respuestas de otra época. Se serializa el reintento para que no viaje con el token nuevo.
5. **Registro único de cachés** (`resetBranchCaches()` + `clearAllCaches()` en logout, también en Tauri/Android): `branchIdentity`, `saleUnitNames`, `searchCache` (clave con sucursal), series, SUNAT, logo/impresoras por tenant.
6. **Errores:** manejar `BRANCH_REQUIRED/FORBIDDEN` (refrescar contexto o ir al selector) y distinguir `SESSION_UPDATED` provocado por un cambio de sucursal propio.
7. **Ya no depender de `branch_id` del cliente:** se mantiene solo para filtros de reporte validados por el backend contra `AllowedBranchIDs`.
8. Específico Tukichef: `MesaPage` recarga sesión al cambiar; reconectar SSE con el token vigente y quitar el token de la URL; `AuthContext.token` sincronizado.

### 6.4 Pruebas (Fase 6)
Escenario con 1 tenant, 2 sucursales y 3 usuarios (administrador global, vendedor S1, cajero S2) sobre una **tabla de matriz**
(consultar/crear/cambiar por S1, S2, todas) más: manipulación de `branch_id`, header y IDs ajenos, solicitudes sin sucursal,
usuario sin asignaciones/inactivo, concurrencia de cambios, JOINs, reportes consolidados y logout/login (caché).
Tests Go de integración sobre SQLite como los de `tenantmigrations`/`quotations`; tests de frontend para el
interceptor y el remontaje.

---

## 7. Decisiones que necesito del dueño del producto

1. **Modo "Todas las sucursales":** ¿es solo de lectura/consolidado, o puede **crear** (qué `branch_id` se usaría)? *Recomendación:* solo lectura; para escribir se exige una sucursal activa concreta.
2. **Rol "Administrador" actual:** ¿se migra a permisos explícitos (`branches.view_all` + `switch` + `manage`) con backfill, y roles personalizados que hoy tengan usuarios con `can_switch_branch` reciben `switch` (no `view_all`)? *Recomendación:* sí.
3. **Usuario con 2+ sucursales asignadas:** ¿puede cambiar **entre sus asignadas** (sin ser "admin" de todas)? *Recomendación:* sí; `view_all` es aparte.
4. **Usuario sin sucursales asignadas:** ¿bloquear con error de configuración o conservar "principal/sin restricción"? *Recomendación:* bloquear, con migración que asigne la principal a los existentes.
5. **Transferencias:** ¿el usuario debe tener acceso al origen **y** el destino confirma? *Recomendación:* origen = sucursal activa (o admin); confirma el destino.
6. **POS por PIN de Tukichef:** ¿queda fijo en su sucursal aunque el usuario sea "Administrador"? *Recomendación:* sí, salvo que tenga `branches.switch`.
7. **`AllowedBranchIDs`:** ¿por request desde BD (invalidación inmediata, 1 consulta) o en el JWT? *Recomendación:* BD por request con caché corta.
8. **Productos compartidos:** ¿editar `sale_price` global y borrar un producto compartido desde una sucursal debe exigir permiso de catálogo global? ¿Repartidores/empresas de delivery pasan a ser por sucursal? ¿La tienda pública tendrá una "sucursal de la tienda"?
9. **Soporte (acceso maestro):** ¿hereda la sucursal del propietario o entra como `ALL_BRANCHES` solo lectura?
10. **Resúmenes diarios, bajas y reversiones SUNAT:** ¿solo administradores (por RUC) o cada sucursal los ve filtrados por sus comprobantes?
11. **Tenants en modo legacy** (sin `home_branch_id`): ¿se puede exigir migrarlos antes de endurecer el middleware?

---

## 8. Plan por fases (cada una verificable y con commits separados)

| Fase | Contenido | Riesgo |
|---|---|---|
| **3a — Cierre inmediato de fugas** | `AuthorizeRecord` + guardas en los ~40 endpoints de 4.2 (404 si otra sucursal); middleware valida usuario activo, sucursal activa y asignación; helpers validan `branch_id` pedido; corregir `CanSwitch` vs `UserCanSwitch`; fix de `bulk-update-prices`. Sin migración, sin cambios de contrato. | Bajo |
| **3b — Contexto y permisos** | `pkg/branchctx` + `BranchContext` en Locals/`context`; V176 (`branches.*` + backfill); anti-escalada; unificar emisión de JWT. | Medio (despliegue: backend + migrate-fleet primero) |
| **3c — Scope GORM** | Registro de entidades, callbacks, `db(c)` ligado a la solicitud, fail-closed, test anti-SQL-crudo; migrar módulos de mayor a menor riesgo. | Medio-alto (por eso va después de 3a) |
| **4 — Frontend** | `BranchProvider`, `key` de remontaje, abort por época, caché/logout, errores `BRANCH_*`, confirmación con trabajo pendiente. Ambas apps. Independiente del backend salvo `mode`/`allowed`. | Bajo-medio |
| **5 — Adaptación de módulos** | Productos (visibilidad por puente), ecommerce, membresías, restaurante, dashboard/stats, series, usuarios. | Medio |
| **6 — Pruebas** | Matriz de aislamiento + concurrencia + caché. Se escriben junto con cada fase, no al final. | – |

Orden recomendado: **3a y 4 en paralelo** (cierran fugas y el F5 sin tocar la arquitectura), luego 3b → 3c → 5.

---

## 9. Qué no se pudo verificar (hay que comprobarlo antes de implementar)
- Qué claims usa el staff de Tukichef y si **siempre** pasa por `BranchContextMiddleware` (si no, el fail-open de `ResolveReadBranchFilter` con `active=0` aplicaría a todo).
- Qué roles reales tienen `cashbank.manage`, `billing.*`, `sales.cancel`, etc. (cuántos usuarios no-admin podrían explotar cada hallazgo).
- Si `internal/fiscal`, `prepayment` y `detraccion` (rutas de tenant), webhooks del facturador, cron y SSE por módulo filtran sucursal.
- Si categorías/áreas/combos/modificadores/clientes/repartidores deben ser globales o por sucursal (decisión de negocio, sección 7).
- Comportamiento del backend ante un token viejo tras `switch-branch` (409 inesperado) y si `refresh_token` conserva sucursal.
- Handlers HTML *legacy* (`TransferSubmit`, `AdjustmentSubmit`, `CreateForm`) no aparecen registrados en `routes.go`; confirmar que ningún otro punto los monta.
- Todo es lectura estática: no se reprodujo el cambio de sucursal en una app corriendo ni se ejecutaron peticiones de prueba.
