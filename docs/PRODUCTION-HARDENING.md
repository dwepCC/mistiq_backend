# Hardening de producción — Tukifac API

Documento basado en el código real (`main.go`, `pkg/database`, handlers multipart, `tenantstorage`).

## 1. Pools MySQL

### Problema original

Cada tenant activo tiene su propio `*sql.DB` en `sync.Map` (`pkg/database/tenant.go`). Con valores antiguos:

- Central: `MaxOpen=25`
- **Por tenant**: `MaxOpen=10`

Con 80 tenants concurrentes en pico: hasta **80×10 + 25 = 825 conexiones** → saturación del VPS MySQL.

### Valores implementados (configurables por env)

| Pool | Variable | Default | Motivo |
|------|----------|---------|--------|
| Central | `DB_CENTRAL_MAX_OPEN` | **40** | Login SA, resolver slug, cron, panel central |
| Central | `DB_CENTRAL_MAX_IDLE` | **15** | Reutilizar conexiones frecuentes |
| Tenant (cada BD) | `DB_TENANT_MAX_OPEN` | **3** | Limitar explosión multi-tenant |
| Tenant | `DB_TENANT_MAX_IDLE` | **2** | |
| Ambos | `DB_CONN_MAX_LIFETIME` | **30m** | Rotar conexiones tras deploy MySQL / NAT |
| Ambos | `DB_CONN_MAX_IDLE_TIME` | **5m** | Liberar conexiones de tenants inactivos |

### Cálculo para ~500 clientes migrados gradualmente

Supón **60–100 tenants** con tráfico simultáneo en hora pico (no los 500 a la vez):

- Central: 40
- Tenants: 100 × 3 = **300**
- **Total pico ≈ 340 conexiones**

En MySQL dedicado: `max_connections >= 400`, reservar margen para admin/backup.

Ajuste si MySQL tiene `max_connections=150`: bajar `DB_TENANT_MAX_OPEN=2` y `DB_CENTRAL_MAX_OPEN=25`.

### Regla operativa

```
DB_CENTRAL_MAX_OPEN + (tenants_activos_concurrentes × DB_TENANT_MAX_OPEN) < 0.7 × max_connections_mysql
```

---

## 2. Migraciones — solo CLI (estilo Laravel)

### Política actual

- **Producción:** el servidor HTTP **no** ejecuta `AutoMigrate` ni seeds en requests.
- `GetTenantDB()` solo abre/reutiliza conexión del pool.
- Tras cada deploy con cambios de esquema:

```bash
cd /opt/tukifac && bash deploy/scripts/deploy.sh
# o: docker exec tukifac-backend-go ./tukifac-api migrate
```

Ver [DEPLOY-VPS-UBUNTU.md](./DEPLOY-VPS-UBUNTU.md) para CI/CD completo (GHCR + SSH).

### Comandos

| Comando | Qué hace |
|---------|----------|
| `migrate` | `EnsureCentralDB` + `MigrateCentral` + memberships + `SeedCentral` + todos los tenants **activos** |
| `migrate-central` | Solo BD central |
| `migrate-tenants` | Solo tenants `status=active` |
| `migrate-tenant <slug>` | Un tenant |

### Lotes MySQL

```env
MIGRATION_BATCH_SIZE=50      # tenants por lote antes de pausa
MIGRATION_BATCH_PAUSE=2s     # pausa entre lotes
```

Si un tenant falla, el proceso **continúa** y muestra resumen SUCCESS/FAILED.

### Desarrollo opcional

```env
AUTO_MIGRATE_DEV=true
```

Migra central + todos los tenants al arrancar `go run .` — **nunca** en producción.

### Alta de tenant nuevo

`TenantService.Create` llama `MigrateTenantSchema` explícitamente (no depende del CLI global).

### Panel Super Admin

`POST /api/superadmin/tenants/:id/migrate` y `migrate-all` siguen disponibles; delegan en `MigrateTenantSchema` / `MigrateTenantsBatch`.

---

## 3. Timeouts HTTP (Fiber)

| Parámetro | Default | Motivo |
|-----------|---------|--------|
| `HTTP_READ_TIMEOUT` | 30s | Slow clients / uploads |
| `HTTP_WRITE_TIMEOUT` | **120s** | Facturador 45s + emisión SUNAT + margen |
| `HTTP_IDLE_TIMEOUT` | 120s | Keep-alive detrás de NPM |

El facturador (`pkg/facturador/client.go`) usa 45s; billing adapters 30s. `WriteTimeout` debe ser **≥ 90s**, usamos 120s.

---

## 4. Health checks

| Ruta | Uso | Peso |
|------|-----|------|
| `GET /` | **Liveness** — proceso vivo | Mínimo |
| `GET /health` | **Readiness** — `Ping` MySQL central (2s timeout) | Ligero |

Docker / compose usan `/health`. NPM puede usar `/` para monitor simple.

**No** se valida facturador en health (evita falsos negativos y latencia).

---

## 5. TrustProxy + NPM

Configurado en `main.go`:

- `TrustProxy: true`
- `TrustProxyConfig`: Loopback, Private, LinkLocal (red Docker/NPM)
- `ProxyHeader: X-Forwarded-For`

Efecto con IP de NPM en red privada:

- `c.IP()` → cliente real
- `c.Scheme()` → `https` con `X-Forwarded-Proto`
- `c.Hostname()` → host público si NPM envía `X-Forwarded-Host`

**NPM:** activar "Forward Hostname", WebSockets si aplica, custom location body size ≥ 12MB.

---

## 6. BodyLimit y uploads

| Capa | Límite |
|------|--------|
| Handlers (productos, contactos, receipts) | **10 MB** (`uploadlimits.MaxFileBytes`) |
| Fiber `BODY_LIMIT_BYTES` | **12 MB** (overhead multipart) |

Logos empresa van en **base64** al facturador, no multipart local.

PDF comprobantes: mayormente streaming desde Lycet, no disco.

---

## 7. Seguridad

| Control | Estado |
|---------|--------|
| Usuario no root en Docker | Sí (`app` UID 10001) |
| `recover` middleware | Sí |
| Errores 500 en producción | Mensaje genérico |
| `ServerHeader` vacío | Sí |
| Security headers | `X-Content-Type-Options`, `X-Frame-Options`, etc. |
| JWT sin query BD por request | Sí (`TenantAuthAPI`) |
| Secrets en env | Sí — nunca en imagen |

**Implementado (fase 2):** `ValidateTenantBinding` en grupo `/api` autenticado — rechaza si `JWT.tenant_slug` ≠ tenant resuelto o `tenant_db` / `tenant_id` no coinciden.

---

## 8. Caché metadata tenant

`LookupTenantBySlug` con TTL `TENANT_METADATA_TTL` (default **5m**).

Reduce queries a `CentralDB` en cada request. Invalidar con `InvalidateTenantCache(slug)` al actualizar tenant.

---

## 9. Persistencia Docker

| Host | Contenedor | Contenido |
|------|------------|-----------|
| `./data/uploads` | `/app/uploads` | Imágenes, fotos, receipts |
| `./data/storage` | `/app/storage` | `invoices/tenants/.../xml,cdr,signed,pdf` + legacy |

`INVOICE_STORAGE_PATH=/app/storage/invoices` obligatorio en `.env` producción.

---

## 10. Checklist pre-migración 500 clientes

1. MySQL: `max_connections`, backups B2, usuario con permisos acotados si es posible
2. Probar `/health` tras deploy
3. Monitorear conexiones MySQL: `SHOW STATUS LIKE 'Threads_connected'`
4. Primer deploy post-cambio esquema: esperar latencia en primer hit por tenant
5. NPM: solo `127.0.0.1:3000` expuesto
6. Firewall MySQL: solo IP VPS backend

---

# Fase 2 — Rate limiting, logging, monitoreo, escalado

## 11. Rate limiting (Fiber v3 `middleware/limiter`)

Basado en rutas reales del código (`routes.go`, `internal/*/routes.go`).

### Rutas detectadas (auth)

| Ruta real | Límite default | Clave |
|-----------|----------------|-------|
| `POST /api/login` | 10/min | IP |
| `POST /api/superadmin/login` | 10/min | IP |
| `POST /api/profile/me/password` | 10/min | IP |
| `POST /api/superadmin/me/password` | 10/min | IP |
| `POST /api/superadmin/users/:id/password` | 10/min | IP |

**No existen** en el código: `refresh token`, `forgot password` — no hay endpoints que limitar aún.

### Facturación (`internal/billing/routes.go`)

Prefijo `/api/billing/*` — emisión SUNAT, resúmenes, guías, etc.

- Default: **60 req/min** por `IP|tenant_slug`

### Uploads (multipart real)

| Ruta | Handler |
|------|---------|
| `POST /api/products/:id/image` | `product_handler` |
| `POST /api/contacts/:id/photo` | `contact_handler` |
| `POST /api/superadmin/payments` | `payment_handler` (receipt) |

- Default: **30 req/min** por `IP|tenant_slug`

### Consulta pública

- `POST /api/consulta/dni`, `/api/consulta/ruc` → **20 req/min** por IP

### Global

- **300 req/min** por `IP|tenant_slug` (excluye `/`, `/health`, `/metrics`, `/uploads/*`, OPTIONS)

### Variables de entorno

```env
RATE_LIMIT_ENABLED=true
RATE_LIMIT_GLOBAL=300
RATE_LIMIT_AUTH=10
RATE_LIMIT_PASSWORD=10   # reservado; auth/password comparten lógica
RATE_LIMIT_BILLING=60
RATE_LIMIT_UPLOAD=30
RATE_LIMIT_PUBLIC_CONSULT=20
```

### IP real detrás de NPM

`c.IP()` usa `TrustProxy` + `X-Forwarded-For` (`main.go`). NPM debe reenviar cabeceras de proxy.

### Limitación

Store en memoria del proceso — con **múltiples réplicas** de backend, usar Redis store (futuro) o rate limit en NPM.

---

## 12. Logging estructurado (`log/slog`)

**Elección: `log/slog` (stdlib Go 1.25)** — sin dependencias nuevas, JSON en producción, integración Docker.

Alternativas evaluadas:

| Librería | Motivo descarte / uso |
|----------|----------------------|
| zap | Más rápido pero nueva dependencia; slog suficiente |
| zerolog | API distinta; slog ya está en stdlib |
| slog | **Elegido** — JSON, niveles, zero config extra |

### Formato por request

```json
{
  "msg": "http_request",
  "request_id": "uuid",
  "tenant": "empresa1",
  "route": "/api/billing/send/123",
  "method": "POST",
  "status": 200,
  "latency_ms": 130,
  "ip": "203.0.113.1",
  "user_id": 42
}
```

- `X-Request-ID` propagado / generado
- No se loguean bodies (evita passwords/tokens)
- Errores 5xx con campo `error`

### Configuración

```env
LOG_LEVEL=info    # producción
LOG_LEVEL=debug   # desarrollo
```

Archivos: `pkg/logger/logger.go`, `pkg/middleware/request_log.go`, `pkg/middleware/request_id.go`

---

## 13. Monitoreo — etapa actual (realista)

| Herramienta | Uso ahora |
|-------------|-----------|
| `GET /health` | Readiness MySQL — Docker healthcheck |
| `GET /` | Liveness |
| `GET /metrics` | Texto estilo Prometheus: uptime, goroutines, memoria |
| `docker logs` / JSON slog | Errores, latencia, tenant |
| `docker stats` | CPU/RAM del contenedor |

**No** se añadió cliente Prometheus completo aún — `/metrics` es future-ready para scrape interno.

Recomendación migración 500 clientes:

1. Uptime: NPM + `/health`
2. Errores: filtrar logs `status>=500` o `level=ERROR`
3. MySQL: `Threads_connected`, slow query log
4. Fase siguiente: Grafana + Prometheus o Loki (logs JSON)

**No exponer** `/metrics` públicamente — solo red interna o VPN.

---

## 14. Cuellos de botella — priorización (código real)

### CRÍTICO

| Riesgo | Ubicación | Mitigación |
|--------|-----------|------------|
| Explosión conexiones MySQL | `tenant.go` pools | ✅ Fase 1: 3 open/tenant |
| Cross-tenant JWT vs slug | `auth.go` + `tenant.go` | ✅ `ValidateTenantBinding` |
| AutoMigrate cada request | `GetTenantDB` | ✅ Eliminado — solo CLI `migrate` |

### ALTO

| Riesgo | Ubicación | Notas |
|--------|-----------|-------|
| Emisión SUNAT lenta | `billing_service`, facturador 45s | Timeouts 120s; rate limit billing |
| Brute force login | `auth_handler`, `auth_sa_handler` | ✅ Rate limit 10/min IP |
| Consulta DNI/RUC pública | `routes.go` sin auth fuerte | ✅ Rate limit 20/min |
| `tenant-by-ruc` expone `token_consulta_datos` | `routes.go:131` | Revisar si debe ser público en prod |

### MEDIO

| Riesgo | Ubicación | Notas |
|--------|-----------|-------|
| N+1 contactos en listado ventas | `sale_service.go` ~572+ | Query extra por lote de IDs — OK con paginación |
| `sync.Map` tenant pools sin límite | `tenant.go` | Muchos tenants distintos en pico → muchos pools; monitorear memoria |
| Cron 24h + query central | `cron/expiration.go` | Solo arranca si `IsCentralSchemaReady()`; entrypoint corre `migrate-central` antes del API |
| Rate limiter memoria por IP | `ratelimit.go` | Crece con IPs únicas; reinicio limpia |

### BAJO

| Riesgo | Ubicación | Notas |
|--------|-----------|-------|
| Ubigeo seed embebido | `ubigeo_seed.go` | Solo primer ensure por tenant |
| SSR legacy sin rutas | `auth.go` TenantAuthWeb | Código muerto |
| `migrations.go` monolítico | Mantenimiento humano | No runtime |

---

## 15. Seguridad multi-tenant (fase 2)

| Vector | Estado |
|--------|--------|
| BD por tenant (`saas_tenant_*`) | Aislamiento fuerte |
| JWT sin revalidar slug | **Corregido** — `ValidateTenantBinding` |
| `X-Tenant-Slug` spoofing con token ajeno | **Bloqueado** si JWT slug ≠ header |
| Superadmin vs tenant | Rutas separadas `/api/superadmin/*` |
| Uploads path traversal | `tenantstorage/delete.go` valida prefijo |
| Login sin tenant header | Permitido para SA; tenant login requiere resolver slug en login |

**Frontend:** siempre enviar `X-Tenant-Slug` coherente con el token en `api.*`.

---

## 16. Troubleshooting producción

### 429 Too Many Requests

- Revisar `RATE_LIMIT_*` en `.env`
- Oficinas detrás de un NAT comparten IP — subir `RATE_LIMIT_GLOBAL` o limitar por `IP|tenant` (ya activo en billing/upload)

### 403 "el token no corresponde a la empresa"

- Token de tenant A con `X-Tenant-Slug` de tenant B — corregir frontend

### Logs por tenant lento

```bash
docker logs tukifac-backend-go 2>&1 | grep '"tenant":"empresa1"'
```

Filtrar `latency_ms` alto o `status":500`.

### MySQL saturado

```sql
SHOW STATUS LIKE 'Threads_connected';
```

Bajar `DB_TENANT_MAX_OPEN` o reducir tenants concurrentes.

### Health degraded

`curl -s http://127.0.0.1:3000/health` — si `mysql: down`, revisar firewall VPS DB.

---

## 17. Tuning recomendado — 500 clientes migración gradual

```env
# Pools
DB_CENTRAL_MAX_OPEN=40
DB_TENANT_MAX_OPEN=3

# Cache
TENANT_METADATA_TTL=5m
MIGRATION_BATCH_SIZE=50
MIGRATION_BATCH_PAUSE=2s

# HTTP
HTTP_WRITE_TIMEOUT=120s
BODY_LIMIT_BYTES=12582912

# Rate limits (ajustar tras observar 429 legítimos)
RATE_LIMIT_GLOBAL=300
RATE_LIMIT_BILLING=60

# Logs
LOG_LEVEL=info
APP_ENV=production
```

Escala horizontal futura: Redis para rate limit + sesiones; no duplicar cron sin leader election.
