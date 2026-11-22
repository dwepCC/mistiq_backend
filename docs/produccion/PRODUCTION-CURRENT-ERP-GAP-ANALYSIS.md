# Módulo de Producción: análisis del ERP actual (capacidades, brechas y riesgos)

> **Errata (auditoría 3, 2026-10-13)** — §7.4 sugería pasar `manufacturing` a `Implemented: true`: debe hacerse solo al liberar y sin asignarlo a planes; `RequireModule` lee el JWT (efecto al re-login); `WriteAuditLog` es central y best-effort; las migraciones tenant no son transaccionales.
> Detalle y evidencia: [PRODUCTION-ARCHITECTURE-DECISION-RECORD.md](PRODUCTION-ARCHITECTURE-DECISION-RECORD.md) §2 y [PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md](PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md) §2. El texto original se conserva.

> **Errata posterior (2026-10-13)** — revisión crítica: correcciones de la verificación independiente (Anexo B): (1) §2.4 afirma que el costo de compra se desagrega si el precio incluye IGV — **falso**: solo se desagregan subtotal/IGV/total; el `UnitCost` del kardex y `purchase_price` es el valor tecleado; (2) las «rutas legacy de series» que escriben stock directo (`adjustmentIn/OutWithSerials`) y los handlers HTML son **código muerto** sin llamador; (3) `WeightedAverageUnitCosts` ignora `PresentationID` y no transfiere costo entre sucursales; (4) la doble fila de saldo está mitigada por `EnsureProductBranchLink`; el riesgo dominante es el lost update; (5) nuevos: comparación flotante sin epsilon en `RecordMovementTx`, `Transfer()` en dos transacciones, la compra actualiza `purchase_price` aunque no haya `ManageStock`.
> Detalle: [PRODUCTION-FINAL-CRITICAL-REVIEW.md](PRODUCTION-FINAL-CRITICAL-REVIEW.md). El texto original se conserva como evidencia del análisis previo.

Fecha: 2026-10-13. Alcance: solo lectura sobre `backend_principal`, `frontend_tenant`, `frontend_restaurant_tenant` y `frontend_central`. No se ejecutó código, migraciones, BD ni VPS.

Convenciones del documento:
- **[V]** = hecho verificado leyendo el código citado (archivo:línea). No significa verificado en ejecución.
- **[I]** = interpretación o inferencia mía a partir del código.
- **[NV]** = no verificado (ver sección 9).
- Todas las rutas son relativas a `D:\tukifac_premium_prod\backend_principal` salvo que se indique `frontend_*`.

---

## 1. Resumen

1. **No existe nada de producción/BOM/recetas en el ERP.** [V] La única señal es: (a) la clave de módulo `manufacturing` en el catálogo central con `Implemented: false` (`pkg/database/migrations.go:546`, semilla en `:695`, `pkg/database/tenant_provision.go:35`); (b) el tipo de operación de kardex `PRODUCTION_OUT` "Salida a producción" (SUNAT 10) (`pkg/database/inventory_catalog_seed.go:30`); (c) el flag de features `restaurant_recipes` que está hardcodeado en `false` y no tiene backend (`pkg/database/schema_features.go:45`, `frontend_tenant/src/contexts/FeatureContext.tsx:15,28`). No hay `PRODUCTION_IN` ni tipo para ingreso de producto terminado.
2. **Lo que sí se reutiliza bien:** catálogo de productos con unidad base + `SaleUnit` (conversión rollo→metro verificada en compras y ventas), stock por (producto, sucursal), kardex único `tenant_stock_movements` con `UnitCost` decimal(15,6), un único punto de escritura de stock (`InventoryService.RecordMovementTx`), tipos de operación SUNAT (incluye merma, desmedro, destrucción), series de documentos con `SELECT ... FOR UPDATE` (`pkg/docseries/correlative.go:34`), patrón de migraciones versionadas, módulos por plan, permisos por rol, idempotencia por clave en ventas.
3. **Brechas críticas para producción:**
   - `RecordMovementTx` hace lectura-modificación-escritura del saldo **sin bloqueo de fila** y sin índice único en las tablas de stock: dos transacciones concurrentes pueden pisarse el saldo y dejar el kardex inconsistente (sección 6).
   - El costo **no está modelado como valoración**: `purchase_price` del producto es "último costo de compra", no promedio; las salidas por venta, transferencias y reversiones se registran con `UnitCost = 0`; el único promedio ponderado existente es un cálculo que reproduce todo el kardex y solo lo usa la importación de ajustes.
   - No hay concepto de "almacén" distinto de "sucursal"; no hay lotes (solo series), ni vencimiento por lote.
   - El reporte de utilidades multiplica un costo en unidad base por una cantidad en unidad comercial: está mal para ventas con `SaleUnit` (sección 5.4).
   - Variante = `TenantProductPresentation` con nombre libre y precio; **no tiene código, costo, atributos estructurados ni columnas talla/color**; "atributos" son texto informativo sin stock.
4. **Decisión de arquitectura sugerida [I]:** módulo nuevo `internal/production` con su propio conjunto de tablas tenant (`tenant_production_*`), clave de módulo existente `manufacturing` (pasar a `Implemented: true`), permisos `production.*`, y **todo movimiento de stock vía `InventoryService.RecordMovementTx`** dentro de una transacción, después de endurecer ese método (bloqueo de fila + costo). No duplicar el sistema de combos ni inventar un segundo kardex.

---

## 2. Inventario de capacidades existentes (con referencias)

### 2.1 Productos

| Aspecto | Hecho | Evidencia |
|---|---|---|
| Modelo | `TenantProduct`: `Type` solo `product`/`service` (default `product`); `ManageStock`, `ManageSeries`, `HasVariants`, `HasCombo`, `IsRestaurant`, `MinStock` decimal(15,3), `PurchasePrice` decimal(15,6), `SalePrice` decimal(15,2), `Unit` (código SUNAT denormalizado) y `UnitID` (FK a `TenantUnit`), `BranchID` (solo platos Tukichef) [V] | `pkg/database/migrations.go:1048-1092` |
| Servicios vs bienes | Servicio = `Type == "service"` (case-insensitive); un servicio no mueve stock en ventas [V] | `internal/sales/service/sale_service.go:103-108`, `:817` |
| Sin clasificación de producción | No hay campo para "materia prima / semi-elaborado / producto terminado / insumo". `Type` solo distingue bien/servicio [V] | `pkg/database/migrations.go:1055` |
| Unidades | `TenantUnit` es catálogo tenant (códigos SUNAT N°03), con `Code`, `Name`, `Symbol` [V]. El producto guarda `UnitID` + `Unit` denormalizada | `pkg/database/migrations.go:1019-1034`, `:1056-1061` |
| Cuota de plan | Los productos cuentan contra `limits.products` del plan [V front] / [I] insumos y semielaborados consumirían cupo | `frontend_tenant/src/contexts/ModulesContext.tsx:30-37` |

### 2.2 Variantes, atributos, unidades de venta, precios

| Aspecto | Hecho | Evidencia |
|---|---|---|
| Variante con stock propio | `TenantProductPresentation` (`ProductID`, `Name` libre, `SalePrice`, `SortOrder`, `Active`, soft delete). Stock propio en `TenantProductPresentationStock(presentation_id, branch_id, quantity)`. El producto debe tener `HasVariants=true` [V] | `pkg/database/migrations.go:1169-1179`, `:1399-1405` |
| Sin talla/color estructurados | La presentación solo tiene `Name` (texto libre). No hay código/SKU, costo, ni código de barras propios por variante [V] | `pkg/database/migrations.go:1169-1179` |
| Atributos | `TenantProductAttribute` (Name/Value) es **solo informativo**: sin stock, sin kardex, sin precio, sin combinatoria con presentaciones [V, lo declara el comentario del modelo] | `pkg/database/migrations.go:1289-1313` |
| Regla de variantes | Un producto con `HasVariants` y presentaciones **siempre** mueve stock por presentación; sin presentación se rechaza | `internal/inventory/service/inventory_service.go:86-117` (`CheckProductPresentation`) |
| SaleUnit | `TenantProductSaleUnit`: `ConversionFactor` decimal(15,6) relativo a la unidad base del producto, `AllowFraction`, `IsBase`, 3 niveles de precio (`Price1/2/3`), código SUNAT propio (`UnitID/Unit`). **Sin stock propio**: es una "lente" sobre `TenantProductStock` [V] | `pkg/database/migrations.go:1181-1233` |
| Conversión | `saleunit.ResolveForLine` es el único punto: `BaseQuantity = cantidad_comercial * factor`; valida pertenencia, activa, factor>0 y `AllowFraction` [V]. No redondea `BaseQuantity` | `pkg/saleunit/resolver.go:33-67` |
| Compra en rollos, consumo en metros | **Sí funciona** si el producto tiene unidad base METRO y una SaleUnit "Rollo" con factor N: la compra convierte cantidad y costo a base (`purchaseBaseUnitCost`) y guarda snapshot (`SaleUnitID`, `SaleUnitQuantity`, `ConversionFactor`) en el movimiento [V] | `internal/purchases/service/purchase_service.go:305-320` (resolución), `:358-371` (movimiento), `internal/purchases/service/purchase_unit_resolver.go:33-60` (validación; no admite productos con series) |
| Conversión es por producto | No existe tabla global de conversiones entre unidades (KG↔g, m↔cm). Cada factor vive en la SaleUnit de **ese** producto [V, por ausencia en el modelo] | `pkg/database/migrations.go:1194` |
| Precio por sucursal | `TenantProductBranchPrice` (producto) y `TenantProductSaleUnitBranchPrice` (SaleUnit): overrides de precio, único `(producto|saleunit, branch)`, sin soft delete. Resueltos en `ProductService.loadProductBranchPriceOverrides` y `pkg/saleunit.ResolvePrice` [V] | `pkg/database/migrations.go:1251-1287`, `pkg/saleunit/price.go:35-70` |
| Multiprecios | Solo en `SaleUnit` (`Price1/2/3`) y su override por sucursal; el producto base solo tiene `SalePrice` [V] | `pkg/database/migrations.go:1225-1227` |
| Migraciones asociadas | v134 SaleUnit, v135-v137 integración venta/compra/precio sucursal, v138 atributos, v139 código SUNAT, v148 precio sucursal producto, v157 presentación en línea de compra [V] | `pkg/database/tenantmigrations/registry.go` |

### 2.3 Sucursales, existencias y kardex

| Aspecto | Hecho | Evidencia |
|---|---|---|
| **No hay almacén separado** | La unidad de ubicación de stock es la sucursal (`branch_id`). No existen `warehouse`/`almacen` como entidad; "almacén" solo aparece como categoría de **series de documentos** (`category="almacen"`) [V] | búsqueda `warehouse` sin resultados en `pkg/` e `internal/`; `pkg/database/inventory_catalog_seed.go:100-111`; `pkg/docseries/document_types.go:82-87` |
| Existencias | `TenantProductStock(product_id, branch_id, quantity decimal(15,3))`, índices **no únicos** sobre `product_id` y `branch_id` [V]. `TenantProductPresentationStock(presentation_id, branch_id, quantity)`, idem [V] | `pkg/database/migrations.go:1387-1405` |
| Kardex | `TenantStockMovement`: `Type` (`in`, `out`, `transfer`, `adjustment`, más `adjustment_in`/`adjustment_out` aceptados en código), `Quantity` decimal(15,3) siempre en unidad base, `UnitCost` decimal(15,6), `Balance` decimal(15,3), `Reference` (string), `OperationTypeID`, `InventoryDocumentID`, `TransferID`, `SaleItemID`, `PurchaseItemID`, `PresentationID`, snapshot de SaleUnit, `UserID`. **No tiene `branch_id` de destino ni FK genérica `source_type/source_id`** [V] | `pkg/database/migrations.go:1407-1447` |
| Único punto de escritura | `InventoryService.RecordMovementTx(tx, MovementInput)`: valida producto+sucursal, `Quantity>0`, resuelve tipo de operación por `OperationCode`, valida presentación, calcula saldo, **rechaza saldo negativo en `out`/`adjustment_out`**, crea el movimiento y actualiza o crea la fila de stock [V] | `internal/inventory/service/inventory_service.go:134-210` |
| `RecordMovementTx` no valida `ManageStock` | Cada llamador lo verifica antes (ventas `sale_service.go:817`, compras `purchase_service.go:352`, documentos `inventory_document_service.go:475`). Un módulo nuevo debe hacerlo [V] | los puntos citados |
| Movimientos fuera del servicio | `adjustmentInWithSerials`/`adjustmentOutWithSerials` insertan `TenantStockMovement` y tocan stock directamente (ignorando errores) en lugar de pasar por `RecordMovementTx` [V]. Son rutas legacy de series | `internal/inventory/service/inventory_service.go:895-985` |
| Tipos de operación | Catálogo `TenantInventoryOperationType` (Tabla 12 SUNAT) sembrado por código: IN: `PURCHASE`, `INITIAL_STOCK`, `RETURN_IN`, `CONSIGNMENT_IN`, `OTHER_IN`, `INVENTORY_ADJUSTMENT_IN`, `TRANSFER`; OUT: `SALE`, `RETURN_OUT`, `DONATION`, **`PRODUCTION_OUT`**, `WITHDRAWAL`, **`SHRINKAGE` (merma)**, **`WASTE` (desmedro)**, `DESTRUCTION`, `CONSIGNMENT_OUT`, `PROMOTION`, `PRIZE`, `OTHER_OUT`, `INVENTORY_ADJUSTMENT_OUT`. Se agregan filas nuevas con migración + `SeedInventoryOperationTypes` (idempotente por `code`) [V] | `pkg/database/inventory_catalog_seed.go:16-40`, `:43-70`; ejemplo: `pkg/database/tenantmigrations/v084_inventory_operation_transfer.go` |
| Documentos manuales | `TenantInventoryDocument` (draft→confirmed→voided) con serie/correlativo de categoría `almacen` (`INGRESO_INVENTARIO`/`EGRESO_INVENTARIO`) por sucursal; bloquea el **documento** con `FOR UPDATE` al confirmar/anular. `TenantInventoryDocumentDetail.UnitCost` es **decimal(15,2)** (pierde precisión vs. el kardex 15,6) [V] | `pkg/database/migrations.go:1465-1501`, `internal/inventory/service/inventory_document_service.go:345-408`, `:532-578` |
| Ajustes | `POST /inventory/adjustment` crea un documento vía `RecordAdjustmentViaDocument` (exige motivo; exige `ManageStock`) y lo confirma [V] | `internal/inventory/service/inventory_document_service.go:217-260` |
| Traslados | Cabecera `TenantTransfer` (pending→confirmed/cancelled) + `TenantTransferLog`. Salida en origen al crear, entrada en destino al confirmar. **Los movimientos de traslado no llevan `UnitCost`** [V] | `internal/inventory/service/inventory_service.go:527-690` |
| Reversión por referencia | La anulación de venta repone leyendo el kardex por `reference = "VENTA/<número>" AND type = "out"` y emite `in` **sin costo** [V]. Las consultas de kardex filtran con `reference LIKE 'COMPRA/%'`, `'VENTA/%'`, `'TRANSFER%'` | `internal/sales/service/sale_cancel_stock.go:25-87`; `internal/inventory/service/inventory_service.go:1023-1031` |
| Series (números de serie) | `TenantProductSerial` con estados `available/reserved/sold/transferred/in_transit/removed` [V]. **No hay lotes** (`lot`, `batch`) en ningún modelo | `pkg/database/migrations.go:1156-1166`; búsqueda `lote/batch` sin modelo |
| Vencimiento | `HasExpiryDate` + `ExpiryDate` en el **producto** (una sola fecha por producto, no por lote) [V] | `pkg/database/migrations.go:1085-1086` |
| Resumen de stock | `StockSummary` lee solo `tenant_product_stocks` (no suma presentaciones). `StockTotalsByProductIDs` sí suma presentaciones para productos con `HasVariants` [V] | `internal/inventory/service/inventory_service.go:1086-1126`, `:756-807` |
| Idempotencia | Solo en ventas: `tenant_sales.idempotency_key` + UNIQUE `(user_id, idempotency_key)` (v150) y replay en conflicto. **Ningún movimiento de stock ni documento de inventario es idempotente** [V] | `pkg/database/tenantmigrations/v150_sale_idempotency_key.go`, `internal/sales/service/sale_idempotency.go`, `sale_service.go:953-958` |

### 2.4 Compras, ventas y costo

| Aspecto | Hecho | Evidencia |
|---|---|---|
| Ingreso de costo por compra | La compra, por cada línea con producto: actualiza `tenant_products.purchase_price = baseUnitCost` (**sobrescribe**, solo si `baseUnitCost > 0`) y registra el movimiento `in` con `UnitCost = baseUnitCost` [V] | `internal/purchases/service/purchase_service.go:94-116`, `:358-371` |
| Costo de compra con IGV | El costo de línea depende de `PriceIncludesIgv` de la compra (si es true, se desagrega) [V]; `TenantPurchaseItem.UnitCost` es decimal(15,2) comercial [V] | `internal/purchases/service/purchase_service.go:183`, `pkg/database/migrations.go:2162-2185` |
| La compra exige caja | Registrar cualquier compra exige sesión de caja abierta del usuario (`ResolveCashSessionForPurchase`) [V, por comentario del modelo] | `pkg/database/migrations.go:2100-2122` |
| Anulación de compra | Revierte stock con `out` leyendo cantidad/costo base del movimiento original (`purchase_item_id`). **No restaura `purchase_price` del producto** [V por ausencia en el flujo] | `internal/purchases/service/purchase_service.go:468`, `:556-587` |
| Venta descuenta stock | Dentro de `s.db.Transaction`, por línea: `RecordMovementTx(out)` con `OperationCode="SALE"`, `SaleItemID`, snapshot SaleUnit. **No pasa `UnitCost`** (el movimiento de venta queda con costo 0) [V] | `internal/sales/service/sale_service.go:709`, `:842-857` |
| Pre-chequeo de stock en venta | Lectura sin bloqueo fuera de la transacción (`stock.Quantity < requested`); el guard real es el de `RecordMovementTx` | `internal/sales/service/sale_service.go:455-500` |
| Costo de venta | `TenantSaleItem.PurchasePrice` = snapshot de `tenant_products.purchase_price` al vender (v145). Es **último costo de compra**, no promedio [V] | `internal/sales/service/sale_service_calc.go:84-97`, `pkg/database/tenantmigrations/v145_sale_item_purchase_price_snapshot.go` |
| Promedio ponderado | Existe `WeightedAverageUnitCosts`: recorre **todos** los movimientos de la sucursal ordenados y recalcula; solo suma cuando `in/adjustment_in` traen `UnitCost > 0`; cae a `purchase_price`. Solo lo usa la importación de ajustes [V]. No hay costo promedio persistido en la fila de stock | `internal/inventory/service/inventory_service.go:809-875`, `inventory_import_adjustment.go:271` |
| Utilidades | `ProfitDetail`: `COALESCE(tenant_sale_items.purchase_price, p.purchase_price, 0)` × `quantity` de la línea [V] | `internal/sales/service/sale_service.go:2021`, `:2089-2110` |
| Combos | `TenantComboGroup`/`TenantComboGroupItem` + `internal/catalog/combos`: el combo es un producto `HasCombo` de precio fijo; **al vender**, el stock sale de los componentes con `ExtraStockMovement`, sin costo; es una explosión en tiempo de venta, no una receta ni una producción [V] | `pkg/database/migrations.go:1351-1385`, `internal/sales/service/sale_combos.go:30-103`, `internal/catalog/combos/combos.go:78-92`, `sale_service.go:905-933` |
| Restaurante | No hay recetas: el plato descuenta stock solo si el propio producto `ManageStock`; los componentes de combos descuentan igual que en el ERP [V]. El flag `restaurant_recipes` es un stub `false` sin implementación [V] | `internal/restaurant/service/restaurant_combos.go:210-257`, `pkg/database/schema_features.go:45` |

### 2.5 Permisos, módulos, auditoría, multi-tenant, migraciones

| Aspecto | Hecho | Evidencia |
|---|---|---|
| Rutas tenant | `tenantAPI := app.Group("/api", TenantAuthAPI(), ValidateTenantBinding(), SubscriptionGate(), BranchContextMiddleware())`; cada módulo expone `RegisterRoutes(tenantAPI)` y se registra en `routes/routes.go` [V] | `routes/routes.go:211`, `:225-249` |
| Gate por módulo | `middleware.RequireModule("key")` lee `claims.Modules` del JWT; `RequireAnyModule` existe [V] | `pkg/middleware/auth.go:209-248` |
| Permisos | `middleware.RequirePermission("modulo.accion")`; `{modulo}.manage` concede todo el módulo; hay implicaciones puntuales en `tenantPermissionImplied` [V] | `pkg/middleware/permissions.go:5-35`, `pkg/middleware/tenant_permissions.go:1-60` |
| Puente restaurante | Solo hace falta si el endpoint también lo consume el POS PIN de Tukichef: `requireInventoryPermOrRestaurant` / `RequireInventoryViewAccess` / `RequireInventoryAdjustAccess` con `HasRestaurantPerm` [V]. Producción en `frontend_tenant` no necesita puente | `pkg/middleware/inventory_access.go:1-50`; memoria del proyecto (incidente 11-sep-2026) |
| Catálogo de permisos | Definido en `RoleService.SeedPermissions` (`internal/users/service/role_service.go:158-167` para inventario, `:208-223` fleet/reports) **y solo se siembra si la tabla está vacía**; los permisos nuevos requieren migración tenant que haga `FirstOrCreate` + backfill a roles (patrón v131, v143, v144, v149) [V] | `pkg/database/tenantmigrations/v131_backfill_modules_ecommerce_fleet_permissions.go:1-110` |
| Roles semilla | Plantillas de roles listan permisos explícitos (p. ej. `inventory.*` en `role_service.go:296-299`) [V] | `internal/users/service/role_service.go:296-299` |
| Módulos por plan | Catálogo en código `moduleCodeCatalog` (Key, Implemented, Requires, Category, SortOrder); `SyncModuleCodeMetadata` lo sincroniza en cada arranque; `saas_modules`, `saas_plan_modules`, `tenant_modules` en BD central; `implemented=false` se oculta al tenant [V] | `pkg/database/migrations.go:502-558`, `:587-620` |
| Módulo ya reservado | `{Key: "manufacturing", Implemented: false, Category: "vertical", SortOrder: 16}`; se crea deshabilitado para cada tenant en `SeedDefaultTenantModules` [V]. Para poder venderlo hay que pasar a `Implemented: true` y definir `Requires` (p. ej. `products,inventory`) | `pkg/database/migrations.go:546`, `pkg/database/tenant_provision.go:30-45` |
| Subfunciones con módulo propio | `despatch` y `advanced_docs` (requieren `billing`) muestran cómo separar y backfillear módulos nuevos en planes/tenants existentes (`MigrateSplitDespatchModules`) [V] | `pkg/database/migrations.go:516-519`, `:2714-2750` |
| Auditoría | **No hay bitácora tenant.** `AuditLog` es **central** (`TenantID`, `UserID`, `Action`, `Entity`, `EntityID`, `Payload`, `IPAddress`) y se escribe con `database.WriteAuditLog(...)` (no propaga error). Ventas anuladas por rechazo y reemisión la usan [V]. El borrado masivo de productos dice textualmente "No existe tabla de auditoría tenant" | `pkg/database/migrations.go:187-197`, `pkg/database/audit_log.go:46-85`, `internal/billing/service/void_rejected.go:208`, `internal/products/service/bulk_delete.go:123` |
| Conexión por tenant | BD física por tenant. El handler obtiene `c.Locals("tenantDB").(*gorm.DB)`; las funciones de servicio reciben `*gorm.DB` ya scopeado (sin `tenant_id` en tablas) [V] | `internal/inventory/handler/inventory_handler.go:20-23`, comentarios en `pkg/saleunit/resolver.go` |
| Pool | `DB_TENANT_MAX_OPEN` por defecto 6 (8 en `.env.production.example`), idle 3–4 [V]. Transacciones largas de producción compiten con login/ventas (ver incidente de pool agotado en memoria) | `config/config.go:196-197`, `.env.production.example:35-36` |
| Migraciones tenant | Interfaz `TenantMigration{Version() int; Name() string; Up(db) error}`; lista en `pkg/database/tenantmigrations/registry.go`; **última registrada: `V160CatalogImages`** (archivo `v160_catalog_images.go`) [V]. Patrón: struct local con `TableName()` para columnas (v145, v150, v157) o `db.AutoMigrate(&database.TenantX{})` para tablas nuevas (v138) [V]. Siempre idempotente (`HasTable`/`HasColumn`/`HasIndex`). Tests `vNNN_*_test.go` con la migración. Ejecución: `migrate-fleet` por cron (workers), historial en `tenant_migration_history`, versión objetivo en central (`migrate-bump-target`) [V] | `pkg/database/tenantmigrations/registry.go`, `migration.go`, `docs/MIGRATIONS-SaaS.md`, `pkg/database/engine/runner.go:21-60` |
| Tenants nuevos | `V001BaselineSchema` llama `ApplyBaselineSchema` (AutoMigrate de los structs actuales) y luego corren V031…Vlast; por eso una tabla nueva debe crearla **su propia migración** para tenants existentes y puede quedar también en el baseline [V]/[I] | `pkg/database/tenant_baseline_schema.go:10-60`, `pkg/database/tenantmigrations/v001_baseline_schema.go:16` |
| Backfills de datos | Registry aparte `pkg/database/tenantbackfills` (run-once), independiente del schema [V] | `docs/MIGRATIONS-SaaS.md` |
| Estructura de módulo | `internal/<modulo>/{routes.go, handler/, service/}`; handler sin estado, `db(c)` por `tenantDB`, servicios `NewXService(db)`; ej. `internal/inventory`, `internal/fleet`, `internal/purchases` [V] | `internal/inventory/routes.go`, `internal/fleet/routes.go` |

### 2.6 Frontend (`frontend_tenant`)

| Aspecto | Hecho | Evidencia |
|---|---|---|
| Stack | React 18 + react-router-dom 6 + axios + react-hook-form + tailwind + vite. **Sin store global (no Zustand/Redux)**: estado vía Contexts (`AuthContext`, `BranchContext`, `ModulesContext`, `FeatureContext`…) y estado local [V] | `frontend_tenant/package.json`, `frontend_tenant/src/contexts/` |
| Rutas | Un único `AppRouter.tsx` con `lazy()` por página y `<Protected perm="...">`; ej.: `inventory/kardex` → `Protected perm="inventory.view"` [V] | `frontend_tenant/src/components/routing/AppRouter.tsx:35`, `:131-138`, `:196` |
| Menú | `Sidebar.tsx`: arreglo `SIMPLE_ITEMS` y grupos con hijos `{id,to,label,icon,module,permission,exact,hideIfLocked}`; el grupo "Inventario" ya existe (`id: 'inventario'`). Módulos separados usan `hideIfLocked` (despatch) [V] | `frontend_tenant/src/components/Sidebar.tsx:16-37`, `:231-248`, `:283-297` |
| Módulo habilitado | `hasModule` de `AuthContext` (JWT) es el gate; `ModulesContext` aporta catálogo/upsell desde `GET /api/session/modules` [V] | `frontend_tenant/src/contexts/ModulesContext.tsx` |
| Servicios API | Un `*.service.ts` por dominio en `src/services` (p. ej. `inventory.service.ts`, `products.service.ts`) [V] | `frontend_tenant/src/services/` |
| Selectores de producto | (a) `ProductPickerModal` (ventas/compras; variantes `sale`/`purchase`, filtra por sucursal activa, muestra stock/compra/precio); (b) `ProductServerSelect` (búsqueda en servidor, hardcodeado a `manage_stock_only=true`, devuelve `{id,name,code}`, **no devuelve unidad ni costo**) [V] | `frontend_tenant/src/components/sales/ProductPickerModal.tsx`, `frontend_tenant/src/components/inventory/ProductServerSelect.tsx:1-60` |
| Filtros del listado | `productsService.list(q, category_id, restaurant_only, active_only, page, per_page, manage_stock_only, catalog_type, branch_id, exclude_combos, brand_id, inactive_only)` [V] | `frontend_tenant/src/services/products.service.ts:420-437` |
| Editor de SaleUnits | `SaleUnitsEditor`/`SaleUnitsModal`, `ProductAttributesEditor`, `ProductPresentationsEditor` ya existen y se reutilizan para configurar rollos/metros [V] | `frontend_tenant/src/components/products/` |
| `frontend_restaurant_tenant` | Es el POS PIN de Tukichef; no es destino de producción [I]. Tiene su propio `FeatureContext` con `restaurant_recipes` (stub) [V] | `frontend_restaurant_tenant/src/contexts/FeatureContext.tsx:15,28` |

---

## 3. Tabla: necesidad de producción → estado → evidencia

Leyenda de estado: **Existe** (se usa tal cual), **Reutilizable** (se usa sin cambios de comportamiento desde el módulo nuevo), **Adaptar** (existe pero hay que modificarlo/endurecerlo), **Desarrollar** (no existe).

| # | Necesidad | Estado | Evidencia / comentario |
|---|---|---|---|
| 1 | Catálogo de materias primas, semielaborados y producto terminado | Adaptar | Todos son `TenantProduct` (`Type=product`). Falta clasificar (campo nuevo o tabla de rol de producción). `migrations.go:1048-1092` |
| 2 | Unidad base y conversión de compra (rollo) a consumo (metro) | Reutilizable | `SaleUnit` + `saleunit.ResolveForLine` + compra con SaleUnit (v136). `purchase_service.go:305-320`. Limitación: conversión por producto, no global; productos con series no soportan SaleUnit en compra (`purchase_unit_resolver.go:51`) |
| 3 | Receta / BOM (insumos y cantidades por unidad producida, merma esperada, rendimiento) | Desarrollar | Sin tablas ni servicios. Combos NO sirven (precio fijo, explosión en venta, sin versiones ni merma, sin costo). `combos.go:78-92` |
| 4 | Versionado de receta (vigencia, versión activa) | Desarrollar | No existe patrón de versionado de entidades en el repo [NV: no se buscó exhaustivamente fuera de productos] |
| 5 | Orden de producción (estados, fechas, responsable, sucursal) | Desarrollar | Sin entidad. Patrón de referencia de estados: `TenantInventoryDocument` draft→confirmed→voided, y `TenantTransfer` pending→confirmed→cancelled |
| 6 | Consumo de insumos (salida de stock por orden) | Adaptar | Camino técnico: `RecordMovementTx(out)` con `OperationCode: "PRODUCTION_OUT"` (ya sembrado). Debe llevar `UnitCost` del insumo (hoy ningún `out` lo lleva) y bloqueo de fila (hoy no existe) |
| 7 | Ingreso de producto terminado | Adaptar | Falta tipo de operación IN para producción (no hay `PRODUCTION_IN`); alta por migración con `SeedInventoryOperationTypes`. Hay que decidir el código SUNAT (Tabla 12 no tiene un código específico; ver riesgo en sección 6) |
| 8 | Mermas y desperdicios en producción | Reutilizable | `SHRINKAGE`, `WASTE`, `DESTRUCTION` ya existen como OUT manuales (`inventory_catalog_seed.go:32-34`). Falta vincularlos a la orden y a la receta |
| 9 | Costeo del producto terminado (insumos + mano de obra + indirectos) | Desarrollar | No hay costo promedio persistido ni centros de costo ni mano de obra. `purchase_price` = último costo. `WeightedAverageUnitCosts` es O(n) por consulta y solo para importación |
| 10 | Kardex valorizado confiable | Adaptar | `UnitCost` existe en el movimiento pero salidas/traslados/reversiones van con 0; el saldo valorizado no es calculable hoy sin reglas nuevas |
| 11 | Reservas / disponibilidad (reservar insumos para una orden) | Desarrollar | No existe reserva de stock (solo `reserved` en series). Restaurante tampoco reserva (`restaurant_order_stock.go:14-24`) |
| 12 | Varios almacenes por sucursal (materia prima vs terminado vs en proceso) | Desarrollar | Almacén ≡ sucursal. Alternativas: modelar almacén de producción como sucursal, o introducir almacén (cambio transversal grande) |
| 13 | Lotes y trazabilidad | Desarrollar | Solo series (`TenantProductSerial`), sin lote. `ManageSeries` limita enteros y SaleUnit |
| 14 | Variantes talla/color como SKU | Adaptar | Presentación con stock propio existe; sin código/costo/atributos estructurados y con nombre libre. `migrations.go:1169-1179` |
| 15 | Producto terminado con variantes | Adaptar | `RecordMovementTx` exige presentación si `HasVariants` (`inventory_service.go:98-105`); la receta debe poder apuntar a presentación de salida y de entrada |
| 16 | Precio de venta por sucursal / multiprecio del terminado | Reutilizable | v137/v148, `pkg/saleunit/price.go`. Sin cambios |
| 17 | Venta del producto terminado y costo de venta | Adaptar | Snapshot `purchase_price` se alimentaría del costo producido solo si se actualiza `tenant_products.purchase_price` (hoy solo lo hace la compra). Ver 5.4 sobre SaleUnit |
| 18 | Permisos granulares `production.*` | Desarrollar | Patrón claro (`RequirePermission`, v131/v143). Requiere migración de catálogo + backfill a roles |
| 19 | Módulo por plan (`manufacturing`) | Adaptar | Clave existe, `Implemented:false`. `migrations.go:546` |
| 20 | Auditoría de acciones de producción | Adaptar | Solo `AuditLog` central vía `WriteAuditLog`. No hay bitácora tenant. Decidir si basta con estados + usuario + fechas en la propia orden |
| 21 | Idempotencia en confirmación/cierre de órdenes | Desarrollar | Existe patrón en ventas (clave única + replay). No hay para stock |
| 22 | Atomicidad y concurrencia en consumo/ingreso | Adaptar | Transacción disponible (`db.Transaction`), pero sin lock de filas de stock (ver 6) |
| 23 | Reportes (consumo vs teórico, costo por orden, rendimiento) | Desarrollar | `GetKardex` y filtros por `reference LIKE`; sin dimensión de orden. `inventory_service.go:986-1084` |
| 24 | Menú, rutas y pantallas | Desarrollar (convención clara) | `Sidebar.tsx` grupo nuevo + `AppRouter.tsx` rutas con `Protected` + `*.service.ts` |
| 25 | Selector de insumos con unidad y costo | Adaptar | `ProductServerSelect` solo devuelve `{id,name,code}`; `ProductPickerModal` es de ventas/compras. Falta un selector que muestre unidad base, stock y costo |
| 26 | Tenants y migración de flota | Reutilizable | Mecanismo de migraciones versionadas y `migrate-fleet`. Siguiente número libre: **V161** |

---

## 4. Variantes y atributos: lo que realmente hay

- Modelo real [V]: el producto "padre" (`TenantProduct`, `HasVariants=true`) más N `TenantProductPresentation` (cada una es un SKU de stock). El "stock total" del padre es la suma de presentaciones (`inventory_service.go:780-799`).
- Una variante talla+color debe codificarse en el `Name` libre de la presentación (por ejemplo "M / Rojo") [I]. No hay maestro de tallas/colores ni combinatoria. `TenantProductAttribute` no participa del stock [V].
- Implicaciones para producción:
  - Una receta cuyo insumo es "tela" por color necesita apuntar a **presentación** de insumo; `MovementInput.PresentationID` lo soporta [V].
  - El costo vive en el producto, no en la presentación: dos colores de una misma tela comparten `purchase_price` [V]. Para costo por variante habría que cambiar el modelo.
  - Las compras ya aceptan `PresentationID` en la línea (v157) [V], pero el reporte `StockSummary` no incluye stock de presentaciones [V].
  - `v156_repair_has_variants` documenta que formularios que no enviaban presentaciones apagaban `has_variants`; es un riesgo real de integridad al editar productos desde otros formularios [V].

---

## 5. Brechas reales y trampas (cosas que existen pero no sirven para producción)

### 5.1 Concurrencia en stock (crítica)
- `RecordMovementTx` lee el saldo con `tx.Where(...).First(&stock)` sin `clause.Locking`, calcula `newBalance` en memoria y escribe [V] (`inventory_service.go:152-210`).
- Búsqueda de `clause.Locking{Strength: "UPDATE"}` en `internal/` y `pkg/`: solo en documentos de inventario (bloquea el **documento**), series de documento, facturación, equipos, restaurante (mesas), prepago y SaaS. **Ninguna sobre `tenant_product_stocks` ni `tenant_product_presentation_stocks`** [V].
- Las filas de stock no tienen índice único `(product_id, branch_id)` [V] (`migrations.go:1387-1393`); el código tolera filas duplicadas en lecturas (`SUM`, comentario en `inventory_service.go:294-296`) pero la escritura asume una sola.
- Bajo `REPEATABLE READ` (default de MySQL/InnoDB, no se encontró configuración distinta [NV]) dos órdenes que consuman el mismo insumo en paralelo pueden calcular el mismo saldo previo y pisarse (lost update) o crear dos filas de stock para el mismo par. [I] La probabilidad aumenta con producción masiva y con pool pequeño.
- **Recomendación:** antes de usar `RecordMovementTx` desde producción, agregarle `SELECT ... FOR UPDATE` sobre la fila de stock (o `UPDATE ... SET quantity = quantity - ? WHERE quantity >= ?` atómico) y un UNIQUE `(product_id, branch_id)` / `(presentation_id, branch_id)` con deduplicación previa. Es un cambio transversal que afecta ventas, compras y restaurante: requiere migración y pruebas.

### 5.2 Costo por movimiento y valoración
- Salidas por venta (`sale_service.go:842-857`), traslados (`inventory_service.go:599-605`, `:669-675`) y reversiones de venta (`sale_cancel_stock.go:67-84`) no pasan `UnitCost` [V]; quedan en 0.
- `WeightedAverageUnitCosts` ignora salidas para el costo (correcto para promedio móvil), ignora entradas con costo 0 (evita diluir el promedio con reversiones) y se recalcula desde cero por consulta, con `Find` de **todos** los movimientos de los productos [V]. No escala como costo de cada consumo de producción.
- `tenant_products.purchase_price` es "último costo": una compra a precio distinto lo sobrescribe; la anulación de la compra no lo restaura [V].
- `TenantInventoryDocumentDetail.UnitCost` es decimal(15,2) y `TenantPurchaseItem.UnitCost` decimal(15,2), mientras el kardex y el producto usan (15,6) [V]. Un costo calculado de producción (p. ej. 4.166667) no sobrevive a un documento de inventario.
- **Trampa:** el reporte "kardex valorizado" no existe; `Balance` es cantidad, no valor.
- **Recomendación [I]:** definir una política única (promedio móvil por producto y sucursal, persistido en la fila de stock con bloqueo) antes de producir costos; escribir el costo en cada movimiento de producción (consumo al costo promedio vigente, ingreso al costo calculado de la orden) y actualizar `purchase_price` del producto terminado solo si se decide que "costo estándar = último costo producido".

### 5.3 Almacenes
- No hay `almacén`; la dimensión es `branch_id`. Para separar materia prima, producto en proceso y terminado hay dos caminos: crear sucursales "virtuales" (afecta series de documentos, precios por sucursal, límite de sucursales del plan, usuarios por sucursal, reportes) o introducir una entidad almacén (cambia `tenant_product_stocks`, kardex y todos los consumidores).
- Verificar el límite `limits.branches` del plan antes de proponer sucursales virtuales (`ModulesContext.tsx:30-37`) [V que existe el límite; el valor por plan [NV]].

### 5.4 Unidades y redondeos
- `BaseQuantity = cantidad * factor` sin redondeo (`pkg/saleunit/resolver.go:55-58`) [V]. El campo destino es decimal(15,3) [V]. Un consumo de receta (p. ej. 0.0004 m por unidad × 1 000) puede perder precisión al guardar. `RecordMovementTx` valida `Quantity > 0` **antes** de que MySQL redondee a 3 decimales; una cantidad < 0.0005 pasaría la validación y quedaría en 0.000 (o error según modo estricto) [I; depende de `sql_mode` [NV]].
- El saldo (`Balance`/`quantity`) se calcula en `float64` y se guarda a 3 decimales; la suma de cantidades redondeadas de movimientos puede diferir del saldo [I].
- `TenantProductStock.Quantity` y todas las cantidades son decimal(15,3): insuficiente para recetas con alta resolución (gramos de aditivo por prenda) a menos que se defina el insumo en la unidad más pequeña [I].
- **Trampa de utilidades con SaleUnit [V por lectura de código, [NV] en ejecución]:** `resolveSaleItemPurchasePrice` guarda `product.PurchasePrice` (costo por unidad **base**) sin multiplicar por el factor de la SaleUnit de la línea (`sale_service_calc.go:84-97`, sin referencia a `ConversionFactor` en `sale_service_calc.go`). `ProfitDetail` multiplica ese costo por `quantity` de la línea, que es cantidad **comercial** (`sale_service.go:2089-2110`). Para una venta de "1.5 Saco 100 KG" el costo mostrado sería ≈ 1.5 × costo/KG en vez de 150 × costo/KG. Si el producto terminado se vende por caja/docena (SaleUnit), la utilidad quedará inflada. Hay que corregirlo antes de apoyar la rentabilidad de producción en ese reporte.
- Productos con series (`ManageSeries`) no admiten SaleUnit en compras (`purchase_unit_resolver.go:51-53`) y exigen cantidades enteras [V]; no mezclar con insumos fraccionables.
- `AllowFraction` solo se aplica a SaleUnits; en un producto "normal" las cantidades fraccionarias se aceptan sin validación de unidad (la validación es `> 0`) [V].

### 5.5 Combos y recetas: duplicidad a evitar
- Combos = explosión en la **venta** con precio fijo; descuentan componentes sin costo, y sus movimientos se revierten leyendo el kardex por referencia de venta (`sale_cancel_stock.go:15-25`) [V]. Reutilizar `TenantComboGroup` para BOM mezclaría selección del cliente con ingeniería de producto y arrastraría reglas de POS/restaurante/ecommerce.
- Costeo de un combo: el snapshot `purchase_price` usa el producto combo (probablemente 0), no la suma de componentes; las salidas de componentes van con costo 0 [V por lectura; valor real [NV]].
- **Recomendación:** BOM propia, independiente de combos. Opcional a futuro: "producto terminado fabricado a pedido" que use la receta para explotar en venta, solo si hay requerimiento.

### 5.6 Cosas que existen como endpoint/tabla pero no sirven tal cual
- `POST /inventory/adjustment` y documentos manuales: exigen documento con serie `almacen` por sucursal y correlativo; no son idempotentes ni transaccionales a nivel de lote de movimientos de una orden de producción; el costo en detalle se trunca a 2 decimales [V].
- `PRODUCTION_OUT` está sembrado pero `AllowManual: true` y sin `RequiresDocument`: un usuario podría usarlo en un egreso manual sin orden de producción [V]. Si se quiere control, hay que decidir si se bloquea (`AllowManual:false` requiere re-seed; el seed solo inserta si el código no existe, no actualiza [V] `inventory_catalog_seed.go:43-70`).
- `StockSummary` no incluye presentaciones (`inventory_service.go:1086-1126`) [V].
- Kardex por "tipo de movimiento" depende de prefijos de `reference` (`COMPRA/`, `VENTA/`, `TRANSFER`) [V]; un movimiento de producción con otra referencia no aparecerá en los filtros rápidos, solo por `OperationCode`/`operation_type_id`.
- `ConfirmTransfer` y `CancelTransfer` leen la cabecera fuera de la transacción y sin bloqueo (`inventory_service.go:632-690`) [V]; el patrón no debe copiarse para confirmar órdenes de producción.

---

## 6. Riesgos de integridad

| Riesgo | Detalle | Severidad |
|---|---|---|
| Pérdida de actualización de saldo | Sin FOR UPDATE ni UNIQUE en stock; lectura-modificación-escritura en Go (5.1) | Alta |
| Doble confirmación de orden / doble consumo | No hay idempotencia en stock; patrón existente de ventas (clave única) no se reutiliza solo. Confirmar debe bloquear la fila de la orden (`FOR UPDATE`, como los documentos de inventario) y comprobar estado | Alta |
| Orden parcial en consumo | Si el consumo de N insumos no es una sola transacción, un fallo a medias deja insumos descontados sin producto terminado. Todo (consumos, ingreso, cambio de estado) debe ir en `db.Transaction` | Alta |
| Kardex y saldo divergentes | Redondeo float64→decimal(15,3) y rutas legacy que escriben stock directo (series) | Media |
| Costo no reproducible | Sin costo en salidas y sin promedio persistido, el costo de producción dependerá de cuándo se calcule | Alta |
| Reversión sin costo | El patrón de reversión (`sale_cancel_stock.go`) repone con `UnitCost=0`; anular una orden de producción con ese patrón perdería el costo de los insumos | Media |
| Aislamiento tenant | Cada servicio recibe un `*gorm.DB` del tenant; mantener `tenantDB` del contexto y nunca guardar `*gorm.DB` en singletons. El `AuditLog` central sí necesita `TenantID` explícito [V] | Media |
| Pool agotado | `DB_TENANT_MAX_OPEN` 6–8. Una transacción de producción con N líneas hace muchos `First` secuenciales (el patrón actual hace varias lecturas por línea). Evitar reportes O(n²) y transacciones largas; el incidente `gylconcesioneseirl` (memoria) muestra el efecto en login | Media |
| Permisos de plan y rol | Sin migración de catálogo + backfill, roles existentes no ven el módulo (lección v131/v132, memoria "Migraciones de tenant deben ser versionadas") | Media |
| Despliegue de migración | Prod no usa AutoMigrate; todo campo nuevo en un struct `tenant_*` necesita migración en `tenantmigrations` **antes** del deploy (memoria, incidente 09-sep-2026) | Alta si se olvida |
| Código SUNAT del ingreso de producción | La Tabla 12 de SUNAT incluye "10 Salida a producción" pero no tiene un código propio de "entrada de producción" que conozca con certeza [I/NV]; confirmar con contabilidad antes de elegir `sunat_code` para `PRODUCTION_IN` (`99 Otros` es el comodín que ya usan otros tipos) | Baja |
| Variantes y `has_variants` | Formularios que reenvían el producto sin presentaciones pueden apagar `has_variants` (v156) | Media |

---

## 7. Convenciones a respetar

1. **Migraciones tenant:** nueva clase `V161...` en `pkg/database/tenantmigrations/v161_*.go`, registrada al final de `TenantMigrations` en `registry.go`; numeración consecutiva global (la última es V160). Idempotente; para columnas usar struct local con `TableName()`; para tablas nuevas `AutoMigrate` del struct en `pkg/database` (patrón v138) más, si procede, agregarlo a `ApplyBaselineSchema` [I: confirmar con el equipo si las tablas nuevas también van al baseline; v138 muestra que ya estaba en baseline]. Test `v161_*_test.go`. Puramente aditivas, sin backfill implícito. Backfills de datos van a `tenantbackfills`. Desplegar: `migrate-central` → deploy → `migrate-fleet` (cron).
2. **Tipos de operación:** nuevas filas por migración llamando `database.SeedInventoryOperationTypes` y añadiéndolas a `inventoryOperationTypeSeeds` (como v084).
3. **Permisos:** definir en `RoleService.SeedPermissions` (`role_service.go`) **y** una migración de backfill con `FirstOrCreate` + asignación a roles existentes (patrón v131/v143/v144/v149). Usar `RequirePermission("production.xxx")`; `production.manage` implica todo vía `splitModuleAction`. Ruta bajo `mod := RequireModule("manufacturing")`. Sin puente restaurantperm salvo que Tukichef consuma el endpoint.
4. **Módulo por plan:** en `moduleCodeCatalog` pasar `manufacturing` a `Implemented: true`, `Requires: "products,inventory"`, nombre/descripción/icono si procede; el seed y `SyncModuleCodeMetadata` lo propagan; asignar en planes desde el panel central. `frontend_central` ya tiene el ícono (`frontend_central/src/services/tenants.service.ts:110`). Si se quiere vender aparte de planes existentes y backfillear, ver `MigrateSplitDespatchModules`.
5. **Capa:** `internal/production/{routes.go,handler/,service/}`; `RegisterRoutes(tenantAPI)` en `routes/routes.go` junto a las demás (`routes.go:225-249`); handlers con `db(c)` desde `tenantDB`, `userID(c)` desde `user_id`.
6. **Stock:** exclusivamente `InventoryService.RecordMovementTx(tx, MovementInput{...})` con `OperationCode`, `Reference` (formato `PRODUCCION/<número>`), `UserID`, `UnitCost`. Verificar `ManageStock` antes. Nunca insertar en `tenant_stock_movements` o modificar `tenant_product_stocks` a mano.
7. **Transacciones:** `db.Transaction` + `Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id)` para confirmar/anular, como `confirmInventoryDocumentTx`; correlativo con `docseries.ReserveNext(tx, seriesID)` si la orden lleva numeración (requiere nueva categoría/tipo de serie en `pkg/docseries/document_types.go`).
8. **Idempotencia:** clave única por intento de confirmación o por orden+operación (patrón v150: columna nullable + UNIQUE, replay en conflicto).
9. **Auditoría:** `database.WriteAuditLog(&database.AuditLog{TenantID, UserID, Action, Entity, EntityID, Payload, IPAddress})`; no hay tabla tenant. Mantener también `created_by/confirmed_by/voided_by` + fechas en la orden (patrón `TenantInventoryDocument`).
10. **Frontend:** rutas `lazy` en `AppRouter.tsx` con `<Protected perm="production.view">`; grupo de menú en `Sidebar.tsx` con `module: 'manufacturing'`, `permission`, y `hideIfLocked: true` si no se quiere upsell; servicio `src/services/production.service.ts`; sin store global, usar Context/estado local y `useBranch()` para la sucursal activa.
11. **Decimales y dinero:** cantidades decimal(15,3); costos decimal(15,6) (`money.RoundSunat` es el redondeo de precisión interna de 6 decimales; `money.RoundDisplay` para presentación) [V `pkg/money/money.go:14-19`].
12. **Tablas tenant:** sin `tenant_id`; BD por tenant. Prefijo `tenant_` y structs `TenantXxx` en `pkg/database`.

---

## 8. Recomendaciones de reutilización

Reutilizar:
- `TenantProduct` + `TenantUnit` + `SaleUnit` + `saleunit.ResolveForLine` para compras en rollos y recetas en metros; `ProductService` para altas.
- `InventoryService.RecordMovementTx` como único escritor de stock/kardex (tras endurecerlo), `MovementInput` con `PurchaseItemID`-like: **agregar** una referencia propia (`ProductionOrderID`) al movimiento mediante columna nullable nueva en `tenant_stock_movements` en vez de depender de `reference LIKE` [I].
- Tipos de operación existentes `PRODUCTION_OUT`, `SHRINKAGE`, `WASTE`, `DESTRUCTION`; agregar `PRODUCTION_IN`.
- Patrón de documento con estados y bloqueo (`TenantInventoryDocument`) como plantilla para la orden de producción.
- `docseries.ReserveNext` para numeración.
- Migraciones, permisos, módulos y menú según la sección 7.
- `ProductPickerModal`/`ProductServerSelect` como base de UI de insumos; extender la respuesta con unidad base y costo.

Evitar:
- Reutilizar `TenantComboGroup` para BOM.
- Documentos de inventario manuales para consumir/ingresar producción (precisión 2 decimales, serie `almacen`, sin vínculo a orden).
- Crear sucursales virtuales sin evaluar límites de plan, series y precios por sucursal.
- Apoyarse en `ProfitDetail` para rentabilidad de producción sin corregir la conversión de SaleUnit (5.4).

Trabajo previo transversal sugerido (fase 0) [I]:
1. Bloqueo de fila y UNIQUE en stock dentro de `RecordMovementTx`, con migración de deduplicación.
2. Política y persistencia del costo promedio (o decisión de costo estándar).
3. Campo `production_order_id` (o genérico `source_type/source_id`) en `tenant_stock_movements`.
4. Alta de `PRODUCTION_IN` y decisión sobre `AllowManual` de `PRODUCTION_OUT`.
5. Corrección de `ProfitDetail`/snapshot para SaleUnit.

---

## 9. Lo que no pude verificar

- Comportamiento en ejecución: no corrí tests ni BD; todo está leído en código. Las trampas de redondeo y de utilidades con SaleUnit son por lectura, sin reproducir.
- `sql_mode` estricto y nivel de aislamiento real de la BD de producción: no consulté el servidor. Afecta el comportamiento de cantidades < 0.0005 y de los lost updates.
- Versión de esquema realmente desplegada en producción: la memoria del proyecto indica que v150/v151 estaban pendientes de migrate-fleet al 05-oct; el repo contiene hasta V160 [V en repo, [NV] en prod]. No asumir que V150–V160 estén aplicadas en todos los tenants al planificar V161.
- Duplicados reales en `tenant_product_stocks` en producción (falta de UNIQUE): no medido.
- Valores de límites por plan (`branches`, `products`) en `saas_plans`.
- Si `ApplyBaselineSchema` incluye ya tablas futuras que vayan al baseline: la lista cubre hasta los modelos mostrados; no revisé el final del archivo completo.
- Búsqueda de "versionado de entidades" en otros módulos (cotizaciones, etc.) fue superficial.
- Frontend: no recorrí `ProductPickerModal` completo ni los formularios de compra para ver cómo envían `sale_unit_id`; el diseño de pantallas de producción queda por definir.
- Código SUNAT apropiado para el ingreso de producto terminado (consulta contable).
- Rutas `.claude/worktrees/*` aparecen en búsquedas pero son copias de trabajo; las ignoré.
