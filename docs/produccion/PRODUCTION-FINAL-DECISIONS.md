# Decisiones del módulo de Producción — Bendey ERP

> **Errata (auditoría 3, 2026-10-13)** — T-08 (idempotencia por orden) y T-11 (contador propio) quedan superadas por ADR-05 y ADR-06. Se añaden las decisiones de arquitectura ADR-01…ADR-17 y los asuntos P-1…P-10.
> Detalle y evidencia: [PRODUCTION-ARCHITECTURE-DECISION-RECORD.md](PRODUCTION-ARCHITECTURE-DECISION-RECORD.md) §2 y [PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md](PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md) §2. El texto original se conserva.

> Estado: **PARA SU APROBACIÓN. Nada de esto está aprobado ni implementado.** Aprobar una decisión **no** autoriza modificar servidores, desplegar ni habilitar el módulo en ningún tenant: cada acción en producción necesita su autorización expresa aparte (ver D-14).
> Contexto: [Revisión crítica](PRODUCTION-FINAL-CRITICAL-REVIEW.md) · [Alcance candidato](PRODUCTION-APPROVED-SCOPE-CANDIDATE.md) · [Roadmap revisado](PRODUCTION-IMPLEMENTATION-ROADMAP-REVISED.md).

## Cómo leer este documento
- **Parte A — Resueltas por evidencia técnica (T-01…T-13):** las resolví verificando código o documentación; solo necesito que las **confirme o las objete**. No son preguntas abiertas.
- **Parte B — Requieren su decisión (D-01…D-14):** negocio, costos, tratamiento contable, alcance funcional o autorización de accesos/despliegues. Cada una tiene alternativas, riesgos, recomendación y consecuencias de aprobar o postergar.

---

## Parte A — Resueltas por evidencia técnica (confirmar u objetar)

| ID | Decisión | Evidencia | Resolución |
|---|---|---|---|
| T-01 | ¿Es real el riesgo de pérdida de actualización de saldo? | `RecordMovementTx` lee sin lock y escribe saldo absoluto (`inventory_service.go:155-209`); ningún `FOR UPDATE` sobre tablas de stock [V-código] | Sí, es real. Se corrige con *ensure-row* + `FOR UPDATE` (o `UPDATE quantity=quantity±?`), orden de bloqueo y reintento ante deadlock, detrás de un flag. Solo `FOR UPDATE` **no** basta |
| T-02 | ¿UNIQUE en stock? | No existe en `tenant_product_stocks` ni `tenant_product_presentation_stocks` (`migrations.go:1387-1405`; v107) [V-código] | Sí, pero **solo tras medir duplicados**; la migración nunca deduplica sola: aborta en el tenant con duplicados y deja informe |
| T-03 | Granularidad del costo promedio | Stock es por sucursal; traslados entran con costo 0; `WeightedAverageUnitCosts` es por sucursal e ignora presentaciones [V-código] | **Por (producto, sucursal)**, persistido con el saldo (no global por producto) |
| T-04 | Momento de mover stock | Comparación de alternativas en la propuesta §6.1 | **Por registro de producción** (consumo real + ingreso del terminado en una transacción) |
| T-05 | ¿Qué es «producido»? | Ambigüedad del Caso D (Anexo A #3) | **Σ unidades buenas** de registros vigentes. Defectuosas informativas |
| T-06 | ¿Reabrir órdenes terminadas? | Complica costos ya cerrados | **No.** Estados: Borrador, En proceso, Terminada, Anulada. Correcciones: anular registro y volver a registrar; producir más después de cerrar: orden nueva |
| T-07 | Mano de obra | Costo irreproducible si se edita tras postear (Anexo A #1) | **Campo del registro** (opcional), no de la orden. Presupuesto de orden = solo informativo |
| T-08 | Idempotencia | Patrón de ventas: UNIQUE (user, key) | Clave en el registro: se revisa **antes** que el estado; UNIQUE `(orden, clave)`; mismo payload → devuelve el registro existente; payload distinto → 409 |
| T-09 | Redondeo/epsilon | 3,141 de 14,000 combinaciones rechazadas falsamente (Anexo B §E) | Redondear cantidad y saldo a 3 decimales antes de comparar; receta/snapshot en decimal(15,6); consumo calculado sobre el total del registro |
| T-10 | Fórmula de desperdicio | RES usa `/(1−%)`, PROP `×(1+%)` | `% sobre cantidad neta`; `bruto = neto × (1+%)` |
| T-11 | Numeración de órdenes | `docseries.ReserveNext` exige serie por sucursal ya configurada | Contador propio con UNIQUE `(sucursal, número)`, sin depender de series |
| T-12 | `AllowManual` de `PRODUCTION_OUT` | Desconozco si algún tenant lo usa en egresos manuales | **No cambiarlo en V1** (evita regresión); revisar tras medición |
| T-13 | Excedentes de producción | — | Bloqueados por defecto; se autorizan **aumentando la cantidad planificada** (permiso `production.manage`, auditado) |

Confirmaciones complementarias: variantes fuera de V1 (ver D-10) y movimientos de Producción se enlazan por `production_entry_id` (columna nullable sin FK dura), no solo por texto de `reference`.

---

## Parte B — Decisiones que requieren su intervención

### D-01 — ¿Autoriza medir producción en modo solo lectura?
- **Pregunta:** ¿Puedo ejecutar las consultas de solo lectura del Anexo B (duplicados de stock, diferencias saldo/kardex, movimientos con costo 0, stock negativo, `sql_mode`, aislamiento) en una **muestra** de tenants de producción, con un usuario de solo lectura creado para ello?
- **Alternativas:** (a) Muestra de 10-20 tenants (grandes, medianos, gyl, algunos pequeños); (b) toda la flota; (c) no medir.
- **Ventajas/desventajas:** (a) rápido y suficiente para dimensionar; (b) dato completo, pero carga en BD y más superficie; (c) sin riesgo de acceso, pero endurecer a ciegas.
- **Riesgos:** acceso a datos reales (privacidad); consultas pesadas (la 3c) sobre el pool pequeño; credenciales que deben quedar fuera de chat y repo.
- **Recomendación:** (a), usando un usuario de solo lectura ya existente en el servidor de BD, con las consultas listadas y horario valle. Sin leer `.env`.
- **Si aprueba:** puedo dimensionar duplicados y decidir el endurecimiento con datos. **Si pospone:** el endurecimiento (T-01/T-02) no puede liberarse; Producción no se habilita en producción.

### D-02 — ¿Aprueba endurecer el inventario compartido (fuera de Producción)?
- **Pregunta:** ¿Autoriza el plan de endurecimiento transversal (redondeo → ensure-row + lock + orden + reintento → UNIQUE) como entregas **independientes del módulo**, con flag y reversión?
- **Alternativas:** (a) Endurecer antes de Producción, por etapas (recomendada); (b) endurecer solo dentro de un nuevo servicio de Producción (duplica la lógica y deja el núcleo expuesto); (c) no endurecer y aceptar el riesgo.
- **Ventajas/desventajas:** (a) corrige un riesgo que hoy ya afecta a ventas/compras; más trabajo previo y despliegue a toda la flota. (b) menor alcance pero crea dos escritores de stock (contra el diseño actual). (c) más rápido; el módulo amplifica el riesgo (consumo de N insumos simultáneos).
- **Riesgos:** regresión en Compras/Ventas/Traslados; deadlocks nuevos al bloquear; pool pequeño por locks más largos. Mitigación: pruebas con MySQL, canary, flag, reversión.
- **Recomendación:** (a), etapas T1-T4 del roadmap, cada una con su propia autorización de despliegue.
- **Si aprueba:** se planifica T1 (redondeo y tests) como primera entrega pequeña. **Si pospone:** Producción solo puede probarse en local.

### D-03 — ¿El costo de producción debe alimentar el costo de venta/utilidad?
- **Contexto:** hoy la venta toma `PurchasePrice` del producto como costo (`sale_service_calc.go:84-99`); un producto fabricado sin compras tendría utilidad inflada [V-código].
- **Alternativas:** (a) **No en V1:** costo de producción solo en reportes de Producción; (b) ventas usan el promedio por sucursal si existe, si no `PurchasePrice`; (c) al cerrar cada registro, actualizar `PurchasePrice` del terminado con el costo unitario de producción.
- **Ventajas/desventajas:** (a) cero cambios a reportes existentes, pero la utilidad de productos fabricados queda incompleta; (b) correcto y coherente, pero cambia las utilidades nuevas para todos los productos con promedio y exige regresión de reportes; (c) simple, pero mezcla «último precio de compra» con «costo de fabricación» (justo lo que se quiere evitar) y se sobrescribe con compras.
- **Riesgos:** (b) discrepancias con lo reportado antes; (c) pérdida del significado de `PurchasePrice`.
- **Recomendación:** (a) para V1; planificar (b) como entrega opcional posterior, solo para productos con origen «fabricado».
- **Si aprueba (a):** V1 se entrega más rápido y seguro; los clientes ven el costo en Producción. **Si pospone:** no bloquea V1; bloquea la promesa comercial de «rentabilidad de lo fabricado».

### D-04 — Base de costo de los insumos: ¿sin IGV?
- **Contexto:** el costo de compra que llega al kardex y al producto es el valor tecleado, **posiblemente con IGV incluido** si el usuario marcó «incluye IGV» (`purchase_service.go:181-184, 317, 364`) [V-código].
- **Alternativas:** (a) Normalizar en el flujo de compras a futuro (costo sin IGV al kardex y al producto); (b) normalizar solo el costo que alimenta el promedio nuevo; (c) no normalizar y advertir en pantalla.
- **Ventajas/desventajas:** (a) la más correcta, pero cambia el significado de datos de compra existentes y puede afectar otros reportes; (b) aísla el cambio, pero kardex y promedio quedan con bases distintas; (c) cero cambios, el costo puede salir ~18 % inflado.
- **Riesgos:** (a) regresión en Compras; (b) inconsistencia entre reportes.
- **Recomendación:** primero **medir** cuántas líneas de compra reales usan «incluye IGV» (consulta 7 del Anexo B) y qué hace la UI con moneda extranjera; luego (b). Validar con el contador.
- **Si aprueba:** se diseña la normalización. **Si pospone:** V1 muestra el costo con advertencia explícita de base.

### D-05 — Tratamiento de mermas y defectuosas en el costo; alcance del «costo»
- **Pregunta:** ¿Se acepta que en V1 el costo de materiales de unidades defectuosas se **absorba** en las unidades buenas (con reporte informativo de la pérdida), y que el costo calculado sea un **costo de gestión**, no un costo contable?
- **Alternativas:** (a) Absorber todo en las buenas (recomendada); (b) separar merma anormal como gasto del período (requiere criterio contable y asiento); (c) configurable por empresa.
- **Ventajas/desventajas:** (a) simple y transparente; puede diferir de la práctica contable del cliente. (b) alineado con NIC 2 en teoría [Ext, de segunda mano]; exige contabilidad integrada que Bendey no tiene. (c) más complejidad.
- **Riesgos:** que el cliente use el costo de gestión como contable sin validación.
- **Recomendación:** (a) en V1, con rótulo «costo de gestión» y reporte de «pérdida vs receta» separado en *defectuosas* y *exceso de consumo*. Validar con el contador del piloto.
- **Si aprueba:** se fija el cálculo. **Si pospone:** no bloquea V1.

### D-06 — Código SUNAT del ingreso de producto terminado (`PRODUCTION_IN`)
- **Pregunta al contador:** ¿qué código de la Tabla 12 corresponde al ingreso de producto terminado de producción propia en el Registro de Inventario Permanente en Unidades Físicas? (No está en el repo y no lo asumo.)
- **Alternativas:** (a) crear `PRODUCTION_IN` con `99` (otros) provisional; (b) esperar la respuesta antes de crear el tipo.
- **Recomendación:** (a); el código se actualiza luego con una migración puntual. No bloquea el diseño interno.
- **Consecuencias:** aprobar (a) permite avanzar; si la respuesta cambia el código, se corrige con una migración de datos simple. Postergar puede bloquear la entrega del tipo de operación.

### D-07 — Defectuosas: ¿informativas o ingreso como «segunda calidad»?
- **Alternativas:** (a) informativas, sin stock (V1); (b) ingreso opcional a un producto «segunda» (V1.5).
- **Ventajas/desventajas:** (a) evita que lo defectuoso se venda por error; (b) recupera valor, añade producto, flujo y costo prorrateado.
- **Recomendación:** (a). **Evidencia:** CONSTRUMARKET nunca registró defectuosos (`imperfect=0`) [V-datos]; es una hipótesis.
- **Si aprueba:** V1 más simple. **Si pospone:** nada se rompe; se vuelve a evaluar con un cliente que lo pida.

### D-08 — Mano de obra: ¿monto manual por registro o tarifa por hora?
- **Alternativas:** (a) monto manual por registro (V1); (b) tarifa × horas con trabajadores (V1.5+).
- **Recomendación:** (a). CONSTRUMARKET no usó trabajadores del módulo [V-datos]; la mayoría de pymes calcula la mano de obra fuera del ERP.
- **Consecuencias:** (a) costo unitario completo con una cifra; (b) añade trabajadores, tiempos y tarifas.

### D-09 — Corrección del reporte de utilidad con `SaleUnit` (`ProfitDetail`)
- **Contexto:** bug confirmado: mezcla costo por unidad base con cantidad comercial (`sale_service.go:2095`); ejemplo: 2 sacos a S/400, factor 100, costo S/3/kg → muestra utilidad 794, correcta 200 [V-código].
- **Alternativas:** (a) corregir dentro de este proyecto; (b) corregir como tarea independiente; (c) no corregir.
- **Recomendación:** (b), como corrección de reportes ya existentes, con prueba de regresión, **antes** de mostrar rentabilidad de lo fabricado. No es prerrequisito de V1 si D-03 = (a).
- **Consecuencias:** corregir cambia utilidades mostradas para ventas con unidades de venta (para bien); postergar deja el error vigente.

### D-10 — Variantes de producto en V1
- **Pregunta:** ¿Confirma que V1 **rechaza** productos con variantes (como insumo o terminado) y que se resuelven con productos separados?
- **Alternativas:** (a) fuera de V1 (recomendada); (b) incluir orden por variante.
- **Ventajas/desventajas:** (a) elimina el problema de reconciliación stock de presentación/producto (`RecordMovementTx` solo actualiza la fila de presentación) y el costo por variante inexistente; (b) cubre calzado/ropa, pero la evidencia y el modelo de costos no están listos.
- **Condición para V1.5:** un cliente real de variantes + verificación de la reconciliación de stock + costo por variante.
- **Si aprueba (a):** V1 sale limpio y sin riesgo de datos. **Si pospone:** puede ampliarse con (b) tras verificación.

### D-11 — Piloto y datos a cargar
- **Pregunta:** ¿Qué cliente será el piloto? CONSTRUMARKET está en baja del VPS antiguo, redujo su actividad y tiene datos débiles (compras casi sin registrar); no lo asumo.
- **Alternativas:** (a) CONSTRUMARKET con carga previa (compras + stock inicial por conteo físico + 8 recetas); (b) otro cliente manufacturero activo; (c) piloto interno con datos de prueba reales (recetas de CONSTRUMARKET en un tenant de laboratorio).
- **Recomendación:** (c) primero, (b) o (a) después, cuando tenga un cliente que registre compras. Criterios de éxito del piloto: ver documento de alcance.
- **Consecuencias:** sin piloto real, V1 no se valida contra el uso.

### D-12 — Planes, módulo y roles por defecto
- **Pregunta:** ¿En qué plan(es) se incluye `manufacturing` y qué roles reciben permisos `production.*`?
- **Recomendación técnica:** `manufacturing` con `Requires: products,inventory`; asignación desde el panel central; backfill solo a rol Administrador; los demás roles manualmente. Precio/plan es decisión comercial suya.
- **Consecuencias:** postergar no bloquea el desarrollo; bloquea el lanzamiento comercial.

### D-13 — Importación de recetas del legacy
- **Alternativas:** (a) carga manual de las 8 recetas activas; (b) script puntual; (c) importador general en el sistema.
- **Recomendación:** (a)/(b) — solo CONSTRUMARKET tiene datos conocidos; no sé si otros tenants del legacy usan producción [NV]. (c) solo si aparecen más.

### D-14 — Autorización de accesos y despliegues
- **Principio:** cualquier acceso a producción (consultas, túneles, usuarios de BD) y cualquier despliegue o migración requiere su autorización **expresa y específica**. Aprobar este documento no la otorga.
- **Pendientes de seguridad heredados:** limpieza de accesos del servidor antiguo (gestionada fuera de este repositorio) y respaldo de la BD del cliente antes de la baja.

---

## Resumen por urgencia
| Bloquea… | Decisiones |
|---|---|
| El **inicio de cualquier cambio** | D-01 (medir), D-02 (endurecer) |
| El **diseño de costos** (no el código base) | D-03, D-04, D-05 |
| La **entrega de tipos de operación** | D-06 (con fallback `99`) |
| El **alcance de V1** | D-07, D-08, D-10 |
| El **lanzamiento comercial** | D-11, D-12 |
| Nada técnico | D-09, D-13 (D-14 es transversal) |
