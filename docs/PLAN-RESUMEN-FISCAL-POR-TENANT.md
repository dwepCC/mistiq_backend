# Plan: resumen fiscal por tenant en la base central (emitidos vs. enviados a SUNAT)

**Fecha:** 2026-10-06
**Estado:** SOLO ANÁLISIS Y DISEÑO. No se escribió código. Pendiente de aprobación explícita ("empieza").
**Pedido del usuario (resumido):** en *Operaciones Fiscales* ver **todos los tenants** con cuántos comprobantes
emitieron, cuántos están aceptados y cuántos **faltan por enviar a SUNAT**, con filtros por **rango de fechas,
tipo de comprobante y RUC**. Motivo: *Documentos fiscales* solo ve lo que llegó al facturador; una venta que
nunca llegó (o quedó trabada en el tenant) no la ve nadie del lado central. El tenant sí la ve en la campanita
de su header. La consulta del panel debe leer **solo la base central** (sin recorrer las bases de los tenants al
abrir la pantalla); se acepta que el dato tenga 10–15 min de atraso.

---

## 1. Decisión de diseño

Una **tabla de resumen en la BD central**, alimentada por un **job periódico** (cada 15 min) que lee —solo
lectura— cada BD de tenant con una consulta agregada, más una **pasada profunda nocturna** (la idea original del
cron de las 12 de la noche, ahora como complemento y no como único mecanismo).

| Alternativa | Frescura | Veredicto |
|---|---|---|
| Solo cron nocturno | hasta 24 h | Descartada: un tenant con 30 pendientes desde las 9 am no se vería hasta el día siguiente |
| Consultar ~600 BD al abrir la pantalla | en vivo | Descartada: lento y pesado |
| Contadores por evento (sumar/restar en cada cambio de estado) | en vivo | Descartada: ver `docs/AUDITORIA-PANEL-CENTRAL-FISCAL-FASE6-CIERRE-PERFORMANCE.md:136` (ya se evaluó para el facturador y se rechazó por drift y por tocar cada transición) |
| **Resumen central + job periódico + pasada nocturna** | 15 min | **Elegida** |

Lo que **no cambia**: la campanita del header del tenant sigue consultando en vivo su propia BD
(`billing_service.go:132` `GetNotificationCounts`); el worker de conciliación de 7 min
(`pkg/cron/fiscal_reconcile.go:13`) sigue igual.

---

## 2. Modelo de datos (BD central)

Se agregan al listado de `MigrateCentral()` (`pkg/database/migrations.go:403-435`), que corre en cada deploy
(`.github/workflows/deploy-production.yml:100-101`, `./tukifac-api migrate-central` antes del restart).

### 2.1 `tenant_fiscal_daily` — volumen por día, tipo y estado (alimenta los filtros)

| Columna | Tipo | Nota |
|---|---|---|
| `tenant_id` | uint | FK lógica a `tenants.id` |
| `day` | DATE | día de `issue_date` (hora de Lima) |
| `doc_code` | varchar(4) | `01` factura, `03` boleta, `07` NC, `08` ND, `09` guía |
| `billing_status` | varchar(20) | `pending`, `sent`, `accepted`, `rejected`, `error` |
| `cnt` | int | cantidad |
| `amount` | decimal(15,2) | suma de `total` (opcional, para el CSV) |
| `refreshed_at` | datetime | |

Clave única `(tenant_id, day, doc_code, billing_status)`. Índices `(day)` y `(tenant_id, day)`.
Tamaño estimado: ~600 tenants × ~6 combinaciones activas × 365 días ≈ 1.3 M filas/año (se purga a 24 meses).

### 2.2 `tenant_fiscal_health` — una fila por tenant (estado actual)

`tenant_id` (PK), `scanned_at`, `scan_ms`, `scan_error` (texto; vacío si OK), `open_pending`, `open_sent`,
`open_error`, `open_rejected` (todos de **cualquier antigüedad**), `oldest_open_at` (pendiente/erróneo más
antiguo), `last_issue_at` (último comprobante).

---

## 3. El job

Archivo nuevo `pkg/cron/fiscal_tenant_snapshot.go`, registrado junto a los demás en `main.go:48-50`.

- **Ciclo corto, cada 15 min:** recalcula la ventana de los **últimos 60 días** de cada tenant.
- **Pasada profunda, una vez al día (00:30 Lima):** historial completo + los abiertos de cualquier antigüedad
  (`oldest_open_at`, `open_*`). Usa `cronlock.TryAcquireDaily` (`pkg/cronlock`), que ya existe.
- **Candado:** `cronlock.TryAcquire("fiscal:tenant_snapshot", 14*time.Minute)` (igual que
  `fiscal_reconcile.go:42`), para que dos instancias no corran a la vez.
- **Tenants:** `database.ListTenantsForMigration(true)` (`pkg/database/migrate_runner.go:48-61`, solo
  `status='active'`).
- **Concurrencia:** 4 workers, timeout de 5 s por tenant. Un tenant que falla guarda `scan_error` y el recorrido
  continúa (nunca aborta toda la pasada).
- **Escritura solo en diferencias:** se carga de la central la ventana del tenant y solo se hace upsert de las
  filas que cambiaron (evita ~100 k escrituras por ciclo).
- **Guardas:** si las tablas nuevas aún no existen (deploy a medias), el job no hace nada y no falla.
- **Solo lectura en los tenants.** No modifica ningún dato de tenant.

### Consulta por tenant (borrador)

```sql
SELECT DATE(s.issue_date) AS day, ds.sunat_code AS doc_code, s.billing_status,
       COUNT(*) AS cnt, SUM(s.total) AS amount
FROM tenant_sales s
JOIN tenant_document_series ds ON ds.id = s.series_id
WHERE s.deleted_at IS NULL
  AND ds.sunat_code IN ('01','03','07','08')
  AND s.issue_date >= :window_start
GROUP BY 1,2,3;
```

Guías (`09`) van en una segunda consulta sobre `tenant_despatches` (tabla propia, estados distintos).

---

## 4. API y pantalla

**Endpoint nuevo en `backend_principal`** (no en el facturador: la fuente es la BD central):
`GET /superadmin/fiscal/tenant-summary` con `from`, `to`, `doc_type[]`, `ruc`, `q` (nombre/slug), `only_pending`,
`sort`, `page`, `per_page`. Permiso `fiscal.view`; se registra en `internal/superadmin/routes.go:180-199` y en
`route_wiring_test.go:224-230`.
`POST /superadmin/fiscal/tenant-summary/:id/refresh` ("Verificar ahora"): escanea **un** tenant al instante.

**Filtros (lo pedido):** rango de fechas con atajos (Hoy, Ayer, 7 días, Este mes, Mes anterior, personalizado),
tipo de comprobante (multi-selección 01/03/07/08/09), RUC (`tenants.ruc` es único, `migrations.go:23`), búsqueda
por nombre/slug, y "solo con pendientes por enviar".

**Columnas:** Tenant · RUC · Emitidos (en el rango) · Aceptados · **Faltan enviar** (= pendientes + error) ·
En envío · Rechazados · Pendiente más antiguo · Último comprobante · Verificado hace. Orden por defecto: más
"Faltan enviar" primero. Paginación en servidor, exportar CSV.

Esta tabla **reemplaza** la tabla "Tenants fiscales" de Operaciones, que hoy sale del facturador
(`FiscalOperationsService.php:103-182`) y solo lista los tenants con empresa configurada allí.

---

## 5. Contraste con el código real

### 5.1 Consistente (verificado)

| Supuesto del plan | Evidencia en el código |
|---|---|
| El tenant ya calcula "pendientes/error/rechazados" para su campanita | `billing_service.go:130-154`; ruta `routes.go:61`; header `Header.tsx:55,69-70,187-224` |
| Ya existe un recorrido de todos los tenants cada 7 min | `worker/reconcile.go:68-90`, `fiscal_reconcile.go:13` → recorrer las BD no es un costo nuevo ni desconocido |
| Hay candado de cron distribuido | `pkg/cronlock` (`TryAcquire`, `TryAcquireDaily`) |
| La lista de tenants activos está disponible | `ListTenantsForMigration(true)`, `migrate_runner.go:48-61` |
| Las tablas centrales se crean con `AutoMigrate` en el deploy, antes del restart | `migrations.go:403`; `deploy-production.yml:100-101` |
| `issue_date` y `series_id` están indexados en `tenant_sales` | `migrations.go:1496,1501` |
| El estado de envío está normalizado | `pkg/billingstate/state.go:117-131` (`LegacyBillingStatus`) |
| El RUC es único por tenant | `migrations.go:23` |
| Permiso `fiscal.view` y patrón de rutas | `routes.go:180-199` |

### 5.2 Inconsistencias o puntos que hay que resolver (verificados, no supuestos)

1. **`tenant_sales.billing_status` NO tiene índice** (`migrations.go:1516`; `Status` tampoco). La consulta de la
   ventana usa el índice de `issue_date`, pero "abiertos de cualquier antigüedad" haría un recorrido completo de
   `tenant_sales` en cada tenant. **Mitigación:** esa parte va solo en la pasada nocturna. **Mejora opcional:**
   migración versionada de tenant (siguiente número libre tras v154) con índice sobre `billing_status`; también
   aceleraría las 3 consultas del header. Requiere correr el fleet, por eso queda como fase aparte.
2. **`billing_status` tiene 5 valores y "sent" no es "enviado a SUNAT".** Según `LegacyBillingStatus`,
   `sent` = en tránsito (en facturador/cola/enviando a SUNAT), `accepted` incluye `OBSERVED`, `error` =
   `FAILED`/`DEAD_LETTER`. La campanita del header solo cuenta `pending`, `error` y `rejected`
   (`billing_service.go:134`), no `sent`. **Definición propuesta:** *Faltan enviar* = `pending` + `error`;
   `sent` se muestra aparte como "En envío"; si lleva más de ~10 min, es sospechoso.
3. **El comentario del modelo está desactualizado:** dice `pending, sent, accepted, rejected`
   (`migrations.go:1516`) pero el código real también usa `error`.
4. **Hay que mantener el JOIN con `tenant_document_series`.** Las notas de venta (`00`) tienen
   `billing_status='pending'` por defecto; solo el filtro por `sunat_code` las excluye
   (`billing_service.go:133,138`). Si el job omitiera ese filtro, inflaría los pendientes.
5. **Hay otra definición de "pendiente" en el sistema.** El dashboard del tenant usa
   `doc_type IN ('FACTURA','BOLETA')` y no el JOIN por `sunat_code` (`dashboard_api.go:103`,
   `dashboard_handler.go:121`). **Decisión:** el resumen central usa la definición del header (por
   `sunat_code`), para que el número del central coincida con la campanita del tenant.
6. **Las guías están en otra tabla** (`tenant_despatches`, `migrations.go:1958-1977`, `Status`:
   `pending|accepted|rejected|error`, sin índice en `status`). No se pueden obtener del mismo `GROUP BY`; van en
   una consulta separada. (El header incluye códigos `09`/`31` en su lista, `billing_service.go:133`, pero eso
   corresponde a series de venta: **a confirmar** cuáles de esas series generan filas en `tenant_sales`.)
7. **Soft delete:** `tenant_sales` tiene `DeletedAt` (`migrations.go:1550`). Con GORM `Model(&TenantSale{})` se
   filtra solo; en SQL crudo hay que añadir `deleted_at IS NULL` (ya está en el borrador).
8. **La fecha no coincide con la del facturador.** Aquí se usa `issue_date` (día del comprobante, indexado). El
   facturador filtra por `created_at` UTC de su documento (`FiscalDocumentRepository`). Los totales por día
   pueden diferir en los bordes. Además una **reemisión** puede cambiar `issue_date` (`migrations.go:1537-1538`).
   Se documenta en la UI como "fecha de emisión del comprobante".
9. **`ListTenantsForMigration(true)` solo trae tenants `active`.** Los suspendidos/inactivos quedarían fuera.
   Decisión pendiente: ¿se escanean también? (propuesta: sí los suspendidos, para que sus pendientes no queden
   ocultos).
10. **El cálculo de tiempo del job es una estimación, no una medición.** No se ha medido cuánto dura hoy la pasada
    de conciliación en producción. Antes de fijar 15 min conviene medirlo en los logs (solo lectura).
11. **El panel del facturador (`/fiscal/operations/tenants`) quedaría sin uso** para esta tabla; no se elimina en
    esta fase para no romper `OperacionesFiscalesPage` hasta que la nueva tabla esté probada.

### 5.3 Relación con documentos anteriores

- `docs/AUDITORIA-PANEL-CENTRAL-FISCAL-FASE10-VOLUMEN-Y-VALIDACION-SEMANAL-PLAN.md` (sin commitear) propone
  volumen por tenant **desde el facturador** y una validación semanal. **No se contradicen:** aquello mide lo que
  ya llegó al facturador; este plan agrega lo que **no** llegó (lado tenant). Si ambos se aprueban, la sección de
  "validación semanal" puede leer también esta tabla (pendientes por tenant y antigüedad).
- `SaasElectronicDocumentUsage` (`pkg/database/saas_documents.go:77-98`) cuenta consumo de **cupo del plan**, no
  comprobantes reales; no sirve como fuente de este resumen.

---

## 6. Riesgos y cuidados

- **Despliegue:** `migrate-central` crea las tablas antes del restart; el job además verifica que existan.
- **Producción:** el job solo hace `SELECT` en las BD de tenant; no modifica nada de ellos.
- **Tenants con esquema antiguo:** si falta una columna, el error se guarda en `scan_error` y se ve en la tabla.
- **Carga:** 4 workers + timeout por tenant; cada consulta usa el índice de `issue_date`.
- **Datos con atraso:** hasta 15 min; "Verificar ahora" por tenant para casos urgentes. La UI muestra "Verificado
  hace X min".
- **Coincidencia con la campanita:** puede diferir unos minutos; esperado y documentado.

---

## 7. Fases propuestas

1. **Backend:** modelos + `MigrateCentral`, job (ciclo corto + pasada nocturna), consultas, tests (SQLite en
   memoria, como el resto de tests de `pkg/database`).
2. **API:** `tenant-summary` y `refresh`, permisos, `route_wiring_test`.
3. **Frontend:** nueva tabla en Operaciones Fiscales con filtros (fechas, tipo, RUC), orden, paginación, CSV.
4. **Opcional:** índice versionado en `tenant_sales.billing_status`; alerta cuando un tenant acumule pendientes
   por más de X horas; acción "Reenviar pendientes" con confirmación.

## 8. Decisiones tomadas (2026-10-06)

1. **Emitidos** = todos los estados; los aceptados se muestran como subtotal.
2. **Tenants suspendidos/inactivos:** se escanean también (el job NO usa `ListTenantsForMigration(true)`; usa la lista completa de tenants no eliminados).
3. **Ventas anuladas:** cuentan igual que en la campanita del tenant (no se excluyen).
4. **Guías de remisión (09 remitente, 31 transportista):** van como un tipo más del filtro, leídas de `tenant_despatches` (consulta aparte).
5. **Medición en producción:** autorizada (solo lectura). Resultado en la sección 9.
6. **Índice en `tenant_sales.billing_status`:** fase aparte, no entra en esta implementación.

## 9. Medición en producción (solo lectura, 2026-10-06)

Se revisaron los logs del contenedor `tukifac-backend-go` (VPS backend). **No se puede obtener la duración de una
pasada de conciliación:** `ReconcileAllTenants` solo registra una línea cuando sincroniza algo
(`reconcile.go:77-89`) y el log de "lock ocupado" es nivel debug (`fiscal_reconcile.go:44`). En las últimas
6 h solo aparece el `cron_started` de arranque (intervalo 420 s = 7 min). Consecuencia: el intervalo de 15 min
queda como estimación; **el job nuevo debe registrar siempre una línea por pasada** (tenants, errores, ms) para
medirlo desde el primer día y ajustar.

## 10. Estado de implementación (2026-10-06)

Implementadas las fases 1–3 del plan.

- **Modelos:** `pkg/database/tenant_fiscal_summary.go` (`TenantFiscalDaily`, `TenantFiscalHealth`), registrados en `MigrateCentral()`.
- **Escáner:** `internal/fiscal/summary/scanner.go` (consulta agregada por tenant, escritura solo de diferencias, errores por tenant en `scan_error`) y `query.go` (`Summarize`, solo BD central).
- **Job:** `pkg/cron/fiscal_tenant_snapshot.go` (cada 15 min, 4 workers, candado `fiscal:tenant_snapshot`; pasada profunda la primera vez y cada noche desde las 00:30 Lima). Registra siempre `fiscal_snapshot_done` con tenants, fallidos, si fue profunda y milisegundos.
- **API:** `GET /superadmin/fiscal/tenant-summary` y `POST /superadmin/fiscal/tenant-summary/:id/refresh` (permiso `fiscal.view`; "Verificar" tiene enfriamiento de 20 s por tenant). Si faltan las tablas responde 503 con mensaje claro.
- **Panel central:** `TenantFiscalSummary.tsx` reemplaza la tabla "Tenants fiscales" de Operaciones; filtros de fecha (atajos + rango), tipo, RUC, nombre, "solo con pendientes", orden, paginación, desglose por tipo, CSV. El enlace "Documentos" abre `/fiscal?tenant=<slug>`.

### Decisiones de implementación que afinan el plan

- Los abiertos de más de 60 días (`older_*`) los recalcula solo la pasada profunda; el ciclo corto los arrastra, así `open_*` es siempre el total sin leer `billing_status` sin índice en cada ciclo.
- Se escanean todos los tenants no eliminados (incluye suspendidos), por una consulta propia y no por `ListTenantsForMigration(true)`.

### Verificación

- Tests: `internal/fiscal/summary` (conteo solo electrónicos, exclusión de notas de venta y eliminadas, diferencias/borrados, pasada profunda vs ciclo corto, error por tenant, filtros/orden/paginación).
- **Contraste con la campanita del tenant (BD local):** para los dos tenants, `GetNotificationCounts` coincide exactamente con `open_pending/open_error/open_rejected` del resumen (doriconta 6/78/1; demo 27/2/0).
- Pendiente de medir en producción: duración real de `fiscal_snapshot_done` tras el primer deploy, para ajustar el intervalo.
- Pendiente (fase 4, opcional): índice en `tenant_sales.billing_status`, alerta por pendientes antiguos, "Reenviar pendientes".

## 11. Fase 4 implementada (2026-10-06)

- **Índice `tenant_sales.billing_status`:** migración versionada de tenant `v155_sale_billing_status_index` (idempotente, solo crea el índice) y `index` en el modelo `TenantSale` para tenants nuevos. **Requiere correr el fleet** (`migrate-fleet-cron`) tras el deploy; hasta entonces todo funciona igual, solo sin el índice. Aplicada y probada en los tenants locales.
- **Pendientes atrasados:** un tenant es "atrasado" si su pendiente/error sin enviar más antiguo tiene 3 días o más (`summary.StaleAfterDays`). Aparece como total "Con pendientes de 3+ días", filtro `stale_only` y la fecha en rojo. Es una alerta visual en el panel; no hay notificación push.
- **Reenviar pendientes:** `POST /superadmin/fiscal/tenant-summary/:id/resend-pending` (permiso `fiscal.retry`, solo tenants activos, enfriamiento de 60 s, auditado como `fiscal_tenant_resend_pending`). Toma hasta 100 ventas electrónicas pending/error con más de 10 min, las más antiguas primero; por cada una sincroniza antes con el facturador (si SUNAT ya la aceptó solo actualiza el estado) y luego reencola con `EnqueueSendToSUNAT` (mismos candados que el reenvío manual del tenant). Nunca toca notas de venta ni rechazados por SUNAT.
- Probado en local: reenvío de 2 comprobantes del tenant demo (quedan 27 para otra pasada), segundo intento bloqueado por el enfriamiento, tenant suspendido rechazado con 409.
