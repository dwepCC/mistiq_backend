> **Anexo B de PRODUCTION-FINAL-CRITICAL-REVIEW.md.** Verificación independiente (subagente, solo lectura) del código de inventario, costos y tipos de operación, 2026-10-13. Yo comprobé personalmente: ausencia de lock en `RecordMovementTx` y que el costo de compra (`UnitCost`) es el valor tecleado (`purchase_service.go:181-184, 317, 364`). Lo demás es hallazgo del subagente con referencia archivo:línea. Las consultas SQL del final son de SOLO LECTURA y NO se han ejecutado: requieren autorización.


# Verificación independiente del análisis previo de inventario (solo lectura)

Fecha: 2026-10-13. Repo: `D:\tukifac_premium_prod\backend_principal`. Rutas relativas a ese repo (se ignoró `.claude/worktrees/*`, copias viejas). No se tocó BD/VPS/migraciones. Única ejecución: un `go run` de aritmética float64 pura en el scratchpad (sin BD).

HECHO = visto en código. INFERENCIA = deducción mía, marcada.

---------------------------------------------------------------------------------

## A. `RecordMovementTx` y sus llamadores

### A.1 Flujo exacto (HECHO) — `internal/inventory/service/inventory_service.go:134-210`
1. L135-140: exige `ProductID>0`, `BranchID>0`, `Quantity>0` (el `Quantity>0` se evalúa sobre el float64 en Go, antes del redondeo a decimal(15,3) de MySQL).
2. L141: `resolveMovementOperationType` (L59-74): si `OperationTypeID` ya viene, no hace nada; si no, y `OperationCode` no está vacío, `LookupOperationTypeByCode` (`operation_type.go:20-31`, filtra `is_active=1`) y falla con `ErrOperationTypeNotFound` si no existe. Con `OperationCode==""` el movimiento queda con `operation_type_id = NULL` sin error. **No valida que `Type` (in/out) coincida con la `direction` del tipo de operación.**
3. L144-148: `CheckProductPresentation` (L86-117): producto con `HasVariants` y presentaciones => obliga a informar presentación (si no, error); presentación debe pertenecer al producto y, en `out`, estar activa; producto sin variantes rechaza `presentation_id`. Si el producto no existe devuelve nil (no falla).
4. L155-161: lee el saldo con `tx.Where(...).First(&pstock|&stock)` — **lectura simple, sin lock, error ignorado** (si no hay fila, `currentQty=0`).
5. L163-177: `in`/`adjustment_in` => saldo+qty; `out`/`adjustment_out` => saldo-qty y **si `newBalance < 0` devuelve error "stock insuficiente"** (sin tolerancia epsilon); `adjustment` => `newBalance = Quantity` (saldo absoluto); cualquier otro tipo (incluido `"transfer"`, que el comentario del struct menciona) => "tipo de movimiento inválido".
6. L179-190: `INSERT` en `tenant_stock_movements` con `Quantity`, `UnitCost`, `Balance=newBalance`, `Reference`, `Notes`, `UserID`, `OperationTypeID`, `InventoryDocumentID`, `TransferID`, `SaleItemID`, `PurchaseItemID`, snapshot SaleUnit (`SaleUnitID/SaleUnitQuantity/ConversionFactor`), `PresentationID`.
7. L192-209: `UPDATE` (o `INSERT` si no había fila) de **solo UNA** tabla: `tenant_product_presentation_stocks` si hay `PresentationID`, o `tenant_product_stocks` si no. **No actualiza el agregado del producto cuando hay presentación** (el "total del producto" es una suma calculada en lectura: `StockTotalsByProductIDs` L780-799; `product_service.go:194-215`).
8. **No verifica `ManageStock`** (ni `Type=service`): cada llamador lo hace antes (ver A.3). Un llamador que olvide la verificación escribe stock de un producto sin control.
9. **No hay config de "permitir stock negativo"** para el stock de productos (grep `negative|allow_negative|oversell` solo aparece en el módulo Equipos, que usa tablas `equip_*` propias — ver F).
10. `Type` realmente usados hoy por llamadores: `in`, `out`, y `adjustment` (solo `bulk_import.go:323`). `adjustment_in/adjustment_out` solo los escriben `adjustmentInWithSerials/adjustmentOutWithSerials` (L895-965), **que no tienen ningún llamador** (grep: solo su definición) => código muerto. Sin embargo `GetKardex` (L1025-1026) filtra `MovementKind="adjustment"` por `adjustment_in/out`, tipos que el código vigente ya no escribe (los ajustes actuales son `in/out` con `inventory_document_id`).

### A.2 Locks en caminos de stock (HECHO)
`grep clause.Locking|FOR UPDATE` en `internal/` y `pkg/`: ocurrencias en `inventory_document_service.go:347,412,454` (fila del **documento**), `billing_service.go:442` (venta original en NC), `billing_async.go:239`, `fiscal_enqueue.go:68`, `prepayment/deduction_service.go:214,266`, `equipos/service/orders.go:386,474`, restaurant (mesas/sesiones), `docseries/correlative.go:37`, `saas/*`. **Ninguna sobre `tenant_product_stocks` ni `tenant_product_presentation_stocks`.** Confirmado.

### A.3 Todos los llamadores (HECHO; verificado cada uno)

| # | Llamador (archivo:línea) | Type | UnitCost | Reference | OperationCode | ¿Dentro de tx? | Verifica ManageStock antes |
|---|---|---|---|---|---|---|---|
| 1 | Venta, líneas — `sales/service/sale_service.go:848` | out | **no pasa (0)** | `"VENTA/"+Number` | SALE | Sí (`s.db.Transaction`, tx de la venta) | Sí (L817: `!ManageStock || servicio => continue`; combos `HasCombo => continue`) |
| 2 | Venta, componentes de combo — `sale_service.go:928` | out | no (0) | `"VENTA/"+Number` | SALE | Sí | Sí (L919) |
| 3 | Restaurante, líneas — `restaurant/service/restaurant_service.go:1476` | out | no (0) | `"VENTA/"+Number` | SALE | Sí | Sí (`!ManageStock => continue`) |
| 4 | Restaurante, componentes combo — `restaurant_combos.go:245` | out | no (0) | `ctx.Reference` (= `"VENTA/…"`) | SALE | Sí (tx del cobro) | Sí |
| 5 | Compra — `purchases/service/purchase_service.go:358` | in | `baseUnitCost` (= `item.UnitCost` o `/factor`), **tal cual lo teclea el usuario** | `"COMPRA/"+Serie-Número` | PURCHASE | Sí | Sí (L352 `if !product.ManageStock { continue }`, pero **después** de actualizar `purchase_price`) |
| 6 | Anulación de compra — `purchase_service.go:568` | out | costo del movimiento original (`purchase_item_id`) | `"ANULACION COMPRA/…"` | PURCHASE (tipo IN usado con `out`) | Sí | Sí |
| 7 | Anulación de venta — `sales/service/sale_cancel_stock.go:67` | in | **no (0)** | `ref` del caller | SALE (tipo OUT usado con `in`) | Sí (tx del caller) | n/a (lee kardex `reference='VENTA/n' AND type='out'`) |
| 8 | NC parcial — `sale_cancel_stock.go:196` | in | no (0) | `ref` del caller (`"NC/<nro>"`) | SALE | Sí | n/a (kardex por `sale_item_id`) |
| 9 | Traslado con líneas, salida origen — `inventory_service.go:599` | out | no (0) | `"TRANSFER-PENDING"` | TRANSFER | Sí (`CreateTransferWithLines` tx) | Sí (L559) |
| 10 | Confirmar traslado — `inventory_service.go:669` | in | no (0) | `"TRANSFER-CONFIRMED"` | TRANSFER | Sí | no (usa lo que registró el log) |
| 11 | Cancelar traslado — `inventory_service.go:719` | in | no (0) | `"TRANSFER-CANCELLED"` | TRANSFER | Sí | no |
| 12 | Traslado con series — `inventory_service.go:420,427` | out/in | no (0) | `"TRANSFER-SERIAL"` | TRANSFER | Sí (tx propia) | Parcial (la verifica `TransferByProduct` L373) |
| 13 | `Transfer()` sin series — `inventory_service.go:327,340` (vía `RecordMovement`) | out, luego in | no (0) | `"TRANSFER"` | TRANSFER | **No: dos transacciones independientes** (cada `RecordMovement` abre su `db.Transaction`); si falla la entrada, la salida ya está commiteada | `TransferByProduct` sí; `ReverseTransfer` (L498) **no** |
| 14 | Documento de inventario (ingreso/egreso/ajuste API/import ajuste) — `inventory_document_service.go:680,746,761,784,805` | in/out | `line.UnitCost` (ajustes API y manuales: normalmente 0; import-ajuste: costo promedio ponderado) | `number` del documento (`ING001-0000000n`), anulación `"ANULACION "+Number` | por `OperationTypeID` del documento (INVENTORY_ADJUSTMENT_IN/OUT, etc.) | Sí (`confirmInventoryDocumentTx` en tx, con lock del documento) | Sí en `replaceDocumentLines` (L492) |
| 15 | Stock inicial producto — `RecordInitialStock/RecordInitialPresentationStock` (`inventory_service.go:233,254`; llamados desde `products/handler/product_handler.go:311,331,353,534`, `bulk_import.go:313`) | in | **no (0)** | `"STOCK_INICIAL"` | INITIAL_STOCK | `RecordMovement` abre tx propia (no la del alta del producto salvo bulk_import, que pasa `tx`) | `bulk_import`: `manageStock`; handler: según input |
| 16 | Import masivo, producto existente — `products/service/bulk_import.go:320` | **adjustment** (saldo absoluto) | no (0) | `"STOCK_INICIAL"` | INITIAL_STOCK | Sí | Sí (`manageStock`) |
| 17 | Handler HTML legacy `AdjustmentSubmit` (`inventory_handler.go:106`) | in/out/adjustment | form `unit_cost` | form | — | tx propia | **Sin ruta registrada** (grep: no hay `AdjustmentSubmit` en routes) => código muerto |

Observaciones HECHO:
- Ningún `out` pasa `UnitCost` (confirmado: filas 1-4, 6 solo copia el original, 9, 12-13). Las reversiones `in` de venta/NC/traslado van con 0.
- El tipo de operación de la anulación de compra (`PURCHASE`, dirección IN) se usa con `Type="out"`; la de venta (`SALE`, dirección OUT) con `Type="in"`. Es coherente con "reversa del mismo concepto" pero rompe cualquier consulta que asuma `iot.direction == type` (el filtro `OperationDirection` de `GetKardex` L1049-1051 filtra por la dirección del catálogo, no por el signo real).
- La anulación de venta identifica sus salidas por **`reference` exacto** (`sale_cancel_stock.go:25-27`), no por FK.

### A.4 Rutas que escriben stock sin pasar por `RecordMovementTx` (HECHO)
- `adjustmentInWithSerials/adjustmentOutWithSerials` (`inventory_service.go:895-965`): escriben kardex y stock directo e ignoran errores, **pero no tienen llamador** (muertos). El análisis previo las presenta como "rutas legacy de series" sin aclarar que no se ejecutan => **imprecisión**.
- `purgeRestaurantProduct` (`products/service/bulk_delete.go:278`) hace `DELETE` de `tenant_product_stocks` (borrado de producto, no movimiento).
- `EnsureProductBranchLink` (`inventory_service.go:213-230`) crea fila de stock en 0 sin kardex (read-then-create sin lock, ver B).
- Series: `applySerialsInTx/OutTx/RestoreOnVoidTx` (`inventory_document_service.go:790-850`) **sí** llaman `RecordMovementTx` y luego tocan solo `tenant_product_serials`. Venta con series: `RecordMovementTx` + `UPDATE tenant_product_serials` (`sale_service.go:848-960`). No escriben stock por fuera.
- No encontré otras escrituras a `tenant_product_stocks`/`..._presentation_stocks` fuera de lo anterior (grep de los nombres de tabla/struct: el resto son lecturas: dashboards, listados, pre-chequeos de venta/restaurante/documentos).
- Pre-chequeos de stock **fuera de `RecordMovementTx`** (lectura sin lock, solo validación): `sale_service.go:475-500` (con `s.db`, fuera de la tx de la venta), `inventory_document_service.go:667-680,733-746` (dentro de tx pero sin lock), `restaurant_order_stock.go:64-75` (con tolerancia `+0.0005`).

---------------------------------------------------------------------------------

## B. Concurrencia

### B.1 Índices/UNIQUE actuales (HECHO)
- `TenantProductStock` (`pkg/database/migrations.go:1387-1393`): `product_id` `index`, `branch_id` `index` (dos índices simples, **no únicos, no compuesto**). `TenantProductPresentationStock` (L1399-1405): idem con `presentation_id`/`branch_id`.
- Baseline (`tenant_baseline_schema.go:10-60`) crea ambas por `AutoMigrate` de esos structs; `v107_presentation_stock.go:10-18` crea la de presentaciones con los mismos índices simples. En `pkg/database/tenantmigrations/` no hay ninguna migración que agregue UNIQUE a estas tablas (grep `product_stocks` solo aparece en v061, v107). Contraste: existen `uniqueIndex:idx_product_branch_price` (L1278-1279) y `idx_sale_unit_branch_price` (L1253-1254) para precios por sucursal => el patrón de UNIQUE compuesto ya existe en el repo.
- Conclusión: **nada impide hoy dos filas para el mismo (producto,sucursal) ni (presentación,sucursal)**.

### B.2 Aislamiento y pool (HECHO / INFERENCIA)
- HECHO: no hay `sql.TxOptions`/`IsolationLevel` en ningún `.go` (grep) ni `transaction_isolation`/`tx_isolation` en el DSN (`pkg/database/tenant.go:26`: `charset=utf8mb4&parseTime=True&loc=Local`). Todas las transacciones son `db.Transaction(func(tx))` por defecto.
- INFERENCIA (no verificable sin servidor): MySQL/InnoDB por defecto = REPEATABLE READ. Bajo RR, los `SELECT` sin `FOR UPDATE` leen del snapshot de la tx; los `UPDATE` sí toman lock X de la fila y leen la última versión commiteada, pero el valor escrito (`quantity = newBalance` absoluto, calculado en Go) viene del snapshot.
- HECHO: `DB_TENANT_MAX_OPEN` por defecto 6, idle 3 (`config/config.go:196-197`) — el análisis previo dice "6-8", correcto (8 en el example de prod según el análisis; no lo verifiqué). La memoria del proyecto habla de 3 conexiones en el incidente gyl; no coincide con el default => en producción el valor real puede ser otro [NO VERIFICADO].

### B.3 Escenarios concretos
**Lost update (venta+venta o venta+producción sobre el mismo producto, sucursal S, saldo 10):**
1. T1 `BEGIN`; `SELECT … FROM tenant_product_stocks WHERE product_id=P AND branch_id=S LIMIT 1` => 10 (snapshot).
2. T2 `BEGIN`; mismo SELECT => 10.
3. T1 resta 4 => `INSERT` kardex (balance 6); `UPDATE … SET quantity=6 WHERE id=X` (lock X); `COMMIT`.
4. T2 resta 3 => `INSERT` kardex (balance 7); `UPDATE … SET quantity=7 WHERE id=X` (espera el lock de T1, luego sobrescribe con 7); `COMMIT`.
5. Resultado: saldo 7 (debió ser 3); kardex con dos `Balance` incoherentes (6 y 7). Además ambas validaciones `newBalance<0` se hicieron contra 10 => **sobreventa posible**: si T1 resta 8 y T2 resta 8 con saldo 10, ambas pasan, el saldo final queda en 2 y salieron 16.
(El código usa `Updates(map…{"quantity": newBalance})`, no `quantity = quantity - ?`: `inventory_service.go:198,207`.)

**Creación concurrente de fila de saldo:** `if stock.ID == 0 { tx.Create(...) }` (L202-206 / 193-197): dos transacciones que no ven fila (primer movimiento de ese par) ejecutan ambas `INSERT` => dos filas. Sin UNIQUE no falla. Luego `First` toma la de menor `id` (GORM ordena por PK) y las lecturas con `SUM` suman ambas, mientras que `product_service.go:204` usa `LIMIT 1` => **lecturas inconsistentes entre pantallas**. Mitigante real (HECHO): `EnsureProductBranchLink` (llamado en alta/edición de producto y bulk import) crea la fila en 0 de antemano, así que en la práctica la carrera solo ocurre para (producto,sucursal) nunca enlazados (p. ej. compra a una sucursal nueva, traslado a destino sin fila, presentaciones nuevas); `EnsureProductBranchLink` mismo es `First`+`Create` sin lock (race propia).

**Deadlock existente hoy (INFERENCIA fundada):** dos ventas que descuentan {A,B} y {B,A} toman locks X vía `UPDATE` en orden distinto => MySQL 1213, que se propaga como error al usuario (no hay reintentos: grep de `1213`/`Deadlock` en `internal`/`pkg` sin lógica de retry en servicios de stock). No lo he visto reproducido.

### B.4 Cómo reproducirlo en un test Go
- Infraestructura existente: tests de inventario/ventas usan **SQLite en memoria** (`setupKardexTestDB`, `inventory_kardex_test.go:14-35`, `file:%s?mode=memory&cache=shared`). SQLite no sirve: el dialector GORM omite `FOR UPDATE` y no hay row locks (el propio `internal/billing/service/note_partial_concurrency_test.go:17-30` lo documenta; los tests de `sa_user_service_test.go:49` y similares fuerzan `SetMaxOpenConns(1)` para serializar).
- Precedente de MySQL opcional: `pkg/saas/docusage/concurrency_test.go:21-23` usa `os.Getenv("DOCUSAGE_MYSQL_DSN")`: si está definido usa MySQL real, si no cae a SQLite. **Es el único precedente de tests MySQL del repo** (grep `mysql` en `_test.go`: ese archivo y `sale_unit_branch_price_test.go`).
- Propuesta: nuevo `inventory_concurrency_test.go` con `t.Skip` si falta `INVENTORY_MYSQL_DSN` (apuntando a una BD local desechable, nunca producción). Pasos: abrir con `gorm.Open(mysql.Open(dsn))`, `database.ApplyBaselineSchema(db)` + `V083`, crear sucursal/producto `ManageStock` y fila de stock con 10; lanzar N goroutines (barrera `sync.WaitGroup` + canal de arranque) que llaman `db.Transaction(func(tx){ return svc.RecordMovementTx(tx, out 1) })`; aserciones: `SUM(quantity)` == 10-N y `kardex.Balance` estrictamente decreciente sin repetidos. Hoy debe fallar (probar el bug) y pasar tras el endurecimiento. Segundo test: N goroutines con (producto,sucursal) sin fila inicial => `COUNT(*)` de filas debe ser 1. Tercero: pares cruzados {A,B}/{B,A} para medir deadlocks y exigir reintento. Para fijar el nivel de aislamiento añadir `SET SESSION transaction_isolation='REPEATABLE-READ'` explícito en el test.

### B.5 Qué haría falta para endurecer sin regresiones (INFERENCIA, no implementado)
1. **Migración V161 (previa, aditiva)**: deduplicar filas por `(product_id,branch_id)` y `(presentation_id,branch_id)` sumando cantidades en la fila de menor `id` y borrando el resto (consultar primero cuántas hay, ver §Lo no verificable), luego `CREATE UNIQUE INDEX`. Hay que decidir qué hacer con diferencias entre saldo y kardex (no corregirlas implícitamente).
2. En `RecordMovementTx`: (a) asegurar la fila antes de leer con `INSERT … ON DUPLICATE KEY UPDATE id=id` / `clause.OnConflict{DoNothing:true}` (requiere el UNIQUE) y luego `SELECT … FOR UPDATE` (`clause.Locking{Strength:"UPDATE"}`) — evita el gap lock de FOR UPDATE sobre fila inexistente, que bajo RR con dos transacciones concurrentes provoca deadlock en el `INSERT` posterior; (b) o directamente `UPDATE … SET quantity = quantity + ? WHERE id=? [AND quantity >= ? para out]` y leer el saldo resultante para `Balance`.
3. **Orden de bloqueo**: hoy ventas procesan ítems en el orden de la petición. Para evitar deadlocks cruzados, ordenar las operaciones por `(branch_id, product_id, presentation_id)` antes de recorrer (ventas, restaurante, documentos, producción) o envolver la tx en reintento ante error 1213/1205 (no existe hoy). Hay que extraer `InventoryService.RecordMovementsTx([]MovementInput)` que ordene y llame a `RecordMovementTx`; así traslados y producción lo reutilizan.
4. **Qué llamadores se podrían romper**: (i) los que hacen pre-chequeo con `s.db` fuera de la tx (`sale_service.go:475`) seguirán igual pero el guard real debe ser el de `RecordMovementTx`; (ii) `Transfer()` (dos tx separadas, `inventory_service.go:327-350`) debería unificarse en una tx; (iii) los tests con SQLite no soportan `FOR UPDATE` (se omite silenciosamente, no rompe) pero `ON DUPLICATE KEY` requiere que GORM traduzca `OnConflict` a SQLite (lo hace); (iv) ventas con ítems repetidos del mismo producto: con lock se serializan dentro de la misma tx sin problema; (v) pool pequeño (6 por tenant): locks más largos aumentan la espera => riesgo de agotamiento ya visto (incidente gyl); mantener las transacciones cortas; (vi) `applyDocumentLineMovement` hace su propio chequeo `stock.Quantity < line.Quantity` previo: seguirá funcionando pero es redundante; (vii) `bulk_import` pasa `tx` a `EnsureProductBranchLink` (usa `s.db`), compatible.

---------------------------------------------------------------------------------

## C. Costos

### C.1 Cómo se calculan/guardan (HECHO)
- `tenant_products.purchase_price` decimal(15,6) (`migrations.go:1067`). Cambia en: (a) **compra** — `catalogPriceUpdates` (`purchase_service.go:94-116`) sobrescribe con `RoundSunat(baseUnitCost)` si `baseUnitCost>0`, **incluso si el producto no maneja stock** (L335-342 ocurre antes del `continue` de L352); (b) edición manual de producto (`product_service.go:828,1090`; el update vuelca `input.PurchasePrice`); (c) `BulkUpdatePrices` (`bulk_price_update.go:100-101`); (d) import masivo (`bulk_import.go:261-264`). No cambia en anulación de compra (`Void` no toca el producto: L468-590) => confirmado. **No existe edición de compra** (solo `Create`, `Void`, `List`, `GetByID`, `GetItems`: `grep ^func` de `purchase_service.go`).
- Movimiento de compra: `UnitCost = baseUnitCost` (L364) = `item.UnitCost` (sin SaleUnit) o `RoundSunat(UnitCost/ConversionFactor)` (con SaleUnit; `purchase_unit_resolver.go:65-75`). Anulación de compra: `out` con el costo/cantidad del movimiento original (L545-576).
- **Contradice al análisis previo (§2.4 fila "Costo de compra con IGV")**: dice que el costo "depende de `PriceIncludesIgv` (si es true, se desagrega)". En el código, `PriceIncludesIgv` solo afecta `tax.CalcItem` para `Subtotal/TaxAmount/Total` de la compra (`purchase_service.go:181-184`). **El costo que va al kardex y a `purchase_price` es `item.UnitCost` tal cual se tecleó** (L317, L364, L104), con IGV incluido si el usuario marcó "incluye IGV". El front envía `unit_cost` sin transformar (`PurchaseRegisterPage.tsx:62,648-650`). => el costo base puede venir **bruto de IGV** y no hay moneda: tampoco se convierte USD a PEN en el movimiento (`Currency` se guarda en la cabecera; no vi conversión de `UnitCost`) [no verifiqué el front de moneda].
- Venta: no hay `UnitCost` en el movimiento (0). El costo vive en `tenant_sale_items.purchase_price` (decimal(15,6), nullable, v145): `resolveSaleItemPurchasePrice` (`sale_service_calc.go:88-99`) = `product.PurchasePrice` actual **por unidad base**, sin multiplicar por factor de SaleUnit; nil para líneas manuales. Restaurante igual (`restaurant_service.go:1583-1594`).
- Traslado: sin `UnitCost` (0). Devolución/anulación de venta y NC: `in` con 0. Ajuste por documento API: `line.UnitCost` = 0 (`RecordAdjustmentViaDocument` no lo setea, L276-281). Documento manual: `UnitCost` de la línea (decimal(15,2) en `TenantInventoryDocumentDetail`, `migrations.go:1497`; validación solo `<0` en IN: `document_validate.go:120`). Import-ajuste: costo promedio ponderado (`inventory_import_adjustment.go:271,340-372`). Stock inicial: 0.
- `TenantPurchaseItem.UnitCost` es decimal(15,2) (`migrations.go:2173`), kardex y producto (15,6) => confirmado.

### C.2 `WeightedAverageUnitCosts` (HECHO) — `inventory_service.go:809-875`
- Qué reproduce: trae el `purchase_price` de los productos como fallback (solo `>0`) y **todos** los movimientos del `(product_id IN, branch_id)` (`Find`, sin paginar, ordenados por `product_id, created_at, id`); recorre en memoria: `in/adjustment_in` con `UnitCost>0` => `avg=(avg*qty+cost*q)/(qty+q)`; `in` con costo 0 solo suma cantidad (no diluye ni cambia avg); `out/adjustment_out` resta cantidad (piso 0); `adjustment` fija `qty=Balance`. Resultado: `avg` si `qty>0 && avg>0`, si no el fallback.
- Limitaciones HECHO: **ignora `PresentationID`** (acumula por `ProductID`, mezcla presentaciones); es **por sucursal** (`branch_id = ?`), un traslado no transfiere costo (la entrada en destino va con 0); `Find` de todo el historial por consulta (O(n)); `adjustment` con saldo absoluto no altera avg; no hay persistencia.
- Quién lo usa: solo `inventory_import_adjustment.go:271` (HECHO) y los tests (`inventory_import_adjustment_test.go:115`, `purchase_sale_unit_integration_test.go:501`). El análisis previo es correcto.

### C.3 Reportes de utilidad y efecto de cambios (HECHO)
- `ProfitDetail` (`sale_service.go:2021-2115`): costo = `COALESCE(tenant_sale_items.purchase_price, p.purchase_price, 0)` × `quantity` de la línea. Snapshot por línea: **un cambio posterior de `purchase_price` no altera ventas ya hechas** que tengan snapshot; líneas anteriores a v145 (NULL) usan el costo actual del catálogo. Si producción actualiza `purchase_price` del terminado, afectará solo ventas nuevas (y las viejas sin snapshot). Si se agrega `average_cost` y se quiere que el reporte lo use, hay que cambiar `resolveSaleItemPurchasePrice` (dos sitios: `sale_service_calc.go:88` y `restaurant_service.go:1583`) y mantener el COALESCE.
- Exclusión de ventas: `status != 'cancelled'` y scope comercial sin NV (L2038-2041).
- **Bug SaleUnit — CONFIRMADO** (el análisis previo lo marcó "[V por lectura]"): `unit_price` de la línea y `quantity` son comerciales (por saco), `purchase_price` es por unidad base (por kg); `unitProfit = SalePrice - PurchasePrice` (L2095) mezcla unidades y `totalProfit = unitProfit*Quantity`. Ejemplo: vende 2 sacos a S/400 (factor 100, costo S/3/kg): el código calcula (400-3)*2=794 de utilidad; lo correcto (400-300)*2=200. Imprecisión menor del análisis previo: describe el error como "≈ 1.5 × costo/KG en vez de 150 × costo/KG" — es correcto en esencia (costo subestimado por el factor). Además el snapshot no se multiplica por factor en ninguna rama (grep `Factor` en `sale_service_calc.go`: sin resultados).
- Otros reportes de costo: `SalesByProduct` usa el mismo concepto (sale_service.go ~L1990-2060 comparte `purchase_price`; no revisé línea por línea [NO VERIFICADO el resto de reportes de analytics en `dashboard/handler`]). El pendiente de memoria "profit-detail con descuento" ya aparece resuelto en el código actual (L2080-2087 prorratea descuentos).

### C.4 ¿Existe ya costo promedio/average en algún lado? (HECHO)
`grep -i "avg_cost|average_cost|weighted|costo_promedio|WeightedAverage"` en `.go` (excluyendo worktrees): solo `WeightedAverageUnitCosts` y sus tests/uso. **No existe campo persistido de costo promedio** en `TenantProduct`, `TenantProductStock` ni `TenantProductPresentationStock`. Aparte: el módulo Equipos tiene su propio concepto de costo promedio sobre `equip_stock_movements` (`internal/equipos/service/reports.go:555-561`, `equip_models.go:274`), independiente del ERP principal.

### C.5 Efecto en reversión (HECHO)
- Reversión de venta/NC: repone cantidad con costo 0 (no repone costo). Anulación de compra: reversa con costo original, **no recalcula ni restaura `purchase_price`** del producto. Con `WeightedAverageUnitCosts`, la `out` de la anulación resta cantidad pero no revierte el promedio (queda el avg que dejó la compra anulada mientras haya saldo).

---------------------------------------------------------------------------------

## D. Tipos de operación, origen del movimiento y combos

### D.1 Catálogo (HECHO) — `pkg/database/inventory_catalog_seed.go:16-40`, modelo `migrations.go:1450-1462`
Campos: `Direction` (IN|OUT), `Code` (UNIQUE), `Name`, `SunatCode` (size 2), `AllowManual`, `RequiresDocument`, `SortOrder`, `IsActive`. Seeds: IN = PURCHASE(02), INITIAL_STOCK(16), RETURN_IN(05), CONSIGNMENT_IN(03), OTHER_IN(99), INVENTORY_ADJUSTMENT_IN(99), TRANSFER(11, *IN*); OUT = SALE(01), RETURN_OUT(06), DONATION(09), PRODUCTION_OUT(10, `AllowManual:true`, `RequiresDocument:false`), WITHDRAWAL(12), SHRINKAGE(13), WASTE(14), DESTRUCTION(15), CONSIGNMENT_OUT(04), PROMOTION(07), PRIZE(08), OTHER_OUT(99), INVENTORY_ADJUSTMENT_OUT(99). Coincide con el análisis previo. **No hay `PRODUCTION_IN`.**
- `SeedInventoryOperationTypes` (L43-70): inserta solo si el `code` no existe; **no actualiza** filas existentes (para cambiar `AllowManual` de `PRODUCTION_OUT` habría que hacer `UPDATE` en una migración). Se invoca en `v083`, `v084`, `v085` y en `tenant_provision_seed.go:45` (tenants nuevos). Confirma el patrón "nueva fila => migración que llame al seed", como `v084`.
- `TRANSFER` es único código con dirección IN que se usa también para `out` (salida de traslado): `inventory_service.go:599` con `OperationCode:"TRANSFER"` y `Type:"out"` => confirma que el tipo de movimiento y la dirección del catálogo **no se validan entre sí**.
- Consecuencia práctica para producción: `RecordMovementTx` fallaría con `ErrOperationTypeNotFound` en tenants que no tengan `PRODUCTION_IN` sembrado si la migración no corrió => la migración V161 debe correr antes del deploy.

### D.2 Cómo se identifica el origen hoy (HECHO)
Columnas en `tenant_stock_movements` (`migrations.go:1407-1447`): `inventory_document_id`, `transfer_id`, `sale_item_id`, `purchase_item_id`, `operation_type_id`, `reference` (varchar 100), `presentation_id`, snapshot SaleUnit. No hay `source_type/source_id`, ni `production_order_id`.
- Reversión de venta: por `reference = 'VENTA/<número>' AND type='out'` (`sale_cancel_stock.go:25-27`), restando lo ya repuesto por NC por `reference IN ('NC/…')` (L117-123). NC parcial: por `sale_item_id` (L151). Anulación de compra: por `purchase_item_id` (`purchase_service.go:553`). Anulación de documento: por las líneas del documento (re-emite el movimiento inverso con `inventory_document_id`).
- Kardex: `MovementKind` rápidos (`inventory_service.go:1018-1033`) filtran por prefijo de `reference`: `COMPRA/%`+type in, `VENTA/%`+type out, `TRANSFER%`; `adjustment` por `type IN (adjustment_in, adjustment_out)` (hoy vacío); `inventory_doc` por `inventory_document_id IS NOT NULL`. Además `TextSearch` (reference/notes LIKE) y filtros por `operation_type_id/code/direction/sunat_code` (join al catálogo). Un movimiento de producción con `reference="PRODUCCION/…"` **no aparece en ningún filtro rápido** salvo `in`/`out` genéricos, texto, o filtro por `OperationCode`. Si además lleva `inventory_document_id` NULL tampoco entra en "documento". Confirma el análisis previo.
- Peculiaridad: la reversión de venta usa `Reference=ref` del caller (L67-84), por lo que el `in` de anulación **no** tiene el prefijo `VENTA/` y no se confunde con las salidas. Para producción, anular una orden por referencia exacta funcionaría igual, pero es frágil (límite 100 caracteres; texto libre) — recomendable FK.

### D.3 "Explosión de componentes" (HECHO)
`TenantComboGroup`/`TenantComboGroupItem` (`migrations.go:1351-1385`): grupos de selección para un producto `HasCombo`. Venta: `ResolveComboItems` (`sales/service/sale_combos.go:25-103`) produce `ExtraStockMovements` (producto+cantidad); `sale_service.go:905-933` registra una salida `out` por componente con `Reference "VENTA/<nro>"`, `OperationCode SALE`, **sin `SaleItemID` ni `UnitCost`**, solo si el componente tiene `ManageStock` y no es servicio. Restaurante: `recordComboComponentStock` (`restaurant_combos.go:210-257`) agrega por producto y emite `out` con la misma referencia. Es una explosión en tiempo de venta (precio fijo), no receta; las reversiones recuperan estos componentes leyendo el kardex por referencia (comentario `sale_cancel_stock.go:15-25`). Coincide con el análisis previo (§2.4 Combos y §5.5).
- No hay otra "explosión" de componentes en el ERP principal (grep de recetas: `restaurant_recipes` es un flag stub; ver análisis previo, no re-verificado).

---------------------------------------------------------------------------------

## E. Unidades y precisión

- `saleunit.ResolveForLine` (`pkg/saleunit/resolver.go:35-67`): lee `TenantProductSaleUnit` por `(id, product_id)`, exige `Active`, `ConversionFactor>0`, y `AllowFraction` o entero (tolerancia 0.0001); retorna `BaseQuantity = commercialQuantity * ConversionFactor` **sin redondear**; devuelve `(nil,nil)` si no hay SaleUnit. Confirmado. Para convertir insumos de una receta a unidad base: el producto debe tener una SaleUnit por cada unidad en la que se exprese la receta (p. ej. "gramo" con factor 0.001 si la base es kg). La conversión es por producto; no existe tabla global (el análisis previo lo dice y es correcto). `ConversionFactor` es decimal(15,6) en BD (`migrations.go:1424`).
- Precisión: `Quantity`, `Balance`, stock, `SaleUnitQuantity` = decimal(15,3); `ConversionFactor` y `UnitCost` = (15,6). Con receta 0.018 kg × 86 u: producto en Go = `1.54800000000000004263` (ejecutado); MySQL lo guarda 1.548 en movimiento y saldo, sin error. Una cantidad < 0.0005 (p. ej. 0.0004 kg × 1 u) **pasa** la validación `Quantity>0` (L138) y MySQL la redondea a 0.000 (`round3(0.0004)=0` verificado): se registra un movimiento de 0 y el saldo no cambia (en modo no estricto o estricto, el redondeo de decimales no es error; con INSERT de decimal fuera de rango sí lo sería, no aplica) [comportamiento exacto de MySQL = INFERENCIA, depende de `sql_mode`].
- **Hallazgo nuevo (no está en el análisis previo)**: `RecordMovementTx` compara `newBalance = currentQty - input.Quantity < 0` en float64 **sin epsilon** (L168-172). `currentQty` viene de un decimal(15,3) parseado, `input.Quantity` es un producto float (p. ej. receta×unidades). Verificación aritmética (script en scratchpad): para cantidades 0.018, 0.0004, 0.125, 0.35, 0.1, 0.07, 0.003 × n=1..2000, en **3141 de 14000 combinaciones** el stock exactamente igual al requerido (redondeado a 3 decimales) queda con `stock - requerido < 0` por error de coma flotante => "stock insuficiente" falso al consumir "todo lo que hay". El pre-chequeo de restaurante usa tolerancia `+0.0005` (`restaurant_order_stock.go:99`), el de ventas/documentos no. Para producción: redondear `Quantity` a 3 decimales antes de llamar y/o añadir epsilon en `RecordMovementTx`.
- Saldo y kardex: `Balance` y stock se calculan en float64 y se persisten a 3 decimales; al leer el siguiente saldo se redondea de nuevo; error acumulado por movimiento ≤ 0.0005, nunca acumulado en BD porque el saldo se relee redondeado cada vez. Pero `SUM(kardex.in) - SUM(kardex.out)` (cantidades también redondeadas por separado) puede diferir 0.001-0.002 del saldo guardado [INFERENCIA].
- `ManageStock=false`: es el **default** (`migrations.go:1071` `default:false`, y v089 lo fija así). Comportamiento de llamadores: ventas/restaurante/combos/compra: `continue` (no mueven stock); documentos de inventario y traslados: **error** ("el producto no controla stock", `inventory_document_service.go:492`, `inventory_service.go:560,374`); ajuste API: error; import-ajuste: fila con error. La compra **sí actualiza `purchase_price`** de un producto sin `ManageStock` (L335-342). `RecordMovementTx` mismo no lo valida (ver A.1.8). `productIsCatalogService` (`sale_service.go:103-108`) además excluye servicios aunque tengan `manage_stock=1` legado.

---------------------------------------------------------------------------------

## F. Stock negativo en el ERP actual

- **Stock de productos (`tenant_product_stocks`/`..._presentation_stocks`): nunca se permite negativo.** Todo `out`/`adjustment_out` pasa por el guard de `RecordMovementTx` (L168-172); ventas/documentos/restaurante además pre-chequean. No hay configuración que lo habilite (grep). Excepción efectiva: `adjustment` absoluto (L173-174) no puede dejar negativo porque `Quantity>0` es obligatorio; tampoco puede fijar **cero** (`Quantity>0`) — un "ajustar a 0" por `bulk_import` (producto existente con `InitialStock>0` solo: `bulk_import.go:279` exige `item.InitialStock > 0`) no es posible por este camino.
- Negativo sí aparece por **carrera** (lost update/sobreventa, B.3): dos `out` válidos contra el mismo snapshot pueden dejar el saldo real en negativo?  No: el saldo final lo fija el último `UPDATE` con su valor absoluto (siempre ≥ 0); el daño es de **saldo incorrecto y sobreventa**, no un negativo numérico. Los `Balance` del kardex tampoco serán negativos. (INFERENCIA coherente con el código.)
- Módulo Equipos (`internal/equipos`): permite `AllowNegative` con nota (`equipos/handler/orders.go:163-169`, `service/orders.go:65` `NegativeStockError`, dashboard `NegativeWatch`), pero opera sobre tablas `equip_*` (`equip_models.go:268-301`), **independiente** de `tenant_product_stocks`. No aplica a producción.

---------------------------------------------------------------------------------

## Tablas "Afirmación → ¿Confirmada? → Evidencia"

### A
| Afirmación del análisis previo | ¿Confirmada? | Evidencia |
|---|---|---|
| Único punto de escritura de stock: `RecordMovementTx` | Parcial | Cierto para código vivo (A.4); existen 2 rutas directas pero sin llamador (`inventory_service.go:895-965`), y el análisis las presenta como "legacy de series" sin decir que están muertas |
| Rechaza saldo negativo en out/adjustment_out | Sí | L168-172 (sin epsilon, ver E) |
| Crea o actualiza la fila de stock | Sí | L192-209 |
| No valida `ManageStock`; cada llamador lo hace | Sí | L134-210; compra L352, ventas L817, documentos L492 |
| Sin lock sobre filas de stock | Sí | grep Locking (A.2) |
| Pre-chequeo de stock en venta sin lock, fuera de la tx | Sí | `sale_service.go:455-500` usa `s.db` |
| Ventas/traslados/reversiones sin `UnitCost` | Sí | tabla A.3 filas 1-4, 7-13 |
| Traslados "pending→confirmed"; salida al crear y entrada al confirmar | Sí | `inventory_service.go:527-683` |
| `ConfirmTransfer`/`CancelTransfer` leen la cabecera fuera de la tx y sin lock | Sí | L633-639, L687-693 |
| Documentos bloquean el documento, no el stock | Sí | `inventory_document_service.go:347,412,454` |
| `POST /inventory/adjustment` exige motivo y `ManageStock` | Sí | `inventory_document_service.go:228-242` |
| Reversión de venta lee kardex por `reference` | Sí | `sale_cancel_stock.go:25-27` |
| `Transfer` (no por estados) es atómico | No afirmado | **Hallazgo nuevo**: `Transfer()` usa dos transacciones separadas (L327-350) y sigue accesible vía `ReverseTransfer` (API `POST /inventory/transfers/:id/reverse`, `routes.go:50`) |

### B
| Afirmación | ¿Confirmada? | Evidencia |
|---|---|---|
| Sin UNIQUE `(product_id,branch_id)` ni `(presentation_id,branch_id)` | Sí | `migrations.go:1387-1405`; `v107_presentation_stock.go:10-18`; sin migración de UNIQUE |
| Lost update posible por read-modify-write en Go | Sí | `inventory_service.go:155-209` (escribe `quantity=newBalance` absoluto) |
| Dos filas por creación concurrente | Sí (con matiz) | L193-206 + falta UNIQUE; mitigado por `EnsureProductBranchLink` (filas en 0 al enlazar) |
| Aislamiento default REPEATABLE READ "no se encontró configuración distinta" | Sí en repo; real en servidor NO verificado | sin `TxOptions` ni isolation en DSN (`tenant.go:26`) |
| "Hay que agregar FOR UPDATE o UPDATE atómico + UNIQUE con dedupe previo" | Sí, con matiz | Hay que añadir ensure-row (`ON DUPLICATE KEY`) antes del lock por riesgo de gap-lock/deadlock con fila inexistente, y orden de bloqueo + reintento de 1213 (no existe) |
| `DB_TENANT_MAX_OPEN` por defecto 6 | Sí | `config/config.go:196` |

### C
| Afirmación | ¿Confirmada? | Evidencia |
|---|---|---|
| `purchase_price` = último costo, sobrescrito por compra solo si `>0` | Sí | `purchase_service.go:94-116,335-342` |
| Anulación de compra no restaura `purchase_price` | Sí | `Void` L468-590 |
| "Costo de línea depende de `PriceIncludesIgv` (se desagrega)" | **No** | `purchase_service.go:181-184` solo afecta subtotal/IGV/total; el costo al kardex/producto es `item.UnitCost` crudo (L317,364,104) |
| Snapshot `TenantSaleItem.PurchasePrice` = costo actual del producto | Sí | `sale_service_calc.go:88-99`, v145 |
| `WeightedAverageUnitCosts`: ignora salidas, ignora entradas con costo 0, O(n), solo importación | Sí | `inventory_service.go:809-875`; uso `inventory_import_adjustment.go:271` |
| Falta dicho: ignora presentación y es por sucursal | (Omisión) | L829,840-845 agrupa por `ProductID`, `branch_id=?` |
| `ProfitDetail` multiplica costo base por cantidad comercial (bug SaleUnit) | Sí | `sale_service.go:2021,2095-2110` |
| `TenantInventoryDocumentDetail.UnitCost` y `TenantPurchaseItem.UnitCost` decimal(15,2) | Sí | `migrations.go:1497,2173` |
| `UnitCost` movimientos (15,6), `purchase_price` (15,6) | Sí | `migrations.go:1432,1067` |
| No existe costo promedio persistido | Sí | grep C.4 |
| Combos: el costo de componentes va en 0 | Sí | `sale_service.go:928`, `restaurant_combos.go:245` |

### D
| Afirmación | ¿Confirmada? | Evidencia |
|---|---|---|
| Seeds listados, `PRODUCTION_OUT` AllowManual=true, sin `PRODUCTION_IN` | Sí | `inventory_catalog_seed.go:16-40` |
| Seed solo inserta si falta el código | Sí | L43-70 |
| Filtros rápidos del kardex por prefijo de `reference` | Sí | `inventory_service.go:1018-1033` |
| Movimiento sin `source_type/source_id` genérico | Sí | `migrations.go:1407-1447` |
| Combos = explosión en venta, sin receta | Sí | `sale_combos.go`, `sale_service.go:905-933` |
| Tipo `adjustment_in/adjustment_out` "aceptados en código" | Parcial | Aceptados en `RecordMovementTx` pero **ningún flujo vivo los escribe** (A.1.10) |
| El tipo de movimiento (`Type`) tiene valor `transfer` | No | Comentario de struct lo lista pero `RecordMovementTx` lo rechaza (L175-176); traslados usan `in/out` |

### E/F
| Afirmación | ¿Confirmada? | Evidencia |
|---|---|---|
| `BaseQuantity = cant × factor` sin redondeo | Sí | `resolver.go:55-58` |
| Cantidad < 0.0005 pasa validación y queda en 0 | Sí (comportamiento MySQL = inferencia) | L138; `round3(0.0004)=0` |
| Saldo en float64 vs decimal(15,3) | Sí | L152-209 |
| Falta: comparación de saldo sin epsilon rechaza consumos exactos | **Nuevo** | E; script: 3141/14000 combinaciones |
| Stock negativo no permitido en productos | Sí | F |
| Productos sin `ManageStock` no mueven stock en ventas/compras | Sí | tabla A.3 |
| Compra actualiza `purchase_price` incluso sin `ManageStock` | **Nuevo** | `purchase_service.go:335-342` antes del `continue` L352 |

---------------------------------------------------------------------------------

## Errores / imprecisiones del análisis previo (resumen)

1. **IGV en costo de compra (C.1)**: el análisis dice que si la compra "incluye IGV" el costo "se desagrega"; solo se desagregan los totales de la compra. El costo del kardex/`purchase_price` es el valor tecleado, potencialmente bruto de IGV. Impacto directo en cualquier costo de producción que consuma `purchase_price`/`UnitCost` de compras.
2. **Rutas legacy de series (A.4)**: `adjustmentInWithSerials/adjustmentOutWithSerials` son código muerto (sin llamador); no son un riesgo real hoy, pero tampoco "rutas que se ejecutan". Lo mismo para `AdjustmentSubmit`/`TransferSubmit` HTML (sin ruta).
3. **Tipos `adjustment_in/out`**: presentados como aceptados; ningún flujo vivo los escribe, y el filtro rápido "adjustment" del kardex (`inventory_service.go:1025`) no encuentra los ajustes actuales (que son `in/out` con `inventory_document_id`).
4. **Omisión**: `WeightedAverageUnitCosts` ignora `PresentationID` y no transfiere costo entre sucursales; para variantes de producto terminado daría un promedio mezclado.
5. **Omisión**: la comparación `newBalance < 0` sin epsilon (E).
6. **Omisión**: `Transfer()` no atómico (dos transacciones) todavía alcanzable por `ReverseTransfer`.
7. **Omisión**: la compra actualiza `purchase_price` aunque el producto no controle stock.
8. **Matiz en §5.1**: el riesgo de "doble fila de saldo" está mitigado en la práctica por `EnsureProductBranchLink` (filas en 0 al enlazar productos a sucursal); el lost update por saldo existente sigue siendo el riesgo real y dominante.
9. **Matiz de pool**: el análisis cita 6-8; el default del código es 6 (verificado); la memoria del incidente menciona 3 conexiones => el valor de producción real no se conoce.
10. **Sin error de fondo** en: ausencia de recetas/BOM, ausencia de `PRODUCTION_IN`, combos como explosión en venta, ProfitDetail+SaleUnit, UNIQUE faltantes, sin locks, ventas/traslados sin costo. Estas afirmaciones se confirman.

---------------------------------------------------------------------------------

## Lo que NO se pudo verificar sin acceso a producción (consultas SQL de solo lectura exactas)

Todas son `SELECT` ejecutables por tenant (BD física por tenant) con el usuario de solo lectura; ninguna modifica nada. Hay que correrlas en una muestra de tenants (especialmente los grandes y gyl/20611288710 etc.) o iterar por la flota.

```sql
-- 1) Duplicados de saldo por (producto, sucursal)
SELECT product_id, branch_id, COUNT(*) AS filas, SUM(quantity) AS suma, MIN(id) AS id_min, MAX(id) AS id_max
FROM tenant_product_stocks
GROUP BY product_id, branch_id
HAVING COUNT(*) > 1;

-- 2) Duplicados de saldo por (presentación, sucursal)
SELECT presentation_id, branch_id, COUNT(*) AS filas, SUM(quantity) AS suma
FROM tenant_product_presentation_stocks
GROUP BY presentation_id, branch_id
HAVING COUNT(*) > 1;

-- 3) Diferencia saldo vs suma de kardex (productos sin variantes), por sucursal.
--    Signo: in/adjustment_in suman; out/adjustment_out restan; 'adjustment' fija saldo (se excluye y se reporta aparte).
SELECT s.product_id, s.branch_id, SUM(s.quantity) AS saldo,
       COALESCE(k.neto,0) AS neto_kardex,
       SUM(s.quantity) - COALESCE(k.neto,0) AS diferencia,
       COALESCE(k.n_adjustment,0) AS movs_adjustment_absoluto
FROM tenant_product_stocks s
LEFT JOIN (
  SELECT product_id, branch_id,
         SUM(CASE WHEN type IN ('in','adjustment_in') THEN quantity
                  WHEN type IN ('out','adjustment_out') THEN -quantity ELSE 0 END) AS neto,
         SUM(type='adjustment') AS n_adjustment
  FROM tenant_stock_movements
  WHERE presentation_id IS NULL
  GROUP BY product_id, branch_id
) k ON k.product_id = s.product_id AND k.branch_id = s.branch_id
GROUP BY s.product_id, s.branch_id, k.neto, k.n_adjustment
HAVING ABS(SUM(s.quantity) - COALESCE(k.neto,0)) > 0.001
ORDER BY ABS(SUM(s.quantity) - COALESCE(k.neto,0)) DESC
LIMIT 200;

-- 3b) Idem por presentación
SELECT ps.presentation_id, ps.branch_id, SUM(ps.quantity) AS saldo, COALESCE(k.neto,0) AS neto_kardex
FROM tenant_product_presentation_stocks ps
LEFT JOIN (
  SELECT presentation_id, branch_id,
         SUM(CASE WHEN type IN ('in','adjustment_in') THEN quantity
                  WHEN type IN ('out','adjustment_out') THEN -quantity ELSE 0 END) AS neto
  FROM tenant_stock_movements WHERE presentation_id IS NOT NULL
  GROUP BY presentation_id, branch_id
) k ON k.presentation_id = ps.presentation_id AND k.branch_id = ps.branch_id
GROUP BY ps.presentation_id, ps.branch_id, k.neto
HAVING ABS(SUM(ps.quantity) - COALESCE(k.neto,0)) > 0.001
LIMIT 200;

-- 3c) Saltos de Balance en el kardex (detecta lost update): Balance != Balance_anterior ± Quantity
SELECT m.id, m.product_id, m.branch_id, m.type, m.quantity, m.balance,
       p.balance AS balance_prev
FROM tenant_stock_movements m
JOIN tenant_stock_movements p
  ON p.id = (SELECT MAX(x.id) FROM tenant_stock_movements x
             WHERE x.product_id = m.product_id AND x.branch_id = m.branch_id
               AND COALESCE(x.presentation_id,0) = COALESCE(m.presentation_id,0) AND x.id < m.id)
WHERE ABS(m.balance - (p.balance + CASE WHEN m.type IN ('in','adjustment_in') THEN m.quantity
                                         WHEN m.type IN ('out','adjustment_out') THEN -m.quantity
                                         ELSE m.balance - p.balance END)) > 0.001
LIMIT 200;   -- puede ser pesada: ejecutar por producto o con ventana de fechas

-- 4) Movimientos con unit_cost = 0 por tipo/operación (dimensiona el vacío de valoración)
SELECT m.type, COALESCE(iot.code,'(sin tipo)') AS operacion, COUNT(*) AS total,
       SUM(m.unit_cost = 0) AS con_costo_cero, SUM(m.unit_cost > 0) AS con_costo
FROM tenant_stock_movements m
LEFT JOIN tenant_inventory_operation_types iot ON iot.id = m.operation_type_id
GROUP BY m.type, iot.code
ORDER BY total DESC;

-- 4b) Entradas (compra/stock inicial/ajuste +) sin costo, entre productos activos con control de stock
SELECT m.product_id, p.name, p.purchase_price, COUNT(*) AS entradas_sin_costo
FROM tenant_stock_movements m JOIN tenant_products p ON p.id = m.product_id
WHERE m.type IN ('in','adjustment_in') AND m.unit_cost = 0 AND p.manage_stock = 1 AND p.active = 1
GROUP BY m.product_id, p.name, p.purchase_price
ORDER BY entradas_sin_costo DESC LIMIT 100;

-- 5) Productos con purchase_price = 0 (y control de stock / activos)
SELECT COUNT(*) AS total_activos_con_stock,
       SUM(purchase_price = 0 OR purchase_price IS NULL) AS sin_costo,
       SUM(purchase_price > 0) AS con_costo
FROM tenant_products
WHERE manage_stock = 1 AND active = 1 AND deleted_at IS NULL AND type <> 'service';

-- 6) Stock negativo (ambas tablas)
SELECT 'producto' AS origen, product_id AS id, branch_id, quantity FROM tenant_product_stocks WHERE quantity < 0
UNION ALL
SELECT 'presentacion', presentation_id, branch_id, quantity FROM tenant_product_presentation_stocks WHERE quantity < 0;

-- 7) Costos de compra potencialmente brutos de IGV (compras con price_includes_igv)
SELECT COUNT(*) AS lineas, SUM(price_includes_igv = 1) AS con_igv_incluido
FROM tenant_purchase_items;

-- 8) Productos con variantes con filas en el stock agregado (invisibles; el comentario de CheckProductPresentation lo menciona)
SELECT s.product_id, s.branch_id, s.quantity
FROM tenant_product_stocks s JOIN tenant_products p ON p.id = s.product_id
WHERE p.has_variants = 1 AND s.quantity <> 0;

-- 9) Aislamiento y sql_mode reales (a nivel servidor, solo lectura)
SELECT @@GLOBAL.transaction_isolation, @@GLOBAL.sql_mode, @@GLOBAL.max_connections;
SHOW GLOBAL VARIABLES LIKE 'innodb_lock_wait_timeout';

-- 10) Versión de esquema por tenant (para saber si V150-V160 están aplicadas)
SELECT MAX(version) FROM tenant_migration_history;   -- nombre de tabla según docs/MIGRATIONS-SaaS.md [no verificado]

-- 11) Tipos de operación presentes en un tenant (PRODUCTION_OUT / seeds)
SELECT code, direction, sunat_code, allow_manual, requires_document, is_active
FROM tenant_inventory_operation_types ORDER BY direction, sort_order;
```

Otros no verificables sin servidor/UI:
- Comportamiento exacto de MySQL ante cantidad 0.0004 (redondeo vs warning vs error) depende de `sql_mode` del servidor (consulta 9).
- Existencia real de lost updates y deadlocks en producción (consultas 1, 3, 3c, y logs de MySQL `SHOW ENGINE INNODB STATUS` -> `LATEST DETECTED DEADLOCK`).
- Valor real de `DB_TENANT_MAX_OPEN` en el `.env` de producción (no leer `.env`; confirmar con el responsable).
- Si en el front la compra en USD convierte `unit_cost` a PEN antes de enviar (no revisé `PurchaseRegisterPage.tsx` en lo relativo a moneda).
- Reportes de dashboard/analytics (`internal/dashboard/handler`) que usen `purchase_price`/costo: no los revisé uno a uno.
- Código SUNAT de la Tabla 12 para un ingreso de producción: no está en el repo; consultar a contabilidad.
