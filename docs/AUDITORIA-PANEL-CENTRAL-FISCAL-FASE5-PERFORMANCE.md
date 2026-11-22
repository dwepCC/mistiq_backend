# Fase 5 — Performance: medición, optimización y verificación

**Fecha:** 2026-09-20
**Metodología:** MEDIR → IDENTIFICAR → OPTIMIZAR → VOLVER A MEDIR, contra la base de datos de producción de `facturador_lycet` (MySQL 8.4.10, Percona Server), vía SSH de solo lectura (mismo acceso ya usado y aprobado en sesiones anteriores para diagnóstico). **Ninguna query de escritura se ejecutó contra producción en esta fase** — todo fue `SELECT`/`EXPLAIN`.

---

## 1. Datos de la base al momento de medir

```
fiscal_documents:    38 404 filas, 314 tenant_slug distintos
fiscal_audit_logs:   224 402 filas
fiscal_tenant_metrics: 11 107 filas, 407 tenant_id distintos, rango 2026-05-25..2026-09-20, última actualización 2026-09-20 01:48:41 (fresca)
empresa (enabled=1): 384
```

---

## 2. Baseline — queries medidas (antes de tocar código)

Todas las medidas con `time` sobre `mysql` CLI contra la BD real. Para las que corresponden a un patrón N+1, se remidieron además en una **sola conexión** (representativo de cómo Doctrine/PDO reutiliza la conexión dentro de un request PHP — muy distinto a 26 invocaciones de proceso `mysql` separadas por SSH, que tienen su propio overhead de arranque).

| # | Query (origen) | EXPLAIN — hallazgo | Tiempo medido |
|---|---|---|---|
| 1 | `pendingCountByTenant()` (`FiscalOperationsService.php:266-277`) | `type=index`, full index scan de `IDX_FISCAL_TENANT_STATUS` (~33k filas) — `status IN (...)` no es columna líder de ningún índice | 38-67ms |
| 2 | `lastEmitByTenant()` (`FiscalOperationsService.php:282-295`) | `type=index`, full index scan (100% filtered) — `MAX(COALESCE(3 columnas))` no puede usar rango de índice | 99ms |
| 3 | `countByTenant()` global, sin filtro de tenant (`FiscalDocumentRepository.php:152-170`, llamada desde `globalStats()`) | full index scan + `Using temporary; Using filesort` (por el `ORDER BY total DESC LIMIT 200`) | **125ms** — la más cara de las agregaciones individuales |
| 4 | `health()` → `failedDocs` (`FiscalHealthService.php:56-62`) | `type=index` sobre `idx_fiscal_doc_tenant_id`, sin rango de fecha | 27-30ms |
| 5 | `detectRetryAnomalies()` (`FiscalAlertService.php:120-145`) | usa `idx_fal_status`/`idx_fal_created_at` razonablemente | 35ms |
| 6 | `detectConsecutiveErrors()` — query de candidatos (`FiscalAlertService.php:150-159`) | `Backward index scan` sobre `idx_fal_created_at`, derived table de 500 filas | 93-99ms |
| 7 | `detectConsecutiveErrors()` — **26 queries de seguimiento, una por tenant candidato** (código anterior a esta fase) | cada una: `ref` sobre `idx_fal_tenant_created`, `LIMIT 5` — indexada y barata individualmente | **75ms en total** (26 queries, una sola conexión) — la causa real de la "sensación de N+1" era el CONTEO de queries, no su costo real |

**Total estimado del ciclo completo `/operations/summary` + `/operations/tenants` + `/operations/queue` + `/health`:** ~25-30 queries SQL (catalogadas en la auditoría de Fase 0), ninguna individualmente lenta a este volumen, pero sumando trabajo redundante (ver abajo).

---

## 3. `fiscal_tenant_metrics` — análisis de equivalencia semántica (sección 4 del pedido)

Se leyó `FiscalMetricsService.php` completo (no solo la tabla) antes de considerar usarla. Conclusión: **NO es un reemplazo seguro** para las queries que el panel necesita, por incompatibilidad real de semántica, no por preferencia:

| Campo necesario por el panel | ¿Existe en `fiscal_tenant_metrics`? | Detalle |
|---|---|---|
| `pending` (documentos AHORA en pending/queued/retrying) | **No existe ningún campo equivalente** | La tabla solo tiene contadores de FLUJO (`documentsEmitted`, `documentsAccepted`, `errors`, `retries`) incrementados por EVENTO de auditoría, no un snapshot del estado actual de los documentos |
| `errors_24h`/`errors_today` | Parcial, pero con semántica distinta | `errors` se incrementa una vez por cada evento `fiscal_emit_failed` — un mismo documento que falla y reintenta varias veces incrementa `errors` varias veces. El panel necesita "cuántos documentos ESTÁN actualmente en error", no "cuántos eventos de fallo hubo" — son números distintos |
| `last_emit_at` | No existe ningún timestamp de última emisión | Solo hay `updatedAt` (cuándo se tocó la fila de métrica del día, no cuándo emitió el tenant) |
| Período | `periodType='day'`, bucket por `DateTimeImmutable('today')` al momento de grabar el evento | Coincide con "hoy calendario", pero `detectRetryAnomalies()` y otras partes del código usan "últimas 24h rodantes" (`NOW() - INTERVAL 24 HOUR`) — **mezcla real de semánticas temporales dentro del mismo dashboard**, documentada abajo, NO corregida en esta fase (no había pedido explícito ni evidencia de cuál es la "correcta") |

**Decisión:** no se usó `fiscal_tenant_metrics` en esta fase para ninguna de las queries auditadas — usarla habría exigido inventar una nueva noción de "pending"/"last_emit_at" que no coincide con la actual, violando la regla de preservar semántica exacta. Queda documentado como **posible trabajo futuro** (agregar esos 2 campos a la tabla y mantenerlos incrementalmente) pero es un cambio de diseño, no una optimización de esta fase.

---

## 4. Índices — auditoría (sección 7 del pedido)

`SHOW INDEX FROM fiscal_documents` reveló **3 índices redundantes**:

- `IDX_FISCAL_TENANT_STATUS` (tenant_slug, status)
- `idx_fiscal_doc_tenant_status` (tenant_slug, status) — **idénticas columnas que el anterior**, nombre distinto
- `idx_fiscal_doc_tenant_slug_status` (tenant_slug, status, created_at) — superset de los 2 anteriores; MySQL podría usar este único índice para cualquier consulta que hoy usa los otros dos

**No se eliminaron** en esta fase: es un cambio de esquema (migración Doctrine) sobre una tabla de producción con escritura constante (colas fiscales activas) — fuera del alcance de "medir y optimizar código de aplicación" sin autorización explícita para tocar el schema. Se documenta como propuesta separada (ver sección "Propuestas NO implementadas" más abajo).

**Índices nuevos propuestos:** ninguno con evidencia suficiente. Se consideró un índice líder en `status` (o `(status, tenant_slug)`) para acelerar `pendingCountByTenant`/`health.failedDocs`, pero:
- No se puede crear un índice de prueba en producción para medir el antes/después sin riesgo (regla explícita: no optimizar por intuición, medir).
- Los tiempos actuales (27-67ms) ya son aceptables a 38k filas; no hay evidencia de que sea un cuello de botella real hoy.
- Queda como candidato a re-evaluar cuando la tabla crezca a cientos de miles de filas (donde un full index scan sí dolería).

---

## 5. N+1 en `detectConsecutiveErrors()` — investigado, "arreglado", REVERTIDO tras medir

Este es el hallazgo más importante de honestidad metodológica de la fase.

**Intento 1 (implementado primero):** reemplazar las 26 queries por-tenant por una sola query con `ROW_NUMBER() OVER (PARTITION BY tenant_slug ...)`. Medido en producción: **361ms** — más lenta que el original, porque `tenant_slug IN (26 valores)` incluye a `snjempresa` (tenant de altísimo volumen, >170 eventos solo en la ventana de 500 recientes), lo que hace que el optimizador descarte el índice `idx_fal_tenant_created` y haga un scan+filesort sobre ~20 600 filas (`EXPLAIN`: `type=ALL`, `Using where; Using filesort`).

**Intento 2 (probado, también descartado):** `UNION ALL` de 26 sub-`SELECT` (un solo round-trip, cada rama usando su propio índice). Medido: **142ms** — mejor que el intento 1, pero **todavía peor** que el código original.

**Medición del código ORIGINAL, hecha correctamente** (26 queries en una sola conexión mysql, sin el overhead de 26 procesos SSH separados — el escenario real de Doctrine con conexión persistente): **75ms totales**. Es decir, el patrón "N+1" que se veía como un problema evidente leyendo el código estáticamente, **no es un cuello de botella real a este volumen de datos**, gracias al índice compuesto `idx_fal_tenant_created` ya existente.

**Decisión: se revirtió el cambio de estrategia de queries.** `detectConsecutiveErrors()` sigue haciendo 1 query de candidatos + N queries por-tenant, exactamente como antes — **no por pereza, sino porque se midió y la alternativa era peor.** Esto es exactamente la regla pedida: *"No aceptar 'la consulta es más rápida' como única validación"* — aquí ni siquiera era más rápida.

**Lo único que se conservó de este intento:** la lógica de decisión ("¿los últimos 5 eventos del tenant son todos `failed`?") se extrajo a un método público y puro, `FiscalAlertService::tenantsWithConsecutiveFailures()`, sin ningún I/O — permite probar la lógica con datos fijos sin mockear Doctrine/DBAL. Es un refactor de testabilidad con impacto cero en queries/performance/comportamiento.

---

## 6. `globalStats()` → `countByTenant()` calculado y descartado — SÍ optimizado

Confirmado con código (no solo intuición): `FiscalOperationsService::summary()` (línea 56, antes de esta fase) llamaba `globalStats()` completo y solo leía `documents_today`/`pending` del resultado (líneas 72-73) — el campo `tenants` (que cuesta la query #3 de la tabla de baseline, 125ms) se calculaba y se tiraba **en cada poll de 30 segundos**.

**Optimización:** `globalStats()` ganó un 5º parámetro opcional `bool $includeTenants = true`. Cuando es `false`, `'tenants' => []` sin ejecutar `countByTenant()`. **Todos los demás campos permanecen exactamente iguales** (probado, ver sección de equivalencia). El único caller que pasa `false` es `FiscalOperationsService::summary()`; `FiscalController::stats()` (cuyo contrato `GET /stats` sí incluye `tenants` — usado por `FiscalStats.tenants` en `frontend_central`) sigue usando el default `true`, **sin ningún cambio de comportamiento**.

**Resultado:** elimina 125ms medidos, garantizados, en cada poll de `/operations/summary` — sin ambigüedad de medición (la query simplemente deja de ejecutarse cuando no se necesita).

---

## 7. Polling frontend (sección 9 del pedido)

Auditado: `grep -n "setInterval"` en ambas páginas. **Solo `OperacionesFiscalesPage.tsx` tiene polling** (`setInterval(load, 30000)`, línea 147) — `FiscalDocumentsPage.tsx` **no tiene ningún polling automático** (solo carga al montar/cambiar filtros, y el botón manual "Actualizar"). Como son rutas separadas (React Router), solo una está montada a la vez en uso normal — **no se encontró duplicación de polling entre las dos páginas**. La única "duplicación" real era interna a los 5 requests paralelos de `/operations` (ya mitigada parcialmente por la optimización de la sección 6). No se modificó el intervalo de polling — no había evidencia que lo justificara, y la regla explícita prohibía "simplemente aumentar el intervalo" sin evidencia de duplicación real.

---

## 8. Timeout de `backend_go` (sección 10 del pedido)

Con las queries individuales en 27-125ms y el N+1 confirmado como NO-problema a este volumen, el tiempo total esperado del lado de `facturador_lycet` para servir `/operations/summary` está muy por debajo del timeout de 30s configurado en `pkg/fiscaladmin/client.go:30`. **No se encontró evidencia de que el timeout sea la causa de ningún problema real** — no se modificó.

---

## 9. Optimizaciones implementadas (resumen)

| # | Cambio | Archivo | Ganancia medida/garantizada | Riesgo de semántica |
|---|---|---|---|---|
| 1 | `globalStats(..., $includeTenants=false)` desde `summary()` | `FiscalDocumentDetailService.php`, `FiscalOperationsService.php` | -125ms por poll de 30s (query eliminada, no solo más rápida) | Ninguno — default preserva 100% el comportamiento anterior para todo otro caller; probado |
| 2 | Extracción de `tenantsWithConsecutiveFailures()` (refactor de testabilidad) | `FiscalAlertService.php` | Ninguna (ni positiva ni negativa) — mismo SQL, misma performance | Ninguno — función pura probada con 7 casos, mismo resultado que el algoritmo anterior |

**Optimización intentada y revertida:** batching de `detectConsecutiveErrors()` vía `ROW_NUMBER()` — descartada tras medir que era 4.8x más lenta que el original (361ms vs 75ms). Documentado en detalle en sección 5.

---

## 10. Pruebas de equivalencia

- `FiscalDocumentDetailServiceGlobalStatsTest.php` (4 tests): confirma que con `includeTenants=false` **nunca** se llama `countByTenant()` (`expects($this->never())`), que con `true`/default el resultado es **idéntico** al comportamiento anterior, y que **todos los demás 12 campos** del array de retorno son exactamente iguales entre ambos modos.
- `FiscalAlertServiceDetectConsecutiveErrorsTest.php` (7 tests): reproduce el algoritmo VIEJO como referencia dentro del propio test (`oldAlgorithm()`) y confirma que `tenantsWithConsecutiveFailures()` produce el **mismo resultado exacto** en 5 escenarios distintos (todos fallidos, uno interrumpido por éxito, menos de 5 eventos, tenant ausente, mezcla de varios tenants).

---

## 11. Medición posterior

| Query | Antes | Después |
|---|---|---|
| `countByTenant()` en el poll de `/operations/summary` | 125ms (ejecutada y descartada) | **0ms — ya no se ejecuta** |
| `detectConsecutiveErrors()` total | 93-99ms (candidatos) + 75ms (26 follow-ups) ≈ 168-174ms | **Sin cambio** (168-174ms) — se revirtió el intento de "mejora" tras confirmar que empeoraba |

**Reducción total garantizada del ciclo de poll de 30s: ~125ms** (de la eliminación real de una query), sobre un total estimado previo de ~940ms+ en agregaciones. No es una reescritura dramática — es exactamente lo que la evidencia sostenía, ni más ni menos.

---

## 12. Regresión fiscal (Fases 1-4)

Suite completa `facturador_lycet`: **171 tests, 444 assertions, 0 fallos** (160 de Fase 1-4 + 11 nuevos de Fase 5). Se verificó explícitamente que las pruebas de accepted/business/manual_only/permanent/transient(retryable)/transient(agotado)/force/409/list-detail-queue/documentos históricos (todas de Fase 1-4) siguen pasando sin cambios — ninguna de esas pruebas fue tocada en esta fase.

---

## 13. Propuestas NO implementadas (requieren decisión aparte)

1. **Eliminar los 3 índices redundantes** en `fiscal_documents` (`IDX_FISCAL_TENANT_STATUS`, `idx_fiscal_doc_tenant_status` duplicados, y evaluar si `idx_fiscal_doc_tenant_id` también es redundante frente a `idx_fiscal_doc_tenant_slug_status`). Reduciría overhead de escritura (cada INSERT/UPDATE mantiene 3 índices por el mismo prefijo de columnas). Requiere migración Doctrine + despliegue coordinado — no se tocó el schema de producción en esta fase.
2. **Agregar campos `pending_count`/`last_emit_at` a `fiscal_tenant_metrics`** para eventualmente poder servir `pendingCountByTenant()`/`lastEmitByTenant()` desde la tabla pre-agregada — cambio de diseño (nuevo campo + lógica de mantenimiento incremental), no una optimización de query. Requiere decidir semántica exacta primero.
3. **Unificar la semántica temporal** ("hoy calendario" vs "últimas 24h rodantes") usada en distintas partes de `/operations` — documentado como inconsistencia real (sección 3), no corregido por falta de evidencia de cuál es la semántica correcta deseada.

Ninguna se implementó — se presentan aquí como evidencia para una decisión futura, tal como pediste.
