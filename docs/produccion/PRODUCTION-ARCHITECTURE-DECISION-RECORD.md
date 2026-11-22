# Registro de decisiones de arquitectura (ADR) — Producción

> **TODAS LAS DECISIONES SON PROPUESTAS. Ninguna está aprobada ni implementada.** Aprobar un ADR no autoriza accesos a producción, migraciones ni despliegues (cada uno requiere autorización específica).
> Fecha: 2026-10-13. Contexto: [auditoría de opciones](PRODUCTION-ARCHITECTURE-OPTIONS-AUDIT.md) · [integración](PRODUCTION-DECOUPLED-INVENTORY-INTEGRATION.md) · [datos](PRODUCTION-DATA-MODEL-REVISED.md) · [roadmap](PRODUCTION-ROADMAP-DECOUPLED-REVISED.md). Decisiones previas aún abiertas: [PRODUCTION-FINAL-DECISIONS.md](PRODUCTION-FINAL-DECISIONS.md) (D-01…D-14).

## 1. Decisiones recomendadas

| ID | Decisión | Estado | Alternativas descartadas / diferidas | Motivo principal |
|---|---|---|---|---|
| ADR-01 | **Arquitectura A**: Producción con modelos, tablas, servicios y reglas propios; consumo e ingreso mediante el servicio oficial de inventario dentro de su propia transacción, a través de un adaptador `StockGateway` | Propuesta | **B1** (inventario operativo propio): descartada. **B2/A+Q** (aprobación previa al ingreso): diferida a V1.5 | B1 no evita `RecordMovementTx` y duplica saldos; el control de calidad se resuelve con un estado, no con un saldo (§5 de la auditoría) |
| ADR-02 | **Un solo saldo y un solo Kardex** (los de Inventario); Producción no escribe tablas de stock | Propuesta | Saldos o kardex propios en Producción | Evita dos fuentes de verdad |
| ADR-03 | Origen del movimiento con **`source_type` + `source_id` genéricos** en `tenant_stock_movements` (reemplaza la propuesta anterior `production_entry_id`) | Propuesta (supera ADR/propuestas previas) | `production_entry_id` específico; vincular solo por `reference` texto | Mantiene a Inventario ajeno a Producción; `reference` es frágil (varchar 100) |
| ADR-04 | **Transacción única** por registro y por anulación; **sin cola, outbox ni saga** en V1 | Propuesta | Procesamiento asíncrono / consistencia eventual | Misma BD; rollback total garantizado [V-código]; la consistencia eventual permitiría vender stock no descontado |
| ADR-05 | Idempotencia con `idempotency_key` + hash de payload, **`UNIQUE(user_id, key)`**, replay temprano, tras el lock y por carrera (reemplaza `UNIQUE(orden, clave)` previo) | Propuesta | Helper genérico reutilizado de Ventas; clave por orden | Replica el patrón v150 verificado; el helper de ventas está atado a `TenantSale` |
| ADR-06 | Número de orden `OP-<id>` derivado del ID | Propuesta (reemplaza contador/`docseries`) | Contador propio; `docseries.ReserveNext` | Menos tablas y locks; no se exige correlativo sin huecos; `docseries` exige serie configurada por sucursal |
| ADR-07 | Receta de un nivel con **`output_quantity`** (receta por lote) y **snapshot** en la orden que guarda cantidad comercial, **factor** y cantidad base | Propuesta (precisa el snapshot previo) | Versionar recetas; re-resolver el factor en cada registro | La historia se preserva con snapshot; el factor congelado hace reproducible la fabricación |
| ADR-08 | Estados `Borrador → En proceso → Terminada` y `Anulada`; **sin «Reabrir»**; producido = buenas; defectuosas informativas; excedente autorizado aumentando lo planificado | Propuesta (mantiene decisiones T-05/T-06/T-13) | Reabrir órdenes; defectuosas que ingresan automáticamente | Menos transiciones y costos cerrados intactos |
| ADR-09 | Costos: **costo de gestión por registro** (materiales reales + mano de obra y otros del registro) ÷ buenas; **promedio por (producto, sucursal) en modo sombra**; los tres conceptos separados (gestión / valoración de inventario / costo de ventas); **sin tocar** reportes de utilidad | Propuesta | Promedio global por producto; último precio de compra; costo estándar | Reproducibilidad sin cambiar reglas comerciales existentes |
| ADR-10 | Trazabilidad en **columnas dentro del tenant y de la misma transacción**; `WriteAuditLog` central solo complementario desde el handler | Propuesta (corrige «auditar con WriteAuditLog en cada acción») | Auditoría solo central | `WriteAuditLog` es central, best-effort y no transaccional [V-código] |
| ADR-11 | **Módulo apagado por defecto**: `manufacturing` a `Implemented:true` solo al liberar, sin asignarlo a planes, sin migraciones tipo «otorgar a todos»; documentar que `RequireModule` depende del JWT (re-login) | Propuesta (corrige «`Implemented:false` lo apaga») | Confiar en `Implemented:false` | `Implemented` solo oculta en `/api/session/modules`, no bloquea el backend [V-código] |
| ADR-12 | Anular un registro cuyo terminado ya se vendió: **bloqueado** en V1 con mensaje; **«ajuste de consumo»** (mover solo la diferencia de insumos) queda para V1.5 | Propuesta | Anulación parcial; permitir stock negativo | Integridad del stock; el hueco real es corregir solo el consumo |
| ADR-13 | **Control de calidad / pendiente de aprobación**: no en V1; `entries.status` es `varchar` y admite `pending_approval/rejected` sin cambio de esquema; insumos al registrar, terminado al aprobar | Propuesta (diferida) | Incluirlo en V1; stock intermedio físico | Costo y complejidad sin demanda demostrada |
| ADR-14 | **Variantes fuera de V1**: el sistema rechaza productos con variantes como insumo o terminado | Propuesta | Orden por variante; matriz de tallas/colores | `RecordMovementTx` solo actualiza la fila de presentación; `StockSummary` las ignora; sin evidencia de demanda |
| ADR-15 | **Sin fecha retroactiva en el Kardex**; la fecha de negocio vive en `entries.entry_date` | Propuesta | `OccurredAt` en `MovementInput` | El Kardex ordena por `created_at`; una fecha pasada reordenaría el historial y desincronizaría `Balance` |
| ADR-16 | **Endurecimiento del núcleo** (redondeo, bloqueo, UNIQUE, costo en sombra) como entregas independientes con flag, medición previa y sin deduplicación automática | Propuesta | Producción con su propio control de concurrencia | Los riesgos son del núcleo y afectan a cualquier arquitectura |
| ADR-17 | Frontend: copiar el **patrón** de `InventoryDocumentPage`, reutilizar componentes UI compartidos, no importar páginas ni selectores acoplados a POS | Propuesta | Reutilizar `ProductPickerModal` o páginas de Compras/Ventas | Evita dependencias innecesarias |

## 2. Qué decisiones anteriores quedan superadas
| Documento | Afirmación | Queda superada por |
|---|---|---|
| PRODUCTION-FINAL-CRITICAL-REVIEW / SCOPE-CANDIDATE | `production_entry_id` en movimientos | ADR-03 |
| PRODUCTION-FINAL-DECISIONS T-08 | Idempotencia `UNIQUE(orden, clave)` | ADR-05 |
| PRODUCTION-FINAL-DECISIONS T-11 / SCOPE-CANDIDATE R15 | Contador propio por sucursal | ADR-06 |
| PRODUCTION-CURRENT-ERP-GAP-ANALYSIS, ROADMAP | `Implemented:false` mantiene el módulo apagado | ADR-11 |
| PRODUCTION-MODULE-PROPOSAL §8 / SCOPE-CANDIDATE §5 | Modelo de 6 entidades sin `output_quantity` ni factor en snapshot | PRODUCTION-DATA-MODEL-REVISED |
| Documentos previos | `WriteAuditLog` en cada acción de estado | ADR-10 |
| PRODUCTION-IMPLEMENTATION-ROADMAP-REVISED | Fases «pistas T/C/P» | PRODUCTION-ROADMAP-DECOUPLED-REVISED (11 etapas); el contenido técnico de T/C se conserva en la etapa 3 |
Los documentos antiguos se conservan como evidencia; se agrega una «Errata» al inicio de los afectados.

## 3. Asuntos pendientes de su aprobación (no resueltos por evidencia)

| # | Asunto | Qué necesito | Referencia |
|---|---|---|---|
| P-1 | Aprobar la **Alternativa A con adaptador** como arquitectura de V1 | Sí/No (o pedir B2 en V1) | ADR-01 |
| P-2 | Aprobar los **cambios aditivos al contrato de Inventario** (I-1…I-6) | Sí/No; son cambios en el núcleo compartido | Integración §2 |
| P-3 | Aprobar el **modelo de datos** (6 tablas + 2 columnas) | Sí/No/ajustes | Modelo de datos |
| P-4 | **Medición de solo lectura** (muestra, usuario, ventana) | Autorización específica (D-01) | Roadmap etapa 2 |
| P-5 | **Endurecimiento del núcleo** por etapas con flag y canary | Autorización por cada release (D-02) | Roadmap etapa 3 |
| P-6 | **Costos:** D-03 (¿costo de ventas?), D-04 (base sin IGV), D-05 (merma/defectuosas), D-06 (código SUNAT), traslados con costo | Decisiones de negocio y contables | FINAL-DECISIONS |
| P-7 | **Alcance V1** (D-07, D-08, D-10) y confirmación de que QC, variantes, reservas y WIP quedan fuera | Confirmar | Scope candidate |
| P-8 | **Piloto y planes** (D-11, D-12) | Cliente, condiciones, planes | Roadmap 10–11 |
| P-9 | **Ajuste de consumo** (ADR-12) en V1.5 | Confirmar prioridad | Integración §5-6 |
| P-10 | Limpieza de seguridad del servidor antiguo (gestionada fuera de este repositorio) | Acción suya | Roadmap etapa 1 |

## 4. Límites de este registro
No se midió producción ni rendimiento real; las puntuaciones de la matriz son juicio razonado sobre código leído; no se revisó a fondo `frontend_restaurant_tenant`; no se confirmó si el refresh token de 30 días reconstruye los módulos del JWT; el código SUNAT de `PRODUCTION_IN` y el tratamiento contable siguen pendientes de contador.
