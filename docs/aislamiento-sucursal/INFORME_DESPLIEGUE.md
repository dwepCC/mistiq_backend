# Informe de despliegue — aislamiento por sucursal (10-nov-2026)

**Nada está pusheado ni desplegado.** Este documento resume lo que hay que subir, en qué orden, cómo comprobarlo y cómo volver atrás.

## 1. Commits por repositorio (sin push)

### backend_principal (16 commits)
`22a70bf` V175 cuentas contables · `4319b24` prueba de suscripciones · `b505872` auditoría y diseño ·
`23fc0cb` **3A** cierre de fugas · `dbcd79f` **3B** permisos `branches.*`, BranchContext, modo ALL, V176 ·
`8163b69` **3C** scope GORM · `9a604f5` endurecer `ResolveWriteBranchID` · `cdeb86e` `products.manage_global` + V177 ·
`2115c5a` informe `branch_id=0` · `3605148` fix guardas fiscales fuera del scope · `c14f85f` fix reglas globales (serie única, contactos) ·
`9265814` fix transferencia con series bajo scope · `b1a6039` docs deuda técnica y ecommerce · `e08f8d0` fix scope con `Table("x alias")` ·
`cd3a058` "ver todas" por BD y no por JWT · `2bd295f` CORS `X-Branch-Mode`.

### frontend_tenant — Tukifac (8)
`d463947` `ea95e42` `3547059` `f271eee` `8a08dba` (lote previo: Contabilidad en Ajustes, desglose de comprobantes, arreglos) ·
`8357629` **Fase 4** cambio de sucursal sin F5 · `ce1d892` etiquetas `branches.*` · `4966863` modo "Todas", formularios fijos a la activa, catálogo global.

### frontend_restaurant_tenant — Tukichef (5)
`56233d6` `35d623d` `e30c3b7` (lote previo: stock en tablet, desglose de comprobantes, versión 1.4.7) ·
`51030e6` **Fase 4** cambio de sucursal sin F5 · `ebbcaae` modo "Todas" y catálogo global.

Tamaño: backend 104 archivos (+9100/−159), Tukifac 102 (+3978/−337), Tukichef 54 (+3641/−167).

## 2. Migraciones de tenant pendientes (aditivas, idempotentes, sin borrar datos)
Producción estaba en **V171** (5-nov). Pendientes: **V172** (limpia contacto "Clientes Varios" de empresa) · **V173** (permisos finos de Caja) ·
**V174** (módulo Contabilidad) · **V175** (cuentas contables por defecto) · **V176** (permisos `branches.view_all|switch|manage`; Administrador los recibe;
roles con usuarios `can_switch_branch` reciben `switch`; usuarios sin asignación reciben su sucursal de origen o la principal) ·
**V177** (permiso `products.manage_global`; lo reciben los roles que ya tenían `products.create|edit|delete|manage`).
No hay migración de ecommerce (solo diseño).

## 3. Pruebas ejecutadas (resultados reales)
- **Backend:** `go build ./...` y `go test ./... -count=1`: 92 paquetes ok, 0 fallos (última corrida tras el commit `2bd295f`).
  Tests nuevos: aislamiento por módulo (3A), permisos/middleware/V176/anti-escalada (3B), `pkg/branchscope` (3C, incl. alias, estricto, transacciones),
  guardas bajo scope, reglas globales, transferencia con series, V177, `RequireGlobalCatalog`, CORS.
  Cada fix posterior a 3C se reprodujo con un test que **fallaba antes** del arreglo.
- **Tukifac:** `tsc --noEmit` 0 · `vitest` 9 archivos / 54 tests ok · `vite build` ok.
- **Tukichef:** `tsc -p tsconfig.app.json --noEmit` 0 · `vitest` 6 archivos / 53 tests ok · `vite build` ok.
- **En vivo (backend local + MySQL, tenant demo):** POS con carrito → confirmación → cancelar/descartar; carrito no se cobra en otra sucursal;
  Tukichef con mesa abierta → confirmación → descarta, redirige y el servidor rechaza operar la sesión anterior (403); login real de usuario con 2 sucursales
  y selector; cambio sin F5 en ambas apps; modo "Todas" en ambas apps (banner, datos consolidados 241 vs 235, escrituras 403 `BRANCH_READ_ONLY`, POS bloqueado,
  salida del modo); 35 endpoints con vendedor y administrador sin 500; log de auditoría sin accesos sin contexto en solicitudes.
- **No probado:** Android y Tauri (sin entorno), diálogo nativo `window.confirm` en el navegador integrado (suprimido; probada la lógica y el texto),
  reportes uno a uno en modo "Todas", flujos de escritura reales de ConvertOrder/Despatch/Retención/Nota independiente con sucursal fija,
  concurrencia de stock, producción real.

## 4. Orden exacto de despliegue
1. **Backup** de la BD central y de una muestra de tenants (o snapshot del VPS de BD).
2. **Backend** (imagen nueva). Compatible con tenants sin V176/V177: conserva la regla por rol "Administrador", no bloquea usuarios sin sucursal y no restringe el catálogo global.
   `BRANCH_SCOPE_STRICT` **sin definir (apagado)**. Verificar `/health` y el log (`[branchscope]`).
3. **`migrate-fleet`** V172→V177 (V173 antes del frontend web de Tukifac). Verificar por muestreo: permisos `branches.*` en el rol Administrador,
   usuarios con fila en `tenant_user_branches`, `products.manage_global` en roles que editaban productos.
4. **Frontend web de Tukifac** (después de V173, V176 y V177).
5. **Apps Tukichef** (web/Tauri/Android 1.4.7): pueden salir cuando el backend esté arriba; el modo "Todas" y el catálogo global requieren V176/V177.
6. **Vigilancia** (varios días): log `[branchscope] AUDITORÍA`; errores 403 `BRANCH_FORBIDDEN`/`BRANCH_READ_ONLY`; quejas de roles que perdieron acciones.
7. **Modo estricto:** solo después de revisar la auditoría, anotar con `System` lo legítimo y completar pruebas de integración (ver DEUDA_TECNICA.md).

## 5. Riesgos conocidos
- **Scope GORM global (3C):** ya rompió 4 flujos que cruzan sucursales (corregidos). Pueden existir más: vigilar auditoría y errores de stock/series/transferencias.
  Límites documentados: `Raw()`/`Exec()` y JOIN no pasan por callbacks (FASE_3C.md, DEUDA_TECNICA.md §5).
- **Escrituras de administradores endurecidas:** clientes viejos (apps Tukichef/Tukifac anteriores) que envíen un `branch_id` distinto de la sucursal activa recibirán 403.
- **Permisos en el JWT** (`products.manage_global`, módulos): un cambio de permisos de rol aplica al renovar el token (hasta 24 h). "Ver todas" ya sí se decide por BD (10 s).
- **Token en la URL del SSE, revocación de sesión, emisor único de JWT, vigencia del token PIN:** sin resolver (DEUDA_TECNICA.md); no empeoran con estos cambios, salvo que cambiar de sucursal renueva 24 h.
- **Roles personalizados con `company.edit`** pierden crear/eliminar sucursales sin `branches.manage`.
- **Ecommerce:** la tienda pública sigue exponiendo `stock_by_branch` de todas las sucursales (hotfix E0 propuesto, no aplicado).
- **Retenciones/percepciones** se crean sin `branch_id` (5 filas en 3 tenants; informe aparte, sin reasignar).
- **`doriconta`** tiene dos sucursales marcadas principales.

## 6. Rollback
- **Interruptor inmediato sin redeploy de código:** variable `BRANCH_SCOPE_DISABLED=1` y reiniciar → apaga los callbacks de 3C (el resto de guardas siguen).
- **Backend:** volver a la imagen anterior. Las migraciones V172–V177 son aditivas y compatibles hacia atrás (solo agregan permisos y asignaciones); no hace falta revertirlas.
  El backend anterior ignora los permisos nuevos y los registros añadidos.
- **Frontends:** redeploy del build anterior (web) o versión anterior de las apps; los frontends antiguos funcionan con el backend nuevo salvo el punto de escrituras endurecidas.
- **Datos:** no hay migraciones destructivas ni reasignaciones; si V176 asignó una sucursal incorrecta a un usuario, se corrige desde Usuarios. Restaurar desde el backup del paso 1 solo ante corrupción.

---

## 7. Actualización (10-nov-2026, tarde): sesión, JWT y SSE

**Commits nuevos.** Backend: `4f2be24` emisor único de JWT · `7c8141a` versión de sesión (V178) y SSE por cabecera.
Tukifac: `509dba1` refresh ante 409 + SSE fetch-stream. Tukichef: `1cdad54` ídem + aviso de fin de turno PIN.

**Migración nueva: V178** (aditiva e idempotente): `branch_session_version` 0 → 1. Sin ella la primera revocación de cada usuario no surtía efecto.

**Qué cambia**
- Un solo emisor de JWT (`internal/auth/service/issuer.go`); `switch-branch` relee rol/permisos/módulos de la BD y **no extiende** el exp (el PIN conserva su turno de 12 h; encadenar cambios de sucursal ya no prolonga la sesión; el refresh de 30 d renueva). Rechaza usuarios inactivos.
- La versión de sesión sube al cambiar rol, permisos del rol, contraseña, PIN o estado activo → el token viejo recibe 409 `SESSION_UPDATED`; los frontends nuevos refrescan y reintentan una vez.
- SSE de facturación: el token viaja por cabecera (fetch-stream). El `?access_token=` queda **solo** como puente en `/api/billing/events` mientras `SSE_ALLOW_QUERY_TOKEN` != 0 (con contador en el log). El stream se cierra al vencer el token o a los 55 min.

**Verificado en vivo (backend local + MySQL):** SSE 200 con cabecera; puente aceptado solo en esa ruta (401 en `/api/sales?access_token=`); `switch-branch` conserva el exp del login; tras subir la versión de sesión el token viejo recibe 409 y la app de Tukichef, con login real, refrescó sin cerrar sesión (token con versión nueva, sigue en la pantalla); el stream conecta desde el navegador sin token en la URL.

**Orden de despliegue (ajustado).** Hay una tensión: el frontend web de Tukifac debe salir después de V173/V176/V177, pero el backend que sube la versión por rol debe salir con (o después de) los frontends que refrescan ante 409.
Recomendado: ventana de mantenimiento corta — backend → migrate-fleet (V172…V178) → frontends (Tukifac web y apps Tukichef) — y **no editar roles, permisos ni contraseñas** hasta que los frontends nuevos estén arriba (un cambio así expulsa a los afectados, que solo deben volver a entrar; los PIN no tienen refresh).
Después: 24 h más tarde activar `RejectLegacyZeroSessionVersion = true` (rechaza tokens con claim de versión 0) en una entrega aparte; cuando el contador del puente deje de crecer, `SSE_ALLOW_QUERY_TOKEN=0`.

**Riesgos nuevos**
- Cambiar la propia contraseña sube la versión: con frontends antiguos el usuario tendría que volver a entrar.
- Streaming de `fetch` en WebView de Android y Tauri sin verificar (plan B documentado: ticket de un solo uso + EventSource).
- `?master_sso=<jwt>` (acceso maestro) sigue en la URL: segunda entrega con código de canje de un solo uso.
- Tras 402/403/404 el stream no reintenta hasta que cambie token, sucursal o época.
- Pruebas: backend 3 corridas completas sin fallos tras estabilizar `TestBillingEventsSSE_BranchIsolation` (era sensible a la carga); Tukifac 93 tests; Tukichef 86 tests.

**Rollback adicional:** volver a la imagen anterior del backend; V178 es inofensiva (versión 1). Si se revierte un frontend con el backend nuevo, los cambios de rol/contraseña vuelven a cerrar sesión (por eso el orden). `SSE_ALLOW_QUERY_TOKEN=1` mantiene vivos a los clientes antiguos.

---

## 8. Hotfix ecommerce E0 (10-nov-2026, noche)

Commit backend: ver `git log` (fix(ecommerce) hotfix E0). **Sin migración, solo backend.** La respuesta pública `GET /api/public/ecommerce/products`
(sin autenticación) filtraba el stock por sucursal con el nombre de cada sucursal, los números de serie y, con "Mostrar stock" apagado, el stock por
presentación y sucursal. Ahora: nunca `stock_by_branch` ni `serials`; con `show_stock` se conserva `stock_total` y la disponibilidad por presentación
sumada y anónima; sin `show_stock` no hay stock ni por presentación. La tienda propia solo usaba `stock_total`, así que no pierde funcionalidad.
Verificado en vivo (8 productos públicos del demo: 0 con stock por sucursal, 0 con series, `stock_total` intacto). Test que falla sin el arreglo:
`TestPublicProducts_DoesNotExposeBranchStockOrSerials`. Despliegue: puede salir con el backend; rollback: imagen anterior.

**Pendiente de ecommerce (no incluido):** `ListOrders`, `print-data` y `status` de pedidos no se pueden aislar por sucursal sin `branch_id` en
`tenant_ecommerce_orders` (migración E1, sin ejecutar); el stock mostrado sigue siendo el total del tenant hasta definir la sucursal de despacho.

---

## 9. Acceso maestro con código de un solo uso (10-nov-2026, noche)

**Commits:** backend `fb1e0a3` · Tukifac `4368931` · frontend_central `e9fcc58` (repo nuevo en esta lista: 1 commit sin push). **Sin migración.**

**Qué cambia:** el superadmin ya no abre el ERP del tenant con `?master_sso=<jwt>` (el JWT de 24 h quedaba en historial, logs de proxy/CDN y Referer).
`POST /api/superadmin/tenants/:id/master-access` devuelve un **código aleatorio de 30 s y un solo uso** (Redis `GETDEL` en producción, memoria sin Redis; guardado como hash
y atado al slug del tenant). La URL pasa a `/?master_code=<código>`; el ERP del tenant lo canjea con `POST /api/auth/master-exchange` (el JWT viaja en el cuerpo, `no-store`,
rate limit de autenticación y auditoría `master_access_exchange[_failed]`) y limpia la URL de inmediato. Un código canjeado en otro tenant queda quemado.

**Puentes de compatibilidad (apagables):** el backend sigue devolviendo `token` salvo `MASTER_ACCESS_LEGACY_TOKEN=0`; el ERP del tenant sigue aceptando `?master_sso=<jwt>` y `/auth/sso?token=`.
**Orden de despliegue:** (1) backend, (2) frontend del tenant (acepta `master_code`), (3) frontend central (empieza a usar el código). Después, cuando no queden ERPs/central antiguos:
`MASTER_ACCESS_LEGACY_TOKEN=0` y retirar el soporte de `?master_sso=` del frontend del tenant. **Rollback:** imagen/builds anteriores; el puente garantiza que el central antiguo siga funcionando con el backend nuevo.
**Requisito:** Redis activo en producción para que el canje valga entre instancias (sin Redis solo funciona con una instancia, porque el código vive en memoria).

**Verificado en vivo (backend local + MySQL, tenant demo, superadmin local):** el endpoint devuelve `code` (43 caracteres) y `token`; el canje funciona una vez y el segundo da 401 `MASTER_CODE_INVALID`;
abrir `/?master_code=…` en el navegador inicia sesión como acceso maestro (`auth_method: master_access`, `impersonated`, actor `superadmin@saas.com`) y la URL queda en `/home` sin código.
Tests: `pkg/mastercode` (memoria y Redis con miniredis: un solo uso, TTL, cross-tenant), handler de canje, Tukifac 98 tests (incl. flujo completo del `AuthProvider`), central 129 tests.
**No probado:** Redis real en producción (miniredis sí), el flujo con el frontend central desplegado.

---

## 10. Sucursal principal única y huérfanos (V179) — 10-nov-2026

Commit backend `e019ca5`. **Migración nueva: V179** (ahora `migrate-fleet` aplica V172→V179). Deja a cada tenant con exactamente una sucursal principal y vincula a ella los registros con `branch_id = 0`
(decisión del dueño). Efecto esperado: `doriconta` conserva la sucursal 1 como única principal; 5 retenciones (3 tenants) pasan a la principal. Código: crear/editar sucursal transaccional, no se puede quitar la marca a la única principal,
retenciones/percepciones se crean con sucursal. Ver `INFORME_BRANCH_ID_CERO.md`. Rollback: no hace falta revertir (aditiva); para deshacer el vínculo de las 5 filas habría que restaurarlas a mano (ids en el informe).
