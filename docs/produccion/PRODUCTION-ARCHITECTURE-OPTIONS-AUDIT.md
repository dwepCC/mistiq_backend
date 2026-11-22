# Auditoría arquitectónica 3 — Producción independiente con inventario oficial compartido

> Fecha: 2026-10-13. **Auditoría y documentación. No se implementó nada; no se tocó código, BD, servidores ni configuración. No hay decisión aprobada.**
> Documentos de esta auditoría: este archivo · [Integración desacoplada con Inventario](PRODUCTION-DECOUPLED-INVENTORY-INTEGRATION.md) · [Modelo de datos revisado](PRODUCTION-DATA-MODEL-REVISED.md) · [Roadmap desacoplado](PRODUCTION-ROADMAP-DECOUPLED-REVISED.md) · [Registro de decisiones (ADR)](PRODUCTION-ARCHITECTURE-DECISION-RECORD.md).
> Evidencia nueva de esta auditoría (informes de subagentes de solo lectura, que complementan el [Anexo B](PRODUCTION-FINAL-REVIEW-ANNEX-B-CODE-VERIFICATION.md)): los hallazgos con «archivo:línea» están citados en las secciones 3 y 4.
> Marcas: **[V-código]** verificado en código · **[I]** interpretación · **[NV]** no verificado.

---

## 1. Veredicto

**Recomiendo la Alternativa A (integración directa controlada), implementada con un adaptador (puerto) hacia Inventario.** Producción tiene modelos, tablas, servicios y reglas propios; no guarda saldos ni crea un segundo Kardex; llama a `RecordMovementTx` **dentro de su propia transacción** a través de una interfaz estrecha (`StockGateway`) que es la única pieza que conoce a Inventario. La alternativa B con saldos físicos propios **no es necesaria**: lo que se busca (separar fabricar de vender, controlar calidad) se resuelve con **estados y registros**, sin inventario duplicado, y puede añadirse después (V1.5) como un estado más del registro de fabricación sin cambiar el modelo de datos.

Por qué, en cuatro hechos verificados:
1. `RecordMovementTx` usa **solo `tx`** (ninguna línea de su cuerpo usa `s.db`) y todos los módulos (ventas, compras, documentos de inventario) ya lo llaman dentro de su propia transacción: si algo falla después, el rollback revierte kardex, saldo y cabecera [V-código: `inventory_service.go:134-210`; llamadores en `sale_service.go:715/848`, `purchase_service.go:296/358`, `inventory_document_service.go:344-423`].
2. La alternativa B **no evita** tocar el servicio compartido: para entregar insumos al «inventario operativo» y para transferir el terminado al comercial, tendría que usar igualmente `RecordMovementTx`. Por tanto **hereda los mismos riesgos de concurrencia** y añade un segundo juego de saldos que conciliar.
3. El ERP **no tiene mecanismo de reserva de stock** [V-código]; sin reservas, un inventario «operativo» solo duplica saldos.
4. Los riesgos reales (lost update, falta de UNIQUE, redondeo) son del núcleo compartido y deben corregirse **con cualquier arquitectura**; no son argumento a favor de B.

**Lo que se comparte y lo que se aísla** (diseño híbrido que propongo):
| Aislado en Producción | Compartido con Inventario |
|---|---|
| Recetas, órdenes, registros de fabricación, consumos, costos, estados, permisos, rutas, UI | Saldos de stock por producto/sucursal/presentación |
| Reglas de negocio (desperdicio, producido = buenas, parciales, anulación) | `tenant_stock_movements` (Kardex oficial) con dos columnas genéricas de origen |
| Adaptador `StockGateway` (única dependencia de Inventario) | `InventoryService.RecordMovementTx` (más una variante que devuelve ID/saldo) |

---

## 2. Qué sigue vigente de las auditorías anteriores y qué cambia

| Conclusión anterior | Estado | Detalle |
|---|---|---|
| Receta de un nivel + snapshot en la orden | **Vigente** | Se amplía con `output_quantity` (receta por lote) y se guarda el factor de conversión en el snapshot |
| Consumo por registro de producción; producido = buenas; sin «Reabrir»; mano de obra por registro | **Vigente** | Sin cambios |
| Costo promedio por (producto, sucursal) en sombra; costo de gestión, no contable; sin IGV | **Vigente** | Sin cambios; ver §7 |
| Endurecer `RecordMovementTx` (redondeo, ensure-row + lock, orden, reintento, UNIQUE) | **Vigente y confirmado como necesario para cualquier arquitectura** | Es prerrequisito de habilitar Producción en datos reales, no del desarrollo |
| `production_entry_id` en `tenant_stock_movements` | **Reconsiderada → `source_type` + `source_id` genéricos** | Mantiene a Inventario ajeno a Producción (principio de independencia); mismo costo de migración |
| Idempotencia `UNIQUE(orden, clave)` | **Reconsiderada → `UNIQUE(user_id, idempotency_key)`** | Replica el patrón verificado v150 (`v150_sale_idempotency_key.go`) |
| Numeración con contador propio | **Reconsiderada → `OP-<id>` derivado del ID** | Elimina una tabla y un lock; no se exige correlativo sin huecos |
| Receta convertida a base en el snapshot | **Precisada** | El snapshot guarda `sale_unit_id`, cantidad comercial, **factor** y cantidad base |
| «Auditoría con `WriteAuditLog` en cada acción» | **Corregida** | `WriteAuditLog` es central, best-effort y no transaccional [V-código: `audit_log.go:41-72`]; la trazabilidad oficial va en **columnas del tenant dentro de la misma transacción** |
| «`manufacturing` con `Implemented:false` queda apagado» | **Corregida** | Solo se oculta de `GET /api/session/modules`; **no bloquea el backend** (JWT con `tenant_modules.enabled=1`, `tenant_session.go:49-56`); además `RequireModule` lee el JWT, así que activar/desactivar surte efecto al re-login (token 24 h) |
| Tipo de operación `PRODUCTION_IN` por migración | **Vigente** | Sin él `RecordMovementTx` falla con `ErrOperationTypeNotFound` y revierte todo |
| Variantes, lotes, reservas, segunda calidad, rutas, MRP fuera de V1 | **Vigente** | Sin nueva evidencia en contra |
| Control de calidad / aprobación previa al ingreso | **Nuevo análisis** | §5: no V1; diseño compatible para V1.5 |

---

## 3. Evidencia del código (nueva respecto a los anexos)

### 3.1 Backend

| Hecho | Evidencia |
|---|---|
| `RecordMovementTx` y sus auxiliares (`resolveMovementOperationType`, `CheckProductPresentation`, `LookupOperationTypeByCode`) solo usan `tx` | `inventory_service.go:59-74, 86-117, 134-210`; `operation_type.go:20-31` |
| Métodos que **sí** usan `s.db` (otra conexión si el servicio se construyó con el pool): `EnsureProductBranchLink`, `GetPresentationStockByBranch/ByProduct`, `RecordMovement` (abre su propia tx), `StockTotalsByProductIDs`, `WeightedAverageUnitCosts`, `StockSummary`, `GetKardex` | `inventory_service.go:213-230, 275, 297, 314, 756-805, 1086` |
| Los módulos existentes construyen `NewInventoryService(tx)` dentro de su transacción | `sale_service.go:~848`, `purchase_service.go:296, 530`, `inventory_document_service.go:384, 427`, `sale_cancel_stock.go:48, 152`, `restaurant_service.go:1455`, `bulk_import.go:292` |
| El servicio no tiene estado: `type InventoryService struct{ db *gorm.DB }` | `inventory_service.go:15-21` |
| `RecordMovementTx` devuelve solo `error`; `movement.ID` y `newBalance` existen internamente y no se devuelven | `inventory_service.go:134, 179-190` |
| `CreatedAt` del movimiento es siempre `time.Now()` (sin fecha retroactiva) | `inventory_service.go:186` |
| No valida `ManageStock` (lo hace cada llamador) ni que `Type` coincida con la dirección del tipo de operación | Anexo B A.1 |
| Columnas de origen hoy: `inventory_document_id`, `transfer_id`, `sale_item_id`, `purchase_item_id`; no hay origen genérico | `pkg/database/migrations.go:1407-1447` |
| Idempotencia v150: columna `idempotency_key` + `UNIQUE(user_id, key)`, replay temprano y por carrera; **no es helper genérico** (atado a `TenantSale`) | `v150_sale_idempotency_key.go`, `sale_idempotency.go:21-82`, `sale_service.go:228-234, 960-966` |
| Patrón documento: borrador sin movimientos → confirmar con `FOR UPDATE` del documento → anular con movimientos inversos por FK | `inventory_document_service.go:344-465` |
| Auditoría: `WriteAuditLog` escribe en BD central, best-effort; no existe auditoría genérica en el tenant; patrón local = columnas `created_by/confirmed_by/voided_by` | `pkg/database/audit_log.go:41-72`; `migrations.go:1479-1483` |
| `RequireModule` lee `claims.Modules` del JWT, sin BD/cache por request | `pkg/middleware/auth.go:215-232` |
| `moduleCodeCatalog`: `manufacturing` `Implemented:false`; `Implemented` solo oculta en `/api/session/modules`; `SeedDefaultTenantModules` lo deja `Enabled:false` | `migrations.go:546, 599-631`; `session_handler.go:53`; `tenant_provision.go:30-45` |
| Permisos: `SeedPermissions` idempotente; `RequirePermission` concede `{module}.manage`; tenants existentes requieren migración de backfill (V131/V143/V149) | `role_service.go:139+`, `pkg/middleware/permissions.go:15-39`, `tenant_permissions.go:5-32` |
| Migraciones tenant: interfaz `Version/Name/Up`; **sin transacción** (DDL parcial si aborta); historial `tenant_migration_history`; la flota continúa con otros tenants salvo circuit breaker (10 fallos consecutivos); última = V160 | `tenantmigrations/migration.go:6-10`, `engine/runner.go:17-80`, `engine/fleet.go:97-126`, `fleet_circuit.go:12-20` |
| Tablas nuevas necesitan su propia `vNNN` (no basta el AutoMigrate del baseline) | `engine/provision.go:10-62`, `v001_baseline_schema.go` |
| Unidades: `saleunit.ResolveForLine(db, productID, saleUnitID, qty)` vive en `pkg/`, acepta `tx`, sin dependencias de dominio; `BaseQuantity` sin redondeo | `pkg/saleunit/resolver.go:17-67` |
| Transacciones sin `context`/timeout propio (`DBContext` y `WithQueryClass` sin llamadores) | `config.go:165-178, 200-203`; `pkg/database/context.go` |
| Branch scoping existe: `BranchContextMiddleware`, `branch.ResolveWriteBranchID` | `pkg/middleware/branch_context.go:22-120`; `pkg/branch/context.go:80-115` |
| No hay mecanismo de reserva de stock (solo `reserved` en series) | grep + `ReserveSerials` (informe de subagente) |
| Lectura de stock para disponibilidad: no existe función que dé stock por (producto, presentación, sucursal) para N productos; `StockSummary` ignora variantes | `inventory_service.go:756-805, 1086-1126` |

### 3.2 Frontend (`frontend_tenant`)
- El router solo valida permisos (`Protected perm`, `AppRouter.tsx:131`); el módulo del plan se valida con `RequireModule` dentro de cada página; `hasModule` sale del JWT (`AuthContext:262`). El Sidebar muestra candado si falta el módulo, salvo `hideIfLocked`.
- Existe `utils/idempotencyKey.ts`; hoy la clave va **en el body** (`idempotency_key`, `sales.service:556`).
- `ProductPickerModal` está acoplado a POS (fijo a `manage_stock_only`); `ProductServerSelect` es ligero pero devuelve solo id/nombre/código.
- Mejor plantilla de UI: `InventoryDocumentPage` (lista/detalle/formulario con estados y permisos por acción) — **copiar el patrón, no importarlo**.
- Errores del backend: solo `toast.error(e.response.data.error)`; los faltantes por línea requieren un panel nuevo y un formato de error acordado.
- No hay React Query/SWR (estado local con `useState`); no hay patrón para ocultar costos por permiso; no hay tests (ni vitest/jest) ni eslint configurado: lo único que valida es `tsc` strict.
- `frontend_central` no necesita cambios (los módulos se asignan en `PlansPage`; `manufacturing` ya tiene icono); `frontend_restaurant_tenant` no [no revisado a fondo].
- Cambios mínimos: rutas en `AppRouter`, grupo en `Sidebar`, `production.service.ts`, `pages/production/*`, etiquetas en `permissionLabels.ts`/`rolePermissionPresets.ts` [I]. Componentes reutilizables sin acoplar: `Modal`, `ConfirmDialog`, `RowMenu`, `SearchSelect`, `RequireModule`, `UnitQuantityInput`, `useBranch`, `idempotencyKey`, `datesPeru`, `money`, `sonner`, `api.ts`. No acoplar: `InventoryDocumentPage` y subcomponentes, `ProductPickerModal`, `PurchaseSaleUnitSelector`, `components/pos/*`, páginas de Ventas/Compras/Cotizaciones.

---

## 4. Las alternativas evaluadas

**A — Integración directa controlada.** Tablas propias de Producción; consumo e ingreso mediante el servicio oficial, en una sola transacción, vía adaptador.
**B1 — Motor con inventario operativo propio (saldos físicos separados).** Producción lleva saldos de insumos y terminados en proceso; al confirmar transfiere el terminado al inventario comercial.
**B2 — Motor con estados, sin saldos propios («fabricación pendiente de aprobación»).** El registro de fabricación existe pero el terminado no entra al stock vendible hasta aprobarse.
**C (tercera alternativa que propongo) = A con adaptador y extensible a B2.** Es A, diseñada para que B2 sea un estado adicional del registro (V1.5), no una arquitectura distinta.

### ¿B necesita saldos físicos propios?
**No.** Lo que B1 intenta lograr (separar «hecho en planta» de «disponible para venta», y no vender lo no aprobado) se obtiene con B2: un **estado** en el registro. Los insumos ya salieron físicamente al registrar; el terminado todavía no existe como stock vendible. Ningún saldo nuevo hace falta: lo «en espera» es un registro, no una cantidad en un almacén. Un segundo saldo solo se justificaría para valorar y gestionar **trabajo en proceso entre etapas** (rutas, centros de trabajo), que está fuera de V1.

### Tabla de la pregunta E (stock intermedio)

| | 1. Sin stock intermedio (A) | 2. Pendiente de aprobación (B2) | 3. Inventario operativo propio (B1) |
|---|---|---|---|
| **Problema real que resuelve** | Registrar producción de forma simple y atómica | Control de calidad / aprobación del supervisor antes de vender; evitar vender lo no verificado | Gestionar WIP físico en varias etapas con valoración propia |
| **Tablas/estados** | Entradas + consumos | Mismas tablas + estado `pending_approval/approved/rejected` y cantidades aprobada/rechazada | Saldos propios por insumo/semiterminado, kardex propio, traslados internos |
| **Materiales consumidos** | `OUT` al registrar | `OUT` al registrar (el material ya se gastó) | `OUT` del comercial → `IN` del operativo; consumo posterior del operativo |
| **Producto en proceso** | Informal (orden «En proceso») | Informal + «pendiente de aprobación» visible solo en Producción | Formal (saldo) |
| **Cómo evita diferencias entre inventarios** | No hay dos | No hay dos (un solo saldo) | Requiere conciliación continua entre ambos y la transferencia |
| **Costos** | Costo del registro al ingresar | Costo calculado al registrar; ingreso al aprobar por (costo total ÷ aprobadas); rechazadas se absorben | Costo viaja entre inventarios; mantener dos promedios |
| **Mermas** | Informativas | Rechazadas = merma informativa o `OUT` de merma | Merma contra saldo operativo |
| **Anulaciones** | Contramovimientos; el terminado debe existir | Antes de aprobar solo revierte insumos; después, ambos | Doble reversión + conciliación |
| **Ventas** | Stock inmediato | Stock tras aprobar | Stock tras «transferencia» |
| **Complejidad backend** | Baja | Media (2 transacciones, estados, permiso extra) | Alta (sistema paralelo + sincronización) |
| **Frontend** | Baja | Media (bandeja de aprobación) | Alta |
| **Migraciones/soporte** | Mínimas | Una columna de estado y tabla opcional | Muchas tablas; soporte de descuadres |
| **Etapa** | **V1** | **V1.5** (solo si hay cliente que lo exige) | **No recomendado**; si algún día hace falta WIP, evaluar un almacén/sucursal virtual del inventario oficial (con el riesgo de límites de plan y series, almacén = sucursal [V-código, Gap]) |

**Cómo debería tratarse B2 si se implementa:** insumos al **registrar** (el material ya se gastó; consumirlos al aprobar dejaría el stock de insumos sobrestimado y la aprobación podría fallar por «stock insuficiente» después de haber producido, y no existen reservas); terminado al **aprobar**. Estados: `applied` (V1) se corresponde con «registrado y aprobado automáticamente»; V1.5 añade `pending_approval` y `rejected` sin cambiar tablas. Un estado de aprobación **no es** un segundo saldo.

---

## 5. Matriz comparativa con puntuación razonada

Escala 1–5 (5 = mejor). Opciones: **A** (directa con adaptador), **A+Q** (A con aprobación posterior, B2), **B** (inventario operativo propio, B1).

| Criterio | A | A+Q | B | Evidencia / razonamiento |
|---|---|---|---|---|
| Simplicidad | 5 | 3 | 1 | A: una transacción, un saldo. A+Q: dos transacciones y estados. B: sistema paralelo + sincronización |
| Integridad de stock | 4 | 4 | 2 | Un solo saldo en A/A+Q (depende del endurecimiento T1–T4). B: dos saldos y transferencias; los mismos riesgos del núcleo + riesgo de descuadre propio. B **no evita** `RecordMovementTx` para el traspaso [V-código] |
| Riesgo de regresión sobre módulos actuales | 3 | 3 | 3 | Las tres exigen endurecer el núcleo (necesario hoy) y una extensión aditiva del contrato; B además necesita traspasos pero no cambia más el núcleo. Empate |
| Reutilización del código actual | 5 | 5 | 2 | A/A+Q reutilizan servicio de inventario, kardex, tipos de operación, `saleunit`, patrón de documento. B reimplementa saldos y kardex |
| Independencia funcional del módulo | 4 | 4 | 5 | A/A+Q: dependencia única y estrecha (`StockGateway`); Inventario no conoce Producción (origen genérico). B: máxima independencia, pero la ganada es aparente porque igual depende de Inventario en los traspasos |
| Costos y trazabilidad | 4 | 4 | 2 | Un solo Kardex oficial; costo por registro guardado. B parte el costo entre dos inventarios |
| Rendimiento | 4 | 4 | 3 | A: N+1 movimientos en una transacción (locks ordenados). B: escrituras extra (saldos propios + traspaso). Sin mediciones reales [NV]; el pool de 6 conexiones pesa en todas |
| Facilidad de pruebas | 4 | 3 | 2 | A: puerto con doble de prueba para lógica; concurrencia con MySQL real (patrón `*_MYSQL_DSN` existente). B: pruebas de conciliación entre dos inventarios |
| Mantenimiento futuro | 4 | 3 | 2 | Un solo lugar para cambios de inventario. B: dos modelos de saldo que mantener sincronizados |
| Tiempo de implementación | 4 | 3 | 1 | A: tablas + servicio + UI. B: mucho más |
| **Total** | **41** | **36** | **23** | |

Límites de la puntuación: no se midió rendimiento ni carga real [NV]; las puntuaciones de regresión, rendimiento y pruebas son juicio razonado con base en el código leído, no resultados.

---

## 6. Riesgos y qué no se pudo verificar

| Riesgo | Efecto | Mitigación |
|---|---|---|
| Lost update / sobreventa en el núcleo compartido [V-código] | Saldos incorrectos con consumos concurrentes | T2 + T4 (ver roadmap) antes de habilitar en datos reales; aplica a **cualquier** arquitectura |
| Deadlocks por orden de bloqueo; sin reintento | Errores intermitentes en registros con muchos insumos | `RecordMovementsTx` ordenado + reintento acotado |
| Transacción sin timeout propio; pool de 6 | Espera indefinida si el pool se satura (incidente gyl) | Transacciones cortas; lecturas precargadas en lote; límite de líneas por registro; prueba de carga |
| Activar/desactivar módulo no inmediato (JWT) | Un tenant puede ver/no ver el módulo hasta re-login | Documentar; apagar por 403 de permisos además de módulo |
| Migración no transaccional | DDL parcial si aborta | Migraciones idempotentes, aditivas, con `HasTable/HasColumn/HasIndex` |
| `Implemented:false` no bloquea | Un tenant con la fila activa accedería | Medir `tenant_modules` con `manufacturing` activo (consulta de solo lectura; autorización necesaria) |
| Costos con IGV; traslados sin costo; entradas sin costo | Costos erróneos | Ver §7 de [Integración](PRODUCTION-DECOUPLED-INVENTORY-INTEGRATION.md) y D-04/D-05 |
| Variantes: `RecordMovementTx` solo actualiza la fila de presentación; `StockSummary` las ignora | Disponibilidad incorrecta | Variantes fuera de V1 |
| **No verificado:** duplicados reales, diferencias saldo/kardex, `sql_mode`, aislamiento, `DB_TENANT_MAX_OPEN` en producción; si el refresh token de 30 d reconstruye módulos en el JWT; frontend restaurante a fondo | — | Requieren medición con autorización (ver roadmap, etapa 2) |

---

## 7. Recomendación final
1. **V1 = Alternativa A con adaptador `StockGateway`**, sin stock intermedio, con `status` del registro previsto para extender a «pendiente de aprobación» en V1.5.
2. Contrato mínimo con Inventario (additivo): `SourceType/SourceID`, `RecordMovementResultTx`, redondeo/epsilon, bloqueo ordenado con reintento. No se cambia la firma de `RecordMovementTx`; no se admite fecha retroactiva.
3. El endurecimiento del núcleo es independiente del módulo y bloquea solo su **habilitación en producción**, no su desarrollo.
4. No construir B1. Si algún día se necesita WIP valorizado, evaluar un almacén virtual del inventario oficial, no saldos propios en Producción.

Decisiones que requieren su aprobación: ver [PRODUCTION-ARCHITECTURE-DECISION-RECORD.md](PRODUCTION-ARCHITECTURE-DECISION-RECORD.md).
