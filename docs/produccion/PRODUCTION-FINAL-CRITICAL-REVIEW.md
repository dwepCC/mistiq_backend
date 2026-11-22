# Revisión crítica final — Módulo de Producción de Bendey ERP

> **Errata (auditoría 3, 2026-10-13)** — superado en: enlace de movimientos (`production_entry_id` → `source_type`/`source_id` genéricos), idempotencia (`UNIQUE(user_id, key)`), numeración de órdenes (`OP-<id>`) y auditoría (columnas del tenant; `WriteAuditLog` solo complementario). Además: `Implemented:false` **no bloquea** el backend y `RequireModule` depende del JWT.
> Detalle y evidencia: [PRODUCTION-ARCHITECTURE-DECISION-RECORD.md](PRODUCTION-ARCHITECTURE-DECISION-RECORD.md) §2 y [PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md](PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md) §2. El texto original se conserva.

> Fecha: 2026-10-13. Estado: **auditoría y documentación. No se ha implementado nada; no se modificó código, BD, configuración ni servidores.**
> Método: dos revisiones independientes (subagentes, solo lectura) + verificación propia en código de los puntos que más pesan.
> - [Anexo A — matriz de coherencia (34 hallazgos)](PRODUCTION-FINAL-REVIEW-ANNEX-A-COHERENCE-MATRIX.md)
> - [Anexo B — verificación del código de inventario/costos](PRODUCTION-FINAL-REVIEW-ANNEX-B-CODE-VERIFICATION.md)
> - Documentos que acompañan esta revisión: [Decisiones](PRODUCTION-FINAL-DECISIONS.md) · [Alcance candidato](PRODUCTION-APPROVED-SCOPE-CANDIDATE.md) · [Roadmap revisado](PRODUCTION-IMPLEMENTATION-ROADMAP-REVISED.md)
>
> Marcas: **[V-código]** verificado en código · **[V-datos]** verificado con consulta a datos reales (CONSTRUMARKET) · **[Ext]** fuente externa · **[I]** interpretación · **[NV]** no verificado.

---

## 1. Resumen ejecutivo

**Veredicto:** el diseño funcional (receta de un nivel → orden → registro de producción → costo → anulación por contramovimientos) sigue siendo correcto y suficientemente simple. **Pero mi propuesta anterior no estaba lista para implementarse**: tenía 5 defectos críticos en reglas de costo/cantidades, una dependencia de Fase 0 mal dimensionada y varias afirmaciones apoyadas en evidencia insuficiente. Ninguno invalida el módulo; todos se corrigen en el diseño.

**Lo que cambia respecto a la propuesta anterior:**

| # | Cambio | Por qué |
|---|---|---|
| 1 | La **mano de obra y otros costos se ingresan en cada registro**, no a nivel de orden | Un costo editable después de postear el ingreso al kardex es irreproducible y el prorrateo no tenía denominador definido (Anexo A #1). |
| 2 | **`average_cost` por (producto, sucursal)**, no por producto, y **«en sombra»** en la primera entrega: no cambia ningún reporte existente | El stock es por sucursal y los traslados no llevan costo (Anexo A #2). Cambiar el costo de ventas afecta a todo el ERP y requiere decisión de negocio/contable. |
| 3 | **«Producido» = unidades buenas.** Las defectuosas son informativas | El Caso D (95 buenas + 5 defectuosas) dejaba el avance y el cierre ambiguos (#3). |
| 4 | **Se elimina «Reabrir orden».** Se corrige anulando registros; si hace falta producir más tras cerrar, se crea una orden nueva | Menos estados y transiciones; evita reabrir costos ya cerrados. |
| 5 | **Anular un registro** retira el terminado **al costo del ingreso original** (guardado en el registro) | No estaba definido (#4). |
| 6 | **Variantes fuera de V1** (se rechazan productos con variantes como insumo o terminado) hasta verificar la reconciliación stock de presentación/producto | `RecordMovementTx` solo actualiza la fila de la presentación; el criterio «saldo = tenant_product_stocks» era falso para variantes (#6, #7). CONSTRUMARKET no las necesita [V-datos]. |
| 7 | **La Fase 0 se divide en 4 entregas independientes** con medición previa de solo lectura, activación por flag y reversión definida | Antes era una sola «Fase 0» que tocaba todo el ERP. |
| 8 | **La conexión con costo de ventas/utilidad pasa a decisión de negocio** y queda fuera de V1 por defecto | `resolveSaleItemPurchasePrice` usa `PurchasePrice`; nada en mi plan lo cambiaba (#5). |
| 9 | **Costo de compra posiblemente con IGV incluido** es un riesgo nuevo y crítico para el costo de producción | Hallazgo nuevo (Anexo B §C.1): `UnitCost` se guarda tal cual lo tecleó el usuario. |
| 10 | **Redondeo/epsilon** en `RecordMovementTx` antes de producción | Hallazgo nuevo: 3,141 de 14,000 combinaciones de prueba rechazan consumir «exactamente todo el stock» por coma flotante. |

**Decisiones que necesito de usted:** ver [PRODUCTION-FINAL-DECISIONS.md](PRODUCTION-FINAL-DECISIONS.md) (14 decisiones de negocio/contables; 13 resueltas por evidencia técnica y que solo debe confirmar).

---

## 2. Hallazgos críticos (corregir antes de implementar)

### C1. Costo de mano de obra irreproducible / sin denominador (Anexo A #1) — Crítico
- **Qué:** la propuesta decía que los costos extra de la orden se «prorratean por registro según unidades buenas+defectuosas». No se define el denominador (¿planificado o total final?) y el costo unitario del ingreso (`PRODUCTION_IN`) se fija al registrar; si luego se edita la mano de obra de la orden, el ingreso ya posteado y el promedio no se recalculan. Además el modal de registro no tenía campo de mano de obra aunque el Caso A la usa.
- **Corrección:** mano de obra y otros costos directos son **campos del registro** (opcionales). Entran al costo de ese ingreso y no se editan después (solo se anula y se vuelve a registrar). Los costos «a nivel de orden» pasan a ser un **presupuesto informativo** que no afecta movimientos.

### C2. `average_cost` global y mal ubicado (Anexo A #2; Anexo B §C) — Crítico
- **Qué:** propuesta y roadmap lo ponían en el producto, global. Pero el stock es por sucursal, los traslados entran a destino con costo 0 [V-código], y `WeightedAverageUnitCosts` solo reproduce el historial por sucursal e ignora presentaciones [V-código].
- **Corrección:** costo promedio **por (producto, sucursal)**, persistido junto al saldo, actualizado dentro de la misma transacción que el movimiento. Sección 4.3 desarrolla la política completa.

### C3. «Producido» sin definir (Anexo A #3) — Crítico
- **Corrección:** `producido = Σ unidades buenas de registros vigentes`. Las defectuosas no cuentan para avance ni cierre. En el Caso D la orden de 100 queda en 95/100: se cierra con confirmación («faltan 5») o se registran 5 buenas más.

### C4. Anulación de registro sin costo definido (Anexo A #4) — Crítico
- **Corrección:** el `OUT` del terminado usa el `unit_cost` del `IN` original (guardado en el registro); los insumos reingresan al costo con que salieron (guardado en cada línea de consumo). Se permite anular registros mientras la orden no esté Anulada, **en cualquier orden cronológico**, sujeto a que haya stock del terminado. Limitación documentada: el promedio móvil no se «restaura» exactamente tras una reversión (4.3-P9).

### C5. El costo de producción no llega al costo de ventas (Anexo A #5) — Crítico (para el beneficio comercial)
- **Qué:** la venta toma `TenantProduct.PurchasePrice` como snapshot (`sale_service_calc.go:84-99`) [V-código]. Mi propuesta decía que el costo del terminado «alimenta el costo base de ventas» sin tarea que lo hiciera. Un producto fabricado sin compras tendría `purchase_price=0`: utilidad inflada.
- **Corrección:** declararlo explícitamente como **decisión de negocio** (D-03). Por defecto, V1 muestra el costo de producción en **reportes de Producción** y no toca los reportes de utilidad; el cambio de `resolveSaleItemPurchasePrice` (dos sitios) se planifica como entrega aparte y opcional.

### C6. Costo de compra posiblemente bruto de IGV (hallazgo nuevo, Anexo B §C.1) — Crítico
- **Qué:** `PriceIncludesIgv` solo afecta subtotal/IGV/total de la compra (`purchase_service.go:181-184`); el `UnitCost` del movimiento y el `purchase_price` del producto son el valor **tal cual se tecleó** (`purchase_service.go:317, 364`) [V-código, confirmado por mí]. Si un cliente compra con «precio incluye IGV», su costo de insumos queda ~18 % sobredimensionado en cualquier costo de producción. El análisis previo (GAP §2.4) afirmaba lo contrario.
- **Impacto:** costos de producción potencialmente inflados; no es corrupción de inventario, pero hace inservible el costo.
- **Corrección:** el costo de insumos usado por Producción debe ser **sin IGV**. Decisión D-04: (a) normalizar en compras a futuro; (b) solo normalizar el costo que alimenta `avg_cost`; o (c) advertir en la UI. Antes: medir cuántas líneas de compra reales usan «incluye IGV» (consulta de solo lectura §9 del Anexo B).

### C7. Pérdida de actualización de saldo y sobreventa — confirmado (Anexo B §A–B) — Crítico (transversal)
- **Qué [V-código]:** `RecordMovementTx` lee el saldo sin lock (`tx.Where(...).First`), calcula `newBalance` en Go y escribe `quantity = newBalance` **absoluto** (`inventory_service.go:155-209`). No hay `FOR UPDATE` sobre ninguna tabla de stock ni UNIQUE (producto,sucursal)/(presentación,sucursal). Escenario reproducible: saldo 10; T1 resta 8 y T2 resta 8 → ambas pasan el chequeo → saldo final 2 pero salieron 16.
- **Matices que corrigen el análisis previo:** (i) el riesgo dominante es el *lost update/sobreventa*, no tanto la doble fila de saldo, que está mitigada por `EnsureProductBranchLink` (crea filas en 0); (ii) no hay reintento ante deadlock (error 1213) y ya hoy ventas con ítems en orden distinto pueden producirlo [I]; (iii) añadir solo `FOR UPDATE` no basta: sobre una fila inexistente bajo REPEATABLE READ genera gap locks y deadlocks → hace falta *ensure-row* (`ON DUPLICATE KEY`) con el UNIQUE, orden de bloqueo y reintento.
- **Aún no medido [NV]:** si en producción ya hay duplicados o diferencias saldo/kardex. Consultas exactas en Anexo B (§ «Lo que NO se pudo verificar»).

### C8. Rechazo falso por coma flotante (hallazgo nuevo, Anexo B §E) — Alto/Crítico para Producción
- `newBalance < 0` se evalúa en `float64` sin epsilon; con cantidades como 0.018 × n el consumo «exacto» del stock se rechaza en el 22 % de las combinaciones probadas. Producción consume decimales pequeños por naturaleza. **Corrección previa a Producción:** redondear `Quantity` y saldo a 3 decimales antes de comparar.

---

## 3. Contradicciones y errores entre documentos (resumen; detalle en Anexo A)

| Tema | Contradicción | Resolución |
|---|---|---|
| Fórmula de desperdicio | Propuesta `neto×(1+%)`; investigación usa `neto/(1−%)` en un ejemplo (Anexo A #8) | Se fija `% sobre cantidad neta`: `bruto = neto×(1+%)`. Ejemplo de la investigación queda como convención alternativa (rendimiento). |
| Estados | Diagrama permitía «Reabrir»; criterios de aceptación no lo cubrían | «Reabrir» eliminado. Estados finales: Borrador, En proceso, Terminada, Anulada. |
| Variantes | «V1» en la propuesta, «decisión abierta» en §12, «no recomendado en V1» en benchmark/investigación | Fuera de V1; condicionadas a verificación (D-10). |
| Política de consumo | La investigación decía «configurable al iniciar/terminar en V1»; la propuesta eligió «por registro» y difirió la configurable | **Intencional y justificado**: el consumo por registro equivale a «al terminar» y soporta parciales sin configuración. Queda documentado. |
| Numeración de migraciones | El roadmap numeraba 4 migraciones y omitía `average_cost`, `PRODUCTION_IN` y permisos | Roadmap revisado usa tabla con dependencias; los números se asignan al implementar. |
| Criterio «saldo cuadra con `tenant_product_stocks`» | Falso para presentaciones (código lo actualiza en otra tabla) | Reformulado; variantes fuera de V1. |
| Decisiones bloqueantes | El roadmap solo listaba 1, 4 y 6 | Reordenadas en el documento de decisiones. |
| Numeración de órdenes | Dependía de `docseries` (requiere serie configurada por sucursal en tenants existentes) | Contador propio con UNIQUE `(sucursal, número)`, sin depender de series preexistentes. |

Diferencias **intencionales y justificadas** (no son errores): backflush por registro frente a «al terminar»; defectuosas sin ingreso a stock; WIP informal; semiterminados encadenados; receta de un nivel con snapshot; ausencia de lotes.

---

## 4. Revisión de las decisiones de arquitectura

### 4.1 `RecordMovementTx` — qué es real
- **Reales [V-código]:** lost update, sobreventa, falta de UNIQUE, ausencia de reintentos, `Transfer()` en dos transacciones (alcanzable vía `ReverseTransfer`), pre-chequeos de stock fuera de la transacción de venta (`sale_service.go:475-500`), comparación flotante sin epsilon.
- **Corregidos respecto al análisis previo:** las «rutas legacy de series» (`adjustmentIn/OutWithSerials`) y los handlers HTML `AdjustmentSubmit/TransferSubmit` son **código muerto**; los tipos `adjustment_in/out` no los escribe ningún flujo vivo; el filtro rápido «adjustment» del kardex no devuelve los ajustes actuales.
- **Comportamiento que Producción debe respetar:** el método no valida `ManageStock` (cada llamador lo hace); no valida que `Type` coincida con la dirección del tipo de operación (`TRANSFER` se usa con `out`); con presentación solo actualiza la fila de presentación; `OperationCode` vacío deja `operation_type_id` en NULL sin error.
- **Cómo reproducir [propuesta de prueba, no implementada]:** test con MySQL real desechable (patrón `DOCUSAGE_MYSQL_DSN` ya existente en `pkg/saas/docusage/concurrency_test.go`), variable `INVENTORY_MYSQL_DSN` con `t.Skip` si falta. Casos: N goroutines restando 1 de un saldo de 10 (esperado `SUM=10−N`, balances del kardex estrictamente decrecientes); N goroutines sobre un par sin fila inicial (esperado 1 fila); pares cruzados A/B–B/A para medir deadlocks y exigir reintento. SQLite **no sirve** (omite `FOR UPDATE`).
- **Endurecer sin regresiones [I]:** (1) redondeo/epsilon; (2) *ensure-row* con UNIQUE + `SELECT … FOR UPDATE`, o `UPDATE quantity = quantity ± ? WHERE … AND quantity >= ?`; (3) extraer `RecordMovementsTx([]MovementInput)` que ordene por `(branch, product, presentation)`; (4) reintento acotado ante 1213/1205; (5) mantener transacciones cortas (pool de tenant pequeño; incidente gyl); (6) activar detrás de un flag con camino anterior intacto. Riesgos de regresión por llamador: ver Anexo B §B.5.

### 4.2 Índices únicos y datos existentes
- **Tablas/combinaciones:** `tenant_product_stocks (product_id, branch_id)` y `tenant_product_presentation_stocks (presentation_id, branch_id)`. No hay otras necesarias para Producción.
- **Creación simultánea hoy:** sin UNIQUE, dos transacciones pueden insertar dos filas; las lecturas son inconsistentes entre pantallas (`LIMIT 1` vs `SUM`).
- **Detección:** consultas 1 y 2 del Anexo B (agrupación con `HAVING COUNT(*)>1`), más 3/3b (saldo vs kardex) y 3c (saltos de `Balance`).
- **Consolidación:** **nunca automática.** Procedimiento propuesto: (a) informe por tenant (duplicados, diferencias con kardex); (b) revisión humana; (c) script de consolidación revisable que suma saldos en la fila de menor `id`, deja evidencia en una tabla de auditoría y no toca el kardex; (d) la migración del UNIQUE **aborta para ese tenant** si aún hay duplicados, y registra el motivo, sin tocar datos; (e) las diferencias saldo–kardex **no** se corrigen implícitamente (si las hay, ajuste documentado con aprobación).
- **Si se encuentran datos inconsistentes:** el tenant queda marcado «sin endurecer»; Producción se habilita solo en tenants endurecidos.

### 4.3 Costos y valoración — evaluación crítica de `average_cost`

**Respuestas a las 10 preguntas:**

1. **¿Hace falta promedio móvil antes de Producción?** Hace falta *algún* costo reproducible para insumos y terminado. Alternativas:
   | Alternativa | Pros | Contras | Veredicto |
   |---|---|---|---|
   | A. Último costo de compra (`purchase_price`) | Existe hoy | Es el último precio, no el costo; cambia con cada compra; compras sin stock lo sobrescriben; puede traer IGV (C6) | ❌ como política |
   | B. Reconstruir con `WeightedAverageUnitCosts` en cada consulta | Sin esquema nuevo | O(n) sobre todo el kardex por producto; ignora presentaciones; por sucursal sin traslados con costo; pool pequeño | ❌ para uso transaccional |
   | C. Costo estándar (lo tipea el usuario) | Simple, estable | No refleja realidad; mantenimiento manual | ⚠️ solo como alternativa para pymes sin compras registradas |
   | D. Promedio móvil persistido por (producto, sucursal) | Reproducible, barato de leer, estándar de pymes | Requiere tocar `RecordMovementTx`; semántica de entradas sin costo | ✅ **recomendado**, en modo sombra |
   | E. Capas de costo (PEPS) | Exacto | Complejo, fuera de alcance | ❌ |
2. **Compatibilidad con Compras/Ventas/Traslados/devoluciones:** en modo sombra **no cambia** ningún movimiento ni reporte existente: solo agrega una columna que se actualiza. Compras: entradas con `UnitCost>0` ponderan. Ventas/salidas: usan el promedio vigente como costo del movimiento *solo en los movimientos de Producción* en V1 (las salidas de venta siguen con `UnitCost=0`). Traslados: la entrada en destino debería heredar el promedio del origen (cambio en traslados: opcional, con decisión).
3. **Entradas sin costo (stock inicial, ajustes, devoluciones):** regla: **no alteran el promedio** (siguen sumando cantidad, como ya hace `WeightedAverageUnitCosts`); si el saldo era 0 y el promedio 0, queda sin costo («costo desconocido»). Producción marca esas líneas como «costo estimado/ausente» (origen del costo: `average` | `last_purchase` | `none`).
4. **Salida que deja stock en cero:** el promedio **se conserva** (no se resetea) hasta la próxima entrada con costo, cuando pasa a ser ese costo.
5. **Stock negativo existente:** en productos no puede ocurrir (guard de `RecordMovementTx`); si el saldo fuera ≤0 el promedio se toma del último valor positivo. Los negativos observados en CONSTRUMARKET fueron del legacy con `stock_control=false`, no del ERP nuevo.
6. **Producción parcial:** cada registro calcula su propio costo con el promedio vigente de cada insumo en ese momento; `costo unitario del registro = (Σ consumos × costo + mano de obra y otros del registro) / unidades buenas del registro`. El costo de la orden es la suma de sus registros vigentes.
7. **Valorización del terminado:** el `PRODUCTION_IN` entra con ese costo unitario y pondera el promedio del terminado en esa sucursal.
8. **No recalcular histórico:** el promedio solo se actualiza hacia adelante, dentro de la transacción del movimiento. El `backfill` inicial se calcula **una vez** reproduciendo el kardex (misma lógica que `WeightedAverageUnitCosts`) con marca de origen `backfill`, en un proceso por lotes fuera de migración, sin reescribir movimientos.
9. **Anulación:** reingreso de insumos al costo con que salieron (guardado por línea); salida del terminado al costo del ingreso (guardado en el registro). **Limitación:** el promedio móvil no restaura su valor anterior exacto (los OUT no cambian el promedio por definición); la diferencia es pequeña y aceptada en V1, documentada.
10. **Impacto en reportes de rentabilidad:** **ninguno** en V1 (modo sombra). Si se decide usar el promedio como costo de venta, cambia `resolveSaleItemPurchasePrice` (`sale_service_calc.go:88` y `restaurant_service.go:1583`) y las utilidades nuevas diferirán de las históricas (que conservan su snapshot). Además existe el bug confirmado de `ProfitDetail` con `SaleUnit` (`sale_service.go:2095`: mezcla costo por unidad base con cantidad comercial; ejemplo: 2 sacos a S/400 con factor 100 y costo S/3/kg muestra utilidad 794 en lugar de 200).

**Política recomendada [I]:** promedio móvil por (producto, sucursal) en sombra, costo sin IGV, origen del costo visible en cada línea, y **no presentarlo como costo contable definitivo**: es un costo de gestión. El tratamiento contable (NIC 2: absorber merma normal, gastar la anormal [Ext, de segunda mano]) debe validarlo el contador del cliente (D-05).

### 4.4 Tipos de operación y origen del movimiento
- **Hecho [V-código]:** catálogo `tenant_inventory_operation_types` (Direction, Code único, SunatCode, AllowManual, RequiresDocument); el seed solo inserta si el código no existe; `PRODUCTION_OUT` (SUNAT 10) existe con `AllowManual: true`; **no existe `PRODUCTION_IN`**; `RecordMovementTx` falla con `ErrOperationTypeNotFound` si el código no está sembrado → la migración debe correr antes del deploy.
- **Origen:** hoy hay FK específicas (sale_item, purchase_item, transfer, inventory_document) y `reference` texto (≤100). Se propone **`production_entry_id` nullable** en `tenant_stock_movements`, sin FK dura. La reversión de venta usa `reference` exacta; para Producción se usa el FK (más robusto).
- **Kardex:** los filtros rápidos usan prefijos de `reference` (`COMPRA/`, `VENTA/`, `TRANSFER`). Un movimiento con `reference = "PRODUCCION/<N>"` no aparece en ningún filtro rápido: se añade filtro por `operation_code`/`production_entry_id` y texto explicativo («Producción OP-0007 / reg. 2»).
- **Anulaciones:** movimientos nuevos con `reference = "ANULACION PRODUCCION/…"` y el mismo `production_entry_id`; el registro queda `anulado`, nunca se borra nada.
- **Código SUNAT de `PRODUCTION_IN`:** **no confirmado.** Para no bloquear el diseño interno: crear `PRODUCTION_IN` con `99` (otros) provisional. Consulta concreta al contador: «¿Qué código de la Tabla 12 SUNAT corresponde al ingreso de producto terminado desde producción propia, para el Registro de Inventario Permanente en Unidades Físicas?».
- **Decisión técnica:** no cambiar `AllowManual` de `PRODUCTION_OUT` en V1 (desconozco si algún tenant lo usa manualmente; cambiarlo podría romper egresos existentes). Se revisará con la consulta 11 del Anexo B.

---

## 5. Revisión del modelo funcional

- **¿El flujo de 8 pasos es suficiente?** Sí, con los ajustes de arriba. El paso «consumo editable» es el mecanismo que absorbe merma y diferencias sin añadir funcionalidades.
- **Recetas de un nivel:** cubren materiales, componentes y envases (CONSTRUMARKET usa el envase como insumo [V-datos]). Semiterminados = productos con su propia receta y orden. **Validación adicional necesaria:** detección de ciclos transitivos entre semiterminados (A usa B, B usa A) — Anexo A #20.
- **Unidades:** la receta guarda cantidad (decimal(15,6)) y unidad (base o `SaleUnit` del insumo); el snapshot guarda la cantidad **ya convertida a unidad base**. Para insumos comprados en saco y consumidos en kg: dar de alta el insumo en kg (como hizo CONSTRUMARKET) o definir una `SaleUnit`.
- **Cambio de receta con órdenes abiertas:** el snapshot no cambia; «Actualizar a receta vigente» solo en Borrador. **Receta histórica:** conservada en las líneas de la orden y en el consumo real de cada registro.
- **Variantes:** fuera de V1 (ver C-resumen #6). Evidencia: ninguna (CONSTRUMARKET usa productos separados). No implementar matrices sin cliente que las demande.
- **Estados:** Borrador → En proceso → Terminada; Anulada desde Borrador o En proceso sin registros vigentes. Sin «Reabrir». Tabla de operaciones permitidas en el documento de alcance.
- **Producción parcial 60 + 40:** cada registro con su consumo y costo; acumulado `producido = 60 → 100`. Sin duplicar movimientos (idempotencia por registro). Menos de lo planificado: se cierra con confirmación. Más de lo planificado: **bloqueado** salvo que alguien con `production.manage` aumente la cantidad planificada (queda en auditoría) — así los excedentes son autorizados sin un flujo adicional.
- **Mermas/defectuosas:** distinguidas así — *material desperdiciado*: parte del consumo real (V1 obligatorio, vía consumo editable); *diferencia estimado vs real*: V1 obligatorio, visible; *producto defectuoso*: contador con motivo (V1 opcional, informativo, **nunca ingresa como vendible**); *recuperable/reutilizable*: V1.5.
- **Producción por pedido:** referencia opcional (cliente/pedido/fecha compromiso). Limitación: sin reservas, un tercero puede vender el stock antes. Para ampliar: reservas blandas + botón «Producir desde venta» (V1.5).

---

## 6. Caso CONSTRUMARKET — qué valida y qué no

**Validado [V-datos]:** receta de un nivel con 4-7 insumos; consumo en kg con envase como componente; descuento de insumos y alta del terminado en el mismo acto (transacciones 101 y 19), con la fórmula `receta × cantidad producida` confirmada en la ficha 125; uso diario por un único responsable; ausencia de lotes/máquinas/mermas; ausencia total de costos.

**Hipótesis (no validadas):** necesidad de variantes (no hay evidencia a favor ni en contra: usan productos separados); utilidad de defectuosos y recuperables; producción por pedido; que el bloqueo de negativos sea aceptable para clientes con compras incompletas; que el costo calculado coincida con el contable.

**Corrección a mi análisis anterior (Anexo A #25, #27):** la comparación «vendido 10,929 vs producido 6,645» **no es concluyente**. La consulta que usé traía ventas de los `item_id` presentes en `production` —incluidos los ítems 5 y 46, ya eliminados— sin stock inicial validado ni compras completas, y el legacy permitía negativos por defecto. Tampoco se sigue que «vendían antes de producir»; por tanto el botón «Producir desde una venta» **no queda sustentado por este caso** (sigue en V1.5 como hipótesis). Igualmente, «solo 78 de 125 fichas coinciden con la receta actual» compara **número de salidas**, no cantidades; puede deberse a cambios de receta *o* a edición de consumos por ficha; el snapshot de receta se justifica por razones independientes. La cifra de «0.65 % de exceso» en la receta es aritmética correcta pero no prueba que sea merma esperada.

**Para una prueba real hará falta cargar:** compras reales de insumos (precio sin IGV), stock inicial por conteo físico, recetas (8 activas, 2 de pintura sin uso), envases por marca, unidades en kg. **Validar stock inicial:** conteo físico + ajuste con documento de inventario aprobado. **Exactitud de recetas:** 3 tandas controladas pesando consumo real y comparando con receta. **Costo unitario:** recálculo manual de 2 registros con la hoja del dueño y revisión del contador. **Piloto exitoso:** ver criterios en [PRODUCTION-APPROVED-SCOPE-CANDIDATE.md](PRODUCTION-APPROVED-SCOPE-CANDIDATE.md). No se asume que CONSTRUMARKET sea el piloto: está en baja del VPS antiguo, redujo su actividad y su calidad de datos es débil.

---

## 7. Evidencia débil y límites (qué no tomar como hecho)

| Afirmación | Estado |
|---|---|
| «Las pymes (Katana, MRPeasy) usan 3-4 estados» | Parcial: BC (5 estados) verificado; Katana/MRPeasy/Odoo/ERPNext listas completas **no verificadas** (Benchmark); tratado como [I] |
| NIC 2 (merma normal en costo, anormal a gasto) | Resúmenes de terceros; el PDF oficial no se pudo leer. Contexto, no asesoría |
| Cifras de merma por rubro (10-30 %, etc.) | Órdenes de magnitud de blogs/tesis; no usar como parámetros |
| «Casi todo [F] de la investigación» | Gran parte de resúmenes de búsqueda; no se abrieron todas las páginas |
| Valores de producción: aislamiento, `sql_mode`, `DB_TENANT_MAX_OPEN`, duplicados de stock, versión de esquema | **[NV]** — requieren consultas de solo lectura autorizadas |
| Código SUNAT de ingreso de producción | **[NV]** — consulta al contador |
| Si otros tenants del legacy usan producción | **[NV]** — solo se auditó CONSTRUMARKET |

---

## 8. Riesgos

**Técnicos:** lost update/sobreventa en un núcleo compartido (C7); deadlocks por orden de bloqueo y falta de reintento; coma flotante (C8); costos con IGV (C6); pool de conexiones pequeño (6 por defecto; incidente gyl); migraciones en 590 tenants con datos desconocidos; `Transfer()` no atómico; consumo `0.0004` redondeado a 0 en BD.
**Funcionales:** que el bloqueo de negativos frustre a clientes con compras no registradas; que el cálculo de costo se confunda con costo contable; que el cliente espere reservas para pedidos; que se pida variantes antes de lo previsto.
**De proceso:** dar por probado lo que solo se leyó; desplegar el endurecimiento sin medir duplicados; pendientes de seguridad del servidor antiguo (gestionados fuera de este repositorio).

---

## 9. Recomendaciones definitivas
1. Aprobar primero **solo la medición de solo lectura** (consultas del Anexo B) en una muestra de tenants; con sus resultados decidir el endurecimiento.
2. Implementar la corrección de redondeo/epsilon y las pruebas con MySQL **antes** que cualquier cosa de Producción.
3. Endurecer inventario por etapas con flag y reversión; no mezclarlo con el módulo.
4. Construir costo promedio **en sombra** y por sucursal; no tocar reportes de utilidad en V1.
5. Producir V1 con las reglas corregidas: mano de obra por registro, producido = buenas, sin reabrir, sin variantes.
6. Resolver con el contador: base de costo sin IGV, tratamiento de merma, código SUNAT.
7. Mantener los siete documentos originales como evidencia; las correcciones quedan anotadas en una «Errata» en cada uno apuntando a este documento.

*Limitaciones de esta revisión: no se accedió a producción; no se ejecutó código; la verificación de código la hizo un subagente independiente y yo reconfirmé los puntos señalados; no se probó nada en ejecución.*
