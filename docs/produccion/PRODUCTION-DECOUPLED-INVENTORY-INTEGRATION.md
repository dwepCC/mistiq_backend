# Contrato de integración Producción ↔ Inventario

> **Propuesta técnica para aprobación. No implementada.** Parte de la [auditoría de opciones](PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md) (Alternativa A con adaptador). Modelo de datos en [PRODUCTION-DATA-MODEL-REVISED.md](PRODUCTION-DATA-MODEL-REVISED.md). Marcas: **[V-código]** verificado · **[I]** interpretación/propuesta · **[NV]** no verificado.

## 1. Principios
1. **Inventario es la única fuente de existencias.** Producción no guarda saldos, no inserta en `tenant_stock_movements` ni en tablas de stock directamente, y no abre transacciones de inventario independientes.
2. **Una sola transacción** por operación de fabricación (registro y anulación): tablas de Producción + movimientos oficiales. Todo o nada [V-código: el rollback de una `Transaction` de GORM revierte kardex, saldo y cabecera; patrón de ventas `sale_service.go:853-936`].
3. **Una sola dependencia estrecha:** la interfaz `StockGateway` (adaptador). La lógica de negocio de Producción no importa `internal/inventory`.
4. **Inventario no conoce a Producción:** el origen se guarda con columnas genéricas `source_type`/`source_id`.
5. **Servicio construido con la transacción:** `NewInventoryService(tx)`, nunca el pool, y **todas** las lecturas de stock/receta/unidades dentro de la misma `tx` (Anexo B y [V-código]: `EnsureProductBranchLink`, `StockTotalsByProductIDs`, `StockSummary` usan `s.db`).

## 2. Contrato mínimo que debe ofrecer Inventario (cambios aditivos)

| # | Cambio | Detalle | Retrocompatibilidad / qué NO cambia |
|---|---|---|---|
| I-1 | Origen genérico | `MovementInput.SourceType string`, `SourceID *uint`; columnas nullable `source_type varchar(32)`, `source_id bigint unsigned` + índice `(source_type, source_id)` en `tenant_stock_movements` (migración aditiva, sin backfill) | Valor cero = comportamiento actual. Ningún llamador cambia |
| I-2 | Resultado del movimiento | `RecordMovementResultTx(tx, in) (MovementResult{MovementID, Balance}, error)`; `RecordMovementTx` pasa a ser envoltorio `_, err :=` | **No cambia la firma de `RecordMovementTx`** (≈20 sitios y tests) |
| I-3 | Redondeo y epsilon | Redondear `Quantity` a 3 decimales al entrar y comparar saldo con tolerancia (0.0005, como `restaurant_order_stock.go:99`) | Solo elimina falsos «stock insuficiente» (3,141/14,000 combinaciones de prueba, Anexo B §E) |
| I-4 | Bloqueo de saldo | *Ensure-row* con UNIQUE + `SELECT … FOR UPDATE` (o `UPDATE quantity = quantity ± ?`), dentro de `RecordMovementTx`; opcional `RecordMovementsTx([]MovementInput)` que ordena por `(branch, product, presentation)`; reintento acotado ante 1213/1205 a cargo del **llamador** | Mejora a todos los llamadores sin cambiar su código; activable por flag; pool de 6: transacciones cortas |
| I-5 | Lecturas dentro de `tx` | Exponer lectura de stock por (sucursal, lista de productos) que acepte `tx` y devuelva un mapa (hoy existe la privada `loadStockByProducts`, `inventory_import_adjustment.go:416`); y lectura de costo de referencia por producto/sucursal | Aditivo |
| I-6 | Tipo de operación | `PRODUCTION_IN` (SUNAT `99` provisional hasta confirmación contable) vía `SeedInventoryOperationTypes` en una migración | `PRODUCTION_OUT` existente sin cambios (`AllowManual` no se toca) |
| — | **Fuera de contrato** | Fecha retroactiva (`CreatedAt` es siempre `now`, `inventory_service.go:186`; el Kardex ordena por `created_at` y una fecha pasada reordenaría el historial); validar `ManageStock` dentro del servicio (lo hace el llamador) | Producción guarda su fecha de negocio en sus tablas |

## 3. El adaptador

```text
Producción (service) ──usa──▶ StockGateway (interfaz, paquete production)
                                   ▲ implementa
                         inventoryGateway (adaptador)
                                   │ NewInventoryService(tx)
                                   ▼
                         InventoryService.RecordMovementResultTx / lecturas Tx
```
Operaciones (todas reciben `tx`): `Availability(tx, branch, productIDs) → map[productID]stock` · `ReferenceCosts(tx, branch, productIDs)` · `Out(tx, req) → MovementResult` · `In(tx, req) → MovementResult`. `req` lleva: producto, sucursal, cantidad (base), costo unitario, código de operación, referencia legible, usuario, `SourceType=production_entry`, `SourceID`, snapshot de unidad de venta opcional. El adaptador traduce errores de inventario a errores tipados de Producción (`ErrStockInsufficient{Product, Needed, Available}`, `ErrOperationTypeMissing`, `ErrInventoryTransient`).
Beneficios: pruebas unitarias con un doble de prueba; un único punto a cambiar si Inventario evoluciona; garantiza por construcción que no hay `s.db` en medio de la transacción.
Regla de revisión: prohibido en el paquete de Producción `NewInventoryService(s.db)`, `RecordMovement(` (sin Tx) y cualquier `Create/Updates` sobre `tenant_stock_movements` o tablas de saldo (verificable con un `grep` en CI).

## 4. Secuencia de «Registrar producción»

```mermaid
sequenceDiagram
    actor U as Usuario
    participant H as Handler
    participant S as ProductionService
    participant G as StockGateway
    participant DB as Transacción tenant
    U->>H: POST registro (Idempotency key, buenas, defectuosas, consumos, MO)
    H->>S: RegisterEntry(ctx, input)
    S->>DB: BEGIN
    S->>DB: ¿clave (user,key) existe? → replay/409
    S->>DB: SELECT orden FOR UPDATE; revalida clave
    S->>DB: valida estado, pendiente, sucursal, productos (ManageStock, sin variantes)
    S->>G: Availability(tx) con locks ordenados (todas las faltas)
    alt faltantes
        S-->>H: 409 STOCK_SHORTAGE [{producto, necesario, disponible}] (ROLLBACK)
    else
        S->>DB: INSERT entry (status=applied, key, hash)
        loop insumos ordenados por producto
            S->>G: Out(PRODUCTION_OUT, qty real, costo, source=entry)
            S->>DB: INSERT consumption (movement_id, unit_cost, origen costo)
        end
        S->>G: In(PRODUCTION_IN, buenas, costo unitario, source=entry)
        S->>DB: UPDATE entry (costos, finished_movement_id); UPDATE orden (acumulados)
        S->>DB: COMMIT
        S-->>H: 201 registro
    end
```
Detalle de pasos y razones:
1. **Replay temprano y tras el lock:** se consulta `UNIQUE(user_id, idempotency_key)` antes de nada y otra vez tras bloquear la orden (dos peticiones con la misma clave sobre la misma orden quedan serializadas por el lock de la orden).
2. **Disponibilidad con locks:** se leen los saldos de **todos** los insumos en orden determinista; si faltan, se devuelve la **lista completa** de faltantes (mejor UX que fallar en el primero) y se hace rollback sin escribir nada.
3. **Inserción del registro antes de los movimientos** para tener `SourceID`.
4. **Orden de los movimientos:** insumos por `product_id` ascendente, el terminado al final; mismo orden en la anulación (mezclando ENTRADAS y SALIDAS, se ordenan todas las filas de stock tocadas).
5. **Costos:** `UnitCost` de cada `OUT` = costo de referencia leído dentro de la transacción (promedio por sucursal si C1 está activo; si no, `purchase_price` con `origen=last_purchase`; si no hay, `none`). `UnitCost` del `IN` = `(Σ consumos × costo + mano de obra + otros) ÷ buenas`. Si `buenas = 0` no hay `IN` (solo consumo).
6. **Reintento por deadlock/lock-wait (1213/1205):** el servicio reintenta **toda la transacción** hasta 3 veces con espera aleatoria corta; es seguro porque nada se confirmó y la clave de idempotencia sigue libre.

## 5. Respuesta a los 10 escenarios de falla

| # | Escenario | Comportamiento |
|---|---|---|
| 1 | Falta stock de un insumo | La verificación bajo lock devuelve todos los faltantes (`409 STOCK_SHORTAGE`); si aun así `RecordMovementTx` rechaza (carrera residual o ausencia de lock mientras T2 no esté activo), el error `stock insuficiente para «X»: requiere N, hay M` produce **rollback total**. No hay movimientos parciales |
| 2 | Falla el ingreso del terminado | Rollback total: los `OUT` previos se revierten con la transacción [V-código]. Causa típica: `PRODUCTION_IN` no sembrado → `ErrOperationTypeNotFound` (`operation_type.go:20-31`). Mitigación: comprobación previa al iniciar el registro y desplegar la migración antes del backend |
| 3 | Dos usuarios consumen los mismos insumos | Misma orden: serializado por `FOR UPDATE` de la orden. Órdenes distintas con insumos comunes: serializado por locks de saldo en orden determinista (requiere I-4 activo). **Sin I-4 hay pérdida de actualización** [V-código]: por eso el módulo no se habilita con datos reales hasta T2/T4 |
| 4 | Reintento por timeout | `idempotency_key` UUID generado al abrir el modal y reenviado igual en reintentos. El servidor no cancela el handler cuando el cliente se desconecta, por lo que la transacción puede haber confirmado: el reintento encuentra la clave y devuelve el **mismo registro** con `idempotent_replay: true` sin tocar inventario. Misma clave con payload distinto (hash) → `409 IDEMPOTENCY_KEY_REUSED`. Condición de carrera de la clave: `UNIQUE` falla → rollback → se relee y se devuelve el registro ganador (patrón `sale_idempotency.go:52-82`). Riesgo residual: el usuario recarga la página (pierde la clave) → aviso de posible duplicado si hay un registro idéntico del mismo usuario en los últimos minutos (opcional V1) |
| 5 | Anular un registro aplicado | Transacción: bloquea orden y registro (`FOR UPDATE`), exige `status=applied`; lee los consumos guardados; emite `IN` de cada insumo con la cantidad y **costo originales** y `OUT` del terminado al costo del `IN` original, todos con `SourceType/SourceID` del registro y referencia `ANULACION PRODUCCION/…`; guarda los IDs de los movimientos de reversa; marca `voided` con usuario, fecha y motivo; recalcula acumulados. Nada se borra |
| 6 | El terminado ya se vendió | El `OUT` del terminado falla por stock insuficiente → rollback total → mensaje: «No se puede anular: se necesitan N unidades del producto terminado y hay M disponibles». V1 **no anula parcialmente**. Salidas para el usuario: restablecer stock (p. ej., anular/devolver las ventas afectadas) y reintentar. **Hueco detectado:** si el error era solo de *consumo* (no de unidades buenas), anular obliga a retirar el terminado aunque esté vendido; la solución limpia es un «ajuste de consumo» que solo mueva la diferencia de insumos (V1.5, ver ADR-12) |
| 7 | Evitar movimientos duplicados | (a) clave de idempotencia; (b) `UNIQUE(entry_id, order_line_id)` en consumos → como máximo un `OUT` por insumo por registro; (c) `finished_movement_id` se asigna una sola vez; (d) prueba de conciliación: por cada registro `applied`, el conteo de movimientos con su `source` = nº de consumos con cantidad > 0 + (1 si buenas > 0) |
| 8 | Vínculo movimiento ↔ registro | `source_type='production_entry'`, `source_id=entry.id` en **todos** los movimientos (incluidas reversas); además cada consumo guarda `out_movement_id`/`void_movement_id` y el registro `finished_movement_id`/`void_finished_movement_id` |
| 9 | Kardex y auditoría | El Kardex oficial muestra operación `PRODUCTION_OUT/IN`, referencia legible («PRODUCCION/OP-0012 reg. 3») y costo; filtrable por `source`. Auditoría: columnas `created_by/voided_by/*_at` y motivos **dentro del tenant y de la misma transacción**; `WriteAuditLog` (central, best-effort, no transaccional) solo como complemento desde el handler [V-código: `audit_log.go:41-72`] |
| 10 | «Inventario indisponible» | Inventario vive en la misma BD del tenant: indisponible = error de conexión, timeout o espera de lock. La transacción falla completa (`503 DB_TRANSIENT` o `409` según causa), sin estado intermedio; el cliente reintenta con la misma clave. **No se usa cola ni outbox en V1** (rechazadas: obligan a existencias eventualmente consistentes, venta de stock aún no descontado y compensaciones complejas). Las transacciones no tienen timeout propio [V-código: `WithQueryClass` sin llamadores]; se limita el nº de líneas por registro y se acotan lecturas |

## 6. Anulación de orden y cierre
- **Anular orden** solo sin registros vigentes (sin movimientos que revertir).
- **Cerrar orden** no mueve stock; marca `closed`. Con producido < planificado pide confirmación.
- **Producir después de cerrar:** orden nueva (puede referenciar `origin_order_id`).

## 7. Costos (separación explícita)
| Concepto | Qué es | Dónde vive |
|---|---|---|
| **Costo de gestión de producción** | Por registro: Σ(consumo real × costo de referencia) + mano de obra + otros costos directos; ÷ buenas = unitario. Defectuosas informativas; el costo se absorbe en las buenas | Tablas de Producción (`entries`, `consumptions`) |
| **Valoración de inventario** | Costo con que el movimiento `IN/OUT` entra al Kardex y, en modo sombra, promedio por (producto, sucursal) | `tenant_stock_movements.unit_cost` y columnas de costo promedio (entrega C1) |
| **Costo de ventas** | Hoy `PurchasePrice` del producto (`sale_service_calc.go:84-99`) | **No se modifica** en esta propuesta; decisión aparte (D-03) |

Casos:
- **Insumos sin costo conocido** (stock inicial, ajustes, devoluciones): no alteran el promedio; la línea se marca `origen=none` y el registro queda «costo incompleto» (visible), sin bloquear.
- **Traslados:** la entrada en destino va con costo 0 [V-código: Anexo B]; un insumo trasladado a otra sucursal puede llegar «sin costo». Opción (decisión): que `Transfer` propague el costo; mientras tanto se marca incompleto.
- **Compras con IGV incluido:** el `UnitCost` guardado es lo tecleado (`purchase_service.go:181-184, 317, 364`); el costo de referencia debe ser **sin IGV** (D-04). Hasta decidirlo, Producción muestra advertencia de base.
- **Anulación:** reversa con los costos guardados; el promedio móvil no se restaura exactamente (limitación documentada).
- **Rótulo obligatorio en UI e informes:** «costo de gestión (no contable)». Su validez contable depende de la revisión del contador.

## 8. Invariantes (cada una debe tener prueba automatizada)
1. Por cada registro `applied` existe exactamente un `IN` (si buenas > 0) y un `OUT` por consumo con cantidad > 0, todos con su `source`.
2. Σ buenas de registros vigentes = `orders.produced_quantity`.
3. Una clave de idempotencia produce como máximo un registro.
4. Ningún movimiento de Producción existe sin `source`.
5. Un registro `voided` tiene exactamente un contramovimiento por cada movimiento original, al mismo costo.
6. Si cualquier paso falla, no queda ninguna fila de Producción ni de inventario de esa operación.
7. El paquete de Producción no escribe tablas de stock (verificado por grep/lint).
8. Sin I-4 activo en un tenant, el módulo está deshabilitado en ese tenant.

## 9. Lo que no se pudo verificar [NV]
Comportamiento en carga real del pool (6); `innodb_lock_wait_timeout` y aislamiento reales; existencia de duplicados de saldo; si algún tenant usa `PRODUCTION_OUT` manualmente. Requieren medición de solo lectura con autorización (ver roadmap, etapa 2).
