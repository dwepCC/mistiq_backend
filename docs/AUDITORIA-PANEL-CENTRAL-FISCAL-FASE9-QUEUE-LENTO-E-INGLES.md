# Fase 9 — Causa raíz de la lentitud de `/fiscal-operations` (monitor de cola) y textos en inglés restantes

**Fecha:** 2026-09-20
**Disparador:** el usuario inició sesión en el panel ya desplegado (Fase 8) y reportó dos cosas en vivo: (a) seguía viendo texto en inglés en ambas vistas, (b) `/fiscal-operations` seguía demorando mucho en cargar.

---

## A) Causa raíz de los 7.5-7.8s en `/superadmin/fiscal/operations/queue`

### Cadena de medición (todas contra producción, con evidencia)

1. **Medido desde el navegador real** (fetch directo con el token de sesión, `performance.now()`): `queue` tardó 7758ms con `group=queued` y 7559ms con `group=failed`, contra 270-599ms del resto de endpoints de la misma vista (`health`, `summary`, `tenants`, `alerts`).
2. **Raíz descartada #1 — MySQL vía SSH directo:** el `SELECT id ... WHERE status IN (...) ORDER BY updated_at DESC LIMIT 25` (la query real de `findByStatuses()`) tardó **32ms** por CLI, pese a `EXPLAIN` mostrar `type=ALL, possible_keys=NULL, rows=32847, Using where; Using filesort` (sin índice en `updated_at`).
3. **Raíz descartada #2 — Redis:** `redis-cli ping` a 127.0.0.1:6379 (mismo host) respondió en 0-10ms, 5 veces seguidas. `FiscalQueueService::isReachable()` no es el cuello de botella.
4. **Raíz descartada #3 — PHP-FPM cold start:** el pool usa `pm=ondemand` con `pm.process_idle_timeout=10s`, lo que sí puede causar arranques fríos costosos en Symfony — pero peticiones repetidas al mismo endpoint (caliente) siguieron tardando 7.5-8s por igual, y una petición 403 (rechazada antes de tocar la lógica de negocio) respondió en 5-10ms incluso "fría". Esto descarta el arranque de PHP-FPM como causa.
5. **Reproducción exacta con el token real (`CLIENT_TOKEN` de `.env.local`), atacando el endpoint interno real (`/api/v1/fiscal/operations/queue`, el que efectivamente usa `backend_go`):** `curl -w` mostró `TTFB=7.52s` en la primera llamada y 7.8-8.1s en llamadas repetidas — reproduce el problema exacto, con auth real, sin pasar por el navegador.
6. **`SHOW FULL PROCESSLIST` en MySQL durante esa petición real:** una única query permanece en estado `executing` desde el segundo 1 hasta el segundo 7-8 completos de la petición — es el `SELECT f0_.id, f0_.document_uuid, ...` (la hidratación completa de la entidad `FiscalDocument` que hace Doctrine en `findByStatuses()`).
7. **Causa raíz confirmada:** la diferencia entre el paso 2 (`SELECT id`, 32ms) y el paso 6 (`SELECT *` vía Doctrine) es que la tabla no tiene índice en `updated_at`, así que MySQL hace *full table scan + filesort* de las ~38 600 filas — pero Doctrine no selecciona solo `id`, selecciona **todas las columnas de la entidad**, incluida `snapshot_json` (promedio 1.4KB, máximo 16KB, ~53MB acumulados en la tabla). Un filesort de filas anchas es muchísimo más costoso que uno de enteros. Reproducido de forma aislada: `SELECT *` con el mismo `WHERE/ORDER/LIMIT` tomó **7.621s** vs `SELECT id` con **0.032s**, mismísima condición, mismísimos datos.

### Por qué afecta a los 3 grupos (`queued`/`processing`/`failed`/`retrying`) por igual

`queueMonitor()` llama a `findByStatuses()` una vez (para el grupo pedido) y a `countByStatuses()` 4 veces (una por cada grupo, para los contadores de las pestañas). Las 5 queries comparten la misma tabla sin índice útil — el costo está dominado por la primera (`findByStatuses`, la única que hidrata entidades completas), por eso el tiempo total es prácticamente el mismo sin importar qué grupo se pida.

### Fix — `migrations/Version20260920010000.php` (facturador_lycet)

```sql
CREATE INDEX idx_fiscal_doc_status_updated ON fiscal_documents (status, updated_at)
```

`status` tiene cardinalidad baja (en producción: `accepted`, `observed`, `error`, `rejected`, `sent`, más `pending`/`queued`/`sending`/`retrying`/`cancelled`). Con este índice, `status IN (...) ORDER BY updated_at` puede resolverse con un range scan por cada valor de `status`, sin escanear ni ordenar la tabla completa. Como beneficio adicional, las 4 llamadas a `countByStatuses()` se vuelven *index-only* (InnoDB incluye `id` implícitamente en todo índice secundario), sin tocar `snapshot_json` en absoluto.

Migración probada localmente (`up`/`down`/`up` contra la BD de desarrollo de Laragon) — ver el mismo patrón de validación usado en Fase 7 (`Version20260920000000.php`).

### Bug adicional encontrado en el camino: `backend_go` descartaba `group`/`limit`/`offset`

`FiscalHandler.OperationsQueueAPI` (`backend_go/internal/superadmin/handler/fiscal_handler.go:347`) llamaba a `fiscaladmin.GetJSON("/api/v1/fiscal/operations/queue", nil)` — pasando `nil` en vez de `collectQuery(c)` (el patrón usado por `OperationsSummaryAPI`/`OperationsTenantsAPI` en las líneas vecinas). Esto significa que **todo click en una pestaña del monitor de cola, o en "Siguiente"/"Anterior" de su paginación (Fase 8), no tenía ningún efecto real**: `facturador_lycet` siempre recibía la petición sin query string y devolvía el `group=queued, limit=25, offset=0` por defecto, sin importar qué pidiera el frontend. Corregido pasando `collectQuery(c)` como los demás endpoints del mismo archivo. Explica además por qué medir con `group=queued` y `group=failed` dio tiempos idénticos en el paso 1 — literalmente era la misma petición interna las dos veces.

---

## B) Texto en inglés restante (barrido tras la Fase 8)

Encontrado navegando el panel en vivo, no cubierto por la primera pasada de traducción (`connectionStatusLabel`/`healthStatusLabel`/`sendModeLabel`/`emailStatusLabel`/`queueTabLabel`):

| Archivo | Antes | Después |
|---|---|---|
| `FiscalDocumentsPage.tsx` | Checkbox "Solo retry" | "Solo reintentos" |
| `FiscalDocumentsPage.tsx` | Encabezado de columna "Retry" | "Reintentos" |
| `FiscalDocumentsPage.tsx` | Botones de lote "Bulk retry/send/force/poll/email" | "Reintentar (lote)/Enviar (lote)/Forzar (lote)/Consultar (lote)/Correo (lote)" |
| `FiscalDocumentsPage.tsx` | Botones individuales del modal de detalle: texto crudo `retry`/`send`/`force`/`poll`/`email` | Mismos verbos traducidos vía la nueva `actionLabel()` |
| `FiscalDocumentsPage.tsx` | Título "Timeline" (sección del modal de detalle) | "Línea de tiempo" |
| `OperacionesFiscalesPage.tsx` | Botón "Timeline" por fila de la cola | "Línea de tiempo" |
| `OperacionesFiscalesPage.tsx` | Título del modal `"Timeline fiscal"` | `"Línea de tiempo fiscal"` |
| `OperacionesFiscalesPage.tsx` | KPI "Retries hoy" | "Reintentos hoy" |

Nueva función `actionLabel()` en `src/lib/fiscalStatus.ts` (mismo patrón 1:1 de las otras traducciones — nunca reclasifica, solo traduce el verbo de la acción ya decidida por el backend).

---

## Pruebas ejecutadas

- `facturador_lycet`: `php bin/phpunit` — **187 tests, 476 assertions, OK**.
- `backend_go`: `go build ./...`, `go vet ./...`, `go test ./internal/superadmin/...` — **OK**.
- `frontend_central`: `npx vitest run` — **84 tests, 9 archivos, OK** (incluye 3 selectores de test actualizados en `FiscalDocumentsPage.test.tsx` para los nuevos textos en español: `'Reintentar'`, `'Enviar'`, `'Forzar'`); `npx tsc --noEmit` — sin errores; `npm run build` — build de producción exitoso.

## Pendiente / fuera de este alcance

- La migración del índice **no se aplicó en producción** — queda para cuando el usuario haga el deploy de `facturador_lycet` (misma disciplina que Fase 7: el `up()`/`down()` ya se validó localmente, pero el `CREATE INDEX` en la tabla de producción de ~38.6k filas lo ejecuta el propio pipeline de deploy).
- No se tocó `docs/INCIDENT-2026-09-17-PRICE-AUTHORIZATION.md` (archivo sin trackear ya presente en `backend_go`, de un incidente anterior no relacionado) — se deja intacto y sin commitear.
