# Agente IA comercial (Bendey) — análisis de arquitectura y plan de implementación en Mistiq

> **Origen**: análisis del código real de `D:\bendey_saas\backend_go` (`pkg/agent/*`, `internal/agent/*`) y `D:\bendey_saas\front_central` (módulo `assistant`), hecho el 2026-09-20 y **re-auditado** contra el código una segunda vez (dos pasadas de verificación independientes, backend y frontend) tras una primera versión de este documento que resultó tener omisiones y un punto incorrecto. Bendey es el proyecto predecesor de Mistiq (mismo origen que el naming legacy `tukifac`/`bendey` que venimos limpiando en este repo); su chatbot comercial **no existe todavía en Mistiq**.
>
> **Alcance confirmado con el dueño del producto**: esto se implementa **solo para `mistiq_backend` + `mistiq_central`** (el equivalente exacto de lo que Bendey hace hoy: un agente de ventas que vende el propio SaaS por WhatsApp/web, pide pagos manuales, da de alta tenants). **No** es para que cada tenant tenga su propio agente — esa extensión queda con la puerta abierta en el esquema (`owner_kind`/`owner_tenant_id`) pero fuera de este trabajo. Ver §5.
>
> **Este documento pasó por una auditoría explícita de fidelidad** (releer el código real dos veces más, sin confiar en el resumen previo) porque el objetivo es que una implementación basada en él funcione **igual** que el original, no solo "parecido". Las secciones marcadas "Detalle exacto verificado" contienen valores numéricos y comportamientos confirmados letra por letra contra el código — no aproximarlos al portar.

---

## 0. Resumen ejecutivo

Bendey tiene un **agente comercial conversacional** (WhatsApp + chat web público) que:

- Corre un bucle **ReAct** (LLM → tools Go → LLM) contra un proveedor intercambiable (OpenAI/DeepSeek), con **RAG** propio sobre MySQL (coseno en Go, sin motor vectorial) y **acciones** (function calling) que crean leads, agendan demos, validan RUC, crean tenants de prueba/pagados.
- Es **asíncrono vía cola Redis** para WhatsApp, pero **completamente síncrono y sin cola** para el chat web público — son dos rutas de concurrencia distintas, no la misma con "menos pasos" (ver §2.9).
- **Sin Redis, WhatsApp no degrada "con cuidado" — pierde por completo dedup, lock de conversación y rate-limit** (los tres se vuelven no-ops), no solo la cola. Esto es una corrección importante sobre lo que decía la primera versión de este documento.
- Tiene un panel de administración completo en `front_central`, con lógica de negocio no trivial (pagos manuales, deep-linking de notificaciones, manejo cuidadoso de secretos en el formulario de config) que hay que preservar explícitamente, no solo el layout de las pantallas.
- Hoy es **operativamente single-tenant**: un único "asistente de plataforma" que vende Bendey mismo. **Este es exactamente el alcance a implementar en Mistiq** (vender Mistiq, desde `mistiq_central`) — no un agente por tenant. Ver §5.

Todo el sistema vive en **un solo binario Go** (motor + HTTP + WS + cron) más un módulo de React en el panel admin. No hay microservicios ni colas externas más allá de Redis.

---

## 1. Arquitectura general

```
Cliente final (WhatsApp) ──▶ Webhook Meta (HMAC condicional) ──▶ [cola Redis] ──▶ Worker ──▶ Orchestrator (ReAct)
Visitante web (widget) ────▶ POST /api/public/assistant/chat (100% síncrono, SIN cola) ──▶ Orchestrator (ReAct)
                                                                                  │
                                                                    ┌─────────────┼─────────────┐
                                                                    ▼             ▼             ▼
                                                              LLM Provider   RAG (MySQL)    Acciones (tools)
                                                             (OpenAI/DeepSeek) coseno en Go   (Go, function-calling)
                                                                    │
                                                                    ▼
                                                     Respuesta ── persistida (MySQL) ── enviada por el canal
                                                                    │
                                                                    ▼
                                              Notifica al panel (WS real + SSE + Web Push) ──▶ front_central
                                                                                          - Bandeja en vivo (toma/responde/cierra)
                                                                                          - Config / Knowledge / Analytics / Test
```

Piezas de infraestructura:

| Pieza | Uso | Qué pasa exactamente si falta |
|---|---|---|
| **MySQL** (GORM) | Persistencia permanente de todo | Obligatoria, no degrada |
| **Redis** | Cola WhatsApp, memoria caliente, rate-limit, dedup, lock por conversación, pub/sub entre instancias | **No es una degradación parcial**: sin Redis, WhatsApp procesa inline SIN dedup, SIN lock de conversación y SIN rate-limit (los tres son no-ops, no versiones reducidas). La memoria cae a solo MySQL. Los hubs de tiempo real caen a "solo entrega local" (una instancia no se entera de eventos de otra) |
| **OpenAI / DeepSeek API** | Chat + embeddings | Intercambiable en caliente; sin fallback automático si falla. DeepSeek sin clave OpenAI de respaldo deja el RAG completo deshabilitado (el chat sigue andando, pero sin contexto de conocimiento) |
| **WhatsApp Cloud API (Meta)** | Canal WhatsApp | Directo contra Graph API, sin Twilio/360dialog |
| **Web Push (VAPID)** | Notificar asesores con el panel cerrado | Deshabilitado limpio si faltan las llaves; requiere Service Worker propio para funcionar en Android/Chrome |

---

## 2. Motor del agente

### 2.1 Flujo de un mensaje — **orden exacto verificado, corregido respecto a una versión anterior de este documento**

1. **Entrada**: `agent.Inbound{AssistantID, Channel, ChannelAccountID, ChannelMsgID, From, Text, Locale, ReceivedAt}`.
2. **WhatsApp**: webhook valida firma HMAC **solo si hay `AppSecret` configurado** (si no hay, acepta cualquier payload sin validar nada — no hay "rechazar por defecto"), encola en Redis (`RPush` a `assistant:inbound:queue`), responde `200` de inmediato. *N* workers (`ASSISTANT_QUEUE_WORKERS`, default **2**) hacen `BRPopLPush`. Sin Redis: `go q.process(in)` inline, **sin ninguna de las tres protecciones de abajo**.
3. **Chat web público**: el handler HTTP corre el pipeline **100% síncrono, sin pasar por la cola en absoluto** — ni siquiera cuando hay Redis. Solo comparte el rate-limit por remitente con WhatsApp; **no tiene lock de conversación ni dedup**. Dos envíos casi simultáneos de la misma sesión pueden ejecutar el orquestador en paralelo sin coordinación (riesgo real de respuestas cruzadas que WhatsApp sí evita cuando hay Redis).
4. Antes de procesar un mensaje de WhatsApp (con Redis disponible): **lock por conversación** (`cronlock`, TTL 100s) + **dedup** por `ChannelMsgID` (TTL 24h, Meta reintenta webhooks) + **rate-limit** por remitente (**15/min** por defecto, `ASSISTANT_RATE_LIMIT_PER_MIN`).
5. **`Orchestrator.Handle` — orden real** (no el que sugería una versión anterior de este documento):
   1. Resuelve `Config` del asistente.
   2. `GetOrCreate` de la conversación.
   3. **Cortocircuito de "saludo desnudo"**: si la conversación es nueva y el texto entrante es *solo* un saludo, responde con un saludo fijo **sin tocar el LLM ni el RAG en absoluto** (costo cero). Si en cambio ya trae una consulta real, marca `FirstTurn` para que el modelo se presente en la misma respuesta.
   4. **Persiste SIEMPRE el turno del usuario primero** — el propio código lo dice explícito: se guarda "aunque responda un humano". **Recién después** de persistir, si `conv.Status != "bot"` (esto incluye `human`, `needs_human` **y también `closed`**, no solo los dos primeros), responde `Silent: true` y no genera nada más. **Importante para el port**: si se invierte este orden (chequear el estado antes de guardar), los mensajes que el cliente escribe mientras un humano lleva la conversación dejan de guardarse — el asesor humano pierde visibilidad de lo que el cliente dijo mientras esperaba.
   5. Carga historial: ventana de **20 turnos**, sin resumen/compactación, solo recorte. `buildMessages` omite del envío al LLM los turnos de rol `tool` (auditoría interna) y mapea `agent` (respuesta humana) a `assistant` para que el modelo entienda el contexto de un handoff previo.
   6. RAG (ver §2.3).
   7. Arma el system prompt por secciones (ver §2.2).
   8. Corre ReAct contra el LLM.
   9. Persiste el turno de respuesta con tokens reales (nunca estimados).
6. **ReAct**: llama al LLM; si trae `tool_calls`, las ejecuta en Go una por una, reinyecta como `role=tool`, vuelve a llamar — hasta `MaxRounds` (default de fábrica **3** a nivel motor; el tipo de agente comercial de Bendey lo sube a **4**).

### 2.2 Prompt por secciones ordenadas

Orden exacto (`Order` 10/20/30/40/50/60/70/80): `identity`, `personality`, `rules` (**protegida**), `policies` (**protegida**), `temporal`, `first_turn`, `outreach`, `knowledge`. Las secciones protegidas se re-inyectan automáticamente aunque un `Composer` a medida no las declare — no es que se ignore un campo vacío, es un mecanismo activo de reinyección. El renderizado omite secciones vacías sin dejar separadores sueltos. **La sección `temporal` usa una zona horaria fija (Perú, UTC-5, sin horario de verano) hardcodeada** — dato de negocio a decidir explícitamente al portar (¿zona fija de Mistiq, o por tenant en el futuro?).

### 2.3 RAG — MySQL + coseno en Go

- Trocea en párrafos de **~800 caracteres**, embebe con el proveedor configurado, guarda el embedding como JSON en columna de texto.
- Trae hasta **500 candidatos** del scope `(assistant_id, kb_id)`, calcula coseno en memoria del proceso, filtra por score mínimo **0.30**, corta a *top-K* (**8** en el flujo real, aunque el default interno del método sin argumento explícito es 5).
- **Al llegar al tope de 500 candidatos, no falla ni avisa al cliente**: puntúa un subconjunto arbitrario (sin `ORDER BY` estable) y solo deja un log de warning — degradación silenciosa que puede hacer que el bot "no encuentre" contenido que sí existe, sin causa visible desde afuera.
- Si falla el embedding de la consulta, cae a búsqueda por palabra clave (`LIKE`).
- Diseño v1 deliberadamente simple, pensado como *interfaz* migrable a un motor vectorial real después — buen patrón a copiar tal cual.

### 2.4 Acciones (function calling) — pipeline y valores exactos

Interfaz: `Name()`, `Description()`, `Schema()`, `Meta()` (`Sensitive`, `ReadOnly`, `Idempotent`, `RequiredPermission`), `Execute(ctx, bizctx, args)`.

Pipeline invariante: catálogo permitido → políticas previas → ejecución (**timeout 20s**, recuperación de pánico, **hasta 2 reintentos** solo si el error es transitorio **e** idempotente, backoff `200ms × (intento+1)`) → políticas de resultado → truncado a **4000 caracteres** antes de reinyectar al prompt.

**Clasificación de errores — pieza central no cosmética**: `ClassFatal | ClassValidation | ClassBusiness | ClassTransient`. Regla dura: para `Fatal`/`Transient` **nunca** se deja pasar el texto real del error al modelo (mensaje genérico fijo); solo `Validation`/`Business` exponen su texto porque fue escrito por el programador de la acción para consumo humano/del modelo. **Sin este mecanismo, el port o filtra errores técnicos internos al cliente final (fuga de información) o pierde la capacidad de que el modelo corrija un argumento inválido en vez de derivar a un humano innecesariamente.**

Acciones reales del agente comercial: crear/calificar lead, agendar demo/capacitación, consultar planes/apps/demo, notificar al equipo (email), pedir handoff, validar RUC, **crear tenant de prueba** (`Sensitive`, kill-switch propio default **desactivado**, lock distribuido por RUC TTL **30s**), entregar instrucciones de pago manual, **crear tenant + suscripción pagada** (`Sensitive`), reportar pago enviado (**no** es `Sensitive`, a diferencia de las dos anteriores), consultar estado de pago.

**Detalle no trivial**: `create_trial_tenant` y `create_tenant_and_subscribe` **comparten la misma clave de lock por RUC** (no dos locks independientes) — es lo que cierra la carrera cruzada entre el camino de prueba gratuita y el de contratación paga para el mismo RUC. Si se portan como dos acciones con locks separados, se reabre esa ventana de doble-alta.

Además, hay un mecanismo de **auditoría separada del mensaje al cliente**: una acción puede devolver al modelo un mensaje con datos sensibles reales (p. ej. la contraseña de una cuenta recién creada) mientras el registro de auditoría de negocio guarda un resumen **sin** ese secreto — separación deliberada para no filtrar credenciales en logs/paneles.

### 2.5 Políticas — valores exactos

Orden fijo: `ScopeIntegrity → Toolset → Confirmation → RateLimit` (verificado además por un test de arquitectura propio).

- **Confirmation**: ventana de **12 turnos**; busca el turno `tool` más reciente con el marcador `[confirmación-requerida]` para esa herramienta exacta, y **solo** el/los turnos de usuario inmediatamente posteriores cuentan como respuesta; la lista de palabras afirmativas válidas es **cerrada, ~27 términos**, mensajes de más de 5 palabras o que no matcheen invalidan la confirmación (hay que repetirla). Aplica solo a acciones `Sensitive`.
- **RateLimit**: **120 ejecuciones/hora por conversación, en memoria y por proceso** — con múltiples réplicas del backend el tope efectivo se multiplica por instancia (advertencia explícita en el propio código: no es exacto en multi-instancia).

### 2.6 Proveedores LLM

Interfaz `Chat/Embed/Name`. OpenAI nativo; DeepSeek reutiliza el cliente OpenAI con otro *base URL* (sin `/embeddings` propio — delega a una clave OpenAI de respaldo si existe; sin ella, **el RAG queda completamente deshabilitado aunque el chat siga funcionando**, degradación parcial silenciosa). Selección por `switch` simple sobre el valor en BD, sin fallback automático entre proveedores. `Reload()` recarga clientes en caliente al guardar config. **El resolver de configuración tiene caché TTL de 5 minutos** — se invalida correctamente al guardar desde el panel, pero cualquier cambio hecho por otra vía (script, migración manual) puede tardar hasta 5 min en reflejarse.

### 2.7 Memoria de conversación

Redis (caliente, ventana con TTL **30 min**, hasta **50 turnos**) + MySQL (permanente) detrás de un único puerto; sin Redis, solo MySQL. Sin resumen — solo recorte a los últimos N turnos. **Detalle importante**: al rehidratar desde MySQL tras un *miss* de Redis, la reconstrucción del turno **pierde campos** que sí viajan cuando la fuente es Redis (`ChannelMsgID`, contadores de tokens) — inconsistencia de datos entre caché caliente y fuente fría a tener en cuenta si se porta tal cual (p. ej. la función de "citar un mensaje" del panel depende de `ChannelMsgID`).

### 2.8 Canal WhatsApp

Cloud API oficial de Meta (Graph API), no Twilio. Firma HMAC del webhook **condicional a que el `AppSecret` esté configurado** (ver §2.1 — no es una garantía incondicional). Verificación de token de suscripción. Normalización de listas/botones interactivos. Plantillas aprobadas para fuera de la ventana de 24h. Adaptación de markdown al formato de WhatsApp. Límites de la lista interactiva respetados por código (botón ≤20, título de fila ≤24, descripción ≤72, máx. 10 filas). **No maneja medios entrantes** (imagen/audio se ignoran).

**Detalle de producto no trivial (BSUID)**: desde que Meta permite a un cliente ocultar su número ("WhatsApp Usernames"), el webhook puede mandar un identificador alternativo (`from_user_id`) en vez del número. Ese identificador se usa con un prefijo propio en **todo** el pipeline (referencia de contacto, memoria, lock de conversación), y el envío de respuesta usa un campo distinto de la API (`recipient` en vez de `to`). Si se omite este caso al portar, los clientes que ocultan su número comparten por error la misma llave de bloqueo de conversación entre sí, y los envíos de respuesta a ellos fallan.

### 2.9 Cola — dos rutas de concurrencia distintas, no una

**Corrección importante**: WhatsApp y el chat web público **no comparten el mismo modelo de concurrencia**, aunque ambos pasen por el mismo orquestador.

- **WhatsApp** (con Redis): lista de trabajo + lista "processing" (`BRPopLPush`), dedup por `ChannelMsgID` (TTL **24h**), rate-limit **15/min** por remitente, *N* workers configurables (default **2**), lock por conversación (TTL **100s**), timeout total de procesamiento por mensaje **90s**. Si el lock está ocupado, el mensaje se re-encola tras dormir **200ms** (puede reordenar mensajes bajo contención). Sin reintentos automáticos en v1.
- **WhatsApp sin Redis**: las tres protecciones (dedup, lock, rate-limit) son **no-ops** — no versiones reducidas, directamente no existen. Cada webhook lanza una goroutine sin ninguna coordinación.
- **Chat web público**: nunca pasa por la cola, con o sin Redis. Solo tiene el rate-limit compartido; sin lock ni dedup propios.
- **Límites de entrada, distintos por canal**: WhatsApp trunca a **4000 caracteres**; el chat web público trunca a **2000** — ambos en silencio, sin avisar al remitente.

---

## 3. Capa HTTP / WebSocket / esquema de datos

### 3.1 Endpoints

| Grupo | Rutas | Auth |
|---|---|---|
| Webhook WhatsApp | `GET/POST /webhooks/assistant/whatsapp` | Pública + firma HMAC **condicional** (ver §2.8) |
| Chat público | `POST /api/public/assistant/chat`, `GET .../messages` (respaldo one-shot si el WS no conecta), `GET .../ws` | Pública, rate-limit por sesión |
| Panel — tiempo real | `GET /api/superadmin/assistant/ws` (WS real, registrado ANTES del middleware de auth del grupo porque el handshake no lleva headers), `GET .../sse` (el que usa el frontend por defecto) | JWT admin por query param en ambos |
| Panel — status | `GET /status` (`ready`, `llm_configured`, `whatsapp_enabled`) | JWT admin |
| Panel — bandeja | `conversations`, `.../messages`, `.../lead`, `.../take`, `.../reply`, `.../close`, `.../reopen`, `.../validate-payment` | JWT admin |
| Panel — config/knowledge/analytics/test/push | `GET/PUT config`, `GET/POST/DELETE knowledge` (+ `seed-defaults`), `GET analytics`, `GET metrics`, `POST test`, `GET/POST/POST push/*` | JWT admin |
| Outreach | `POST /api/superadmin/assistant/outreach` — **usado desde la pantalla de Tenants del panel, no desde el módulo Assistant** | JWT admin |

### 3.2 Dos hubs en tiempo real

- **`webchat` hub**: pub/sub por sesión de chat, **sin auth** (solo exige `session_id`), solo-push, sincroniza el hilo completo al conectar.
- **`panel` hub**: pub/sub global, payload mínimo `{conversation_id}`, el cliente vuelve a pedir por REST.
- Ambos se reparten entre instancias vía Redis Pub/Sub, y **ambos degradan a entrega solo local si Redis no está disponible o el publish falla** — con múltiples réplicas sin Redis, un asesor conectado a la instancia B no se entera de un evento generado en la instancia A.
- Parámetros de keepalive verificados: ping cada **25s**, idle timeout **70s**, write timeout **10s**, límite de lectura **4096 bytes** por frame WS; SSE con `retry: 3000` + ping cada 25s.

### 3.3 Validación de pago manual

Bendey opera con Yape/Plin manual, sin pasarela. Cuando el cliente **afirma** haber pagado, la acción del agente (que **no** es `Sensitive`) solo marca el lead como `payment_reported` y deriva a un humano — nunca valida sola. Un asesor confirma manualmente desde el panel, lo que escribe en BD quién y cuándo (`PaymentValidatedAt`/`PaymentValidatedBySAUserID`) — ese campo de auditoría solo lo escribe el endpoint humano, nunca una acción del agente.

**Matiz importante sobre la "garantía" de separación**: existe un test que impide que el código de este flujo importe paquetes de facturación/SUNAT/suscripciones — pero es literalmente un chequeo de imports prohibidos sobre **dos archivos fijos por nombre**, no una verificación estructural genérica de "todo el flujo de pago". Si el código se reorganiza a un tercer archivo, el test deja de proteger nada sin que nadie lo note. Al portar, si se quiere la misma garantía, conviene replicar el mecanismo sabiendo exactamente qué cubre y qué no.

### 3.4 Esquema de base de datos

6 tablas, gestionadas por `AutoMigrate` (sin `.sql` sueltos):

| Tabla | Propósito | Columnas clave |
|---|---|---|
| `assistants` | Config del asistente | `owner_kind`/`owner_tenant_id` (sin usar activamente), proveedor/modelo/credenciales LLM y embeddings, overrides de prompt, credenciales WhatsApp, `agent_type` (enlaza con el tipo de código, default `commercial`), `enabled_tools`, `max_tool_rounds` (override de tope de rondas por instancia), **`enable_trial_tenant`/`enable_paid_contract`** (kill-switches, default `false`) |
| `assistant_conversations` | Un hilo por contacto | `channel`, `contact_ref`, `status` (`bot\|needs_human\|human\|closed`), `assigned_sa_user_id`, `outreach_tenant_id`/`outreach_goal` (campaña saliente), `onboarding_json` (columna reservada, sin lógica aún) |
| `assistant_messages` | Cada turno | `role` (`user\|assistant\|tool\|agent`), `content`, `tool_name`/`tool_call_id`, `channel_msg_id`, `reply_to_id` (auto-referencia, sistema de citas), tokens reales |
| `assistant_leads` | Prospecto/oportunidad | `tenant_id` (puente lead→cuenta real), `kind`, `status` (incluye el flujo de pago manual: `payment_reported`, `payment_validated`, etc.), `payment_validated_at`/`payment_validated_by_sa_user_id` |
| `assistant_knowledge_chunks` | Corpus RAG | `kb_id` (scope, hoy = `assistant_id`), `content`, `embedding` (JSON en texto) |
| `assistant_push_subscriptions` | Web Push de asesores | `sa_user_id`, `endpoint`, claves VAPID |

---

## 4. Panel de administración (`front_central`, módulo "Assistant IA")

Cinco páginas bajo un grupo de menú colapsable, protegidas solo por "estar logueado" en el frontend (sin permiso granular propio ahí — si existe, vive en el backend).

### 4.1 Bandeja (`/assistant`) — más lógica de negocio de la que parece

Maestro-detalle: lista con pestañas por estado + buscador local (solo filtra lo ya cargado), hilo con burbujas por rol (cliente/asesor/IA/nota de herramienta), compositor deshabilitado salvo que la conversación esté en `human` (con mensajes distintos según sea `bot`/`needs_human` — "Pulsa Tomar conversación" — o `closed` — "Pulsa Reabrir"), métricas rápidas arriba.

**Piezas de negocio y de UX no triviales que hay que preservar, no solo el layout**:

- **Deep-linking desde notificaciones**: clic en una notificación de escritorio/push lleva a `/assistant?conv=<id>`; la página busca esa conversación, si no está en la pestaña activa **cambia automáticamente a "Todas"** para encontrarla, la abre y limpia el query param. Sin esto, el sistema de avisos no lleva a ningún lado útil.
- **Marcado de no leídos**: contador en memoria (no persiste, no viene del backend) que se incrementa cuando llega un mensaje de rol `user` y la conversación no está abierta+con foco de ventana; alimenta también el título de la pestaña del navegador (`(N) ...`).
- **Scroll inteligente**: si el asesor está cerca del fondo (<80px) autoscrollea con mensajes nuevos; si está leyendo mensajes viejos, no mueve el scroll y muestra un *pill* flotante "N mensajes nuevos" que al clic baja suave. Al cambiar de conversación, baja al fondo instantáneo.
- **Sistema de citas**: cualquier mensaje se puede "responder a" (`reply_to_id`), se envía al backend y se renderiza citado en la burbuja.
- **Compositor sensible al dispositivo**: en desktop, Enter envía (Shift/Ctrl/Cmd+Enter = salto de línea); en dispositivos táctiles Enter **siempre** hace salto de línea y hay un botón explícito de salto de línea.
- **Lógica de pago manual completa**: pill distinto si `needs_human_reason === "payment_reported"`, badge de `lead_status` persistente (`trial_registered`/`in_contract`/`payment_reported`/`payment_validated`), modal "Datos del cliente" con botón "Marcar pago como validado" (con confirmación explícita) y enlace directo a la ficha del tenant si ya está vinculado. Esto es lógica de negocio central del flujo comercial, no decoración.
- El endpoint de **outreach** (mensajería saliente proactiva) se dispara desde la pantalla de Tenants del panel, no desde este módulo — si se porta "Assistant IA" como unidad aislada, ese punto de entrada externo hay que preservarlo aparte.

### 4.2 Probar chat (`/assistant/test`)

Playground aislado: corre el pipeline real (RAG + LLM + acciones) con una sesión sintética separada de las conversaciones reales — endpoint propio, no toca datos ni métricas de producción.

### 4.3 Conocimiento (`/assistant/knowledge`)

CRUD de texto plano agrupado por título (sin carga de archivos); un "documento" en pantalla es en realidad varios fragmentos con el mismo título concatenados. Reingestar el mismo título **reemplaza** todo su contenido anterior. Al borrar, se borran todos los fragmentos del grupo en paralelo. Botón para sembrar el corpus por defecto (idempotente).

### 4.4 Configuración (`/assistant/config`) — el punto más delicado de portar

Formulario grande: datos generales, proveedor/modelo LLM + clave, proveedor de embeddings, credenciales WhatsApp + plantillas aprobadas, datos de demo, kill-switches de caminos comerciales, datos de pago manual, `max_tool_rounds`, overrides de prompt.

**Riesgo real y explícito si se porta sin este detalle**: los 5 campos de credenciales (API keys, tokens de WhatsApp) se muestran **siempre vacíos** en el formulario aunque ya existan guardados — el backend solo expone flags `has_*` (nunca el valor), y el campo **solo se envía al guardar si el usuario escribió algo nuevo**. Una implementación que en cambio mande siempre el valor del input (vacío por defecto) **borraría las credenciales existentes en cada guardado**. Este patrón ("vacío = conservar, con badge de que ya hay algo configurado") es obligatorio replicar tal cual.

### 4.5 Analítica (`/assistant/analytics`)

Selector de rango (7/14/30/90 días). **Embudo comercial real de 6 escalones** (no 5): conversaciones → conversaron de verdad → **preguntaron por precios** (señal de intención de compra) → dejaron sus datos → calificados → se hicieron clientes — más demos agendadas y derivaciones a humano como métricas aparte de la cadena. Evolución diaria. Estimación de costo por tokens **hardcodeada a tarifas de un proveedor específico, sin leer el proveedor realmente configurado** — corregir esto al portar, no repetirlo. Uso de herramientas.

### 4.6 Tiempo real y notificaciones

El archivo se llama `assistantSocket.ts` pero usa **Server-Sent Events**, no WebSocket — un único evento `conversation_updated` que dispara un refetch por REST (distinto según quién escucha: la bandeja hace fetch incremental por `after=<último id>`, la campana global hace un refetch completo de pendientes). Conexión perezosa con referencia contada (se abre con el primer listener, se cierra con el último). Sin estado global — cada componente escucha por su cuenta con `useState`/`useRef`. El token de auth va como *query param* porque `EventSource` no admite headers custom.

La **campana global** (`AssistantChatBell`, en el header de toda la app) usa el mismo mecanismo: suena **siempre** que aparece un id nuevo en pendientes, pero la notificación de escritorio se omite si el usuario ya está mirando la bandeja con la ventana enfocada. Requiere infraestructura de Web Push completa (VAPID + Service Worker propio) para avisar con el panel cerrado, con degradación silenciosa si no está configurado.

### 4.7 Contrato REST de referencia (`assistant.service.ts`)

Base `/superadmin/assistant/*`, respuestas envueltas en `{data: ...}` salvo donde se indica sin envoltorio:

| Método | Ruta | Payload | Respuesta |
|---|---|---|---|
| GET | `/status` | — | `{ready, llm_configured, whatsapp_enabled}` (sin envoltorio) |
| GET | `/metrics` | — | `AssistantMetrics` |
| GET | `/analytics?days=` | query | `AssistantAnalytics` (embudo de 6 escalones + demos/handoffs + diario + costo + tools) |
| GET / PUT | `/config` | `UpdateAssistantConfig` (secretos solo si se escriben) | `AssistantConfig` (flags `has_*`, nunca el valor real) |
| GET / POST / DELETE | `/knowledge`, `/knowledge/:id` | `{source_type,title,content}` | `KnowledgeItem[]` / `{chunks}` |
| POST | `/knowledge/seed-defaults` | — | `{docs, chunks}` (sin envoltorio) |
| POST | `/test` | `{text, from}` | `{reply, silent}` (sin envoltorio) |
| GET | `/conversations?status=` | query opcional | `Conversation[]` (incluye `lead_status`, `needs_human_reason`, `last_message_role`, `is_lead`) |
| GET | `/conversations/:id/messages?after=` | query opcional | `AssistantMessage[]` (incluye `reply_to_id`, `channel_msg_id`, `tool_name`) |
| POST | `/conversations/:id/take` `/close` `/reopen` `/validate-payment` | — | — |
| POST | `/conversations/:id/reply` | `{text, reply_to_id}` | — |
| GET | `/conversations/:id/lead` | — | `LeadSummary` (`status`, `tenant_id`, `payment_validated_at/by_name`) |
| POST | `/outreach` | `{tenant_id, phone, goal, message, template_name?, template_lang?, template_params?, prepare_only?}` | `{conversation_id}` (sin envoltorio) — llamado desde Tenants, no desde este módulo |
| GET / POST / POST | `/push/vapid-key`, `/push/subscribe`, `/push/unsubscribe` | — / `PushSubscriptionJSON` / `{endpoint}` | — |

Más el stream: `GET /superadmin/assistant/sse?token=<jwt>` (SSE, evento único `{type:"conversation_updated", conversation_id}`).

---

## 5. Alcance real de esta implementación: solo plataforma (`mistiq_central`), no tenants

El agente de Bendey es un **agente de ventas**: capta prospectos por WhatsApp/web, muestra información comercial, gestiona el pago manual, da de alta la cuenta de prueba o pagada — vende el propio SaaS Bendey, para Bendey mismo. Es exactamente el mismo caso de uso que Mistiq necesita: un agente que **venda Mistiq**, operado desde `mistiq_central` — no un agente que cada tenant use para atender a sus propios clientes finales.

Por ahora esto se implementa igual de "single-tenant" que en Bendey, casi 1:1:

- Un único asistente de plataforma en `mistiq_backend`, sin resolución por tenant en webhook ni chat público.
- El panel completo (bandeja, config, conocimiento, analítica, playground) vive en `mistiq_central`.
- Las acciones (crear lead, agendar demo, validar RUC, crear tenant de prueba/pagado, pago manual) son el mismo flujo comercial, vendiendo Mistiq en vez de Bendey.

Los campos `owner_kind`/`owner_tenant_id` se conservan en el modelo desde el día uno como puerta abierta a futuro, **sin cablear ningún resolver por tenant ni scope de `tenant_id` en el motor** — esa extensión queda fuera de este trabajo.

---

## 6. Plan de implementación propuesto en Mistiq

### 6.1 Backend (`mistiq_backend`)

1. **`pkg/agent/`** — puerto del motor con los valores exactos de §2 preservados (no aproximados): timeouts, TTLs, umbrales, tamaños de ventana, y en particular:
   - El **orden real** de persistir-antes-de-chequear-estado en el orquestador (§2.1).
   - La **clasificación de errores** (`Fatal/Validation/Business/Transient`) y su `Sanitize()` — sin esto se filtran errores técnicos al cliente o se pierde la corrección de argumentos por el modelo.
   - El **lock de RUC compartido** entre `create_trial_tenant` y `create_tenant_and_subscribe`.
   - Decidir explícitamente la zona horaria de la sección `temporal` del prompt (Bendey la trae fija a Perú).
2. **`internal/agent/`** — capa HTTP/WS montada en el grupo de auth **superadmin/plataforma** de `mistiq_central`, sin resolución por tenant. Replicar la asimetría real entre canales: WhatsApp con cola+lock+dedup (condicionados a que haya Redis), chat web 100% síncrono sin cola.
3. **Esquema de datos**: portar las 6 tablas conservando `owner_kind`/`owner_tenant_id` sin usar, con migraciones versionadas (`pkg/database/tenantmigrations`, no `AutoMigrate` puro).
4. **Acciones**: framework completo + catálogo comercial equivalente (crear lead, calificar, agendar demo, validar RUC, crear tenant de prueba/pagado reusando el servicio de alta de tenants que ya existe en `mistiq_central`, reportar/validar pago manual) — preservando qué acciones son `Sensitive` y cuáles no, y el mecanismo de auditoría-sin-secretos.
5. Nomenclatura: usar `mistiq`/`mistiq_backend` desde el día uno en cualquier config/env var — no repetir el patrón `TUKIFAC_*`/`bendey` que ya limpiamos esta sesión.

### 6.2 Panel de administración — todo en `mistiq_central`

- Portar las 5 páginas completas, **incluyendo explícitamente** lo listado en §4.1–4.6 (deep-linking, no-leídos, scroll inteligente, citas, compositor por dispositivo, lógica de pago manual, manejo seguro de secretos en Configuración, embudo de 6 escalones, Web Push con Service Worker) — no solo el layout visual de cada pantalla.
- Corregir de entrada el cálculo de costo en Analítica para que lea el proveedor/modelo realmente configurado en vez de tarifas hardcodeadas.
- Reusar el flujo de alta de tenant que ya existe en `mistiq_central` en vez de duplicar esa lógica dentro del agente.

### 6.3 Orden de trabajo sugerido (MVP incremental)

1. Motor + canal web (síncrono, más simple de depurar) + RAG básico + memoria — sin acciones ni panel todavía.
2. Persistencia (esquema completo, `owner_kind`/`owner_tenant_id` presentes sin usar) + endpoint de prueba tipo `AssistantTestPage`.
3. Panel mínimo en `mistiq_central`: bandeja (con manejo de estado real, no solo maquetado) + configuración básica con el patrón seguro de secretos desde el inicio.
4. Canal WhatsApp (Cloud API de Meta) + cola Redis + webhook, con la clasificación de errores y el manejo condicional del HMAC.
5. Acciones comerciales + políticas (con los valores exactos de §2.4/2.5).
6. Base de conocimiento + analítica (con costos correctos desde el inicio).

---

## 7. Huecos / cosas no verificables solo con el código leído

- **No se encontró ningún frontend/widget** que consuma el chat público (`POST /api/public/assistant/chat`, WS asociado) en ninguno de los dos repos analizados — puede vivir en otro repo del monorepo Bendey no incluido en la lectura. Confirmar con el equipo de Bendey antes de portar el canal web para no reinventar un contrato incompatible.
- No se leyeron en detalle todas las ~15 acciones individuales del catálogo comercial (solo las más relevantes: trial, lead, handoff, pago) — si se decide portar alguna lógica específica de negocio además del framework, hay que leer esas primero.
- No se verificó el DDL exacto generado por `AutoMigrate` (tipos MySQL reales, collation).
- `Assistant.LLMParamsJSON` existe en el modelo pero no se encontró código que lo lea o escriba — no vale la pena portarlo tal cual.
- El test de arquitectura de pagos protege solo dos archivos por nombre fijo (ver §3.3) — no es una garantía estructural genérica; si se replica, hay que saber exactamente qué cubre.

---

## Créditos / trazabilidad

Este documento pasó por **dos rondas** de análisis de código independientes: (1) tres informes iniciales (motor, capa HTTP/DB, UI) y (2) dos auditorías de verificación que releyeron el código real por segunda vez específicamente para confirmar o corregir lo escrito en (1), encontrando un punto incorrecto (orden de persistencia en el orquestador) y numerosas omisiones de detalle operativo (timeouts, TTLs, umbrales, casos borde de degradación) que ya están incorporadas arriba. Todo contra el snapshot de `D:\bendey_saas` del 2026-09-20, con citas `archivo:línea` en los informes originales (no reproducidas aquí por brevedad) — si se necesita el detalle línea por línea de alguna pieza puntual al portarla, releer el archivo original correspondiente en vez de asumir que esta síntesis lo cubre todo al 100%.
