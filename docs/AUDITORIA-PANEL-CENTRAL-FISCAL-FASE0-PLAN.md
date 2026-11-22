# Auditoría técnica Fase 0 — Plan de alineación Panel Central Fiscal

**Fecha:** 2026-09-19
**Precede a:** implementación (Fase 1 en adelante, pendiente de autorización explícita).
**Basado en:** `backend_go/docs/AUDITORIA-PANEL-CENTRAL-FISCAL-2026-09.md` (auditoría previa) + verificación directa de código fuente adicional (tests existentes, entidad `FiscalDocument`, serializadores, `FiscalTenantMetric`).
**Regla seguida:** ninguna afirmación de este documento es una suposición — todo está verificado leyendo el código actual, y se cita archivo:línea. Donde el comportamiento actual choca con lo que el usuario espera, se documenta el conflicto explícitamente en vez de resolverlo unilateralmente.

---

## 1. Resumen ejecutivo

`backend_go` (`/superadmin/fiscal/*`) es un **proxy HTTP puro** hacia `facturador_lycet` — confirmado exhaustivamente, sin BD fiscal propia, sin traducir filtros, sin clasificar errores. Esto significa que **no hay que tocar `backend_go` para arreglar ningún bug de clasificación/guard** — todo vive en `facturador_lycet`. El único cambio elegible en `backend_go` seria cosmético (nombres de campo) y **no se encontró ninguno necesario** (ver sección 4).

Se confirmaron **4 bugs reales** (no hipótesis) con evidencia directa de código y, en 2 de los 4 casos, con tests existentes que prueban el comportamiento actual (uno de ellos contradice explícitamente una de las reglas que el usuario pide para "Case 4" — ver sección 11, decisión #1):

1. Bulk `send`/`retry` no protege documentos `status=rejected + error_type=business` (el guard solo mira `status=error`).
2. Las acciones individuales (`send`/`retry`/`force`/`email`/`poll`) no tienen **ningún** guard — y esto está **probado y aceptado por un test existente** (`FiscalControllerEnqueueActionTest::testAcceptedDocumentSendQueuesWithoutStatusOverwrite`), no es un descuido, es comportamiento actual documentado por el propio test suite.
3. El endpoint de **listado** (`GET /documents`, tabla principal de `/fiscal`) **no envía `error_type` ni `retryable`** — sí lo hace el endpoint de **detalle**. Esto rompe silenciosamente el badge de estado en la tabla principal (bug de contrato, no de UI).
4. `OperacionesFiscalesPage.tsx` recibe `error_type`/`retryable` desde el backend (`serializeQueueItem`) pero el tipo TypeScript no los declara y la tabla nunca los renderiza — el operador no puede distinguir buckets en la cola.

Además se confirmó que `force` **no tiene ninguna semántica especial en la emisión** — es idéntico a `send`/`retry` en todo excepto: (a) en bulk, nunca es filtrado por `shouldSkip()`; (b) individualmente, ya no había filtro que bypassear, así que hoy `force` individual es 100% idéntico a `send`/`retry` individual.

Se descubrió una oportunidad de performance de alto valor y bajo costo: existe una tabla `fiscal_tenant_metrics` (`FiscalTenantMetric` entity) **ya mantenida incrementalmente** por `FiscalMetricsService` (llamada desde `FiscalAuditService` en cada drenado de auditoría), pero **nunca leída** por `FiscalOperationsService`/`FiscalHealthService`/`FiscalAlertService` — estos recalculan en vivo, con agregaciones sobre `fiscal_documents`/`fiscal_audit_logs`, exactamente lo que esa tabla ya precalcula. Es el candidato principal para "3. optimizar queries" antes de considerar cualquier cache.

---

## 2. Hallazgos confirmados

### H1 — Bulk `send`/`retry` no cubre `status=rejected + error_type=business`

**Archivo:** `facturador_lycet/src/Service/Fiscal/FiscalBulkActionService.php:104-138` (método `shouldSkip`).

```php
if (in_array($action, ['send', 'retry'], true)
    && $doc->getStatus() === FiscalDocument::STATUS_ERROR   // 👈 solo status=error
    && in_array($doc->getErrorType(), [ERROR_BUSINESS, ERROR_PERMANENT, 'manual_only'], true)
) {
    return true;
}
```

`business` **siempre** deja el documento en `status=STATUS_REJECTED` (nunca `ERROR`) — confirmado en `FiscalEmitProcessor.php:255-260` (ruta en vivo) y `FiscalReclassifyHistoricalCommand.php:292-298` (ruta histórica). El guard nunca se evalúa para estos documentos.

**Flujo de reproducción:** `FiscalDocumentsPage.tsx` → filtro `Estado=Rechazado` (`group=rejected`) → botón "Bulk retry" sin selección → `runBulk()` (`FiscalDocumentsPage.tsx:255-258`) manda `{filters:{group:'rejected'}, max:200}` → `FiscalController::bulk()` → `FiscalBulkActionService::byFilters()` → hasta 200 documentos `rejected` (incluidos los `business`) van a `fiscal:emit` sin ninguna protección.

**Test existente relacionado:** `FiscalBulkActionServiceShouldSkipTest.php` — su helper `exhaustedDoc()` (línea 36-44) **siempre** fuerza `status=STATUS_ERROR`. No existe ningún test con `status=REJECTED`. El gap de cobertura de test es la razón por la que el bug no se detectó antes.

**Consecuencia:** reintento masivo de comprobantes terminales, llamadas reales desperdiciadas a SUNAT/PSE, contradice el propósito explícito del bucket `business` ("nunca reintentar solo").

---

### H2 — Acciones individuales sin guard, comportamiento **probado y aceptado** por el test suite actual

**Archivo:** `facturador_lycet/src/Controller/v1/FiscalController.php:537-578` (`enqueueAction`, usado por `sendManual`/`retry`/`forceSend`/`resendEmail`/`pollTicket`, líneas 300-335).

```php
private function enqueueAction(string $uuid, string $queue, string $status): JsonResponse
{
    $doc = $this->repo->findOneBy(['documentUuid' => $uuid]);
    if ($doc === null) { return 404; }
    $this->queue->push($queue, ['document_uuid' => $uuid]);   // sin chequeo de status/errorType/retryable
    ...
}
```

**Test que prueba esto como comportamiento actual (no accidental):** `tests/Controller/v1/FiscalControllerEnqueueActionTest.php:156-166`:

```php
public function testAcceptedDocumentSendQueuesWithoutStatusOverwrite(): void
{
    $doc = $this->makeDocument('uuid-accepted', FiscalDocument::STATUS_ACCEPTED);
    [$controller, , , $em] = $this->buildController($doc);
    $em->expects($this->never())->method('flush');
    $result = $this->invokeEnqueue($controller, 'uuid-accepted', 'queued');
    $this->assertSame(Response::HTTP_ACCEPTED, $result['status']);
    $this->assertSame(FiscalDocument::STATUS_ACCEPTED, $doc->getStatus());
}
```

`buildController()` (línea 56-60) configura `$queue->expects($this->once())->method('push')` **sin condicionar al status** — es decir, el test EXPLÍCITAMENTE espera que un documento `ACCEPTED` sea encolado a `fiscal:emit` al llamar `send`. Este no es un bug oculto: es comportamiento firmado por un test. **Cualquier fix a esto rompe un test existente a propósito** — hay que decidir conscientemente si se actualiza el test o si se preserva el comportamiento y solo se agrega fricción en el frontend (confirmación/warning).

**Dónde impacta en las dos vistas auditadas:**
- `FiscalDocumentsPage.tsx:570-579` — el modal de detalle muestra los 5 botones de acción (`retry`,`send`,`force`,`poll`,`email`) **sin condición alguna sobre `detail.document.status`** — se pueden pulsar sobre un documento `accepted`.
- `OperacionesFiscalesPage.tsx:418-427` — el botón "Reprocesar" sí está condicionado a `status IN (error, retrying, queued)` (no aparece para `accepted`), es decir, **más protegido que la otra vista**, pero sigue sin distinguir `error_type` dentro de `error`.

---

### H3 — El endpoint de listado (`GET /documents`) no envía `error_type` ni `retryable`; el de detalle sí

**Archivo A (incompleto):** `facturador_lycet/src/Controller/v1/FiscalController.php:740-800` (`serializeDocSummary`, usado por `list()` línea 203-254). Campos devueltos: `document_uuid, tenant_id, tenant_slug, sale_id, document_type, series, number, status, send_mode, provider, sunat_mode, original_sunat_mode, reissue_count, sunat_code, sunat_message, has_cdr, tenant_sync_state, customer_name, company_ruc, company_name, company_environment, total, customer_email, email_status, retry_count, created_at, accepted_at`. **No incluye `error_type`, `retryable`, `next_retry_at`.**

**Archivo B (completo):** `facturador_lycet/src/Service/Fiscal/FiscalDocumentDetailService.php:134-183` (`serializeDocument`, usado por `detail()`). Sí incluye `error_type` (línea 177), `retryable` (línea 178), `next_retry_at` (línea 160), `retry_count` (línea 176).

**Consecuencia real, ya reproducida en este mismo código:** `FiscalDocumentsPage.tsx:63-81` (`fiscalGroup`) recibe `doc.error_type` de la fila de tabla (que viene de `serializeDocSummary`, siempre `undefined`) y decide el badge:

```ts
case 'error':
  return errorType === 'transient' ? {label:'En proceso', variant:'blue'} : {label:'Requiere acción', variant:'red'}
```

Como `error_type` siempre llega `undefined` en la tabla, **todo documento con `status=error` se muestra como "Requiere acción" (rojo) en la tabla principal**, incluidos los que están simplemente reintentando de forma transitoria y normal (`transient`, no agotado) — que deberían verse "En proceso" (azul). El modal de detalle sí muestra el estado correcto porque usa el endpoint de detalle (`serializeDocument`), que sí trae el campo. **Esto es la explicación más directa y concreta del "no sé por qué se pueden procesar..." — el operador ve el estado equivocado en la vista de lista, que es justo la vista desde la que decide qué reenviar masivamente.**

Esta es una incompatibilidad real de contrato con lo que el frontend ya espera (el tipo `FiscalDocumentSummary` en `fiscal.service.ts:39-41` YA declara `error_type?`/`retryable?` — el frontend fue escrito asumiendo que el backend los mandaría). Por eso calificaría como excepción válida a "no modificar `facturador_lycet`" (regla 12 del pedido): es un contrato roto, no una decisión de negocio nueva.

---

### H4 — `OperacionesFiscalesPage` recibe `error_type`/`retryable` pero no los tipa ni los muestra

**Archivo backend (completo):** `facturador_lycet/src/Service/Fiscal/Observability/FiscalOperationsService.php:312-346` (`serializeQueueItem`) — incluye `'error_type' => $doc->getErrorType()` (línea 336) y `'retryable' => $doc->isRetryable()` (línea 337).

**Archivo frontend (incompleto):** `frontend_central/src/services/fiscal-operations.service.ts:56-79` (`interface FiscalQueueItem`) — no declara `error_type` ni `retryable`. Sí declara `status`, `retry_count`, `sunat_message`, `pse_message`, `pse_response`, `display_message`, `queued_at`, `next_retry_at`.

**Archivo frontend (render):** `frontend_central/src/pages/fiscal/OperacionesFiscalesPage.tsx:390-408` — la columna "Estado" renderiza `item.status` a secas. La columna "Error" renderiza el mensaje pero no el bucket. El botón "Reprocesar" (líneas 418-427) solo mira `item.status`.

A diferencia de H3, **este no es un bug de backend** — el dato ya viaja correcto en el JSON (`error_type`, `retryable`). Es 100% un gap de frontend: falta declarar los campos en el tipo y usarlos para renderizar/condicionar. Coherente con la regla del usuario ("el frontend solo debe mostrar/interpretar, no reclasificar") — aquí no hace falta ninguna reclasificación, solo exponer lo que ya llega.

---

### H5 — `force` no tiene semántica especial en la emisión — es un bypass de guard, no un modo de emisión distinto

Búsqueda exhaustiva de `'force'`/`"force"` en `facturador_lycet/src/Service/Fiscal/`: la única lógica condicionada a la acción `force` está en `FiscalBulkActionService::shouldSkip()` línea 106 (`if ($action === 'force') { return false; }`). `FiscalEmitProcessor`, los providers (`SunatDirectProvider`, `ValidaPseProvider`) y `FiscalQueueService` **no reciben ni consultan** el nombre de la acción original — solo procesan `document_uuid` desde la cola `fiscal:emit`, igual que si viniera de `send` o `retry`.

**Confirmado explícitamente por el test** `FiscalBulkActionServiceShouldSkipTest::testForceNeverSkipsRegardlessOfClassification` (líneas 74-80) + su docblock de clase (líneas 15-20): *"'force' sigue sin filtro (override explícito)"*.

**Implicación concreta para la matriz (sección 3):** en bulk, `force` sobre un documento `ACCEPTED` **tampoco se salta** (el chequeo de `STATUS_ACCEPTED` en `shouldSkip()` línea 114 solo aplica a `in_array($action, ['send','retry'])`, no a `force`) — es decir, hoy `force` puede reencolar un documento YA ACEPTADO. Esto está documentado como diseño intencional ("override explícito"), pero vale confirmarlo con el usuario porque el efecto de re-emitir un documento ya aceptado (aunque el proveedor probablemente responda "ya informado" y no rompa nada) no está cubierto por ningún test que verifique qué pasa después en la emisión real.

**Conclusión:** no hay "comportamiento de force" que preservar en la lógica de emisión — force = bypass del guard de `shouldSkip()`. Si se decide dar más protección a `send`/`retry`, `force` debe seguir sin esa protección por diseño (según el propio código/tests actuales), salvo que el usuario decida lo contrario.

---

## 3. Matriz de acciones (estado actual verificado, no propuesto)

Leyenda: `ENCOLA` = se hace push a la cola correspondiente. `SKIP` = `shouldSkip()` devuelve true (solo aplica a bulk). Individual = sin guard alguno (H2) salvo lo indicado.

| Acción | accepted | transient (no agotado) | transient agotado (retry_count=5, retryable=false) | business (status=rejected) | manual_only (status=error) |
|---|---|---|---|---|---|
| **send** (bulk) | SKIP (`shouldSkip` línea 114) | ENCOLA | ENCOLA *(diseño actual, ver H1-nota)* | **ENCOLA — bug H1** | SKIP |
| **retry** (bulk) | SKIP | ENCOLA | ENCOLA *(diseño actual — ver decisión #1, sección 11)* | **ENCOLA — bug H1** | SKIP |
| **force** (bulk) | **ENCOLA** *(nunca se salta, ver H5)* | ENCOLA | ENCOLA | ENCOLA *(por diseño, override explícito)* | ENCOLA *(por diseño)* |
| **send** (individual) | ENCOLA *(H2, probado por test)* | ENCOLA | ENCOLA | ENCOLA | ENCOLA |
| **retry** (individual) | ENCOLA *(sin guard)* | ENCOLA | ENCOLA | ENCOLA | ENCOLA |
| **force** (individual) | ENCOLA *(idéntico a send/retry hoy, H5)* | ENCOLA | ENCOLA | ENCOLA | ENCOLA |
| **poll** (bulk y individual) | ENCOLA *(sin regla en `shouldSkip`, cae al `return false` final)* | ENCOLA | ENCOLA | ENCOLA | ENCOLA |
| **email** (bulk) | ENCOLA salvo email no entregable (`shouldSkip` línea 133, regla independiente de status/error_type) | igual | igual | igual | igual |
| **consult** (bulk) | SKIP solo si `status IN (accepted,observed)` Y ya tiene CDR (línea 110-113) | ENCOLA | ENCOLA | ENCOLA *(deliberado — "útil incluso en rechazados")* | ENCOLA |

**Nota sobre "transient agotado" en retry (bulk):** el código actual y su test (`testRetryDoesNotSkipExhaustedTransient`) declaran esto **intencional** (comentario en `FiscalBulkActionService.php:117-122`: *"'transient' SÍ se deja pasar aunque esté agotado... reintentar manualmente después de una caída pasajera de SUNAT/PSE es exactamente el caso de uso de este botón"*). Esto **contradice directamente** el "Case 4" que pediste verificar (`retry_count=5, retryable=false, retry → NO ENCOLAR`). Ver decisión #1 en la sección 11 — no lo resolví unilateralmente.

---

## 4. Contratos por capa

### 4.1 `facturador_lycet` → campos disponibles en el `FiscalDocument` (fuente de verdad)

`error_type` (`transient|permanent|business|manual_only|null`), `retryable` (bool), `retry_count` (int, total de intentos incluido el primero), `next_retry_at` (datetime|null), `status` (10 valores, `FiscalDocument.php:18-27`), `sunat_code`, `sunat_message`, `ticket`, `has_cdr`.

### 4.2 Lo que expone cada endpoint de `facturador_lycet`

| Endpoint | Serializador | `error_type` | `retryable` | `retry_count` | `next_retry_at` |
|---|---|---|---|---|---|
| `GET /documents` (list) | `FiscalController::serializeDocSummary` (`FiscalController.php:740`) | ❌ **falta (H3)** | ❌ **falta (H3)** | ✅ | ❌ falta |
| `GET /documents/{uuid}` (detail) | `FiscalDocumentDetailService::serializeDocument` (`FiscalDocumentDetailService.php:134`) | ✅ | ✅ | ✅ | ✅ |
| `GET /operations/queue` (queue monitor) | `FiscalOperationsService::serializeQueueItem` (`FiscalOperationsService.php:312`) | ✅ | ✅ | ✅ | ✅ |

### 4.3 `backend_go` — transformación

Ninguna (confirmado por auditoría previa: proxy puro, `collectQuery()` copia query params 1:1, `PostJSON`/`GetJSON` no tocan el body). No hay pérdida ni renombrado de campos entre `facturador_lycet` y lo que llega al navegador — **el problema de H3/H4 no está en `backend_go`, está en qué manda `facturador_lycet` (H3) y qué declara/usa el frontend (H4)**.

### 4.4 `frontend_central` — qué declara cada interfaz TS

| Interfaz | Archivo | `error_type` | `retryable` | `retry_count` | `next_retry_at` |
|---|---|---|---|---|---|
| `FiscalDocumentSummary` | `fiscal.service.ts:19-44` | ✅ declarado (línea 40) — **pero backend nunca lo llena en list (H3)** | ✅ declarado (línea 41) — **idem** | ✅ | ❌ no declarado (tampoco se usa) |
| `FiscalQueueItem` | `fiscal-operations.service.ts:56-79` | ❌ **no declarado (H4)** | ❌ **no declarado (H4)** | ✅ | ✅ |

**Conclusión de contrato:** no hay ningún caso de nombre distinto (`error_type` vs `errorType`) — el naming es consistente snake_case en todas las capas. El problema es 100% de **campos faltantes**, no de mapeo. Un caso está en el backend (H3), otro en el frontend (H4).

---

## 5. Performance — clasificación de queries (taxonomía pedida)

Polling: `OperacionesFiscalesPage.tsx:138-142`, cada 30s, 5 requests en paralelo.

| # | Query / operación | Archivo:línea | Clasificación | Motivo |
|---|---|---|---|---|
| 1 | `pingDb()` (`SELECT 1`) | `FiscalHealthService.php:103-111` | NECESARIA | Trivial, health check real |
| 2 | `providerStatusSummary()` (GROUP BY sobre `empresa`) | `FiscalHealthService.php:116-136` | NECESARIA | Tabla `empresa` es pequeña (tenants, no documentos) |
| 3 | `sunatConnectivitySummary()` | `FiscalHealthService.php:141-154` | NECESARIA | Idem |
| 4 | `failedDocs` COUNT sin rango de fecha sobre `fiscal_documents` | `FiscalHealthService.php:56-62` | REQUIERE ÍNDICE + OPTIMIZABLE | `WHERE status IN (error,rejected)` sin filtro de fecha; verificar índice sobre `status`; considerar acotar u obtener de `fiscal_tenant_metrics` |
| 5 | `alerts->countOpen()` / `criticalAlerts` COUNT | `FiscalHealthService.php:64-71` | NECESARIA | Tabla `fiscal_alerts` pequeña |
| 6-9 | Redis: `queueLength`×2, `scheduledRetryCount`×2, heartbeat | `FiscalHealthService.php:40-49` | NECESARIA | Redis, no SQL |
| 10 | `detectConnectionIssues()` (itera `empresas->findBy`) | `FiscalAlertService.php:67-98` | CACHEABLE | `connection_status` no cambia cada 30s; no necesita re-evaluarse en cada poll |
| 11 | `detectQueueSaturation()` | `FiscalAlertService.php:100-118` | NECESARIA | 1 llamada Redis |
| 12 | `detectRetryAnomalies()` (agregado 24h sobre `fiscal_audit_logs`) | `FiscalAlertService.php:120-145` | REQUIERE ÍNDICE + CACHEABLE | Verificar índice `(status, created_at, tenant_slug)`; no necesita precisión de 30s |
| 13 | `detectConsecutiveErrors()` — query inicial + **1 query adicional por cada tenant_slug encontrado** | `FiscalAlertService.php:147-192` | **N+1 (prioridad máxima)** | Patrón confirmado línea 164-168 dentro de un `foreach` |
| 14 | `globalStats()` → `countByStatus()` | `FiscalDocumentDetailService.php:94` | NECESARIA | Acotado a hoy cuando lo llama `summary()` |
| 15 | `globalStats()` → `countByFilters($todayFilters)` | `FiscalDocumentDetailService.php:116` | REDUNDANTE | Repite trabajo de #14 con un filtro casi idéntico (`from=today`) — se puede derivar del mismo resultado |
| 16 | `globalStats()` → `emails->countPending()` | `FiscalDocumentDetailService.php:125` | NECESARIA | — |
| 17 | `globalStats()` → `countByTenant()` (GROUP BY sin fecha) | `FiscalDocumentDetailService.php:127`, repo en `FiscalDocumentRepository.php:152-170` | **REDUNDANTE / trabajo desperdiciado** | El resultado (`stats['tenants']`) **no se usa** en `FiscalOperationsService::summary()` (líneas 72-73 solo leen `documents_today`/`pending`) — se calcula y se descarta en cada poll |
| 18-20 | `emissionsByHourSince`, `errorsByProviderSince`, `avgDurationByProvider` | `FiscalOperationsService.php:82-84,300-307` | REQUIERE ÍNDICE | Agregados sobre `fiscal_audit_logs` acotados a "hoy" — correctos en alcance, verificar índice `(created_at)` |
| 21 | `tenantOperationsSummary(24)` | `FiscalAuditLogRepository` (no auditado línea a línea aún) | **REQUIERE CAMBIO DE DISEÑO** | Candidato directo a leerse desde `fiscal_tenant_metrics` (ver hallazgo debajo) en vez de agregar `fiscal_audit_logs` en vivo |
| 22 | `pendingCountByTenant()` (GROUP BY sin fecha) | `FiscalOperationsService.php:266-277` | REQUIERE ÍNDICE | `WHERE status IN (pending,queued,retrying) GROUP BY tenant_slug` — verificar índice `(status, tenant_slug)` |
| 23 | `lastEmitByTenant()` (MAX sin ningún WHERE) | `FiscalOperationsService.php:282-295` | **REQUIERE CAMBIO DE DISEÑO** | Sin filtro de estado ni fecha, agrega sobre TODA la tabla; con 38k+ filas y creciendo, empeora con el tiempo. Buen candidato para mover a `fiscal_tenant_metrics` o a una columna denormalizada en `empresa` actualizada al emitir |
| 24-31 | `findByStatuses`+`countByStatuses` × 4 grupos (queue monitor) | `FiscalOperationsService.php:172-198` | NECESARIA (con índice) | Acotado por `status`, requiere índice pero el diseño en sí es correcto |
| 32-33 | Redis: `emit_queue`, `retry_scheduled` (queue monitor) | `FiscalOperationsService.php:188-195` | NECESARIA | — |

### Hallazgo de diseño clave: `fiscal_tenant_metrics` existe y no se usa para lectura

`FiscalTenantMetric` (`src/Entity/FiscalTenantMetric.php`) — tabla con `tenantId, tenantSlug, ruc, periodDate, periodType, documentsEmitted, documentsAccepted, errors, retries, avgDurationMs, successRate, provider, sendMode` **por día**, mantenida incrementalmente por `FiscalMetricsService` (inyectado en `FiscalAuditService.php:22,32`, se actualiza en cada drenado de la cola `fiscal:audit`). Ningún método de `FiscalOperationsService`/`FiscalHealthService`/`FiscalAlertService` la lee — todos recalculan en vivo desde `fiscal_documents`/`fiscal_audit_logs` exactamente los mismos números (`errors_24h`, `retries_24h`, `avg_duration_ms` por tenant) que esta tabla ya precalcula por día. Es el candidato natural para resolver #17, #21, #22, #23 con **cambio de diseño (leer de una tabla ya agregada), no cache** — cumple la prioridad pedida ("eliminar trabajo innecesario" antes que "cache").

### Orden de optimización propuesto (siguiendo la prioridad pedida)

1. **Eliminar trabajo innecesario:** #17 (`countByTenant` descartado) — no llamar `globalStats()` completo desde `summary()`, o exponer un método liviano que solo traiga `documents_today`/`pending`.
2. **Eliminar N+1:** #13 (`detectConsecutiveErrors`) — reemplazar el loop por una sola query con window function, o leer directamente de `fiscal_tenant_metrics`/`fiscal_audit_logs` con una consulta agregada única.
3. **Optimizar queries:** #15 (redundante con #14), #21/#22/#23 → migrar lectura a `fiscal_tenant_metrics` donde el grano diario sea suficiente (a confirmar con el usuario: ¿"hoy" en vez de "últimas 24h rodantes" es aceptable para los cards del dashboard?).
4. **Revisar índices:** #4, #12, #18-20, #22, #24-31 — confirmar (no asumir) qué índices existen hoy sobre `fiscal_documents`/`fiscal_audit_logs` antes de proponer nuevos (la migración `Version20260527000000` mencionada en `FISCAL-CENTRAL-PANEL.md` ya agregó algunos — falta verificar cuáles cubren estos patrones específicos).
5. **Medir:** una vez aplicado 1-4, medir tiempo real de cada endpoint en staging/producción antes de decidir si aún hace falta cache.
6. **Cache (recién al final, no ahora):** solo si after 1-5 el polling de 30s sigue siendo costoso.

---

## 6. Plan de fases propuesto

### FASE 1 — Guards y consistencia de acciones (backend, `facturador_lycet`)
- Decidir (sección 11, decisiones #1-#3) y luego: extender `FiscalBulkActionService::shouldSkip()` para cubrir `status=REJECTED+business` (H1).
- Decidir si `enqueueAction()` individual gana un guard o si el fix es solo de UX en frontend (H2) — **requiere actualizar/reescribir el test `testAcceptedDocumentSendQueuesWithoutStatusOverwrite` conscientemente si se cambia**.
- Confirmar semántica de `force` con el usuario (H5) antes de tocarlo.

### FASE 2 — Contrato/API
- Agregar `error_type`, `retryable`, `next_retry_at` a `FiscalController::serializeDocSummary()` (H3) — cambio mínimo, aditivo, no rompe nada existente.
- Verificar si algún otro endpoint tiene el mismo gap antes de darlo por cerrado.

### FASE 3 — UX fiscal (`frontend_central`)
- Declarar `error_type`/`retryable` en `FiscalQueueItem` (H4) y renderizarlos en `OperacionesFiscalesPage.tsx` (badge de bucket, deshabilitar/advertir "Reprocesar" cuando no aplique).
- Igual revisión en `FiscalDocumentsPage.tsx`: condicionar los botones del modal de detalle a `status`/`error_type` en vez de mostrarlos siempre.
- Ninguna reclasificación por texto — solo consumir los campos que ya (tras Fase 2) estarán completos.

### FASE 4 — Tests integrales
- Cubrir los 8 casos pedidos por el usuario (sección 9) — hoy solo 1.5 de 8 están cubiertos (ver detalle sección 9).

### FASE 5 — Performance
- Aplicar el orden de la sección 5 (eliminar trabajo innecesario → N+1 → optimizar → índices → medir).

### FASE 6 — Medición y validación final
- Confirmar en producción: tiempos de `/fiscal-operations`, ausencia de reintentos indebidos, badges correctos.

*(Orden técnicamente justificado: Fase 1 y 2 son ambas en `facturador_lycet` y son prerequisito de Fase 3 — no tiene sentido mostrar bien en el frontend un dato que el backend aún no manda completo (H3) ni arreglar el frontend antes de decidir la semántica de guards (H1/H2/H5), porque esa decisión afecta qué debe mostrar/advertir la UI.)*

---

## 7. Archivos a modificar (por fase, sujeto a aprobación)

- `facturador_lycet/src/Service/Fiscal/FiscalBulkActionService.php` (Fase 1, H1)
- `facturador_lycet/src/Controller/v1/FiscalController.php` (Fase 1 si se decide guard individual, H2; Fase 2, H3)
- `facturador_lycet/tests/Service/Fiscal/FiscalBulkActionServiceShouldSkipTest.php` (Fase 1/4, nuevos casos)
- `facturador_lycet/tests/Controller/v1/FiscalControllerEnqueueActionTest.php` (Fase 1/4, si cambia H2, requiere tocar el test existente conscientemente)
- `frontend_central/src/services/fiscal-operations.service.ts` (Fase 3, H4)
- `frontend_central/src/pages/fiscal/OperacionesFiscalesPage.tsx` (Fase 3, H4)
- `frontend_central/src/pages/fiscal/FiscalDocumentsPage.tsx` (Fase 3, condicionar botones del modal)
- `facturador_lycet/src/Service/Fiscal/Observability/FiscalAlertService.php` (Fase 5, N+1 de `detectConsecutiveErrors`)
- `facturador_lycet/src/Service/Fiscal/Observability/FiscalOperationsService.php` (Fase 5, #17/#22/#23)
- `facturador_lycet/src/Service/Fiscal/FiscalDocumentDetailService.php` (Fase 5, #15)

## 8. Archivos que NO se tocarán en esta iniciativa

- `backend_go/internal/superadmin/handler/fiscal_handler.go`, `pkg/fiscaladmin/client.go` — confirmado proxy puro sin bugs propios; no requieren cambio salvo que Fase 2 revele algo nuevo.
- `backend_go/internal/billing/service/*` — fuera de alcance explícito (fase posterior, auditoría independiente, sección 12 del pedido).
- `vendor/greenter/*` — no tocado.
- `FiscalEmitProcessor.php`, `SunatDirectProvider.php`, `ValidaPseProvider.php`, `FiscalErrorBucketClassifier.php` — lógica de clasificación/emisión ya implementada y en producción esta semana; no se encontró ninguna incompatibilidad real de contrato que la justifique tocar (H3 se arregla en el serializador, no en el clasificador).
- `FiscalCdrRecoveryService.php` — reglas de CDR ya cerradas en la sesión anterior, fuera de alcance de esta iniciativa salvo nueva evidencia.

---

## 9. Tests existentes vs. faltantes (los 8 casos pedidos)

| Caso pedido | Cubierto hoy | Evidencia |
|---|---|---|
| 1. `status=rejected, error_type=business, bulk retry` → NO ENCOLAR | ❌ **falta** — es exactamente H1, sin test | — |
| 2. `status=error, error_type=manual_only, retryable=false, retry` → NO ENCOLAR | ⚠️ Parcial — cubierto para **bulk** (`testRetryAndSendSkipNonRetryableBuckets`), **no** para individual (H2, sin guard) | `FiscalBulkActionServiceShouldSkipTest.php:46-53` |
| 3. `status=error, error_type=transient, retryable=true, retry_count<5, retry` → ENCOLAR | ✅ Cubierto indirectamente (transient nunca se salta) — no hay test con `retry_count` explícito bajo, pero el comportamiento general está probado | `testRetryDoesNotSkipExhaustedTransient` prueba el caso agotado; el caso no-agotado no tiene test dedicado pero es el mismo camino de código |
| 4. `status=error, error_type=transient, retryable=false, retry_count=5, retry` → NO ENCOLAR | ❌ **Contradicho** por un test existente que espera lo contrario (`ENCOLAR`) — ver decisión #1 | `testRetryDoesNotSkipExhaustedTransient` (línea 64-72) |
| 5. `business, force` → según contrato actual | ✅ Documentado — `force` nunca se salta (H5), confirmado por test | `testForceNeverSkipsRegardlessOfClassification` |
| 6. `manual_only, force` → según contrato actual | ✅ Mismo test, mismo resultado | Idem |
| 7. Bulk por filtros, `selected.size=0`, `filters={group:rejected}` | ❌ **Falta** — no hay test de integración `byFilters()` con `group=rejected` que verifique que documentos `business` se salten (consistente con que H1 es un bug real) | — |
| 8. Frontend recibe `error_type/retryable/retry_count` y los representa sin reclasificar por texto | ❌ **Falta** — no se encontraron tests de frontend (Jest/Vitest) para `fiscalGroup()` ni para el render de `OperacionesFiscalesPage` | No se encontró carpeta de tests en `frontend_central` para estos componentes (a confirmar si existe un runner de tests configurado en el proyecto) |

---

## 10. Riesgos de regresión

- **`force`:** si se decide agregar cualquier guard a `force`, se rompe la semántica de "override explícito" documentada en 3 lugares (comentario de clase del test, comentario de `shouldSkip()`, y el propio nombre de la acción). No tocar sin decisión explícita del usuario.
- **Bulk actions:** extender `shouldSkip()` a `status=REJECTED` podría bloquear un caso de uso legítimo hoy no documentado (¿alguien depende de poder reintentar un `rejected` no-business?) — revisar si existe ese caso antes de aplicar Fase 1.
- **Documentos históricos:** los 382 documentos reclasificados la semana pasada (13 PSE + 11 directo en `business`, más los `manual_only`/`permanent`) son exactamente los que H1 pone en riesgo hoy — cualquier fix debe verificarse contra ellos específicamente (spot-check en producción, mismo patrón ya usado en la sesión anterior).
- **Documentos ya aceptados:** H2 confirma que hoy se puede reencolar un `accepted` — si se agrega guard, hay que decidir qué responde la API a un intento de `send` sobre un documento aceptado (¿400? ¿200 no-op?) para no romper la UI que hoy asume `202 Accepted` siempre.
- **Documentos pendientes:** no se detectó riesgo — `enqueueAction()` para `pending`/`queued` ya tiene comportamiento estable y testeado (`testPendingDocumentSendQueuesAndFlushes`).
- **PSE vs SUNAT directo:** el guard de `shouldSkip()` no distingue `send_mode` — cualquier fix debe seguir aplicando igual a ambos canales (confirmado que `business`/`manual_only` ocurren en ambos, por los ejemplos reales verificados la sesión anterior: `rodriguezruiz`/`grupogamtek` eran PSE, `industrialrafaz` era directo).
- **Límite de 5 intentos:** ningún hallazgo de esta auditoría toca `applyFailure()`/`maxRetries()` — el límite ya implementado no está en riesgo por este trabajo, salvo que la decisión #1 (sección 11) resulte en tocar el guard de bulk retry para transient agotado, lo cual no cambia el límite en sí, solo si un humano puede reintentar manualmente después de agotado.

---

## 11. Preguntas / decisiones que necesitan tu aprobación antes de Fase 1

**Decisión #1 (la más importante — conflicto directo):** tu "Case 4" pide que `status=error, error_type=transient, retryable=false, retry_count=5, retry` → **NO ENCOLAR**. Pero el código actual, con un test que lo prueba explícitamente (`testRetryDoesNotSkipExhaustedTransient`), fue diseñado a propósito en la Fase 4 de la sesión anterior para que esto **SÍ encole** — con el razonamiento (comentario en el propio código): un transitorio agotado (SUNAT/PSE caído 5 veces) debería poder reintentarse manualmente una vez resuelto el problema externo, sin esperar a que alguien lo mueva de bucket. ¿Cuál es la regla correcta? Opciones:
  - (a) Mantener el diseño actual (transient agotado SÍ reintentable manualmente) y tu Case 4 se ajusta a eso.
  - (b) Cambiar a que transient agotado tampoco se reintente automáticamente en bulk, y el humano deba usar `force` para ese caso también.
  - (c) Alguna variante intermedia (p. ej. permitir bulk retry en transient agotado pero avisar/confirmar en el frontend).

**Decisión #2:** ¿H1 (bulk retry/send sobre `rejected+business`) se arregla agregando `OR $doc->getStatus() === STATUS_REJECTED` a la misma condición de `shouldSkip()`, o quieres una condición más general basada solo en `error_type` sin mirar `status` en absoluto (ya que `business` nunca convive con otro status de todos modos)?

**Decisión #3:** ¿H2 (acciones individuales sin guard) se resuelve con guard duro en backend (rompe el test existente, hay que reescribirlo a propósito), o solo con fricción en frontend (confirmación/advertencia visual, sin tocar backend ni el test)? Mi lectura del pedido es que prefieres frontend-first para esto, pero no lo asumo.

**Decisión #4:** H3 (agregar `error_type`/`retryable`/`next_retry_at` a `serializeDocSummary()`) — ¿confirmas que esto es la excepción válida a "no modificar facturador_lycet" porque es un contrato roto, no una decisión de negocio nueva? Técnicamente es un cambio aditivo de bajo riesgo (nuevos campos en un array de respuesta), pero quiero tu confirmación explícita antes de tocar ese archivo.

**Decisión #5:** para Fase 5 (performance), ¿aceptas que los cards de `/operations/summary` y la tabla de tenants pasen de "últimas 24h rodantes" a "hoy calendario" (grano de `fiscal_tenant_metrics`, que es por día) si eso permite eliminar la mayoría de las agregaciones en vivo? Si necesitas estrictamente 24h rodantes, el cambio de diseño es más limitado (no puede apoyarse tan directamente en la tabla ya agregada).

**Decisión #6:** ¿confirmas que force sobre un documento `accepted` (hoy posible, ver H5) debe seguir siendo posible sin ningún guard, como override administrativo total, o también quieres al menos una confirmación visual en el frontend para ese caso específico?

Quedo a la espera de tu autorización explícita y de las respuestas a estas 6 decisiones antes de tocar cualquier código de Fase 1.
