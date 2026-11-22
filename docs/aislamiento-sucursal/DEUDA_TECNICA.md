# Aislamiento por sucursal: deuda técnica pendiente (análisis, sin implementar)

Fecha: 10-nov-2026. Alcance: `backend_principal` (Go/Fiber/GORM v1.31.1), `frontend_tenant` (Tukifac) y `frontend_restaurant_tenant` (Tukichef).
Método: lectura estática. **No se modificó código ni tests, no se hizo commit, no se tocó producción ni `.env`.** Todo lo marcado
"(inferido)" sale de razonar sobre el código y no se reprodujo en una app en marcha: hay que confirmarlo con una prueba antes de
corregirlo. Contexto previo: `AUDITORIA_Y_DISENO.md`, `FASE_3B.md`, `FASE_3C.md`, `pkg/branchscope/branchscope.go`.

Nota para quien busque con `grep`: `backend_principal/.claude/worktrees/awesome-tu-0f5463/` es una copia vieja del backend; hay que
excluirla de cualquier búsqueda o test que recorra el árbol.

---

## 0. Resumen ejecutivo priorizado

| # | Severidad | Hallazgo | Sección | Esfuerzo |
|---|---|---|---|---|
| 1 | **Alta** | Las guardas de la Fase 3A que infieren la sucursal de un documento (`DocBranch`, `RetentionBranch`, `PerceptionBranch`, `seriesBranch`) consultan con el `*gorm.DB` **ya acotado** de la Fase 3C. Lo que no ven lo tratan como "sin sucursal / documento de otro sistema" y por tanto **legible y escribible**: fallan abiertas. Efecto: un usuario de una sola sucursal ve retenciones, percepciones, bajas y reversiones de otras sucursales y puede emitir una **reversión SUNAT** sobre un documento ajeno por serie/correlativo. Los tests de 3A no instalan los callbacks, por eso nunca lo detectaron. | 5.4, 6 | 1.5 d |
| 2 | **Alta** | La revocación de sesión no funciona como se creía: (a) `branch_session_version = 0` nunca invalida (`claims > 0 &&` en el middleware) y la columna se creó sin backfill, así que **la primera revocación de cada usuario no surte efecto**; (b) cambiar rol, permisos o contraseña no incrementa la versión; (c) los permisos viajan en el JWT y `view_all` se evalúa desde el JWT, vigente hasta 24 h; (d) `switch-branch` copia los permisos viejos y **renueva 24 h**, de modo que encadenando cambios de sucursal una sesión con permisos revocados no caduca nunca; (e) eso mismo extiende sin límite una sesión de acceso maestro (soporte) y alarga la del PIN de 12 h a 24 h. | 1, 2, 3 | 3 d |
| 3 | **Alta** | El token JWT completo (24 h, no de un solo uso) viaja en la URL del SSE `/api/billing/events?access_token=…` en ambos frontends, y el backend lo acepta en **cualquier** ruta (`auth.go:133`). El acceso maestro también entrega su token en la URL de una página (`/?master_sso=<jwt>`). El stream SSE, además, **no se cierra cuando vence el token ni cuando cambian los permisos**, y Tukifac no se reconecta tras refrescar/cambiar de sucursal ni maneja errores. | 4 | 2 d |
| 4 | **Alta/Media** | Invariantes globales comprobados con el `db` acotado: **la unicidad del código de serie SUNAT** (`assertSeriesCodeUnique`, sin índice único en BD) deja crear la misma serie en dos sucursales; el borrado de contactos cuenta "documentos asociados" solo en las sucursales del usuario (puede borrar un contacto usado en otra); la verificación de serial duplicado; y `ConfirmTransfer` con series **no hace nada y no avisa** si el confirmante solo tiene asignado el destino (el `UPDATE` queda en 0 filas). | 5.3 | 3 d |
| 5 | **Media** | (a) Un `Table("tenant_x AS alias")` con alias **desactiva los callbacks sin avisar y sin entrar a la auditoría** (GORM fija `Statement.Table` al alias): hay 12 consultas así sobre tablas registradas (T1-T12 de 5.2.2), todas hoy protegidas solo por un filtro manual. (b) Cinco sitios distintos firman JWT de tenant con lógica copiada y vigencias fijas en el código; hay un `LoginSubmit` HTML muerto que firma un token sin claims de sucursal. | 1, 5.2 | 2 d |

Orden recomendado: **1 → 2 (con los frontends) → 4 → 3 → 5**. El hallazgo 1 es una corrección chica (una línea por sitio más tests con
callbacks reales) y cierra una fuga activa; conviene hacerlo primero y antes de activar `BRANCH_SCOPE_STRICT=1`.

---

## 1. Emisión y vigencia del JWT de tenant

### 1.1 Inventario de sitios que firman tokens

Secreto único `config.AppConfig.JWTSecret` (HS256) para access **y** refresh; el refresh se distingue solo por `claims.Type == "tenant_refresh"`.
El superadmin usa otro secreto (`SAJWTSecret`, `auth_sa_handler.go:176`) y queda fuera de este documento.

| # | Sitio (archivo:línea) | Qué emite | Vigencia | Cómo la fija | Claims que copia / omite |
|---|---|---|---|---|---|
| A | `internal/auth/handler/auth_handler.go:100-117` `LoginSubmit` | Access, cookie `token` (`:122-129`) | 24 h literal (`:111`) y cookie 24 h | `time.Now().Add(24*time.Hour)` | Solo `UserID, Email, RoleID, RoleName, Tenant*, TenantVersion, Type`. **Sin** permisos, módulos, sucursal, plan, versión de sesión. **Código muerto**: no está montado en ninguna ruta (`internal/auth/routes.go` solo registra `/api/login` y `/api/session/refresh`). |
| B | `auth_handler.go:262-289` `LoginAPI` (`POST /api/login`) | Access + **refresh** (`:300`) | Access 24 h literal (`:283`); refresh 30 d | Literal | Todos los claims. Es una **copia** de la lógica de `BuildTenantSession` (mismas consultas a rol, permisos, módulos, plan, staff, sucursal) más campos de respuesta. Firma con `jwt.NewWithClaims` directo (`:288`). |
| C | `internal/auth/service/tenant_session.go:31-141` `BuildTenantSession` | Access | `PasswordSessionTTL` = 24 h (`restaurant_token.go:27`) vía `BuildTenantToken` (`:132`) | Constante | Todos, incluidos `Impersonated`, `MasterActorID/Email`. Lo usan `RefreshAPI` (`auth_handler.go:380`) y `MasterAccess` (`superadmin/service/tenant_service.go:855`). |
| D | `internal/auth/handler/session_handler.go:214-250` `issueTokenWithBranch` (`SwitchBranch` `:144-205`; `RefreshAPI` con sucursal `auth_handler.go:389-395`) | Access | 24 h literal (`:243`) | Literal, **desde ahora** | **Copia los claims del token viejo** (rol, permisos, módulos, plan, status, `PermVer`, `TenantVersion`, `AuthMethod`, `Impersonated`, `MasterActor*`) y solo reemplaza `ActiveBranchID` y `BranchSessionVersion`. No vuelve a leer rol ni permisos de la BD. |
| E | `internal/restaurant/handler/staff_auth_handler.go:91-114` `PinLogin` | Access (sin refresh) | `PinSessionTTL` = 12 h (`restaurant_token.go:24`) | Constante, y además se pasa dos veces (en `RegisteredClaims` `:109-112` y como `ttl` `:114`) | **Omite** `Permissions` y `PlanID`. Lleva `EmployeeType`, `StaffID`, `PermVer`, módulos. |
| F | `pkg/middleware/refresh_token.go:38-58` `BuildTenantRefreshToken` | Refresh | `RefreshSessionTTL` = 30 d (`:17`) | Constante | Solo `UserID, tenant*, TenantVersion, AuthMethod, Type`. Solo lo emite `LoginAPI` (`:300`). PIN y acceso maestro no reciben refresh. |
| G | `pkg/middleware/restaurant_token.go:12-21` `BuildTenantToken` | Helper de C y E | recibe `ttl` | Pone `IssuedAt/ExpiresAt` solo si vienen nulos | — |

Validación (para comparar): `pkg/middleware/auth.go:141` parsea sin `jwt.WithValidMethods` (solo `ratelimit.go:57` lo usa); con clave `[]byte`
`none` no pasa, pero HS384/HS512 sí. No hay `jti`, `iss`, `aud` ni `nbf` en ningún token. `ParseTenantRefreshToken` (`refresh_token.go:61-77`)
no verifica `TenantVersion`.

### 1.2 Problemas

1. **Lógica duplicada en cinco sitios** (A, B, C, D, E): cualquier claim nuevo (p. ej. `PlanID`, `Status`) hay que acordarse de agregarlo en todos.
   Ya divergieron: E omite `PlanID`; A omite casi todo; D no relee permisos.
2. **La vigencia no es una política, es un literal** (`24 * time.Hour` aparece en tres archivos). De ahí sale el efecto de la sección 3.
3. **D hereda y re-estampa**: un token "refrescado" por cambio de sucursal conserva permisos, módulos, estado del tenant y `Impersonated` del
   anterior y gana 24 h nuevas (sección 2).
4. Código muerto que firma tokens (A) y handlers HTML sin ruta (`LoginSubmit`, `ListPage`, `POSPage`, `NewPage`, `IndexPage`, `Home` del dashboard)
   que, si alguien los monta por error, evitan todo el aislamiento.

### 1.3 Propuesta: un emisor único (`internal/auth/service/issuer.go`)

```go
type Profile struct {
    Kind        string        // "pwd" | "pin" | "master_access"
    TTL         time.Duration // 24h | 12h | 24h
    WithPerms   bool          // false para PIN
    IssueRefresh bool         // true solo para pwd
    AbsoluteExp bool          // true para pin y master_access: Reissue NO extiende exp
}
var profiles = map[string]Profile{ ... } // ÚNICO lugar con vigencias

type Issuer struct{ secret []byte }

// Primer token de una sesión (login, PIN, acceso maestro, refresh): lee BD y arma TODOS los claims.
func (i *Issuer) Issue(ctx, tenant, tdb, user, opts) (*Session, error)
// Cambio de sucursal / ajuste de sesión: relee rol, permisos, módulos y status de la BD (las mismas 3-4 consultas de Issue),
// cambia solo lo pedido y conserva Kind/MasterActor*. exp: si Profile.AbsoluteExp → exp = old.ExpiresAt (no extiende); si no → TTL nuevo.
func (i *Issuer) Reissue(ctx, old *TenantClaims, tdb, user, change Change) (*Session, error)
```

- Un solo `buildClaims()` y una sola función `sign()` (HS256 + `WithValidMethods` en el parseo; añadir `jti` aleatorio, `nbf`).
- `LoginAPI`, `RefreshAPI`, `MasterAccess`, `PinLogin` y `SwitchBranch` pasan a llamar `Issue`/`Reissue`. `LoginAPI` conserva sus campos de
  respuesta (suscripción, sucursales) pero deja de armar claims.
- Se **elimina** `LoginSubmit` y los demás handlers HTML sin ruta.
- Test guardián (estilo "no SQL crudo"): un test Go que recorre el árbol (excluyendo `.claude/worktrees`) y falla si aparece
  `SignedString(` o `jwt.NewWithClaims(` fuera de `internal/auth/service/issuer.go` y de los tests/superadmin.
- Test de tabla: para cada `Profile`, el token emitido por `Issue` y por `Reissue` contiene el mismo conjunto de claims (comparando nombres de
  campo por reflexión), y `Reissue` de un PIN conserva `exp`.

**Esfuerzo:** 1.5-2 d (código ~1 d, tests ~0.5 d). **Despliegue:** solo backend, mismos claims y misma firma: los tokens vigentes siguen siendo válidos.
**Rollback:** revertir la imagen; no hay migración ni cambio de contrato.

---

## 2. Política de access / refresh token y revocación

### 2.1 Estado actual (hechos)

| Elemento | Vigencia / mecanismo | Evidencia |
|---|---|---|
| Access (pwd) | 24 h | `restaurant_token.go:27`, `auth_handler.go:283` |
| Access (PIN) | 12 h; 24 h tras `switch-branch` | `restaurant_token.go:24`, `session_handler.go:243` |
| Access (acceso maestro) | 24 h; **se renueva 24 h con cada `switch-branch`** | `tenant_service.go:855`, `session_handler.go:243` |
| Refresh | 30 d, **sin rotación y sin almacenamiento** (no se puede revocar uno concreto; el mismo token sirve 30 días) | `refresh_token.go:17`, `auth_handler.go:397-401` ("No rota el refresh token") |
| Refresh → access | Reconstruye claims desde BD (rol, permisos, módulos, sucursal) y exige usuario activo | `auth_handler.go:365-374, 380` |
| Cuándo refresca el cliente | Solo ante 401 (access vencido), una vez, con un único refresh en vuelo | `frontend_tenant/src/services/api.ts:327-345` |
| Usuario inactivo / borrado | El middleware lo rechaza en cada request (401) | `branch_context.go:45-52, 87-89` |
| Revocación por versión | `branch_session_version` del usuario vs. claim → 409 `SESSION_UPDATED` | `branch_context.go:59-64`; `pkg/branch/context.go:188-194` |
| Qué incrementa la versión | Solo cambios de **asignación de sucursales** (`SetUserAssignedBranches(..., true)`, `user_service.go:260-266, 363-368`) | `grep BumpSessionVersion` |
| Permisos de Tukichef (PIN) | Caché 15 min por `(tenant, user, PermVer del token)`; `BumpPermCacheVersion` sube la versión del servidor pero el token conserva su `PermVer` | `restaurantperm/cache.go:14`, `staff/service.go:53-66`, `restaurant_context.go:41` |
| Reacción del frontend a 409 `SESSION_UPDATED` | Cierra sesión (no refresca) | `frontend_tenant/src/services/api.ts:305`, `frontend_restaurant_tenant/src/services/api.ts:297` |

### 2.2 Qué pasa hoy en cada caso

| Evento | Resultado hoy |
|---|---|
| **Desactivar o borrar usuario** | Efectivo en la siguiente petición (401), por `branch_context.go:45-52, 87-89`. **Pero** el middleware se salta cuatro rutas (`branch_context.go:16-21`: `/api/login`, `/api/session/switch-branch`, `/api/session/context`, `/api/company/branches`): un usuario ya desactivado con token vigente puede seguir llamando a `switch-branch` (que sí lo recarga con `LoadTenantUserForBranch`, pero **no comprueba `Active`**, `session_handler.go:157-169`) y obtener un token nuevo de 24 h. El token nuevo se rechaza en la siguiente petición normal, así que el daño es menor, pero `switch-branch` no debería emitir nada para un usuario inactivo. |
| **Cambiar el rol del usuario** | No incrementa la versión. El JWT conserva `RoleID`, `RoleName` y `Permissions` hasta que expire (24 h) o se refresque; el frontend solo refresca al vencer. |
| **Quitar permisos a un rol** (`SetRolePermissions`, `role_service.go:85`) | No incrementa nada. Todos los usuarios de ese rol con token vigente conservan los permisos quitados hasta 24 h. |
| **Quitar `branches.view_all`** | El middleware evalúa `ResolveViewAll(tdb, claims.Permissions, …)` con los permisos **del JWT** (`branch_context.go:93`): el usuario sigue viendo todas las sucursales hasta que caduque. (En cambio `SwitchBranch`/`login` usan `RoleViewsAll` contra BD, `pkg/branch/perms.go:70-78`: dos fuentes de verdad distintas.) |
| **`switch-branch` tras quitar permisos** | `issueTokenWithBranch` copia `old.Permissions` (`session_handler.go:230`) y concede 24 h más. **Encadenable**: cada cambio de sucursal reinicia el reloj con los permisos viejos; una sesión con permisos revocados no caduca mientras el usuario siga cambiando de sucursal. |
| **Cambiar contraseña / PIN** | No invalida access ni refresh (no hay versión de contraseña). El refresh de 30 d sigue funcionando. |
| **Reasignar sucursales** | Sí revoca, salvo el defecto de abajo. |
| **Cambio de plan / módulos** | El JWT conserva `Modules` y `PlanID` hasta expirar; `SubscriptionGate` sí consulta el estado en vivo (`auth.go:187-194`). |

**Defecto de la versión de sesión (inferido, a confirmar con un test):** `branch_context.go:59` es
`!legacy && claims.BranchSessionVersion > 0 && user.BranchSessionVersion != claims.BranchSessionVersion`. La columna se agregó sin backfill
(`tenantmigrations/v031_multi_branch.go:12, 50-52`; `database/migrations.go:815` `default:0`), así que los usuarios existentes tienen versión 0 y
sus tokens llevan `0`. Cuando ocurra el primer `BumpSessionVersion` (0 → 1) el token con claim `0` se trata como "legado" y **no se invalida**.
La revocación solo funciona a partir de la segunda vez.

### 2.3 Propuesta

1. **Una sola versión de sesión por usuario**, con semántica clara: `tenant_users.branch_session_version` pasa a significar "session_version"
   (no se renombra la columna; se documenta). Se incrementa al: reasignar sucursales (ya), **cambiar `role_id`**, **cambiar permisos del rol**
   (`UPDATE tenant_users SET branch_session_version = branch_session_version + 1 WHERE role_id = ?` dentro de `SetRolePermissions`),
   **cambiar contraseña/PIN**, y al activar/desactivar.
2. **Migración V177 (aditiva, idempotente):** `UPDATE tenant_users SET branch_session_version = 1 WHERE branch_session_version = 0`. Con eso los
   tokens nuevos llevan ≥ 1 y son revocables desde la primera vez. Los tokens ya emitidos con `0` siguen aceptados como legado durante la
   ventana de 24 h; pasada esa ventana se cambia el middleware a rechazar `claims.BranchSessionVersion == 0` (409 → el cliente refresca).
3. **El middleware deja de confiar en permisos del JWT para decisiones de sucursal:** `view_all` y `switch` se consultan contra BD (la
   consulta del usuario ya existe en el middleware; añadir una por request con caché corta por `session_version` — es lo que el diseño de 3B
   ya recomendaba, decisión 7 de `AUDITORIA_Y_DISENO.md`). `RequirePermission` puede seguir leyendo el JWT porque la versión lo revoca.
4. **Frontends: ante 409 `SESSION_UPDATED` refrescar (no cerrar sesión)** y reintentar la petición una vez; cerrar sesión solo si el refresh
   falla. Hoy ambos cierran sesión (`api.ts:305` y `:297`), lo que convertiría la revocación por rol en expulsión masiva. **Los frontends
   deben salir antes (o a la vez) que el backend que incrementa la versión por rol.**
5. **Refresh con rotación** (diferido, mayor esfuerzo): tabla `tenant_refresh_tokens(jti, user_id, family, expires_at, used_at)` para detectar
   reutilización y permitir "cerrar todas las sesiones". Mientras tanto, mitigar con: `Active` comprobado en `switch-branch`, y que
   `ParseTenantRefreshToken` valide `TenantVersion`.
6. `switch-branch` deja de re-estampar a ciegas: usa `Issuer.Reissue` (sección 1.3), que relee rol/permisos/módulos/status de la BD y,
   para PIN y acceso maestro, **no extiende `exp`**.

**Esfuerzo:** backend 1 d, frontends 1 d (ambas apps, interceptor y pruebas), tests 0.5 d, V177 0.25 d → ~3 d.
**Orden de despliegue:** (1) frontends con "refrescar ante 409"; (2) migrate-fleet V177; (3) backend con el incremento por rol/contraseña y
`view_all` desde BD; (4) 24 h después, endurecer a "rechazar claim 0". **Rollback:** cada paso es independiente; revertir el backend deja las
versiones en 1 (inofensivo); si se revierte el frontend con el backend nuevo desplegado, los usuarios verían cierres de sesión al cambiar roles
(por eso el orden).

---

## 3. Vigencia del token PIN (12 h frente a 24 h)

**Hoy:** `PinLogin` emite 12 h (`restaurant_token.go:24`, `staff_auth_handler.go:110-114`). `SwitchBranch` (permitido al PIN solo con
`branches.switch`, `FASE_3B.md`) llama a `issueTokenWithBranch`, que fija **24 h desde el momento del cambio** (`session_handler.go:243`) y conserva
`AuthMethod: "pin"` (`:232`). Un turno de 12 h pasa a ser de 24 h, y encadenable. El PIN no tiene refresh token, así que no hay otro camino
que lo acote. En el acceso maestro ocurre lo mismo (la sesión de soporte debería ser de duración fija).

**Cómo mantenerla:**
- `Profile{Kind:"pin", TTL: 12h, AbsoluteExp: true}` y `Issuer.Reissue` copia `ExpiresAt` del token anterior (no recalcula). Lo mismo para
  `master_access`.
- Para `pwd` se puede conservar el comportamiento actual (renovar a 24 h) o también fijar `exp`; con el refresh de 30 d el usuario no
  nota diferencia. Recomendado: no extender, que lo haga el refresh (así las revocaciones de la sección 2 se aplican).
- Test: `PinLogin` → `switch-branch` → `exp` del token nuevo == `exp` del anterior (± 0 s) y ≤ 12 h desde el login.
- Frontend de Tukichef: ya cierra sesión por 401; con `exp` fijo no hay cambio. Opcional: avisar al usuario 5 min antes de que termine el
  turno.

**Esfuerzo:** 0.5 d (dentro del refactor de la sección 1). **Despliegue/rollback:** solo backend; sin migración. Efecto visible: los
cambios de sucursal ya no alargan el turno.

---

## 4. Token en la URL del SSE `/api/billing/events`

### 4.1 Cómo está hoy

**Backend**
- Ruta: `internal/billing/routes.go:43` → `api.Get("/billing/events", mod, loadRest, view, handler.SSEAccessTokenMiddleware, h.BillingEventsSSE)`,
  dentro del grupo `tenantAPI` (`routes/routes.go:214`: `TenantAuthAPI`, binding, suscripción, `BranchContextMiddleware`).
- `TenantAuthAPI` acepta el token por cabecera, cookie **o `?access_token=`** y lo hace **para cualquier ruta del grupo** (`pkg/middleware/auth.go:128-134`).
  `verifiedTenantID` del rate limit hace lo mismo (`ratelimit.go:44-49`).
  `SSEAccessTokenMiddleware` (`billing/handler/sse_middleware.go:10-16`) copia el query a `Authorization` pero corre **después** de `TenantAuthAPI`,
  así que hoy es redundante.
- Registro de peticiones: `RequestLogger` registra `c.Path()` (sin query) (`pkg/middleware/request_log.go:25-31`), por lo que el log de la
  aplicación **no** guarda el token. No hay otro logger de URL completa en el backend (`grep OriginalURL` vacío).
- El stream (`billing/handler/sse_handler.go:21-117`) filtra por sucursal con `branchguard.ReadableFunc` capturado al conectar, manda
  `retry: 3000` y keepalive cada 25 s, **y no tiene fin**: no se cierra al vencer el token, al cambiar permisos ni al cambiar de sucursal.

**Frontends**
- Tukifac: `frontend_tenant/src/hooks/useBillingEvents.ts:29-37` lee `localStorage.token` **una sola vez** al montar (dependencia `[enabled]`), crea
  `new EventSource(url)` con `access_token` y `tenant_slug` en el query. No hay `onerror`/`onopen`: tras refrescar el token o cambiar de sucursal
  el stream queda con el token y el alcance viejos; cuando el token vence, la reconexión automática del navegador recibe 401 y EventSource se
  cierra para siempre sin que nadie lo note (los estados SUNAT dejan de actualizarse hasta recargar).
- Tukichef: `frontend_restaurant_tenant/src/hooks/useBillingEvents.ts:28-39` igual, pero con `authToken` como dependencia (reconecta al cambiar el
  token). Comentario propio del código: "RIESGO CONOCIDO … el token sigue yendo en la URL".
- Hermano del mismo problema: el acceso maestro abre `https://<tenant>/?master_sso=<jwt de 24 h>`
  (`frontend_central/src/utils/tenantUrl.ts:65`, leído en `frontend_tenant/src/lib/masterSso.ts:6-17`; también el enlace antiguo
  `/auth/sso?token=`). La URL de una **página** queda en historial del navegador, en logs del CDN/proxy del frontend y en `Referer` de recursos
  de terceros cargados antes de `history.replaceState`.

### 4.2 Riesgos

| Dónde se filtra | Probabilidad | Comentario |
|---|---|---|
| Logs de proxy / Cloudflare (Logpush, "Security Events", cualquier reverse proxy con `$request_uri`) | Media-alta | Registran URI completa con query. No hay configuración de Nginx en el repo (no verificable). El log de la app no lo guarda. |
| Historial / DevTools / capturas de soporte | Media | Pestaña Network y copiar-como-cURL muestran el token; soporte comparte capturas. |
| `Referer` | **Baja para el SSE** (la URL del API nunca es una página, `Referrer-Policy: strict-origin-when-cross-origin` en `security.go:10` recorta a origen); **Media para `master_sso`** (sí es una página). |
| Reutilización | El token es un JWT completo de 24 h, **no de un solo uso ni acotado a la ruta**, y sirve para cualquier endpoint. |

### 4.3 Alternativas

| Opción | Cómo | Pros | Contras |
|---|---|---|---|
| **A. Ticket de un solo uso** | `POST /api/billing/events/ticket` (con `Authorization`) devuelve un id opaco de 32 bytes con vida de 30 s guardado en Redis (`tenantcache.RDB()`; hay Redis en producción) → `EventSource(".../events?ticket=…")`; el servidor lo consume (`GETDEL`) | Mantiene `EventSource` y su reconexión nativa; el query nunca contiene un JWT | Estado en servidor (Redis o memoria si no hay); el navegador reconecta solo con el **mismo** ticket ya consumido → hay que reemitir uno por reconexión (cerrar y recrear el `EventSource` en `onerror`); cliente nuevo igual hay que escribirlo |
| **B. Cookie httpOnly** | El backend pone una cookie de sesión corta para `/api/billing/events` | Sin cambios en `EventSource` salvo `withCredentials` | Frontends y API en orígenes distintos (el axios usa `withCredentials: false`; `AllowCredentials: true` solo en CORS): exige `SameSite=None; Secure` y manejo de CSRF; las apps Tauri/Android no comparten cookies de la misma forma. **Descartada** por frágil. |
| **C. `fetch` con streaming y cabecera `Authorization`** | `fetch(url, {headers:{Authorization}, signal})` y leer `response.body` con un parser SSE (~60 líneas o `@microsoft/fetch-event-source`) | **Sin estado nuevo en el servidor**, el token va en cabecera como el resto de la API, reintento y reconexión controlados por nosotros (renovar token y reabrir cuando cambia) | Hay que escribir/mantener el cliente; verificar el streaming en los WebViews de Tauri/Android (soportado en WebView2/Chromium modernos; confirmar en el APK) |

### 4.4 Elegida: **C (fetch-stream con cabecera)**, con A como puente solo si algún WebView no soporta streaming

Se elige C porque no agrega estado al servidor, reutiliza todo el pipeline de autenticación y sucursal que ya valida la cabecera, y resuelve de
paso los defectos de reconexión que hoy tiene el hook de Tukifac.

**Cliente (un helper compartido por ambas apps, `src/lib/sse/fetchEventStream.ts`):**
1. `connect()` hace `fetch(`${apiBase}/billing/events`, { headers: { Authorization: `Bearer ${token}`, 'X-Tenant-Slug': slug, Accept: 'text/event-stream' }, signal })`
   (sin `tenant_slug` ni token en el query).
2. Lee `response.body.getReader()`, decodifica con `TextDecoder`, separa eventos por línea en blanco, entrega `event: billing.status.updated` + `data:`.
3. **Reconexión:** backoff exponencial con jitter (1 s → 30 s). Ante `401`: llama a `refreshAccessToken()` (el que ya existe en `api.ts`) y reabre
   una vez; si falla, se detiene y muestra el aviso de "reconectando…". Ante `403 BRANCH_*`/`409 SESSION_UPDATED`: se refresca contexto y se reabre.
4. **Se reabre cuando cambia lo que define el alcance:** dependencias `[enabled, token, activeBranchId, resetEpoch]` (Tukichef ya usa `token`;
   Tukifac debe sumarlo junto con la época de sucursal).
5. Al reconectar, el consumidor **vuelve a pedir** la lista/estado (los eventos son notificaciones de cambio, no un log reproducible; no se usa `Last-Event-ID`).
6. `AbortController` al desmontar y al cambiar de época (encaja con el abort por época del interceptor de axios, Fase 4).
7. Prueba unitaria con un `ReadableStream` simulado (eventos partidos en varios chunks, keepalives, 401 → refresh → reconexión).

**Servidor (cambios pequeños):**
1. Quitar el soporte de `?access_token=` de `TenantAuthAPI` y de `verifiedTenantID`, y borrar `SSEAccessTokenMiddleware`.
   **Puente de compatibilidad** (las apps nativas instaladas tardan en actualizarse): aceptar el query **solo** si
   `path == "/api/billing/events"` y `SSE_ALLOW_QUERY_TOKEN=1` (por defecto activo al principio), contando y registrando (sin el valor) cuántas
   conexiones lo usan; apagarlo cuando el contador llegue a cero.
2. **El stream termina cuando vence el token:** capturar `claims.ExpiresAt` y cerrar el writer en ese instante (`time.After`); el cliente
   renueva y reabre. Además un tope duro de vida (p. ej. 55 min) para que los cambios de permisos/sucursal se re-evalúen al reconectar.
3. Cabecera `Cache-Control: no-store` y `Referrer-Policy: no-referrer` en la respuesta (ya tiene `no-cache, no-transform`).
4. Acceso maestro: sustituir `/?master_sso=<jwt>` por un **código de canje de un solo uso** (`POST /api/superadmin/tenants/:id/master-access` devuelve
   `code` de 30 s en Redis; el frontend del tenant llama `POST /api/auth/master-exchange {code}` y recibe el JWT por el cuerpo). Misma técnica
   que la opción A, aplicada donde el token sí viaja en una URL de página. Esfuerzo propio: 1 d; puede ir en una segunda entrega.

**Esfuerzo:** helper + dos hooks + tests 1-1.25 d; backend 0.5 d; canje de acceso maestro 1 d (aparte) → ~2 d sin el canje.
**Orden de despliegue:** (1) backend con el puente (`SSE_ALLOW_QUERY_TOKEN=1` y cierre al vencer el token: no rompe a nadie); (2) frontends con fetch-stream; (3) tras el
periodo de adopción, `SSE_ALLOW_QUERY_TOKEN=0` y borrar el código. **Rollback:** reactivar la variable; los frontends nuevos siguen funcionando
(la cabecera siempre fue válida).

**Mitigación inmediata sin tocar clientes (opcional):** restringir el query a la ruta del SSE y confirmar que Cloudflare/Nginx no guardan
la query (o enmascararla en el log del proxy). Verificar con quien administre el proxy; no es comprobable desde el repo.

---

## 5. Auditoría de SQL manual y de consultas fuera de los callbacks de `pkg/branchscope`

### 5.1 Reglas que determinan si una consulta queda cubierta

Verificadas en `pkg/branchscope/branchscope.go:162-188` (`resolve`) y en el código de GORM v1.31.1:

1. Los callbacks solo actúan sobre la **tabla principal** de la sentencia (`tx.Statement.Table`). Un `JOIN` a tabla registrada **no** se filtra.
2. `Raw()` y `Exec()` no pasan por `Query/Update/Delete` (sí `Rows()` y `Scan()` de un `Table()/Model()`).
3. **Alias en `Table()`**: `chainable_api.go:58-74` fija `Statement.Table` al **alias** (`Table("tenant_product_stocks ps")` → `"ps"`;
   `"... AS s"` → `"s"`). `IsRegistered("ps")` es falso → los callbacks **no se aplican y tampoco se audita**. Sin alias (`Table("tenant_sales")`) sí funciona.
   (Aunque se aplicara, `clause.CurrentTable` se renderiza con el nombre base y rompería la consulta con "unknown column".)
4. Subconsultas armadas con `db.Select(...)` y pasadas a `Where("x IN (?)", sub)` **no** ejecutan callbacks (solo la sentencia externa).
5. Las consultas sin `Scope` ni `System` (login, refresh, rutas públicas, `database.GetTenantDB` directo) se auditan en el log pero **no fallan** hasta
   `BRANCH_SCOPE_STRICT=1`.
6. **Efecto colateral (nuevo):** cualquier **comprobación de existencia, unicidad o conteo** hecha con el `db` de la solicitud solo ve las
   sucursales del usuario. Cuando la regla de negocio es global esto es un fallo abierto (ver 5.3 y 5.4).

### 5.2 Tabla completa

Leyenda de clasificación: **OK** (cubierto o intencionalmente interno), **OK-manual** (no pasa por callbacks, pero el endpoint resuelve
la sucursal con `ResolveReadBranchFilter`/`ResolveReportBranchFilter`/`activeBranch` y la consulta la aplica; frágil si alguien
olvida el parámetro), **FILTRO** (requiere filtro manual / decisión), **CAMBIO** (requiere cambio de código).
"Callbacks" indica si la consulta pasa por ellos para la tabla registrada que figura.

**5.2.1 `Raw()` / `Exec()` sobre las tablas auditadas**

| # | Archivo:línea | Tabla(s) | Callbacks | ¿Sucursal validada por otra vía? | Clasificación |
|---|---|---|---|---|---|
| R1 | `internal/fiscal/summary/scanner.go:370-400` (`scanSales`, `Raw().Rows()`) | `tenant_sales`, `tenant_document_series` | No (Raw; además `System`) | Intencional: barrido de salud de TODO el tenant (`scanner.go:73` usa `branchscope.System`) | OK |
| R2 | `scanner.go:387` (`scanDespatches`) | `tenant_despatches`, series | No; `System` | Ídem | OK |
| R3 | `scanner.go:431-447` (`scanOpen`) | ventas y guías | No; `System` | Ídem | OK |
| R4 | `internal/restaurant/service/dashboard_service.go:439-443` (total) | `tenant_sale_items` JOIN `tenant_sales s` | No (Raw) | `branchClause` desde `activeBranch(c)` (`restaurant_handler.go:1060`), siempre > 0 | OK-manual (si `branchID==0` sumaría todo el tenant: añadir guarda) |
| R5 | `dashboard_service.go:460-473` (top productos) | ídem | No | Ídem | OK-manual |
| R6 | `dashboard_service.go:519-530` (top categorías) | ídem | No | Ídem | OK-manual |
| R7 | `pkg/database/branch_migration.go:125-160`; `tenantbackfills/v031…v036, v146`; `v034_product_codes.go:100`; `v035_merge_duplicate_contacts.go:76`; `database/migrations.go:2644, 2758, 2766` | tablas con `branch_id` (backfills) | No | Migraciones y backfills: sin solicitud ni usuario | OK (sistema) |
| R8 | `internal/equipos/service/catalog.go:50`; `database/central.go`, `tenant.go`, `saas/docusage/migrate.go`, `migrations.go:479-497` | `equip_*` / tablas centrales | — | No tocan las tablas auditadas | n/a |

No hay `Exec()` en rutas de solicitud sobre estas tablas.

**5.2.2 `Table("tabla alias")` sobre tablas registradas (los callbacks quedan apagados en silencio)**

| # | Archivo:línea | Tabla | Callbacks | ¿Sucursal por otra vía? | Clasificación |
|---|---|---|---|---|---|
| T1 | `internal/inventory/service/inventory_service.go:309` (`GetPresentationStockByProduct`) | `tenant_product_presentation_stocks AS ps` | **No (alias)** | `branchID` desde `StockAPI` (`inventory_handler.go:442-470`): `ResolveReadBranchFilter` o `visibleBranchIDs` (post-filtro) | OK-manual |
| T2 | `inventory_service.go:808` (`StockTotalsByProductIDs`, variantes) | `…presentation_stocks AS ps` | No (alias) | `StockSummaryAPI` `inventory_handler.go:489-515`: `ResolveReadBranchFilter` | OK-manual |
| T3 | `inventory_service.go:1121` (`StockSummary`) | `tenant_product_stocks ps` | No (alias) | Solo lo llama `IndexPage` (`inventory_handler.go:37`), **handler HTML sin ruta**; con `branch_id` crudo | OK (código muerto: borrar) |
| T4 | `internal/products/service/product_service.go:508` (`enrichReport`, stock por sucursal) | `tenant_product_stocks AS s` | No (alias) | `branchID` de `ListWithCategoryNames` resuelto en `product_handler.go:660-671` | OK-manual |
| T5 | `product_service.go:541` | `…presentation_stocks AS ps` | No (alias) | Ídem | OK-manual |
| T6 | `product_service.go:570` | `…presentation_stocks AS ps` | No (alias) | Ídem | OK-manual |
| T7 | `product_service.go:1361` (`GetStock`) | `…presentation_stocks AS ps` | No (alias) | Suma de TODAS las sucursales (no se usa para decidir borrados: `grep .GetStock(` vacío) | OK |
| T8 | `product_service.go:1380` (`GetStockByBranch`) | `…presentation_stocks AS ps` | No (alias) | `branchID` explícito del llamador | OK-manual |
| T9 | `internal/dashboard/handler/dashboard_scope.go:177` (`queryLowStock`) | `tenant_product_stocks ps` | No (alias) | `branchID` de `ResolveReportBranchFilter(c, …, true)` (`analytics_api.go:90`); 0 solo si `IsBranchAdmin` | OK-manual |
| T10 | `dashboard_handler.go:104` (`Home`) | `tenant_product_stocks ps` | No (alias) | **Sin filtro de sucursal**; `Home` no está montado en ninguna ruta | OK (código muerto: borrar) |
| T11 | `internal/restaurant/service/restaurant_service.go:205` (`ListTables`) | `tenant_restaurant_tables t` JOIN pisos `f`, sesiones `ts` | No (alias) | `t.branch_id = ?` con `activeBranch(c)` (`restaurant_handler.go:182-188`), siempre > 0 | OK-manual |
| T12 | `internal/billing/worker/resend_pending.go:38` | `tenant_sales AS s` JOIN series | No (alias) | Worker con `System` (`resend_pending.go:68`) | OK |
| T13 | `internal/payables/service/payable_service.go:91` | `tenant_purchase_payables AS pay` JOIN `tenant_purchases AS p` | Principal no registrada; compras solo por JOIN | `p.branch_id = f.BranchID` con `ResolveReadBranchFilter` (`payable_api.go:28-29`) | OK-manual |
| T14 | `internal/memberships/service/membership_service.go:571-575` (`ListBillingHistory`) | `tenant_membership_invoices i` JOIN `tenant_sales s` | Principal no registrada | La membresía se carga antes con el `db` acotado y `RecordReadable` (`membership_handler.go:89, 108, 260`) | OK-manual |
| T15 | `internal/sales/service/sale_pending_refunds.go:44-51` | `tenant_cash_movements cm` JOIN `tenant_sales s` | Principal no registrada | `branchID` de `sale_handler.go:832-836` (activa o `ResolveReadBranchFilter`) | OK-manual |

**5.2.3 `Table("tabla")` sin alias, `Model()` y `Joins()` con la tabla registrada como principal (los callbacks SÍ actúan)**

| # | Archivo:línea | Tabla principal | Callbacks | Observación | Clasificación |
|---|---|---|---|---|---|
| J1 | `internal/sales/service/sale_service.go:1322, 1413` (`List`) | `tenant_sales` (+ series, contactos) | Sí | Además `params.BranchID` desde el handler | OK |
| J2 | `sale_service.go:1562-1567` (`joinEffectiveSunatCode`) | `tenant_sales` + subselect y `JOIN tenant_sales eff_fe` | Principal sí; subselect/JOIN no | Deriva el comprobante hijo de la misma nota: misma sucursal | OK |
| J3 | `internal/billing/service/billing_service.go:143` | `tenant_sales` JOIN series | Sí | `GetNotificationCountsForBranch(branchID)` con `ResolveReportBranchFilter` (`billing_handler.go:590`) | OK |
| J4 | `internal/receivables/service/receivable_service.go:137, 424` | `tenant_sales` | Sí | `branchID` desde `ResolveReadBranchFilter` (`receivable_api.go:28-29, 128-129`) | OK |
| J5 | `internal/purchases/service/purchase_service.go:424` | `tenant_purchases` | Sí | `ResolveReadBranchFilter` (`purchase_api.go:26`) | OK |
| J6 | `internal/inventory/service/inventory_service.go:1019, 1065` (kardex) | `tenant_stock_movements` | Sí | `params.BranchID` por `ResolveRead…` | OK |
| J7 | `internal/accounting/service/docs.go:217-222, 260, 346` | `tenant_sales` / `tenant_purchases` (`Table` sin alias) | Sí | El módulo contable **no resuelve sucursal**: queda por callbacks (solo las asignadas; todas si `view_all`) | OK |
| J8 | `internal/dashboard/handler/analytics_api.go:172-299, 410-428, 624-627, 700-714` | `tenant_sales` (Model) | Sí | Más `branchScope(branchID)` | OK |
| J9 | `analytics_api.go:216-219` (ventas por sucursal) | `tenant_sales` JOIN `tenant_branches` | Sí | Para un usuario acotado solo aparecen sus sucursales (correcto) | OK |
| J10 | `internal/billing/worker/reconcile.go:34` | `tenant_sales` (`Table` sin alias) | Sí, pero `System` (`reconcile.go:30`) | Interno | OK |
| J11 | `internal/cashbank/service/cashbank_report_service.go:1277-1300` (`legacyQ`) | `tenant_sales` JOIN `tenant_cash_sessions` | Sí (principal) | Además `f.BranchID` | OK |
| J12 | `cashbank_report_service.go:1675-1698` (compras no efectivo) | `tenant_purchases` JOIN sesiones | Sí (principal) | Además `f.BranchID` | OK |
| J13 | `internal/restaurant/service/restaurant_service.go:987` (`GetKitchenComandas`) y `restaurant_cancelled_comandas.go:40` | `tenant_comandas` (no registrada) JOIN `tenant_table_sessions ts` | Principal no registrada | `ts.branch_id = ?` con `activeBranch(c)` | OK-manual |

**5.2.4 `Joins()` donde la tabla registrada solo se une (los callbacks no la filtran)**

| # | Archivo:línea | Principal → registrada unida | ¿Sucursal por otra vía? | Clasificación |
|---|---|---|---|---|
| U1 | `internal/accounting/service/ledger.go:121-123` (cobros a crédito) | `tenant_sale_payments` → `tenant_sales` | **Ninguna**: suma cobros de **todas** las sucursales aunque el usuario esté acotado; mientras `SalesDocs` (J7) sí está acotado → asiento incoherente | **CAMBIO** (filtrar `tenant_sales.branch_id IN allowed` o consultar desde `tenant_sales`) |
| U2 | `ledger.go:145-150` (CxP y pagos) | `tenant_purchase_payables`/`_payments` → `tenant_purchases` | **Ninguna** | **CAMBIO** (ídem) |
| U3 | `internal/cashbank/service/cancelled_sales_report.go:174-183, 291-297` | `tenant_sale_payments` → `tenant_sales`, `tenant_cash_sessions` | `f.BranchID`/`session.BranchID` (sesión leída con `db` acotado) | OK-manual |
| U4 | `cashbank_report_service.go:212-219` | `tenant_cash_movements` → `tenant_cash_sessions` | `f.BranchID` desde `ResolveReadBranchFilter` (`cashbank_api.go:391-395`) | OK-manual |
| U5 | `cashbank_report_service.go:1126-1136, 1157-1171, 1198-1214` | `tenant_sale_payments` → `tenant_sales`/`tenant_cash_sessions` | `session.BranchID` o `f.BranchID` | OK-manual |
| U6 | `cashbank_report_service.go:1528-1535, 1761-1767` | `tenant_bank_movements`/`tenant_purchase_payments` → `tenant_cash_sessions` | `f.BranchID` | OK-manual |
| U7 | `cashbank_report_service.go:1899-1901` (`GetSessionProductsReport`) | `tenant_sale_items` → `tenant_sales` | La sesión se lee antes con el `db` acotado (`:1888-1895`) y no se encuentra si es de otra sucursal | OK-manual |
| U8 | `analytics_api.go:45, 311, 340, 478` | `tenant_sale_items`/`tenant_sale_detraccion` → `tenant_sales s` | `branchID > 0` desde `ResolveReportBranchFilter` (`s.branch_id = ?`) | OK-manual |
| U9 | `analytics_api.go:436, 455`; `profit_api.go:115-121` | `tenant_cash_movements m` → `tenant_cash_sessions cs` | `cs.branch_id = ?` si `branchID > 0` | OK-manual |
| U10 | `internal/sales/service/sale_service.go:1682, 1738, 1804` (resumen del listado) | `tenant_sale_detraccion`/`tenant_sale_payments` → `tenant_sales ts` + `idSub` | `idSub` es una subconsulta: **no** lleva el scope de callbacks; sí los filtros de `List` (incluida la sucursal ya resuelta) | OK-manual |
| U11 | `sale_service.go:1949-1960, 2156-2167` (ventas por producto / ganancia) | `tenant_sale_items` → `tenant_sales` | `params.BranchID` desde `resolveSalesReportBranch` (`sale_handler.go:387-394, 448, 547, 580`) | OK-manual |
| U12 | `internal/restaurant/service/dashboard_service.go:202, 350` | `tenant_sale_items`/`tenant_sale_payments` → `tenant_sales s` | `branchScopeSales(branchID)` (activa) | OK-manual |
| U13 | `internal/prepayment/deduction_service.go:52-57` (`ListOpenVouchers`) | `tenant_sale_prepayment_vouchers` → `tenant_sales` | **Ninguna**: lista anticipos de cualquier sucursal para el contacto | **FILTRO / decisión de negocio** (¿un anticipo de la sucursal A se puede aplicar en B?) |
| U14 | `internal/prepayment/voiding.go:19-22` | `tenant_sale_prepayment_applications` → `tenant_sales c` | Es un conteo global a propósito (impedir anular un anticipo ya deducido) | OK (invariante global) |
| U15 | `internal/billing/service/note_partial.go:59-63` | `tenant_sale_items tsi` → `tenant_sales ts` (cantidad ya devuelta) | Debe ser global (invariante: no devolver más de lo vendido) | OK (invariante global) |
| U16 | `product_service.go:194-242, 280-285` (subselects de stock) | `tenant_products` + `EXISTS/SELECT` sobre stocks | Filtran por `branchID` del listado; sin él, `SUM` de todas (solo `view_all`) | OK-manual |

**5.2.5 `Table(variable)` y comprobaciones de uso (el scope encoge el resultado)**

| # | Archivo:línea | Qué hace | Problema | Clasificación |
|---|---|---|---|---|
| V1 | `internal/contacts/service/contact_service.go:338-356` (`CountLinkedDocuments`, usado por `Delete` `:359-380`) | Cuenta registros que apuntan al contacto en `tenant_sales`, `tenant_quotations`, `tenant_purchases`, `tenant_memberships`, `tenant_table_sessions` (**registradas**) con `s.db.Table(table)` | Con el `db` acotado solo cuenta las sucursales del usuario: un usuario de una sucursal puede **borrar un contacto con documentos en otra** | **CAMBIO** (contar con `branchscope.System(db, "contactos: uso global")`) |
| V2 | `internal/company/service/company_service.go:434-458` (`assertSeriesCodeUnique`), llamado en `:500` y `:685` | Garantiza que un código de serie no exista ya en el tenant (regla SUNAT, `docseries/errors.go:12`); solo las series de almacén se limitan por sucursal | Con el `db` acotado solo ve las series de las sucursales del usuario → se puede crear `F001` en la sucursal 1 existiendo ya en la 2. **No hay índice único** en `TenantDocumentSeries` (`migrations.go:942-954`) | **CAMBIO** (comprobar con `System`) **y** índice único `(series)` para no-almacén tras limpiar duplicados |
| V3 | `internal/company/service/series_usage_service.go:63-110` (`countDocuments`) | Cuenta documentos que usan una serie | Las series son por sucursal; los documentos de esa serie son de la misma sucursal | OK |
| V4 | `inventory_document_service.go:756`, `inventory_service.go:926, 982` | Comprueba serial duplicado `product_id + serial` | `TenantProductSerial` está acotado: un serial existente en otra sucursal no se ve → duplicado posible. **No hay índice único** | **CAMBIO** (`System` o filtrar por producto sin scope) |
| V5 | `inventory_service.go:642-693` (`ConfirmTransfer`), series en `:662-669` | Mueve seriales `in_transit` del origen al destino con `UPDATE … WHERE branch_id = origen` | Si quien confirma solo tiene asignado el destino, el `WHERE` del callback excluye el origen: **0 filas, sin error**; el stock sí entra al destino y los seriales siguen "en tránsito" en el origen (inferido; ver 5.3) | **CAMBIO** (`System` acotado a ese `transfer_id` tras validar permisos, o comprobar `RowsAffected == len(serials)`) |
| V3b | `internal/products/service/bulk_delete.go:278` (`purgeRestaurantProduct`) | `DELETE` de `TenantProductStock` por `product_id` | Un usuario acotado borra solo el stock de sus sucursales y el producto se elimina igual: filas huérfanas en las otras | FILTRO (menor) |
| V6 | `internal/cashbank/service/financial_reversal.go:15` | `Table(variable)` de movimientos de caja/banco | Tablas no registradas | OK |

**5.2.6 Entidades que el scope sí protege como tabla principal (para completar la cobertura pedida)**

`tenant_cash_sessions` (`ListSessionsEnriched`, `ListOpenSessionsInBranch`, `ValidateCashSessionForUser`), `tenant_quotations`, `tenant_purchases`
(`List`, `GetByID`), `tenant_despatches` (`ListDespatchesAPI`), `tenant_inventory_documents`, `tenant_document_series` (`ListSeries`),
`tenant_memberships`, `tenant_restaurant_floors/tables`, `tenant_table_sessions`, `tenant_product_stocks`/`…presentation_stocks` y
`tenant_stock_movements` cuando se consultan con `Model(&…)`: cubiertos por los callbacks (`BranchScopedModels`, `pkg/database/branch_scope_models.go`).
En estos el filtro manual por parámetro es redundante, no un riesgo.

### 5.3 Hallazgos nuevos de esta auditoría (detalle)

1. **Aliased `Table()`** (5.1 punto 3, filas T1-T12). Hoy todas las consultas con alias están protegidas por un parámetro manual; si alguien lo
   omite la tabla queda abierta **sin aparecer en la auditoría**. Propuesta: test de AST que falle si `Table("<registrada> <alias>")` aparece
   sin comentario `//branchscope:manual <motivo>`; y, a medio plazo, reescribirlas como `Model(&TenantProductStock{})…` o sin alias.
2. **V2 (series SUNAT):** riesgo fiscal real (dos puntos de emisión con la misma serie). Corregir con `System` en la comprobación y un índice
   único; antes de crear el índice hay que auditar duplicados existentes por tenant (consulta de solo lectura `GROUP BY series HAVING COUNT(*) > 1`).
3. **V1, V4:** mismo patrón (invariante global comprobado con visión parcial).
4. **V5:** confirmar con una prueba SQLite: usuario con solo la sucursal B confirma una transferencia con series de A hacia B; esperar que los
   seriales queden `in_transit` en A mientras el movimiento de entrada existe en B.
5. **U1/U2 (contabilidad):** las sumas de cuentas por cobrar/pagar no se acotan; para usuarios con una sola sucursal los asientos mezclan datos
   de otras. Es lectura, severidad media.
6. **Rutas públicas y de login** (ecommerce, tienda, `PinLogin`, `RefreshAPI` con `database.GetTenantDB` directo: `auth_handler.go:353`,
   `pkg/middleware/auth.go:378`, `tenant.go:58`) consultan entidades registradas sin `Scope` ni `System`: aparecen en el log de auditoría y fallarían
   con `BRANCH_SCOPE_STRICT=1`. **No activar el modo estricto** hasta anotarlas con `System(db, motivo)` (o darles un `Scope` explícito) y
   revisar el log de auditoría de producción.

### 5.4 Interacción 3A × 3C que falla abierta (ver también la sección 6)

Las guardas de `internal/sales/branchguard/guard.go` se escribieron en la Fase 3A, **antes** de los callbacks. Las que dependen de
"no encontrado = otro sistema / sin sucursal" ahora reciben un `db` acotado:

| Función (archivo:línea) | Lógica | Efecto con el scope |
|---|---|---|
| `seriesBranch` (`guard.go:154-165`) | Busca la serie por nombre y devuelve su sucursal; sin coincidencia devuelve **0** | Serie de otra sucursal: invisible → 0 → "documento del tenant" |
| `RetentionBranch` / `PerceptionBranch` (`:167-185`) | compra/venta origen, si no `seriesBranch` | Compra/venta ajena invisible → cae a `seriesBranch` → 0 |
| `DocBranch` (`:245-276`) | busca la venta por serie y correlativo; `found=false` si no existe | Venta de otra sucursal invisible → `found=false` → "no existe localmente" |
| `LinesReadable` (`:278-290`), `LinesInScope` (`:327-343`), `LineWritable` (`:292-297`) | Con `found=false` devuelven legible/en alcance/escribible | Listados y estados de **bajas y reversiones** muestran las de otras sucursales (`billing_handler.go:530, 575, 870, 905`); **`CreateReversionAPI` acepta un documento ajeno** (`:887`) y el servicio no lo busca antes de enviarlo (`billing_service.go:2637-2654` solo arma el payload) |
| `RetentionReadable`/`PerceptionReadable` (`:188-202`) | `RecordReadable(c, RetentionBranch(...))` | 0 → `RecordReadable(0)` es verdadero → estado de retención/percepción ajena legible (`billing_handler.go:752, 789, 811`) |

`CreateVoidedAPI` **no** queda abierta por esta vía porque el servicio busca la venta con el `db` acotado y falla (`billing_service.go:1382-1396`),
pero es suerte, no diseño. Los tests de aislamiento de 3A (`billing_branch_isolation_test.go`) no llaman a `branchscope.Install/Attach`; el único test
que usa el scope es `pkg/branchscope/branchscope_test.go`.

**Corrección propuesta (1.5 d):** en `branchguard` toda inferencia de sucursal (`SaleBranch` para inferir, `seriesBranch`, `DocBranch`, `RetentionBranch`,
`PerceptionBranch`) usa `branchscope.System(db, "branchguard: inferir sucursal de documento")` **y solo selecciona `id, branch_id`**, de modo que lo
que se decide es "¿esta sucursal me pertenece?" y no "¿lo veo?". Las funciones de lectura/escritura por ID (`SaleReadable`, `SessionWritable`, …) se dejan
con el `db` acotado (fallan cerradas, que es lo correcto). Agregar a la suite un helper de test que monte SQLite con
`branchscope.Install` y `Attach` y repetir allí los casos de `billing_branch_isolation_test.go`. **Rollback:** revertir el commit; sin migración.

### 5.5 Test guardián recomendado (estilo "anti-SQL crudo" de 3C)

Test Go que recorra el AST de `internal/` y `pkg/` (excluyendo `*_test.go`, `tenantmigrations`, `tenantbackfills`, `.claude/worktrees`) y falle si
encuentra, sobre un `*gorm.DB` no marcado `System`:
(a) `.Raw(`/`.Exec(` cuyo SQL mencione una tabla de `BranchScopedModels`; (b) `.Table("<registrada> <alias>")`; (c) `.Joins("… JOIN <registrada> …")`
con principal no registrada. Cada excepción lleva un comentario `//branchscope:manual <motivo>` que el test lee. La lista de tablas se obtiene de
`database.BranchScopedModels()` (no se duplica). **Esfuerzo:** 1 d. La tabla 5.2 es el estado inicial de excepciones.

### 5.6 Estimación del resto de correcciones

| Ítem | Esfuerzo |
|---|---|
| 5.4 (guardas con `System` + tests con scope real) | 1.5 d |
| V2 serie única (`System` + auditoría de duplicados + índice en migración aparte) | 0.75 d |
| V1 contactos, V4 seriales, V5 `ConfirmTransfer` con series, V3b | 1 d |
| U1/U2 contabilidad, U13 anticipos (tras decisión de negocio) | 0.75 d |
| Borrar handlers HTML muertos (`LoginSubmit`, `IndexPage`, `Home`, `ListPage`, `POSPage`, `NewPage`, `CreateSubmit`…) | 0.5 d |
| Test guardián 5.5 | 1 d |
| Anotar rutas públicas/login con `System` y preparar `BRANCH_SCOPE_STRICT=1` | 1 d |

**Orden de despliegue:** todo backend, sin cambios de contrato, salvo el índice único de series (migración aditiva posterior a la limpieza,
desplegada **antes** del código que depende de ella; ver memoria "migraciones de tenant versionadas"). **Rollback:** revertir imagen;
`BRANCH_SCOPE_DISABLED=1` queda como válvula de emergencia.

---

## 6. Retenciones, percepciones, resúmenes diarios, bajas y reversiones SUNAT

### 6.1 Cómo se modelan hoy

| Entidad | Tabla (`database/migrations.go`) | `branch_id` propio | Vínculos | Cómo se infiere la sucursal |
|---|---|---|---|---|
| Retención | `TenantRetention` (`:2048`) | **No** | `SaleID` (la venta-vehículo `DocType="RETENCION"`), `PurchaseID`, `Series`, `Correlative` | `RetentionBranch` (`guard.go:167-175`): compra origen → `seriesBranch(serie)` |
| Percepción | `TenantPerception` (`:2072`) | **No** | `SaleID`, `SourceSaleID`, `Series` | `PerceptionBranch` (`:178-185`): venta origen → `seriesBranch` |
| Resumen diario | `TenantSunatSummary` (`:1945`) | No (es del RUC entero) | `FecResumen`; el detalle está en `PayloadJSON` | No se infiere: solo `CoversAllBranches` (`guard.go:299-319`) lo lista/emite |
| Comunicación de baja | `TenantSunatVoided` (`:1961`) | No | Solo `PayloadJSON` con `details[].tipoDoc/serie/correlativo` | `LinesInScope`/`LinesReadable`: parsea el payload y llama a `DocBranch` por cada línea |
| Reversión | `TenantSunatReversion` (`:2096`) | No | Ídem baja | Ídem |

Además, al emitir una retención o percepción el servicio crea una **venta-vehículo** `TenantSale`
(`billing_service.go:2300-2315` para retención; análogo en percepción `:2524` y ss.) **sin `BranchID`**: el campo solo queda poblado porque el callback
`createCheck` lo completa con la sucursal activa del contexto (`branchscope.go:284-290`). Un emisor que corra sin contexto (worker, `System`)
dejaría la venta con `branch_id = 0`. La serie sí se valida contra la sucursal de emisión (`reserveRetentionPerceptionSeriesTx`, `billing_service.go:2280`),
pero en la ruta con `Series` en texto libre se hace `tx.Where("series = ?").First(&seriesRec)` (`:2285-2292`) sin comprobar sucursal.

Los listados filtran **después de traer todo** (`billing_handler.go:752, 811, 530, 870`) usando las funciones de inferencia, que son las que
fallan abiertas (5.4).

### 6.2 Qué sucursal infieren y por qué es frágil

1. La inferencia es indirecta (compra/venta origen → serie por **nombre**), y el nombre de serie puede repetirse entre sucursales (`seriesBranch` devuelve
   "la primera", `guard.go:151-153`).
2. Una retención sin compra origen y con una serie que ya no existe o fue renombrada queda "sin sucursal" (0) = visible para todos.
3. Bajas y reversiones pueden mezclar comprobantes de varias sucursales en una misma comunicación (SUNAT lo permite: una por RUC y día); `LinesReadable`
   exige que **todas** las líneas sean legibles; un payload ilegible solo lo ve quien ve todas.
4. Con el scope de 3C, toda la inferencia falla abierta (5.4).

### 6.3 Propuesta (no ejecutar todavía)

**Principio:** el servicio de emisión **escribe** la sucursal; las lecturas y las guardas **leen la columna** (una consulta, sin inferir).

**Migración aditiva V178 (numeración tentativa; la última existente es V176 y V177 se propone en 2.3; verificar la siguiente libre):**
```
ALTER TABLE tenant_retentions      ADD COLUMN branch_id BIGINT UNSIGNED NULL, ADD INDEX idx_tenant_retentions_branch (branch_id);
ALTER TABLE tenant_perceptions     ADD COLUMN branch_id BIGINT UNSIGNED NULL, ADD INDEX idx_tenant_perceptions_branch (branch_id);
ALTER TABLE tenant_sunat_voided    ADD COLUMN branch_id BIGINT UNSIGNED NULL;   -- NULL/0 = comunicación multi-sucursal o sin inferir
ALTER TABLE tenant_sunat_reversions ADD COLUMN branch_id BIGINT UNSIGNED NULL;
-- tenant_sunat_summaries: se deja sin branch_id (documento del RUC; ver decisión 10 de AUDITORIA_Y_DISENO.md)
```
(se implementa con el patrón de `tenantmigrations`: `mig.HasColumn` + `AddColumn`, idempotente, sin borrar nada.)

**Backfill (en la misma migración, solo `UPDATE`, sin tocar documentos ya emitidos):**
- Retenciones: `branch_id` = `tenant_sales.branch_id` vía `sale_id` (la venta-vehículo); si 0, `tenant_purchases.branch_id` vía `purchase_id`; si no, la sucursal de la serie
  **solo si el nombre de serie es único** en `tenant_document_series`; si no, queda NULL (se resuelve a mano; se registra el conteo en el log de la migración).
- Percepciones: `tenant_sales.branch_id` vía `sale_id`, luego `source_sale_id`, luego serie única.
- Bajas/reversiones: se recorre `PayloadJSON` por registro; si todas las líneas resuelven a **una** misma sucursal, esa; si resuelven a varias o a ninguna, NULL.
- Ventas-vehículo `RETENCION`/`PERCEPCION`/guías con `branch_id = 0`: completar desde la serie única.
  Idempotente (`WHERE branch_id IS NULL`).

**Código (en el despliegue siguiente a la migración):**
1. `CreateAndSendRetention` / `CreateAndSendPerception` fijan `BranchID` explícito (el de `input.BranchID`, ya validado por `ResolveWriteBranchID`,
   `billing_handler.go:766-770`) en la venta-vehículo y en el registro. No depender del `createCheck`.
2. En la ruta con serie en texto libre, resolver la serie con `docseries.ValidateForBranch` (como en la ruta por `SeriesID`).
3. `CreateVoided`/`CreateReversion`: calcular el conjunto de sucursales de las líneas con **`System`**, guardar `branch_id` si es único; si hay más de una,
   exigir `CoversAllBranches` (como el resumen diario) para emitirla.
4. Listados y estados por ID filtran con `WHERE branch_id IN (allowed) OR branch_id IS NULL` (o registrando las tablas en `BranchScopedModels`,
   que ya tiene el comportamiento `NULL/0 = del tenant`); `branchguard` se simplifica a leer la columna.
5. Resumen diario: sin cambio de modelo; mantener `CoversAllBranches`. Documentar que el resumen del RUC incluye todas las sucursales (SUNAT lo exige así).
6. Mientras tanto (hotfix de 5.4) las funciones de inferencia usan `System`.

**Esfuerzo:** migración + backfill + pruebas 1 d; cambios en el servicio y listados 1 d; ajustar `branchguard` y tests con scope real 0.5 d → ~2.5 d
(sin contar 5.4, que va antes).
**Orden de despliegue:** (1) hotfix 5.4; (2) migrate-fleet de la migración aditiva (las columnas aún no se usan; es seguro); (3) backend que escribe y lee `branch_id`;
(4) revisar el conteo de NULL por tenant y completar a mano. **Rollback:** revertir el backend (las columnas nuevas quedan sin uso, inocuas); no hay
borrado de datos.

---

## 7. Plan consolidado

| Orden | Entrega | Contenido | Esfuerzo | Requiere |
|---|---|---|---|---|
| 1 | Hotfix de guardas | 5.4 (`System` en la inferencia) + helper de pruebas con scope real | 1.5 d | Solo backend |
| 2 | Revocación y emisor | 1.3 (emisor único), 2.3 (versión de sesión + V177 + frontends refrescan ante 409), 3 (PIN/master con `exp` fijo) | ~5 d | Frontends primero, luego migrate-fleet V177, luego backend |
| 3 | Integridad con scope | V1, V2 (+ índice), V4, V5, U1/U2; test guardián 5.5; borrar código muerto | ~4 d | Backend; índice único tras auditar duplicados |
| 4 | SSE | 4.4 (fetch-stream, cierre al vencer, retiro del query token) | ~2 d (+1 d canje de acceso maestro) | Backend puente → frontends → apagar puente |
| 5 | Fiscal con `branch_id` | 6.3 | ~2.5 d | Migrate-fleet antes del backend |
| 6 | Modo estricto | Anotar rutas públicas con `System`, revisar log de auditoría de producción y activar `BRANCH_SCOPE_STRICT=1` | ~1 d | Después de 1 y 3 |

## 8. Lo que no se pudo verificar

- Nada se ejecutó: los hallazgos 1, 2(a), V1, V4, V5 son por lectura y deben confirmarse con una prueba (SQLite con `branchscope.Install/Attach`).
- Configuración de Cloudflare/Nginx (si registran la query string de `/api/billing/events`); no está en el repo.
- Comportamiento de fetch-streaming en los WebViews de Tauri y de la app Android (capacidad esperada, no probada).
- Duplicados reales de series entre sucursales en los tenants de producción (requiere una consulta de solo lectura con `claude_ro`, no ejecutada).
- Si algún cliente externo llama `/api/billing/events` con `access_token` (habría que medirlo antes de apagar el puente).
- Decisión de negocio pendiente: anticipos entre sucursales (U13) y bajas/reversiones multisucursal (6.3 punto 3).
