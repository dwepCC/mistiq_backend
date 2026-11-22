# Fase 10 — Plan: volumen de comprobantes por tenant y control de validación semanal

**Fecha:** 2026-09-22
**Precede a:** implementación (pendiente de autorización explícita — este documento es solo análisis + propuesta, no se tocó código).
**Pedido original del usuario:** "necesito ver cuántos comprobantes está emitiendo un tenant en un rango de fechas, y llevar un control de validación semanal, junto con lo que ya agregamos de documentos atendidos/no atendidos."
**Regla seguida:** igual que en la Fase 0 — ninguna afirmación es una suposición, todo está verificado contra el código actual con cita `archivo:línea`. Donde hace falta una decisión de producto (no técnica), se deja explícita en la sección 6 en vez de asumirla.

---

## 1. Resumen ejecutivo

Hay dos pedidos distintos que conviene resolver con la **misma infraestructura** en vez de dos features separadas, porque comparten la misma fuente de datos (`fiscal_documents` en `facturador_lycet`) y el mismo eje temporal (semana):

1. **Volumen por tenant/rango de fechas** — hoy es **parcialmente posible pero incompleto e inconsistente** (sección 2). El filtro por tenant + fecha ya existe en `/fiscal`, pero el total del período nunca se muestra, no hay desglose por tipo de comprobante, y el ranking "quién emite más" ignora el rango de fechas por completo (bug de inconsistencia real, no solo ausencia de feature — ver Hallazgo H1).
2. **Control de validación semanal** — **no existe nada hoy**, ni tabla ni endpoint ni vista. Se construye sobre lo que ya existe de "atendido" (`countUnattendedNeedingAction()`, Fase 1 de este mismo trabajo) agregando antigüedad y una marca de "semana revisada".

La propuesta (sección 4) evita crear infraestructura nueva donde ya hay una que sirve: reutiliza `globalStats()`/`countByStatus()`/`countByTenant()` (ya aceptan `tenant_slug`/`from`/`to`) extendiéndolos en vez de duplicarlos, y usa el **audit log ya existente** de `backend_principal` para la marca de "semana revisada" en lugar de una tabla nueva — cero migraciones nuevas en `backend_principal`, una migración pequeña opcional en `facturador_lycet` solo si se aprueba el desglose por antigüedad con índice dedicado (sección 4.2).

---

## 2. Estado actual — qué ya existe (verificado, no supuesto)

### 2.1 Filtrado y conteo por tenant + rango de fechas: existe, pero incompleto

- `GET /api/v1/fiscal/stats` (`facturador_lycet/src/Controller/v1/FiscalController.php:187-211`) → `FiscalDocumentDetailService::globalStats($tenantSlug, $from, $to, $tenantId)` (`FiscalDocumentDetailService.php:88-138`). Ya acepta `tenant_slug`/`from`/`to` y hace **`total = array_sum($counts)`** donde `$counts` viene de `countByStatus($tenantSlug, $from, $to)` (`FiscalDocumentRepository.php:124-147`, que SÍ respeta `from`/`to`).
- **El frontend nunca muestra ese `total`.** `FiscalDocumentsPage.tsx:304-315` (las 8 tarjetas KPI: Hoy/Aceptados/Rechazados/Pendientes/En cola/Errores/Retry/Emails pend.) no incluye ninguna tarjeta de total — el dato ya llega en la respuesta de `getStats()` (`fiscal.service.ts:111-112`) y se descarta. Esto significa que "¿cuántos comprobantes emitió este tenant en este rango?" **ya es técnicamente respondible hoy con cero cambios de backend**, solo falta pintarlo.
- La tarjeta **"Hoy"** (`documents_today`) es engañosa cuando hay un rango de fechas distinto seleccionado: `globalStats()` la calcula SIEMPRE contra "hoy a medianoche" (`FiscalDocumentDetailService.php:103-113`), ignorando el `from`/`to` elegido por el operador. Si filtras "01-15 de agosto" y ves "Hoy: 3", ese 3 es de HOY, no del rango — confuso, aunque no es un bug nuevo (preexistente, nunca reportado hasta ahora porque nadie miraba el total del rango).

### 2.2 Ranking por tenant: existe, pero **ignora el rango de fechas por completo** (inconsistencia real)

- `countByTenant(?string $tenantSlug = null, int $limit = 50)` (`FiscalDocumentRepository.php:178-196`) — la función que alimenta `stats.tenants` en la respuesta de `/fiscal/stats` — **no tiene parámetros `from`/`to`**, a diferencia de `countByStatus()` en el mismo archivo, 30 líneas arriba, que sí los tiene. `globalStats()` la llama sin pasarle fechas (`FiscalDocumentDetailService.php:136`).
- **Consecuencia concreta:** si filtras `/fiscal` por "última semana", los KPI de estado (Aceptados/Rechazados/etc.) sí reflejan esa semana, pero si alguna vista llegara a usar `stats.tenants` para un ranking, mostraría el histórico completo de cada tenant, no la semana — dos partes de la MISMA respuesta con dos alcances de tiempo distintos. Es un bug de consistencia latente, no solo una ausencia de feature.
- No existe ningún endpoint ni método que agrupe por **tipo de comprobante** (factura/boleta/nota/guía) en absoluto, con o sin fecha. `document_type` es un filtro válido en `GET /documents` (uno a la vez, vía `createFilteredQuery()`, `FiscalDocumentRepository.php:296-298`), pero no hay `GROUP BY document_type`.

### 2.3 "Atendido"/"no atendido": ya construido (Fase 1-3 de este mismo trabajo, 2026-09-22), reutilizable para la validación semanal

- `countUnattendedNeedingAction(?string $tenantSlug = null): int` (`FiscalDocumentRepository.php:155-173`) — cuenta documentos en estado terminal (`error`/`rejected`/`observed`/`cancelled`) con `attended = false`. Ya expuesto en `list()` como `unattended_needing_action` (`FiscalController.php`, ver commit `37564ee`).
- **Lo que le falta para servir de base al control semanal:** (a) desglose por tenant (hoy es un solo número global o filtrado a un tenant a la vez, no una tabla "todos los tenants con su backlog"), (b) antigüedad (no distingue "lleva 2 días sin atender" de "lleva 40 días sin atender" — para una cadencia semanal, esa distinción es el dato que realmente importa).

### 2.4 Lo que NO sirve para esto (para no confundir conceptos, verificado)

- **`pkg/saas/docusage` en `backend_principal`** (`quota.go`, `types.go`) trackea consumo de **cupo del plan de facturación** (`SaasElectronicDocumentUsage`, `SaasDocumentQuotaPeriod` en `pkg/database/saas_documents.go`), no comprobantes SUNAT reales — cuenta "intentos de emisión" contra un período mensual de facturación (`QuotaPeriodEnd`), no un rango de fechas arbitrario, y vive en la BD central de `backend_principal`, no en `facturador_lycet`. Es un sistema legítimo pero **responde una pregunta distinta** ("¿cuánto cupo le queda a este tenant este mes?", no "¿cuántos comprobantes reales emitió entre el 1 y el 15?"). Se menciona en la sección 5 como cruce opcional a futuro, no como fuente de datos de esta fase.
- **"Resumen diario de boletas"** (mencionado en memoria como posible funcionalidad redundante) es el envío obligatorio a SUNAT de resúmenes de boletas (`CreateSummary()`, `backend_principal/internal/billing/service/billing_service.go:1091`), **no tiene nada que ver** con reportar volumen al panel central — es cumplimiento fiscal, no observabilidad administrativa. No se toca en esta fase; si sigue vigente como pendiente, es un tema aparte.
- No existe ningún permiso `fiscal.report`/`fiscal.export` en el catálogo RBAC (`backend_principal/pkg/database/sa_rbac_seed.go:80-84`) — hace falta uno nuevo si se quiere separar "ver volumen/backlog" de "ver documentos individuales" (ver sección 4.4).

---

## 3. Interpretación del pedido y alcance propuesto

Interpreto el pedido en dos partes que se construyen la una sobre la otra:

- **Parte A — Volumen:** una vista donde, para un tenant (o para todos, ranking) y un rango de fechas, se vea cuántos comprobantes se emitieron, con desglose por tipo (factura/boleta/nota/guía) y por resultado (aceptados vs. el resto). Esto es principalmente **completar y corregir** lo que ya existe (sección 2.1-2.2), no construir desde cero.
- **Parte B — Validación semanal:** un proceso repetible donde alguien, cada semana, revisa cuántos documentos siguen "no atendidos" por tenant, con antigüedad, y deja constancia de que se hizo esa revisión (para poder responder "¿cuándo fue la última vez que se revisó esto?" sin tener que confiar en la memoria de alguien). Esto es **nuevo**, pero se apoya en el trabajo de "atendido" ya hecho.

No interpreto "control de validación semanal" como una automatización que fuerza acciones (por ejemplo, auto-cancelar documentos viejos) — sería una decisión de negocio de alto impacto que no se pidió y que no debería quedar implícita en un documento de análisis. Si esa lectura es la correcta, decírmelo antes de la implementación (ver sección 6).

---

## 4. Propuesta de solución

### 4.1 Parte A — Volumen por tenant y rango de fechas

**Backend (`facturador_lycet`), cambios quirúrgicos sobre lo existente, sin endpoints nuevos:**

1. `FiscalDocumentRepository::countByTenant()` gana `?\DateTimeInterface $from = null, ?\DateTimeInterface $to = null` (mismo patrón exacto que `countByStatus()`, 30 líneas arriba en el mismo archivo). `globalStats()` le pasa `$from`/`$to`. Con esto, `stats.tenants` deja de ser inconsistente con el resto de la respuesta (corrige el hallazgo de la sección 2.2) — y de paso, ranking de volumen por tenant en el rango, gratis.
2. Nuevo método `FiscalDocumentRepository::countByType(?string $tenantSlug, ?\DateTimeInterface $from, ?\DateTimeInterface $to): array` — copia literal de `countByStatus()` cambiando `GROUP BY d.status` por `GROUP BY d.documentType`. `globalStats()` agrega `by_type` al array de retorno.
3. `documents_today` dejar de calcularse siempre contra "hoy" cuando el caller ya pasó un `from`/`to` explícito — o, más simple y con menos riesgo de romper otros callers (`FiscalOperationsService::summary()` también usa `globalStats()`), **renombrar en el frontend** a algo que dejе claro que es independiente del filtro ("Hoy (global)") en vez of tocar la semántica del backend. Prefiero esta segunda opción — cambiar el significado de un campo que ya consume otro caller es más riesgo del que amerita un problema cosmético.

**Backend (`backend_principal`): cero cambios.** `GET /superadmin/fiscal/stats` ya reenvía todo el query string (`collectQuery(c)`, `fiscal_handler.go:162-168`) y el body de la respuesta tal cual — los campos nuevos (`by_type`, `tenants` ya corregido) llegan al frontend sin tocar Go.

**Frontend (`frontend_central`):**

1. `FiscalStats` (`fiscal.service.ts:3-17`) gana `by_type: Record<string, number>`.
2. Tarjeta KPI nueva "Total del período" en `FiscalDocumentsPage.tsx` usando `stats.total` (ya llega, nunca se pinta — ver 2.1).
3. Mini-tabla "Por tipo de comprobante" junto a las tarjetas KPI (factura/boleta/nota crédito/nota débito/guía — traducir `document_type` con una tabla de labels nueva en `fiscalStatus.ts`, mismo patrón que `sendModeLabel`/`emailStatusLabel`).
4. Cuando **no** hay `tenant_slug` seleccionado: mostrar `stats.tenants` (ya corregido para respetar el rango) como una tabla "Volumen por tenant" ordenada de mayor a menor — responde directamente "qué tenant está emitiendo más en este rango".

**Esfuerzo estimado:** bajo. Sin migración, sin endpoint nuevo, 2 métodos de repositorio + ~80-120 líneas de frontend. Es la parte de mayor retorno por menor esfuerzo de todo este documento.

### 4.2 Parte B — Control de validación semanal

**Backend (`facturador_lycet`):**

1. `FiscalDocumentRepository::countUnattendedNeedingAction()` se extiende (o se agrega un método hermano `unattendedBacklogByTenant()`) para devolver, por tenant, un desglose por antigüedad usando `d.updatedAt` (mismo campo que ya tiene índice compuesto `(status, updated_at)` desde la Fase 7 — `idx_fiscal_doc_status_updated`, así que esta query no necesita migración nueva si se reutiliza ese índice):
   - `0-7 días`, `8-14 días`, `15-30 días`, `+30 días` (umbrales exactos a confirmar, sección 6).
2. Exponer esto en `FiscalController::list()` o como parte de un `GET /fiscal/stats` extendido con `?group_by=attendance_backlog` — technically se puede reutilizar `/fiscal/documents` con `unattended_only=true&group_by_tenant=true`, pero para no sobrecargar un endpoint que ya hace mucho, propongo un método nuevo y pequeño en `FiscalOperationsService` (mismo patrón que `tenantsTable()`/`queueMonitor()`) en vez de forzarlo dentro de `list()`.

**Backend (`backend_principal`) — la parte nueva de verdad:**

1. Nuevo endpoint `POST /superadmin/fiscal/weekly-review` (`fiscal_handler.go`, mismo patrón que el resto del archivo): toma un snapshot del backlog actual (llamando al endpoint de la sección anterior en `facturador_lycet`) y lo persiste como un `database.AuditLog` (`Action: "fiscal_weekly_review"`, `Payload`: JSON con el snapshot completo por tenant/antigüedad, `UserID` del superadmin que lo marcó) — **reutiliza la tabla de auditoría que ya existe**, cero migración nueva.
2. Nuevo endpoint `GET /superadmin/fiscal/weekly-review/history` — lista las últimas N revisiones (query simple sobre `AuditLog` filtrando por `Action`), para poder ver la tendencia ("hace 3 semanas había 80 pendientes, la semana pasada 45, esta semana 30").
3. Nuevo permiso `fiscal.weekly_review` en el catálogo (`sa_rbac_seed.go`) — separado de `fiscal.attend` porque son decisiones de granularidad distinta (una es "este documento puntual está resuelto", la otra es "certifico que revisé el backlog completo de la semana"); alguien podría tener uno sin el otro (ej. Soporte podría necesitar ver el backlog sin poder "cerrar" la semana).

**Frontend (`frontend_central`):**

1. Sección nueva "Validación semanal" — puede vivir como pestaña dentro de `FiscalDocumentsPage.tsx` o como bloque en `OperacionesFiscalesPage.tsx` (inclino por esta última: ya es la pantalla de observabilidad/seguimiento operativo, mientras que `FiscalDocumentsPage` es más de gestión documento-por-documento — separar "gestionar documentos" de "supervisar el proceso" mantiene la distinción que ya se pidió explícitamente para "atendido": *"no confundir con los estados técnicos"*).
2. Tabla por tenant: backlog total, desglose por antigüedad, fecha de la última revisión marcada (si existe) y quién la hizo.
3. Botón "Marcar semana revisada" (con confirmación, mismo patrón que "Forzar") que llama al `POST` de arriba y muestra el snapshot que acaba de quedar registrado.
4. Historial simple (lista, no gráfico en v1) de revisiones pasadas por tenant.

**Esfuerzo estimado:** medio. 1-2 métodos nuevos en `facturador_lycet` (sin migración si se reutiliza el índice de Fase 7), 2 endpoints + 1 permiso nuevo en `backend_principal` (sin migración, reutiliza `AuditLog`), 1 sección de UI nueva razonablemente autocontenida en `frontend_central`.

### 4.3 Qué NO se propone tocar en esta fase

- No se propone ninguna acción automática sobre documentos viejos (auto-cancelar, auto-marcar-atendido, etc.) — ver sección 3.
- No se propone cruzar esto con `docusage`/cuotas de plan todavía — ver sección 5 como posible fase futura, no ahora.
- No se toca el "resumen diario de boletas" — es un tema distinto (cumplimiento SUNAT, no observabilidad), sigue pendiente de confirmación aparte según la nota de memoria previa.

### 4.4 Permisos

- Lectura del volumen (Parte A): reutilizar `fiscal.view` — ya existe, es de solo lectura, encaja exactamente con lo que ya cubre (ver comprobantes).
- Lectura del backlog/antigüedad (Parte B, tabla): también `fiscal.view` — es observación, no acción.
- Marcar "semana revisada" (Parte B, escritura): permiso nuevo `fiscal.weekly_review`, no reutilizar `fiscal.attend` (razón en 4.2).

---

## 5. Mejoras adicionales propuestas (no pedidas explícitamente, pero relacionadas)

No me ciño solo al pedido original — esto es lo que un ingeniero de procesos agregaría a esta misma inversión, en orden de prioridad:

1. **Exportar a CSV** la tabla de volumen por tenant/tipo y el snapshot de cada revisión semanal — el "control" semanal probablemente termina compartiéndose fuera del panel (email, reunión), y hoy no hay forma de sacar esos datos sin capturas de pantalla. Costo bajo (ya hay un patrón de descarga autenticada en `downloadFiscalFile`, se puede replicar generando CSV en el propio frontend a partir de la respuesta JSON, sin tocar backend).
2. **Tarjeta de backlog en el Dashboard principal** (`DashboardPage.tsx`) — un número "N documentos fiscales sin atender en toda la flota" visible sin entrar a `/fiscal`, mismo espíritu que las tarjetas de "Suspendidas"/"Inactivas" que ya se agregaron esta sesión para el negocio SaaS. Hace que el backlog sea visible por defecto, no solo para quien recuerda ir a mirarlo.
3. **Alerta automática de backlog** (Fase 4, opcional, requiere decisión de umbral) — nuevo `detectAttendanceBacklog()` en `FiscalAlertService.php` (mismo patrón que `detectQueueSaturation()`/`detectConsecutiveErrors()`, ya existentes), que dispara si: (a) el backlog de un tenant supera un umbral, o (b) pasaron más de 8 días desde la última "semana revisada" marcada. Esto convierte el control semanal de "hay que acordarse de entrar a mirar" a "el sistema avisa si nadie lo revisó" — pero necesita que el usuario decida los umbrales antes de construirlo, así que lo dejo como fase separada, no bloqueante para las partes A y B.
4. **Cruce con cuota de documentos (`docusage`)** (futuro, mayor alcance) — mostrar junto al volumen real de SUNAT el `%` de cupo del plan consumido (`DocumentUsageView.UsagePercent`, ya existe) para el mismo tenant, detectando tenants que van a agotar su cupo antes de fin de mes. Es información valiosa pero cruza un sistema de billing con uno de compliance fiscal — mayor superficie de cambio, lo dejo fuera de esta fase.

---

## 6. Decisiones que necesito antes de implementar

1. **¿Qué cuenta como "comprobante emitido" para el volumen?** ¿Todos los estados (incluye rechazados/error, refleja carga operativa real) o solo los aceptados por SUNAT (refleja emisión legal válida)? Propongo mostrar ambos (total + aceptados como subconjunto, ya lo permite `by_status`) en vez de forzar una sola definición — confirmar si es suficiente o si se necesita elegir uno solo para simplificar la UI.
2. **Umbrales de antigüedad** para el desglose del backlog semanal: propongo 0-7 / 8-14 / 15-30 / +30 días — confirmar o ajustar.
3. **¿La "revisión semanal" es 100% manual** (alguien entra y hace clic) **o quieres además un recordatorio/alerta automática** si nadie la marcó en más de N días? Propongo empezar manual (sección 4.2) y dejar la alerta como mejora 5.3, separada.
4. **¿Dónde vive la sección de validación semanal**: dentro de "Documentos fiscales" o dentro de "Operaciones fiscales"? Propongo Operaciones Fiscales (razón en 4.2) — confirmar.
5. **Exportar a CSV** (mejora 5.1): ¿se incluye en esta misma fase o queda para después?

---

## 7. Orden de implementación sugerido (si se aprueba)

1. Parte A completa (bajo esfuerzo, alto retorno, sin dependencias de decisiones pendientes salvo la #1 de la sección 6).
2. Parte B — backend (`facturador_lycet` + `backend_principal`), sin frontend todavía, verificable con tests igual que el resto de este trabajo.
3. Parte B — frontend.
4. Mejoras 5.1 y 5.2 (bajo esfuerzo, se pueden intercalar en cualquier punto).
5. Mejora 5.3 (alerta automática) y 5.4 (cruce con cuotas) — fases separadas, después de validar que las partes A y B se usan y sirven en la práctica.
