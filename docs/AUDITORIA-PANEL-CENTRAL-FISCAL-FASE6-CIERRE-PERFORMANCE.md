# Fase 6 — Cierre técnico de performance y resolución de propuestas pendientes

**Fecha:** 2026-09-20
**Naturaleza de esta fase:** 100% análisis y verificación. **Cero cambios de código** (confirmado con `git status` idéntico antes/después en los 3 repos).

---

## Resumen de decisiones

| Propuesta | Decisión |
|---|---|
| A) Índices redundantes | **IMPLEMENTAR EN FASE FUTURA** (con autorización) — solo para UN índice duplicado exacto confirmado. El resto de índices auditados NO son redundantes (uno de ellos, `IDX_FISCAL_CREATED`, se sospechaba redundante y el EXPLAIN lo refutó). |
| B) Evolución de `fiscal_tenant_metrics` | **NO IMPLEMENTAR** — complejidad y nuevos modos de falla no justificados frente a queries de 38-125ms ya aceptables. |
| C) Semántica temporal "hoy" vs "24h" | **NO IMPLEMENTAR** (nada que corregir en código) — cada indicador está correctamente etiquetado y su implementación coincide con su etiqueta. Existe una pregunta de UX/producto (no de bug) documentada para decisión futura. |

---

## A) Índices — auditoría completa con dependencias reales

Se auditaron TODAS las queries reales contra `fiscal_documents` en todo `src/` (no solo `FiscalDocumentRepository.php`) — 4 archivos concentran el 100% de las queries: `FiscalDocumentRepository.php` (11 métodos), `FiscalOperationsService.php` (2 SQL nativas), `FiscalHealthService.php` (1 QueryBuilder vía EntityManager), `OutboundEmailLogRepository.php` (1 JOIN externo). Ningún Command ni Provider ejecuta SQL adicional contra esta tabla.

### Tabla de dependencias por índice

| Índice | Columnas | Queries reales que lo usan | ¿Cubierto por otro índice? |
|---|---|---|---|
| PRIMARY | id | Identidad de entidad, ORDER BY en casi todos los métodos | No — imprescindible |
| UNIQ_FISCAL_DOC_UUID | document_uuid | ~15 `findOneBy(documentUuid)` en 10 archivos + JOIN de `OutboundEmailLogRepository` | No — imprescindible |
| UNIQ_FISCAL_FINGERPRINT | fiscal_fingerprint | `FiscalDocumentService.php:86,106,241` (idempotencia de emisión) | No — imprescindible, además es constraint de unicidad |
| **IDX_FISCAL_TENANT_STATUS** | tenant_slug, status | `createFilteredQuery()` → `findByFilters`/`countByFilters` (`FiscalController.php:208,221`; `FiscalBulkActionService.php:80`) | **Sí — duplicado EXACTO de `idx_fiscal_doc_tenant_status`** (ver EXPLAIN abajo) |
| IDX_FISCAL_SALE | tenant_id, sale_id | Diseñado para `findByTenantSale()` — **método no llamado desde ningún lugar de `src/`** (confirmado por grep) | No hay otro índice que cubra exactamente esta combinación — pero su único consumidor diseñado está muerto |
| **IDX_FISCAL_CREATED** | created_at | `countByFilters`/`countByStatus` sin tenant (usado por `globalStats()`, disparado cada 30s desde `/operations/summary`); listado sin filtro de tenant | **Se sospechaba redundante con `idx_fiscal_doc_cursor` — REFUTADO por EXPLAIN** (ver abajo) |
| idx_fiscal_doc_tenant_status | tenant_slug, status | Idéntica a `IDX_FISCAL_TENANT_STATUS` — mismas líneas | **Sí — duplicado exacto**, creado por accidente en `migrations/Version20260525000000.php:21` sin notar que `IDX_FISCAL_TENANT_STATUS` ya existía desde `Version20260523000000.php:64` |
| idx_fiscal_doc_series_number | series, number | `createFilteredQuery()` L267+270, búsqueda exacta serie+número | No — único índice para esta combinación |
| idx_fiscal_doc_cursor | created_at, id | `applyCursor()` + `ORDER BY created_at DESC, id DESC` presente en el 100% de llamadas a `findByFilters()` | No — resuelve el patrón de paginación del endpoint de listado más usado |
| idx_fiscal_doc_tenant_id | tenant_id, status | `createFilteredQuery()` cuando el caller HTTP pasa `tenant_id` (nunca desde procesos internos — solo vía API) | No — `tenant_id` y `tenant_slug` son columnas físicamente distintas, no intercambiables |
| idx_fiscal_doc_tenant_slug_status | tenant_slug, status, created_at | Patrón dominante: `tenant_slug`+`status`+`ORDER BY created_at` (el más frecuente del sistema) | No — es el índice "ganador" para el patrón con ORDER BY (ver EXPLAIN) |

### Evidencia EXPLAIN (contra producción, 38 404 filas)

**Caso 1 — confirma que `IDX_FISCAL_TENANT_STATUS` e `idx_fiscal_doc_tenant_status` son intercambiables (duplicados reales):**
```sql
EXPLAIN SELECT tenant_slug, COUNT(*) FROM fiscal_documents
WHERE status IN ('pending','queued','retrying') AND tenant_slug IS NOT NULL GROUP BY tenant_slug;
-- possible_keys: IDX_FISCAL_TENANT_STATUS, idx_fiscal_doc_tenant_status, idx_fiscal_doc_tenant_slug_status
-- key: IDX_FISCAL_TENANT_STATUS
```
MySQL considera los 3 como candidatos y elige uno de los 2-columna — el otro 2-columna (idéntico) nunca aporta nada que el elegido no cubra.

**Caso 2 — con `tenant_slug`+`status`+`ORDER BY created_at DESC, id DESC` (patrón real de `FiscalController::list()`), MySQL elige el índice de 3 columnas, no los de 2:**
```sql
EXPLAIN SELECT id FROM fiscal_documents WHERE document_type NOT IN ('00','NV')
  AND tenant_slug='snjempresa' AND status='error' ORDER BY created_at DESC, id DESC LIMIT 51;
-- key: idx_fiscal_doc_tenant_slug_status (524 bytes), Backward index scan, sin filesort
```

**Caso 3 — CRÍTICO, refuta la sospecha inicial sobre `IDX_FISCAL_CREATED`:**
```sql
EXPLAIN SELECT id FROM fiscal_documents WHERE document_type NOT IN ('00','NV')
  ORDER BY created_at DESC, id DESC LIMIT 51;
-- key: IDX_FISCAL_CREATED (5 bytes) -- NO eligió idx_fiscal_doc_cursor (created_at, id)

EXPLAIN SELECT COUNT(id) FROM fiscal_documents WHERE document_type NOT IN ('00','NV') AND created_at >= CURDATE();
-- key: IDX_FISCAL_CREATED
```
En los 3 casos probados donde `idx_fiscal_doc_cursor` también era candidato (`possible_keys` lo incluye), el optimizador prefirió consistentemente `IDX_FISCAL_CREATED` (índice más angosto, `key_len=5` vs `key_len=9`) — **contradice la hipótesis inicial de que era redundante**. Se mantiene tal cual: es exactamente el tipo de conclusión que la Fase 5/6 pedían no asumir sin medir.

**Caso 4 — sin `ORDER BY`, el 2-columna sí sigue siendo preferido sobre el de 3 columnas** (ej. `countByStatus` con tenant, sin orden):
```sql
EXPLAIN SELECT status, COUNT(id) FROM fiscal_documents WHERE document_type NOT IN ('00','NV') AND tenant_slug='snjempresa' GROUP BY status;
-- key: IDX_FISCAL_TENANT_STATUS (402 bytes) -- no eligió idx_fiscal_doc_tenant_slug_status (524 bytes)
```
Esto confirma que el índice de 3 columnas **no** vuelve completamente inútiles a los 2 de columnas duplicadas para TODOS los patrones — solo para los que incluyen el `ORDER BY created_at`. Como los dos de 2 columnas son idénticos ENTRE SÍ, eliminar uno de ellos (dejando el otro + el de 3 columnas) preserva ambos planes de ejecución óptimos observados.

### Decisión final por índice

- ✅ **Mantener sin cambios:** PRIMARY, UNIQ_FISCAL_DOC_UUID, UNIQ_FISCAL_FINGERPRINT, `idx_fiscal_doc_series_number`, `idx_fiscal_doc_cursor`, `idx_fiscal_doc_tenant_id`, `idx_fiscal_doc_tenant_slug_status`, **`IDX_FISCAL_CREATED`** (sospecha refutada por EXPLAIN).
- 🟡 **Eliminar UNO de los dos duplicados exactos** — `idx_fiscal_doc_tenant_status` (el agregado por accidente en `Version20260525000000`, más nuevo; se conserva `IDX_FISCAL_TENANT_STATUS`, el original de la creación de tabla). **IMPLEMENTAR EN FASE FUTURA**, con autorización explícita — ver migración propuesta abajo.
- 🟡 **`IDX_FISCAL_SALE`** — su consumidor diseñado (`findByTenantSale()`) es código muerto, pero no hay evidencia de uso real vía `performance_schema` (el usuario de aplicación `tukifacuser` no tiene permiso `SELECT` sobre `performance_schema.table_io_waits_summary_by_index_usage` — intentado y confirmado denegado, no se escaló el privilegio). **NO IMPLEMENTAR** sin esa evidencia adicional — requiere acceso de un usuario con más privilegios o habilitar slow query log por un período antes de decidir.

### Migración propuesta (NO aplicada, solo para tu revisión)

```php
<?php
declare(strict_types=1);
namespace DoctrineMigrations;
use Doctrine\DBAL\Schema\Schema;
use Doctrine\Migrations\AbstractMigration;

final class Version20260921000000 extends AbstractMigration
{
    public function getDescription(): string
    {
        return 'Elimina idx_fiscal_doc_tenant_status: duplicado exacto de IDX_FISCAL_TENANT_STATUS (tenant_slug, status), agregado por accidente en Version20260525000000 sin notar que ya existía desde Version20260523000000';
    }

    public function up(Schema $schema): void
    {
        $this->addSql('DROP INDEX idx_fiscal_doc_tenant_status ON fiscal_documents');
    }

    public function down(Schema $schema): void
    {
        $this->addSql('CREATE INDEX idx_fiscal_doc_tenant_status ON fiscal_documents (tenant_slug, status)');
    }
}
```

**Impacto esperado:** reduce el costo de mantenimiento de índice en cada INSERT/UPDATE de `fiscal_documents` (una escritura de índice B-tree menos por cada operación); cero impacto en lecturas (todo plan que usaba `idx_fiscal_doc_tenant_status` puede usar `IDX_FISCAL_TENANT_STATUS`, columnas idénticas). **Riesgo:** bajo — `DROP INDEX` en MySQL 8 sobre InnoDB es una operación `ALGORITHM=INPLACE` (no bloquea lecturas/escrituras largo tiempo, aunque toma un lock metadata breve). **Estrategia de rollback:** el método `down()` recrea el índice exacto; `CREATE INDEX` también es `INPLACE` y no bloqueante en operación normal, pero sí consume tiempo/IO proporcional al tamaño de la tabla (38k filas hoy: segundos, no minutos). **Despliegue recomendado:** aplicar en horario de bajo tráfico igual que cualquier migración, aunque el riesgo es bajo dado el tamaño actual de la tabla.

**No se ejecutó esta migración.** Queda para tu autorización explícita.

---

## B) `fiscal_tenant_metrics` — análisis costo/beneficio de evolucionarla

### Qué almacena hoy (confirmado leyendo `FiscalMetricsService.php` completo)

Contadores de EVENTOS acumulados por día (`periodDate`, bucket calendario), incrementados de forma async desde el drenado de la cola de auditoría: `documentsEmitted` (se incrementa en 3 eventos distintos: `fiscal_emit_success`, `fiscal_emit_failed`, `fiscal_document_queued` — un solo documento puede incrementarlo más de una vez), `documentsAccepted`, `errors` (por evento `fiscal_emit_failed`, no por documento-en-error-ahora), `retries`, `avgDurationMs` (promedio incremental), `successRate` (derivado). **No existe ningún campo de estado actual** (pending, last_emit_at).

### Qué pasaría con cada escenario si se intentara agregar "pending_count"/"last_emit_at"

| Escenario | Efecto sobre un contador de estado en una tabla bucketeada por día |
|---|---|
| Emisión nueva | pending_count += 1 al crear, pero ¿en qué fila de período? El documento puede quedar pending y cruzar la medianoche |
| Accepted | pending_count -= 1 — requiere ENCONTRAR la fila correcta (¿la del día de creación? ¿la de hoy?) y decrementar atómicamente |
| Rejected/business | mismo problema — decremento, pero el documento nunca estuvo en period "hoy" necesariamente |
| Transient (retry programado) | sigue "pending" — no cambia el contador, pero si expira el reintento automático (Fase 1/4) tampoco cambia — el contador de "pending" no distingue "reintentando" de "esperando su primer envío" salvo que se diseñen sub-contadores |
| Force | reingresa a la cola — ¿se vuelve a incrementar pending_count aunque ya estaba error, no pending? Requiere lógica de transición explícita para CADA combinación de status origen→destino |
| Cancelación | decremento desde donde sea que esté (pending/queued/retrying) |
| Documentos antiguos/reclasificación histórica | el comando `app:fiscal:reclassify-historical` (ya usado esta semana) cambia `status`/`error_type` de documentos de MESES atrás — ¿se debe ajustar retroactivamente el contador de un `period_date` de mayo? Rompe la utilidad del bucket diario como "snapshot de ese día" |
| Concurrencia (múltiples workers) | requiere `UPDATE ... SET pending_count = pending_count + 1` atómico por fila — factible, pero cada transición de estado necesitaría un write adicional SÍNCRONO en el hot path de emisión, algo que el diseño actual EVITA deliberadamente (`FiscalMetricsService` solo se alimenta del drenado ASYNC de auditoría, con manejo de excepciones "nunca bloquear emisión") |
| Recuperación ante fallo/crash a mitad de una transición | el contador queda desincronizado permanentemente de la realidad (`fiscal_documents` sigue siendo la fuente de verdad) hasta una reconciliación manual — **una nueva clase de bug que HOY no puede existir**, porque hoy siempre se lee directamente de `fiscal_documents` |

### Conclusión

Agregar estos campos exigiría: (a) acoplar escrituras de contador a CADA transición de estado en `FiscalEmitProcessor`, `FiscalCdrRecoveryService`, `FiscalBulkActionService`, comandos de reclasificación, cancelación — una superficie mucho mayor que el drenado async actual; (b) resolver el desajuste conceptual entre "snapshot de estado actual" (lo que pending/last_emit necesitan) y "bucket acumulado por día calendario" (lo que la tabla ya es); (c) un mecanismo de reconciliación periódica para corregir drift ante fallos — que efectivamente vuelve a ejecutar la query que se quería evitar, solo que en background.

**El beneficio** — evitar 2 queries de 38-99ms cada 30s, no identificadas como cuello de botella real — **no justifica** ese costo de complejidad y el nuevo modo de falla (drift de contador). **Decisión: NO IMPLEMENTAR.** Se mantienen las queries en vivo (`pendingCountByTenant()`, `lastEmitByTenant()`), que siempre reflejan la verdad exacta de `fiscal_documents` sin posibilidad de desincronización.

---

## C) Semántica temporal — mapa completo

| Indicador | Endpoint | Query/archivo:línea | Semántica implementada | Etiqueta que ve el operador | ¿Coinciden? |
|---|---|---|---|---|---|
| documents_today | `/operations/summary` (card) + `/stats` | `globalStats()`→`countByFilters(from=today midnight)` (`FiscalDocumentDetailService.php:102-116`) | Calendario (00:00 hora servidor) | "Documentos hoy" | ✅ Sí |
| errors_today, retries_today, avg_duration_ms | `/operations/summary` (cards) | `globalSummarySince(today midnight)` (`FiscalOperationsService.php:55-60`) | Calendario | "Errores hoy", "Retries hoy" | ✅ Sí |
| Emisiones por hora (chart) | `/operations/summary` | `emissionsByHourSince(today midnight)` (línea 85) | Calendario | Implícito en el chart del día | ✅ Sí |
| Errores por proveedor (chart) | `/operations/summary` | `errorsByProviderSince(today midnight)` (línea 86) | Calendario | Implícito | ✅ Sí |
| Errores 24h, Retries (tabla tenants) | `/operations/tenants` | `tenantOperationsSummary(24)` → `NOW() - 24 hours` (`FiscalAuditLogRepository.php:81-104`) | Rolling 24h | "Errores 24h" (columna literal) | ✅ Sí |
| Retries anormales (alerta) | interno, `runDetection()` | `detectRetryAnomalies()` → `-24 hours` (`FiscalAlertService.php:120-145`) | Rolling 24h | Mensaje: "N en 24h" | ✅ Sí |
| `fiscal_tenant_metrics.periodDate` | escritura (no endpoint) | `FiscalMetricsService.php:38`, `new DateTimeImmutable('today')` | Calendario (bucket) | N/A (dato interno) | N/A |
| pending (card + tabla tenants) | `/operations/summary`, `/operations/tenants` | Sin filtro temporal — estado ACTUAL, no una ventana | Estado actual (no aplica "hoy"/"24h") | "Pendientes" | ✅ Correcto — es intencionalmente atemporal |

### Conclusión

**No se encontró ningún caso donde la etiqueta mostrada al operador no coincida con lo que la query realmente calcula.** Cada indicador que dice "hoy" es calendario; cada uno que dice "24h" es rolling. No hay ningún bug de datos ni de código que corregir — **no se modificó nada**.

Lo que SÍ existe es una decisión de **diseño/UX no resuelta**: la misma pantalla (`/fiscal-operations`) combina dos ventanas temporales distintas (cards = "hoy calendario", tabla de tenants = "24h rolling") sin explicar la diferencia — un operador que mire ambas cerca de la medianoche podría ver números que no cuadran entre sí sin entender por qué (ej. a las 00:15, "Errores hoy" solo cubre 15 minutos mientras "Errores 24h" en la tabla cubre el día completo anterior). **Esto no es un bug — es una pregunta de producto**: ¿ambas vistas deberían usar la misma ventana? Si es así, ¿cuál (calendario o rolling)? No hay información suficiente en el código ni en el pedido para decidir esto por mi cuenta, así que **no se tocó nada** y queda documentado como pendiente de decisión funcional.

---

## Validación: la optimización de Fase 5 sigue correcta

Confirmado por lectura directa de código (no solo memoria de la fase anterior):

```
grep "globalStats(" en todo src/:
  FiscalDocumentDetailService.php:88   → definición (firma con $includeTenants=true por defecto)
  FiscalOperationsService.php:59        → globalStats(null, $today, null, null, false)  ← único caller con false
  FiscalController.php:192-197          → globalStats($tenantSlug, $from, $to, $tenantId)  ← 4 args, sin tocar el 5º, sigue en true
```

- `/operations/summary` → `countByTenant()` sigue sin ejecutarse (verificado en código; ya probado por `FiscalDocumentDetailServiceGlobalStatsTest::testIncludeTenantsFalseSkipsCountByTenantAndReturnsEmptyArray`).
- `/stats` → sigue incluyendo `tenants` con datos reales (4 argumentos, default `true` sin cambios).
- Ningún otro caller de `globalStats()` existe en el proyecto.
- Los 11 tests de Fase 5 (`FiscalDocumentDetailServiceGlobalStatsTest` + `FiscalAlertServiceDetectConsecutiveErrorsTest`) siguen pasando, sin modificación.

---

## Regresión completa (Fase 6)

```
facturador_lycet (PHPUnit, suite completa):  171 tests, 444 assertions — OK
backend_go (paquetes relacionados):          fiscaladmin OK, superadmin/handler OK
frontend_central (Vitest):                   74 tests, 9 archivos — OK
tsc -b:                                      sin errores
eslint:                                      mismos 2 errores + 1 warning PRE-EXISTENTES (no nuevos, verificado ya en Fase 3/4/5)
vite build:                                  ✓ built en ~12s
```

Se verificaron explícitamente (re-ejecutando la suite, no solo revisando código) las reglas fiscales de Fases 1-4: accepted, business, manual_only, permanent, transient (retryable/agotado), force, 409, contrato list/detail/queue, documentos históricos sin `error_type` — todas siguen en verde, sin ningún archivo de Fases 1-4 tocado en esta fase.

---

## Archivos modificados en Fase 6

**Ninguno.** `git status` en los 3 repositorios es idéntico al final de Fase 5 (confirmado antes y después de esta fase). Esta fase fue exclusivamente auditoría, medición con EXPLAIN, y documentación.

---

## Pendientes que requieren tu decisión/autorización

1. **Migración de índice** (sección A): autorizar o no la eliminación de `idx_fiscal_doc_tenant_status` (duplicado exacto). Migración preparada, no aplicada.
2. **`IDX_FISCAL_SALE`**: decidir si vale la pena obtener acceso a `performance_schema` (usuario con más privilegios) o habilitar slow query log temporalmente para confirmar uso real antes de considerar eliminarlo — no se recomienda eliminar sin esa evidencia.
3. **Semántica temporal "hoy" vs "24h"** (sección C): decisión de producto, no de código — ¿debería unificarse la ventana temporal entre los cards y la tabla de tenants de `/operations`, y si es así, a cuál de las dos?

Ninguna de las 3 requiere acción inmediata; ninguna se implementó sin tu autorización.
