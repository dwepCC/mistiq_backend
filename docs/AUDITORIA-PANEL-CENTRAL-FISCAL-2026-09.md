# Auditoría — Panel central fiscal (`/fiscal` y `/fiscal-operations`)

**Fecha:** 2026-09-19
**Alcance:** `frontend_central` (`FiscalDocumentsPage.tsx`, `OperacionesFiscalesPage.tsx`) + `backend_go` (`/superadmin/fiscal/*`) + `facturador_lycet` (`FiscalController`, `FiscalOperationsController` y servicios asociados).
**Motivo:** verificar consistencia con la reclasificación de errores (`FiscalErrorBucketClassifier`, buckets `transient`/`manual_only`/`business`) implementada y desplegada en `facturador_lycet` esta semana (commits `856a539`, `54353d2`), y diagnosticar por qué `/fiscal-operations` carga lento.
**Estado:** solo diagnóstico — **no se modificó código**. Los hallazgos están ordenados por severidad; cada uno cita archivo:línea verificado directamente en el código actual.

---

## 0. Arquitectura confirmada (para que el resto del documento tenga contexto)

```
frontend_central (FiscalDocumentsPage / OperacionesFiscalesPage)
        │  fetch a /superadmin/fiscal/...
        ▼
backend_go  (internal/superadmin/handler/fiscal_handler.go)
        │  proxy HTTP puro (pkg/fiscaladmin/client.go) — SIN base de datos fiscal local,
        │  SIN traducción de filtros, reenvía query params y payload tal cual
        ▼
facturador_lycet  (FiscalController + FiscalOperationsController, /api/v1/fiscal/*)
        │  source of truth: tabla fiscal_documents
        ▼
MySQL (facturador)
```

Confirmado leyendo `backend_go/internal/superadmin/handler/fiscal_handler.go` completo: **no hay duplicación de la lógica de clasificación de errores (`transient`/`manual_only`/`business`) en este namespace**. `backend_go` no sabe qué es un bucket, no lo necesita saber — todo pasa transparente hacia `facturador_lycet`. Esto significa que, para estas dos páginas, **el 100% de los bugs de lógica/consistencia y de performance detectados están en `facturador_lycet`**, no en `backend_go` ni en el proxy. `backend_go` solo se ve implicado en un riesgo de timeout (ver 3.4).

Nota aparte (fuera del alcance de las dos vistas, pero relevante para el negocio): sí existe una clasificación de errores **duplicada y desactualizada** en `backend_go/internal/billing/service/` (pipeline de emisión tenant-a-tenant, no el panel central) — ver sección 4.

---

## 1. Bugs de consistencia (funcional) — prioridad alta

### 1.1 `FiscalBulkActionService::shouldSkip()` no protege documentos `status=rejected` + `error_type=business` en acciones masivas `send`/`retry`

**Archivo:** `facturador_lycet/src/Service/Fiscal/FiscalBulkActionService.php:104-138`

```php
private function shouldSkip(FiscalDocument $doc, string $action): bool
{
    if ($action === 'force') { return false; }
    if ($action === 'consult') { ... }
    if ($doc->getStatus() === FiscalDocument::STATUS_ACCEPTED && in_array($action, ['send', 'retry'], true)) {
        return true;
    }
    if (in_array($action, ['send', 'retry'], true)
        && $doc->getStatus() === FiscalDocument::STATUS_ERROR   // 👈 SOLO status=error
        && in_array($doc->getErrorType(), [
            FiscalDocument::ERROR_BUSINESS, FiscalDocument::ERROR_PERMANENT, 'manual_only',
        ], true)
    ) {
        return true;
    }
    ...
}
```

El guard que evita desperdiciar reintentos masivos en documentos terminales (`business`/`permanent`/`manual_only`) **solo se evalúa cuando `status === STATUS_ERROR`**. Pero el bucket `business` (rechazo real de negocio SUNAT/PSE, código ≥2000, o código `1032`) **siempre deja el documento en `status = STATUS_REJECTED`**, nunca en `ERROR` — confirmado en:

- Ruta en vivo: `FiscalEmitProcessor.php:255-260` (`$result->rejected` → `setStatus(STATUS_REJECTED)` + `setErrorType(ERROR_BUSINESS)`).
- Ruta de reclasificación histórica: `FiscalReclassifyHistoricalCommand.php:292-298` (mismo patrón, para ambos canales PSE y directo).

**Consecuencia real y reproducible:** en `FiscalDocumentsPage.tsx`, un superadmin filtra `Estado = Rechazado` (`group=rejected`) y pulsa **"Bulk retry"** o **"Bulk send"** sin seleccionar filas (`runBulk` usa `filters: {...filterQuery}` cuando `selected.size === 0`, `FiscalDocumentsPage.tsx:255-258`). Esto llama a `POST /documents/bulk/retry` con `filters={group:'rejected'}` → `FiscalBulkActionService::byFilters()` → `findByFilters()` trae **todos** los documentos con `status=rejected` (hasta `max`, por defecto 200) → `shouldSkip()` nunca los salta porque no están en `STATUS_ERROR` → **se reenvían literalmente todos a la cola `fiscal:emit`**, incluidos los `error_type=business` (p. ej. código `1032` "comprobante ya informado con estado anulado o rechazado", que **nunca** va a aceptar solo por reintentarse — el correlativo ya está quemado en SUNAT).

Este gap es **más peligroso ahora que antes de esta semana**, porque la reclasificación histórica recién aplicada (`app:fiscal:reclassify-historical --apply`, 382 documentos) dejó **13 documentos PSE + 11 del canal directo** exactamente en este estado (`status=rejected`, `error_type=business`) — antes de la reclasificación, esos documentos ya estaban `rejected` pero sin `error_type` explícito; el bug existía igual, pero ahora hay una categoría con nombre (`business`) pensada específicamente para "nunca reintentar solo", y el guard que debería hacerlo cumplir no cubre este caso.

**Nota de diseño:** el comentario en el propio código (línea 117-122) dice explícitamente *"No desperdiciar una acción masiva de reenvío en documentos que no se van a resolver solo reintentando"* — la intención documentada ya existe, solo falta el `OR $doc->getStatus() === STATUS_REJECTED` en la condición.

---

### 1.2 Las acciones individuales (`send`/`retry`/`force`/`email`/`poll`) no tienen ningún guard de bucket — a diferencia de las acciones masivas

**Archivo:** `facturador_lycet/src/Controller/v1/FiscalController.php:537-578` (método `enqueueAction`, usado por `sendManual`, `retry`, `forceSend`, `resendEmail`, `pollTicket` — líneas 300-335)

```php
private function enqueueAction(string $uuid, string $queue, string $status): JsonResponse
{
    $doc = $this->repo->findOneBy(['documentUuid' => $uuid]);
    if ($doc === null) { return ...; }
    $this->queue->push($queue, ['document_uuid' => $uuid]);   // 👈 sin ningún chequeo de status/errorType
    ...
}
```

A diferencia de `FiscalBulkActionService::shouldSkip()` (sección 1.1), este método **no verifica en absoluto** `status`, `errorType` ni `retryable` antes de encolar. Cualquier click en "Reenviar"/"Reintentar"/"Forzar" sobre **cualquier** documento —incluido uno `business` terminal o `manual_only`— lo vuelve a encolar sin aviso.

**Dónde se usa esto en las dos vistas auditadas:**
- `FiscalDocumentsPage.tsx:270-279` (`runAction`, botones del modal de detalle) → `POST /documents/{uuid}/{action}`.
- `OperacionesFiscalesPage.tsx:159-170` (`retryDoc`, botón "Reprocesar" en la tabla de cola) → `POST /documents/{uuid}/retry`.

Es decir: **las acciones masivas SÍ están parcialmente protegidas (con el gap de 1.1), pero las acciones de un solo documento —que es justo lo que expone `/fiscal-operations`— NO tienen ninguna protección**. Es inconsistente en el sentido opuesto al que uno esperaría (uno asumiría que lo masivo es lo más peligroso y lo individual un override consciente, pero aquí ninguno de los dos filtra por completo y el individual filtra menos que el masivo).

Esto conecta directo con lo que reportaste ("no sé por qué se pueden procesar las facturas para reenviarlos o forzar de manera masiva"): técnicamente **se puede**, porque no hay ningún guard en el path individual, y el guard del path masivo tiene el hueco de 1.1.

---

### 1.3 `OperacionesFiscalesPage` no muestra `error_type` ni `retryable` — el operador no puede distinguir "vale la pena reintentar" de "nunca va a funcionar"

**Archivos:**
- Backend ya devuelve el dato: `facturador_lycet/src/Service/Fiscal/Observability/FiscalOperationsService.php:312-346` (`serializeQueueItem`) incluye `'error_type' => $doc->getErrorType()` y `'retryable' => $doc->isRetryable()`.
- El tipo TypeScript **no lo declara**: `frontend_central/src/services/fiscal-operations.service.ts:56-79` (`interface FiscalQueueItem`) — no tiene `error_type` ni `retryable` como campos.
- La tabla **no lo renderiza**: `frontend_central/src/pages/fiscal/OperacionesFiscalesPage.tsx:390-408` — la columna "Estado" muestra `item.status` a secas (`pending`/`error`/`retrying`...), nunca el bucket. La columna "Error" muestra el mensaje SUNAT/PSE pero no el bucket.
- El botón "Reprocesar" se muestra igual para todos: `OperacionesFiscalesPage.tsx:418-427` — condición `item.status === 'error' || item.status === 'retrying' || item.status === 'queued'`, sin mirar `retryable`/`error_type` (que ni siquiera están tipados para poder mirarlos).

**Contraste:** `FiscalDocumentsPage.tsx` (la otra vista) sí hace esta distinción — tiene `fiscalGroup()` (líneas 63-81) que colapsa el bucket a una etiqueta con color, y el modal de detalle (líneas 611-621) muestra el hint textual ("se reintenta automáticamente" / "requiere acción manual" / "rechazo de negocio SUNAT/PSE"). **`/fiscal-operations` no tiene ningún equivalente** — es la vista pensada para "procesar en cola" y es justo la que menos contexto de bucket muestra.

*(Verificado y descartado: revisé si el badge de `FiscalDocumentsPage.tsx` podía mostrar mal un caso `status=error && error_type=business` — no es alcanzable, `business` nunca deja el documento en `status=error` (ver 1.1), así que ese caso puntual no es un bug real, solo código que nunca se ejecuta con esa combinación.)*

---

## 2. Performance — por qué `/fiscal-operations` carga lento

`OperacionesFiscalesPage.tsx:138-142` hace **polling cada 30 segundos** (`setInterval(load, 30000)`) mientras la pestaña esté abierta, disparando **5 requests en paralelo** (`Promise.allSettled`, líneas 110-120): `health`, `summary`, `tenants`, `queue`, `alerts`. Cada uno de estos, en `facturador_lycet`, dispara varias queries SQL — ninguna cacheada. Conteo de queries por ciclo de 30s (una sola pestaña abierta):

| Endpoint | Archivo:línea | Queries SQL por llamada | Notas |
|---|---|---|---|
| `GET /health` | `FiscalHealthService.php:38-101` | ~6 (ping, 2 agregados sobre `empresa`, 1 `COUNT` sin rango de fecha sobre `fiscal_documents` completo, 2 sobre `fiscal_alerts`) + 3 llamadas Redis | `failedDocs` (línea 56-62) cuenta `status IN (error,rejected)` en **toda la tabla**, sin filtro de fecha |
| `GET /operations/summary` | `FiscalOperationsService.php:51-87` | ~9-10: `runDetection()` (4 sub-detecciones, la peor con N+1, ver 2.1) + `globalStats()` (4 queries, una de ellas descartada, ver 2.2) + 3 agregados (`emissionsByHourSince`, `errorsByProviderSince`, `avgDurationByProvider`) | |
| `GET /operations/tenants` | `FiscalOperationsService.php:93-167` | 1 query por `empresas->findBy()` + `tenantOperationsSummary(24)` + **2 agregados sin rango de fecha sobre toda `fiscal_documents`** (`pendingCountByTenant`, `lastEmitByTenant`, líneas 266-295) | Ver 2.3 |
| `GET /operations/queue` | `FiscalOperationsService.php:172-198` | 8 queries (`findByStatuses`+`countByStatuses` × 4 grupos) + 2 Redis | |
| `GET /alerts` | `FiscalAlertService.php` | 2 (`countOpen`, `findOpen`) | liviano |

**Total estimado: ~25-30 queries SQL cada 30 segundos por cada superadmin con la pestaña abierta**, más varias llamadas Redis. Ninguna tiene cache. Este es, con evidencia directa de código, el motivo más probable de la lentitud reportada — no es un problema de una sola query pesada, es volumen de aggregate queries redundantes en cada poll.

### 2.1 `detectConsecutiveErrors()` tiene un patrón N+1, y corre en cada poll de `/operations/summary`

**Archivo:** `facturador_lycet/src/Service/Fiscal/Observability/FiscalAlertService.php:147-192`

```php
$rows = $conn->fetchAllAssociative(<<<'SQL'
SELECT tenant_slug, COUNT(*) AS failures FROM (
  SELECT tenant_slug, status FROM fiscal_audit_logs
  WHERE tenant_slug IS NOT NULL AND event_type IN ('fiscal_emit_failed','fiscal_emit_success')
  ORDER BY created_at DESC LIMIT 500
) recent GROUP BY tenant_slug HAVING failures >= :min
SQL, ...);
foreach ($rows as $row) {
    // 👈 una query ADICIONAL por cada tenant_slug encontrado arriba
    $recent = $conn->fetchAllAssociative('SELECT status FROM fiscal_audit_logs WHERE tenant_slug = :slug ... LIMIT :lim', ...);
    ...
}
```

Esto se ejecuta en **cada** llamada a `summary()` (`FiscalOperationsService.php:53` → `$this->alertService->runDetection()`), es decir cada 30s mientras la vista esté abierta — no es un cron aparte, es parte del hot path de carga de la página. `detectRetryAnomalies()` (líneas 120-145, misma clase) es un agregado adicional sobre `fiscal_audit_logs` con rango de 24h que también corre en cada poll.

### 2.2 `globalStats()` calcula `countByTenant()` (agregado GROUP BY sin fecha) y el resultado se descarta

**Archivo:** `facturador_lycet/src/Service/Fiscal/FiscalDocumentDetailService.php:88-129`, línea 127: `'tenants' => $this->documents->countByTenant($tenantSlug)`.

`countByTenant()` (`FiscalDocumentRepository.php:152-170`) hace `SELECT tenantSlug, COUNT(d.id) ... GROUP BY tenantSlug ORDER BY total DESC LIMIT 200` sin filtro de fecha — agrega sobre **toda** la tabla `fiscal_documents` cada vez.

`FiscalOperationsService::summary()` (línea 56) llama `globalStats(null, $today, null)` y del array devuelto **solo usa `documents_today` y `pending`** (líneas 72-73) — el campo `tenants` que costó calcular se descarta sin usar. Es trabajo desperdiciado en cada poll de 30s. (Nota: sí tiene sentido cuando lo llama `FiscalController::stats()` para `/fiscal` — `FiscalStats.tenants` se usa en el tipo TS, aunque no until vi que `FiscalDocumentsPage.tsx` tampoco renderiza ese campo `tenants` en ningún lado — revisar si vale la pena seguir calculándolo ahí tampoco, pero eso es una pregunta de producto, no un bug.)

### 2.3 `pendingCountByTenant()` / `lastEmitByTenant()` agregan sobre toda la tabla sin acotar por fecha, en cada poll de `/operations/tenants`

**Archivo:** `FiscalOperationsService.php:266-295`.

```php
'SELECT tenant_slug, COUNT(*) AS c FROM fiscal_documents WHERE status IN (\'pending\',\'queued\',\'retrying\') AND tenant_slug IS NOT NULL GROUP BY tenant_slug'
'SELECT tenant_slug, MAX(COALESCE(accepted_at, sent_at, updated_at)) AS last_emit FROM fiscal_documents WHERE tenant_slug IS NOT NULL GROUP BY tenant_slug'
```

La segunda query en particular no tiene ningún `WHERE` sobre estado ni fecha — agrega `MAX(...)` sobre la tabla completa agrupando por `tenant_slug`. Con la tabla ya en ~38k+ filas (dato real de producción, verificado hoy) y creciendo, esto empeora con el tiempo si no se acota o cachea.

### 2.4 Riesgo de timeout en el proxy (`backend_go`), no causa raíz pero agrava el síntoma percibido

**Archivo:** `backend_go/pkg/fiscaladmin/client.go:30` — timeout HTTP fijo de 30s hacia `facturador_lycet`. Si cualquiera de los endpoints de arriba se degrada (p. ej. bajo carga concurrente de varios superadmins con la pestaña abierta), el usuario puede llegar a esperar hasta 30s antes de ver un error 502 en vez de un timeout más corto con reintento/backoff. No es la causa de la lentitud, pero explica por qué en el peor caso se "cuelga" la vista en vez de fallar rápido.

---

## 3. Duplicación de lógica de clasificación — fuera de las dos vistas, pero en el mismo dominio fiscal

**No se encontró duplicación dentro de `/fiscal` ni `/fiscal-operations`** (backend_go es proxy puro para ambas, confirmado). Sí se encontró una clasificación de errores **paralela y desactualizada** en el pipeline de emisión tenant-a-tenant (`backend_go/internal/billing/service/`), que no es parte de estas dos vistas pero sí vive en el mismo dominio "fiscal" y quedó desalineada con el trabajo de esta semana:

- `fiscal_status_sync.go:191-209` (`isFiscalConfigErrorMessage`) clasifica errores por **substring del mensaje SUNAT** (`"openssl_sign"`, `"certificado inválido"`, etc.), no por el campo `error_type`/`errorType` — de hecho el struct que parsea la respuesta de `facturador_lycet` (líneas 117-137) **no tiene ningún campo para `error_type`**, así que aunque quisiera, esta capa no puede leer si `facturador_lycet` clasificó un documento como `manual_only`.
- `fiscal_status_sync.go:211-218` (`isFiscalTerminalStatus`) tiene su propia lista hardcodeada `"accepted","rejected","observed","error"`.
- `fiscal_guard.go:164-194` (`classifyAfterSync`) tiene una tercera clasificación propia (`already_accepted`/`rejected_final`/`already_processing`/`allow`), ajena también al bucket nuevo.
- La tabla local `TenantInvoice` (`pkg/database/migrations.go:1773-1798`) no tiene columna `error_type` ni `retryable`.

**Riesgo concreto:** si `facturador_lycet` ahora clasifica algo como `manual_only` y detiene los reintentos automáticos (`maxRetries()` bajado de 20 a 5, `retryable=false`), pero el mensaje SUNAT no calza con ninguno de los substrings hardcodeados en `isFiscalConfigErrorMessage`, el `TenantInvoice.JobStatus`/`PipelineStatus` local de `backend_go` puede seguir mostrando "reintentando" en el ERP del tenant mientras `facturador_lycet` ya lo dejó en terminal manual — una desincronización de UX entre lo que ve el tenant y lo que realmente está pasando en el source of truth.

*(Este punto queda fuera del pedido original de auditar `/fiscal` y `/fiscal-operations`, pero se documenta porque es la misma familia de bug — desconocimiento del bucket nuevo — y probablemente valga la pena una auditoría dedicada aparte.)*

---

## 4. Resumen priorizado

| # | Hallazgo | Severidad | Alcance | Acción sugerida (pendiente de aprobación, NO implementada) |
|---|----------|-----------|---------|---------------------------------------------------------|
| 1.1 | Bulk `send`/`retry` no protege `status=rejected + error_type=business` | 🔴 Alta — puede reintentar masivamente comprobantes terminales, gasta llamadas reales a SUNAT/PSE | `/fiscal` (bulk) | Agregar el mismo chequeo de `errorType` cuando `status===REJECTED`, en `FiscalBulkActionService::shouldSkip()` |
| 1.2 | Acciones individuales (`send`/`retry`/`force`) sin ningún guard de bucket | 🟠 Media-alta — mismo riesgo pero un documento a la vez, y es la acción principal de `/fiscal-operations` | `/fiscal` + `/fiscal-operations` | Añadir advertencia/confirmación en el frontend cuando `error_type` sea `business`/`manual_only`, y opcionalmente un guard soft en `enqueueAction()` |
| 1.3 | `/fiscal-operations` no expone `error_type`/`retryable` en la cola | 🟠 Media — causa raíz de la confusión operativa reportada | `/fiscal-operations` | Tipar `error_type`/`retryable` en `FiscalQueueItem` (TS) y mostrarlos en la tabla/botón |
| 2.1 | `detectConsecutiveErrors()` con N+1 en cada poll de 30s | 🟡 Media — contribuye a la lentitud | `/fiscal-operations` | Cachear resultado de `runDetection()` con TTL corto (Redis), o moverlo a un cron aparte en vez del hot path del summary |
| 2.2 | `countByTenant()` calculado y descartado en `summary()` | 🟢 Baja — quick win | `/fiscal-operations` | Evitar llamar `globalStats()` completo cuando solo se necesitan 2 campos, o separar esos 2 campos en un método liviano |
| 2.3 | Agregados sin rango de fecha (`lastEmitByTenant`, `pendingCountByTenant`, `failedDocs` en health) | 🟡 Media — empeora con el crecimiento de la tabla | `/fiscal-operations` | Cache TTL corto (15-30s) compartido entre pollers, o acotar por fecha/estado con índice dedicado |
| 4 | Clasificación de errores duplicada y desactualizada en `backend_go/internal/billing/service` | 🟡 Media — fuera del alcance pedido, pero mismo dominio | Pipeline emisión tenant (no el panel central) | Auditoría dedicada aparte; no se investigó a fondo en esta pasada |

---

## 5. Lo que NO se tocó / decisiones pendientes del usuario

Este documento es solo diagnóstico, según lo pedido. Antes de implementar cualquier fix hace falta decidir, en particular:

- **1.1**: ¿el fix debe extender `shouldSkip()` a `status=REJECTED+business`, o el producto quiere permitir reintentar rechazos de negocio bajo algún caso de uso legítimo (p. ej. después de que el tenant corrija manualmente el comprobante y quiera forzar reenvío)? Si es lo segundo, el botón "force" ya cubre ese caso (nunca se salta) — bulk `send`/`retry` no debería necesitarlo.
- **1.2**: ¿agregar solo advertencia visual en frontend, o también un guard duro en backend para acciones individuales? Afecta si un superadmin puede seguir "forzando" un `business` a propósito vía el botón individual (hoy sí puede, y probablemente deba poder seguir pudiendo).
- **2.x**: ¿cache Redis con qué TTL es aceptable para los KPIs de `/fiscal-operations`? ¿30s (mismo intervalo de poll, básicamente elimina el problema) o menos agresivo?
- **Sección 4**: ¿se audita por separado el pipeline `backend_go/internal/billing/service`, o queda fuera de alcance por ahora?
